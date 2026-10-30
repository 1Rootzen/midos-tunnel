package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"golang.org/x/crypto/acme"
)

func TestValidDomain(t *testing.T) {
	good := []string{"example.com", "panel.example.com", "a.b.c.co", "x-y.example.org"}
	bad := []string{"", "localhost", "example", ".example.com", "example.com.", "ex ample.com",
		"-bad.example.com", "bad-.example.com", "203.0.113.7", "under_score.example.com"}
	for _, d := range good {
		if !validDomain(d) {
			t.Errorf("validDomain(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if validDomain(d) {
			t.Errorf("validDomain(%q) = true, want false", d)
		}
	}
}

func TestPanelTLS(t *testing.T) {
	dir := t.TempDir()
	cfg := &PanelConfig{CertPEM: dir + "/panel.crt", KeyPEM: dir + "/panel.key", Listen: "0.0.0.0:443"}

	// no domain -> self-signed certificate, no aux listener
	if err := writeSelfSignedCert(cfg.CertPEM, cfg.KeyPEM); err != nil {
		t.Fatal(err)
	}
	tc, start, err := panelTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(tc.Certificates) != 1 || tc.GetCertificate != nil || start != nil {
		t.Fatal("no-domain config should load a static certificate with no ACME")
	}

	// http-01 -> ACME certificates and a port-80 challenge starter
	cfg.ACMEDomain, cfg.ACMEMethod = "panel.example.com", "http-01"
	tc, start, err = panelTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tc.GetCertificate == nil || start == nil {
		t.Fatal("http-01 should use GetCertificate and return a starter")
	}
	for _, p := range tc.NextProtos {
		if p == acme.ALPNProto {
			t.Fatal("http-01 must not advertise the acme-tls/1 ALPN protocol")
		}
	}

	// tls-alpn-01 -> ACME over the TLS listener, no aux starter
	cfg.ACMEMethod = "tls-alpn-01"
	tc, start, err = panelTLS(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if start != nil {
		t.Fatal("tls-alpn-01 needs no separate listener")
	}
	found := false
	for _, p := range tc.NextProtos {
		if p == acme.ALPNProto {
			found = true
		}
	}
	if !found {
		t.Fatal("tls-alpn-01 must advertise the acme-tls/1 ALPN protocol")
	}
}

func TestHandleTLSSettings(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// a valid domain with http-01 is accepted and persisted
	if w := p.do("POST", "/api/settings/tls", map[string]string{"domain": "panel.example.com", "method": "http-01", "email": "me@example.com"}, true); w.Code != http.StatusOK {
		t.Fatalf("valid http-01 settings answered %d", w.Code)
	}
	if got := p.s.config().ACMEDomain; got != "panel.example.com" {
		t.Fatalf("domain not saved: %q", got)
	}

	// session should now report the domain back to the UI
	var sess map[string]any
	_ = json.NewDecoder(p.do("GET", "/api/session", nil, false).Body).Decode(&sess)
	if sess["acme_domain"] != "panel.example.com" || sess["acme_method"] != "http-01" {
		t.Fatalf("session did not reflect acme settings: %v", sess)
	}

	// an IP, not a name, is rejected
	if w := p.do("POST", "/api/settings/tls", map[string]string{"domain": "203.0.113.7", "method": "http-01"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("IP domain answered %d, want 400", w.Code)
	}

	// tls-alpn-01 is refused unless the panel listens on 443 (the test panel is on :1)
	if w := p.do("POST", "/api/settings/tls", map[string]string{"domain": "panel.example.com", "method": "tls-alpn-01"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("tls-alpn-01 on non-443 answered %d, want 400", w.Code)
	}

	// an empty domain turns automatic TLS back off
	if w := p.do("POST", "/api/settings/tls", map[string]string{"domain": ""}, true); w.Code != http.StatusOK {
		t.Fatalf("clearing the domain answered %d", w.Code)
	}
	if p.s.config().ACMEDomain != "" {
		t.Fatal("domain was not cleared")
	}
}
