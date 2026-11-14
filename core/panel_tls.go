package main

// Automatic TLS. With a domain name set, the panel obtains a real certificate
// from Let's Encrypt on first use and renews it on its own, so an operator
// never has to touch a certificate file. Two validation methods are offered:
//
//	http-01     Let's Encrypt connects to port 80. The panel answers the
//	            challenge there and redirects everything else to itself. The
//	            panel can stay on any port.
//	tls-alpn-01 the challenge rides the TLS listener, so no port 80 is needed,
//	            but Let's Encrypt connects to port 443 - the panel must be on 443.
//
// With no domain set, the panel loads the self-signed or uploaded certificate
// exactly as before, so nothing changes for an IP-only install.

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// acmeDir caches issued certificates and the ACME account key next to the panel
// config, so a restart reuses them instead of asking Let's Encrypt again (which
// is rate-limited).
func acmeDir(cfg *PanelConfig) string {
	return filepath.Join(filepath.Dir(cfg.CertPEM), "acme")
}

// acmeManager builds the autocert manager for the configured domain, or nil
// when automatic TLS is off.
func acmeManager(cfg *PanelConfig) *autocert.Manager {
	if cfg.ACMEDomain == "" {
		return nil
	}
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(cfg.ACMEDomain),
		Cache:      autocert.DirCache(acmeDir(cfg)),
		Email:      strings.TrimSpace(cfg.ACMEEmail),
	}
}

// panelTLS builds the server's TLS config. With a domain set it serves
// Let's Encrypt certificates; otherwise it loads the self-signed or uploaded
// certificate as before. When the HTTP-01 method is chosen it also returns a
// function the caller must run to answer the challenge on port 80.
func panelTLS(cfg *PanelConfig) (*tls.Config, func(), error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12}
	m := acmeManager(cfg)
	if m == nil {
		cert, err := tls.LoadX509KeyPair(cfg.CertPEM, cfg.KeyPEM)
		if err != nil {
			return nil, nil, fmt.Errorf("certificate: %w", err)
		}
		t.Certificates = []tls.Certificate{cert}
		return t, nil, nil
	}
	// The domain gets its Let's Encrypt certificate. Anything else - the
	// panel opened by IP, or a name that is not the domain - gets the panel's
	// own certificate rather than a refused handshake, so the panel stays
	// reachable when the domain's DNS points elsewhere or the server's IP
	// changed.
	own, ownErr := tls.LoadX509KeyPair(cfg.CertPEM, cfg.KeyPEM)
	if ownErr != nil {
		if gen, err := selfSigned("localhost"); err == nil {
			own, ownErr = gen, nil
		}
	}
	t.GetCertificate = func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if ownErr == nil && !strings.EqualFold(strings.TrimSuffix(h.ServerName, "."), cfg.ACMEDomain) {
			return &own, nil
		}
		return m.GetCertificate(h)
	}
	if cfg.ACMEMethod == "tls-alpn-01" {
		t.NextProtos = []string{"h2", "http/1.1", acme.ALPNProto}
		return t, nil, nil
	}
	t.NextProtos = []string{"h2", "http/1.1"}
	start := func() {
		// HTTPHandler answers the challenge and 302s anything else to https.
		if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
			log.Printf("acme http-01 listener on :80: %v", err)
		}
	}
	return t, start, nil
}

// validDomain accepts a dotted host name (not an IP) of valid-looking labels.
// It is a sanity check, not a full RFC validator; Let's Encrypt does the real
// one when it tries to issue.
func validDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return false
	}
	if net.ParseIP(d) != nil {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (s *panelServer) handleTLSSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
		Method string `json:"method"`
		Email  string `json:"email"`
		// Restart asks the panel to restart itself once the answer is sent,
		// so the new certificate is used without a trip to the shell.
		Restart bool `json:"restart"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	req.Domain = strings.TrimSpace(strings.ToLower(req.Domain))
	if req.Domain != "" {
		if !validDomain(req.Domain) {
			jsonError(w, http.StatusBadRequest, "domain name is not valid (use a name like panel.example.com, not an IP)")
			return
		}
		if req.Method == "" {
			req.Method = "http-01"
		}
		if req.Method != "http-01" && req.Method != "tls-alpn-01" {
			jsonError(w, http.StatusBadRequest, "method must be http-01 or tls-alpn-01")
			return
		}
		if req.Method == "tls-alpn-01" {
			if _, port, _ := net.SplitHostPort(s.config().Listen); port != "443" {
				jsonError(w, http.StatusBadRequest, "TLS-ALPN validates on port 443, so the panel must listen on 443 first")
				return
			}
		}
	}
	s.mu.Lock()
	next := *s.cfg
	next.ACMEDomain = req.Domain
	next.ACMEMethod = req.Method
	next.ACMEEmail = strings.TrimSpace(req.Email)
	if err := savePanelConfig(s.cfgPath, &next); err != nil {
		s.mu.Unlock()
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg = &next
	s.mu.Unlock()
	if req.Restart {
		go func() {
			time.Sleep(time.Second)
			_ = s.systemctl("restart", "midos-tunnel-panel.service")
		}()
	}
	if req.Domain == "" {
		s.audit(r, "panel-tls", "auto-ssl off")
		jsonOK(w, map[string]string{"note": "automatic SSL is off; restart the panel to go back to the self-signed certificate: systemctl restart midos-tunnel-panel"})
		return
	}
	s.audit(r, "panel-tls", "auto-ssl "+req.Domain+" ("+req.Method+")")
	jsonOK(w, map[string]string{"note": "saved. Point " + req.Domain + " at this server, then restart the panel and open https://" + req.Domain + " — the certificate is issued on the first visit: systemctl restart midos-tunnel-panel"})
}
