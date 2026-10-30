package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

// previewHost answers the fleet's facts and stats scripts with made-up but
// moving numbers, so the servers page and designer look alive in a preview.
type previewHost struct {
	seed  float64
	facts string
	tuns  []string // "name active carriers rtt"
	kern  string
}

func (h previewHost) Run(_ context.Context, cmd string, _ io.Reader, out io.Writer) error {
	switch {
	case strings.Contains(cmd, `echo "hostname=`):
		_, _ = io.WriteString(out, h.facts)
	case strings.Contains(cmd, "/proc/stat"):
		t := float64(time.Now().Unix())
		busy := uint64(t*100*0.25 + 400*math.Sin(t/40+h.seed))
		total := uint64(t * 100)
		rx := uint64(t * (3e6 + 1e6*h.seed))
		tx := uint64(t * (1e6 + 4e5*h.seed))
		fmt.Fprintf(out, "cpu cpu  %d 0 0 %d 0 0 0 0 0 0\n", busy, total-busy)
		fmt.Fprintf(out, "mem 4100000000 %d\n", uint64(1.3e9+4e8*h.seed))
		fmt.Fprintf(out, "disk 42000000000 %d\n", uint64(9e9+5e9*h.seed))
		fmt.Fprintf(out, "load 0.%d1 0.%d4 0.18\nup %d\n", int(h.seed*7)%10, int(h.seed*3)%10, 86400*3+int(h.seed*50000))
		fmt.Fprintf(out, "net %d %d\n", rx, tx)
		for _, x := range h.tuns {
			f := strings.Fields(x)
			fmt.Fprintf(out, "tun %s %s {\"carriers\":[%s]}\n", f[0], f[1], strings.Repeat(`{"transport":"tls","rtt_ms":`+f[3]+`},`, int(f[2][0]-'0'))+`{"transport":"tls","rtt_ms":`+f[3]+`}`)
		}
		if h.kern != "" {
			fmt.Fprintf(out, "kernel %s\n", h.kern)
		}
	}
	return nil
}

func previewFacts(host, os, cpu string, cores int, mem string) string {
	return fmt.Sprintf("hostname=%s\nos=%s\nkernel=6.8.0-49-generic\narch=x86_64\nvirt=kvm\ncpu=%s\ncores=%d\nmem=%s\ndisk=42000000000\nagent=%s\nuid=0\n", host, os, cpu, cores, mem, version)
}

// previewFleet puts a mother with three working servers, one being installed
// and one that failed onto the preview panel.
func previewFleet(s *panelServer) {
	f1 := previewFacts("ir-tehran-1", "Ubuntu 24.04.1 LTS", "Intel Xeon E5-2680 v4 @ 2.40GHz", 4, "8200000000")
	f2 := previewFacts("de-fsn1", "Debian GNU/Linux 12 (bookworm)", "AMD EPYC 7763 64-Core Processor", 2, "4100000000")
	f3 := previewFacts("nl-ams", "Ubuntu 22.04.5 LTS", "AMD EPYC-Rome Processor", 2, "2050000000")
	hosts := map[string]hostExec{
		"self":          previewHost{seed: 0.2, facts: previewFacts("mother", "Ubuntu 24.04.1 LTS", "Intel Xeon Gold 6230", 2, "4100000000")},
		"203.0.113.11":  previewHost{seed: 0.6, facts: f1, tuns: []string{"fl-ir-teh-de-fsn-k3abc active 2 48.5"}, kern: "active"},
		"198.51.100.40": previewHost{seed: 0.9, facts: f2, tuns: []string{"fl-ir-teh-de-fsn-k3abc active 2 48.5"}},
		"198.51.100.7":  previewHost{seed: 0.4, facts: f3, kern: "active"},
	}
	fleetExecFor = func(_ *panelServer, n topoNode) hostExec {
		if n.Kind == "self" {
			return hosts["self"]
		}
		if h, ok := hosts[n.IP]; ok {
			return h
		}
		return previewHost{}
	}
	pf := func(f string) *nodeFacts { x, _ := parseFacts(f); return &x }
	now := time.Now().Unix()
	t := topology{SelfRole: "none",
		Nodes: []topoNode{
			{ID: "self", Kind: "self", Label: "mother", X: 0, Y: 0},
			{ID: "nir1", Kind: "iran", Label: "ir-tehran", IP: "203.0.113.11", X: 300, Y: -100, SSHUser: "root", SSHPort: 22, State: "ready", Facts: pf(f1), HostKey: "SHA256:x", AddedAt: now - 86400},
			{ID: "nkh1", Kind: "kharej", Label: "de-hetzner", IP: "198.51.100.40", X: -300, Y: -160, SSHUser: "root", SSHPort: 22, State: "ready", Facts: pf(f2), HostKey: "SHA256:y", AddedAt: now - 80000},
			{ID: "nkh2", Kind: "kharej", Label: "nl-vultr", IP: "198.51.100.7", X: -300, Y: 60, SSHUser: "root", SSHPort: 22, State: "ready", Facts: pf(f3), HostKey: "SHA256:z", AddedAt: now - 70000},
			{ID: "nir2", Kind: "iran", Label: "ir-mashhad", IP: "203.0.113.33", X: 300, Y: 140, SSHUser: "root", SSHPort: 22, State: "installing", AddedAt: now - 60},
			{ID: "nkh3", Kind: "kharej", Label: "fi-helsinki", IP: "198.51.100.9", X: -300, Y: 260, SSHUser: "root", SSHPort: 2222, State: "error", Error: "اتصال SSH به 198.51.100.9:2222 ناموفق بود: connection refused", AddedAt: now - 30},
		},
		Links: []topoLink{
			{ID: "lk3abc", A: "nir1", B: "nkh1", Kind: "direct", Transport: "tls", Port: 443, Ports: "443, 2053", Carriers: 2, Preset: "balance", Tunnel: "fl-ir-teh-de-fsn-k3abc"},
			{ID: "lk4def", A: "nir1", B: "nkh2", Kind: "kernel", Mode: "esp", Ports: "8443", Tunnel: "kernel", PSK: "preview-psk-123", Slot: 1},
			{ID: "lk5ghi", A: "nir2", B: "nkh2", Kind: "direct", Transport: "kcp", Port: 4000, Ports: "443", Carriers: 2, Reverse: true},
		},
	}
	_ = s.saveTopology(&t)

	job := &fleetJob{started: time.Now().Add(-70 * time.Second)}
	for _, st := range []struct{ n, s, note string }{
		{"اتصال SSH و بررسی دسترسی root", "ok", ""}, {"نصب کلید سرور مادر", "ok", "رمز SSH ذخیره نمی‌شود؛ مدیریت با کلید انجام می‌شود"},
		{"خواندن مشخصات سرور", "ok", "Ubuntu 22.04.5 LTS · x86_64"}, {"به‌روزرسانی سیستم‌عامل", "run", ""},
	} {
		i := job.begin(st.n)
		job.end(i, st.s, st.note)
	}
	_, _ = io.WriteString(&job.log, "Get:1 http://archive.ubuntu.com/ubuntu jammy-updates InRelease [128 kB]\nReading package lists...\nThe following packages will be upgraded:\n  libssl3 openssl systemd ...\nUnpacking openssl (3.0.2-0ubuntu1.18) ...\n")
	s.fleetJobs["nir2"] = job

	if raw, err := os.ReadFile("testdata/bench_real.txt"); err == nil {
		clean := cleanBench(string(raw))
		info, ioRuns, speed := parseBench(clean)
		for _, id := range []string{"nir1", "nkh1", "self"} {
			s.saveBench(id, benchResult{Host: "preview", At: now - 3600, OK: true, Info: info, IO: ioRuns, Speed: speed, Raw: clean})
		}
	}
}
