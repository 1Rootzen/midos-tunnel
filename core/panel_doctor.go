package main

// Doctor: a one-screen health check of the box and the tunnels on it, with a
// one-click fix for the problems that have a safe, obvious repair. Every check
// is read-only; a repair only ever restarts a unit, reloads systemd, writes a
// missing unit file, or sets a sysctl - never anything that could drop a live
// tunnel the operator did not ask about.

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type doctorCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // ok, warn, fail
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // a key handleDoctorRepair understands
}

func (s *panelServer) handleDoctor(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"checks": s.runDoctor()})
}

func (s *panelServer) runDoctor() []doctorCheck {
	var out []doctorCheck
	cfg := s.config()

	// the template unit that every managed tunnel is started from
	if _, err := os.Stat(s.unitDir + "/midos-tunnel@.service"); err != nil {
		out = append(out, doctorCheck{"template", "یونیت قالب تانل", "fail",
			"فایل midos-tunnel@.service پیدا نشد؛ تانل‌های مدیریت‌شده بدون آن اجرا نمی‌شوند.", "write-template"})
	} else {
		out = append(out, doctorCheck{"template", "یونیت قالب تانل", "ok", "نصب است.", ""})
	}

	// each tunnel's unit
	for _, t := range s.findTunnels() {
		st := s.unitStatus(t.Unit)
		switch {
		case st.Active == "failed":
			fix := ""
			if t.Managed {
				fix = "restart:" + t.ID
			}
			out = append(out, doctorCheck{"tunnel:" + t.ID, "تانل " + t.ID, "fail", "به خطا خورده است.", fix})
		case st.Active == "active" && st.Restarts >= 5:
			out = append(out, doctorCheck{"tunnel:" + t.ID, "تانل " + t.ID, "warn",
				fmt.Sprintf("فعال است ولی %d بار ری‌استارت شده؛ لاگ را ببینید.", st.Restarts), "restart:" + t.ID})
		case st.Active == "active":
			out = append(out, doctorCheck{"tunnel:" + t.ID, "تانل " + t.ID, "ok", "فعال است.", ""})
		default:
			out = append(out, doctorCheck{"tunnel:" + t.ID, "تانل " + t.ID, "warn", "متوقف است.", ""})
		}
	}

	// kernel-tunnel prerequisites
	if v := strings.TrimSpace(readFirstLine("/proc/sys/net/ipv4/ip_forward")); v == "1" {
		out = append(out, doctorCheck{"forward", "فوروارد IPv4", "ok", "روشن است.", ""})
	} else {
		out = append(out, doctorCheck{"forward", "فوروارد IPv4", "warn",
			"خاموش است؛ برای تانل‌های کرنلی لازم است.", "sysctl-forward"})
	}
	if v := strings.TrimSpace(readFirstLine("/proc/sys/net/ipv4/tcp_congestion_control")); v == "bbr" {
		out = append(out, doctorCheck{"bbr", "کنترل ازدحام BBR", "ok", "فعال است.", ""})
	} else {
		out = append(out, doctorCheck{"bbr", "کنترل ازدحام BBR", "warn",
			"فعال نیست (اکنون: " + orDash(v) + "؛ BBR توان عبور را بهتر می‌کند).", "sysctl-bbr"})
	}

	// disk headroom
	if total, free := diskUsage("/"); total > 0 {
		freePct := 100 * float64(free) / float64(total)
		switch {
		case freePct < 5:
			out = append(out, doctorCheck{"disk", "فضای دیسک", "fail",
				fmt.Sprintf("فقط %.0f%% آزاد است.", freePct), ""})
		case freePct < 15:
			out = append(out, doctorCheck{"disk", "فضای دیسک", "warn",
				fmt.Sprintf("%.0f%% آزاد است.", freePct), ""})
		default:
			out = append(out, doctorCheck{"disk", "فضای دیسک", "ok",
				fmt.Sprintf("%.0f%% آزاد است.", freePct), ""})
		}
	}

	// panel certificate expiry (the self-signed / uploaded one; ACME renews itself)
	if cfg.ACMEDomain != "" {
		out = append(out, doctorCheck{"cert", "گواهی TLS", "ok", "صدور خودکار Let's Encrypt فعال است.", ""})
	} else if c := certExpiry(cfg.CertPEM); !c.IsZero() {
		d := time.Until(c)
		switch {
		case d < 0:
			out = append(out, doctorCheck{"cert", "گواهی TLS", "fail", "گواهی منقضی شده است.", ""})
		case d < 15*24*time.Hour:
			out = append(out, doctorCheck{"cert", "گواهی TLS", "warn",
				fmt.Sprintf("تا %d روز دیگر منقضی می‌شود.", int(d.Hours()/24)), ""})
		default:
			out = append(out, doctorCheck{"cert", "گواهی TLS", "ok",
				fmt.Sprintf("تا %d روز دیگر معتبر است.", int(d.Hours()/24)), ""})
		}
	}

	// clock sync (a wrong clock breaks TLS certificate checks)
	out = append(out, s.timeSyncCheck())

	// outbound HTTPS, needed for updates and ACME
	if dialOK("1.1.1.1:443", 4*time.Second) {
		out = append(out, doctorCheck{"outbound", "دسترسی خروجی HTTPS", "ok", "برقرار است.", ""})
	} else {
		out = append(out, doctorCheck{"outbound", "دسترسی خروجی HTTPS", "warn",
			"اتصال خروجی روی ۴۴۳ برقرار نشد؛ آپدیت و صدور گواهی ممکن است کار نکند.", ""})
	}
	return out
}

func (s *panelServer) timeSyncCheck() doctorCheck {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := s.run(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
	v := strings.TrimSpace(string(out))
	if err != nil {
		return doctorCheck{"clock", "همگام‌سازی ساعت", "warn", "وضعیت ساعت خوانده نشد.", ""}
	}
	if v == "yes" {
		return doctorCheck{"clock", "همگام‌سازی ساعت", "ok", "ساعت با NTP همگام است.", ""}
	}
	return doctorCheck{"clock", "همگام‌سازی ساعت", "warn", "ساعت با NTP همگام نیست.", ""}
}

func (s *panelServer) handleDoctorRepair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fix string `json:"fix"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	var err error
	switch {
	case req.Fix == "write-template":
		err = s.ensureTemplateUnit()
	case req.Fix == "sysctl-forward":
		err = s.applySysctl("net.ipv4.ip_forward=1")
	case req.Fix == "sysctl-bbr":
		err = s.applySysctl("net.ipv4.tcp_congestion_control=bbr")
	case strings.HasPrefix(req.Fix, "restart:"):
		id := strings.TrimPrefix(req.Fix, "restart:")
		t, ok := s.tunnelByID(id)
		if !ok || !t.Managed {
			jsonError(w, http.StatusBadRequest, "no such managed tunnel")
			return
		}
		err = s.systemctl("restart", t.Unit)
	default:
		jsonError(w, http.StatusBadRequest, "unknown repair")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "doctor", "repaired "+req.Fix)
	jsonOK(w, map[string]any{"ok": true, "checks": s.runDoctor()})
}

func (s *panelServer) applySysctl(kv string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := s.run(ctx, "sysctl", "-w", kv)
	if err != nil {
		return fmt.Errorf("sysctl %s: %v %s", kv, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func certExpiry(path string) time.Time {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return cert.NotAfter
}

func dialOK(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
