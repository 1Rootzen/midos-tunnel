package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAuditTrail(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	// make an auditable change
	cfg := map[string]any{"mode": "client", "psk": testPSK,
		"endpoints": []map[string]string{{"transport": "tcp", "server": "1.2.3.4:443"}}, "ports": []string{"443"}}
	p.do("POST", "/api/tunnels", map[string]any{"name": "a1", "config": cfg}, true)

	var entries []auditEntry
	_ = json.NewDecoder(p.do("GET", "/api/audit", nil, false).Body).Decode(&entries)
	var sawLogin, sawCreate bool
	for _, e := range entries {
		if e.Event == "login" {
			sawLogin = true
		}
		if e.Event == "tunnel-create" && e.Detail == "a1" {
			sawCreate = true
		}
	}
	if !sawLogin || !sawCreate {
		t.Fatalf("audit missing events: %+v", entries)
	}
	// newest first
	if len(entries) > 1 && entries[0].Time < entries[len(entries)-1].Time {
		t.Fatal("audit not newest-first")
	}
}

func TestUploadCert(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// a bad pair is refused
	if w := p.do("POST", "/api/settings/cert", map[string]string{"cert": "x", "key": "y"}, true); w.Code != 400 {
		t.Fatalf("bad cert accepted: %d", w.Code)
	}

	certPEM, keyPEM := makeCertPair(t)
	w := p.do("POST", "/api/settings/cert", map[string]string{"cert": certPEM, "key": keyPEM}, true)
	if w.Code != 200 {
		t.Fatalf("upload cert: %d %s", w.Code, w.Body)
	}
	saved, err := os.ReadFile(p.s.config().CertPEM)
	if err != nil || !strings.Contains(string(saved), "BEGIN CERTIFICATE") {
		t.Fatalf("cert not saved: %v", err)
	}
}

func TestTelegramSettingsAndSessionFlags(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	if w := p.do("POST", "/api/settings/telegram", map[string]string{"token": "123:abc", "chat": "456"}, true); w.Code != 200 {
		t.Fatalf("telegram settings: %d", w.Code)
	}
	var sess map[string]any
	_ = json.NewDecoder(p.do("GET", "/api/session", nil, false).Body).Decode(&sess)
	if sess["telegram"] != true {
		t.Fatalf("session should report telegram on: %+v", sess)
	}
	// a test send with an unreachable token should fail cleanly, not panic
	w := p.do("POST", "/api/settings/telegram/test", nil, true)
	if w.Code == 200 {
		t.Log("telegram test unexpectedly succeeded (network available?)")
	}
}

func makeCertPair(t *testing.T) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}
