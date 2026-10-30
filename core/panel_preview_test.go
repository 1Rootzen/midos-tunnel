package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TestPanelPreview serves the panel with live data for looking at it in a
// browser. It runs only with MIDOS_PANEL_PREVIEW set, and as a test file it is
// never part of the shipped binary - including its pre-made session.
func TestPanelPreview(t *testing.T) {
	if os.Getenv("MIDOS_PANEL_PREVIEW") == "" {
		t.Skip("set MIDOS_PANEL_PREVIEW=1 to serve a preview")
	}
	// a real relay pair, so the pages show real carriers and traffic
	svc, local, relay := freePort(t), freePort(t), freePort(t)
	echoServer(t, svc)
	srv := &Config{Mode: "server", PSK: testPSK, TCPListen: fmt.Sprintf("127.0.0.1:%d", relay),
		Targets: "127.0.0.1", Ports: []string{fmt.Sprint(svc)}}
	cliCfg := &Config{Mode: "client", PSK: testPSK, Name: "uk-london", Carriers: 2,
		Endpoints:   []Endpoint{{"tcp", fmt.Sprintf("127.0.0.1:%d", relay)}, {"kcp", "198.51.100.21:4000"}},
		Ports:       []string{fmt.Sprintf("%d:%d", local, svc)},
		StatsListen: "127.0.0.1:39150"}
	startPair(t, srv, cliCfg)
	for i := 0; i < 5; i++ {
		relayRoundTrip(t, local, 512*1024)
	}

	dir := t.TempDir()
	tunnels := filepath.Join(dir, "tunnels")
	_ = os.MkdirAll(tunnels, 0o700)
	if err := writeTunnelConfig(filepath.Join(tunnels, "uk-london.json"), cliCfg); err != nil {
		t.Fatal(err)
	}
	_ = writeTunnelConfig(filepath.Join(tunnels, "us-dallas.json"), &Config{Mode: "client", PSK: testPSK, Name: "us-dallas",
		Endpoints: []Endpoint{{"tls", "198.51.100.22:443"}}, Ports: []string{"57144", "54294"}, UDPPorts: []string{"54294"},
		StatsListen: "127.0.0.1:39151"})

	hash, _ := bcrypt.GenerateFromPassword([]byte("preview-password"), bcrypt.MinCost)
	cfg := &PanelConfig{Listen: "127.0.0.1:47777", BasePath: "/preview1234", Username: "admin",
		PasswordHash: string(hash), TunnelsDir: tunnels, PublicHost: "203.0.113.20"}
	s := newPanelServer(filepath.Join(dir, "panel.json"), cfg, func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && len(args) > 1 && args[0] == "show" {
			if strings.Contains(args[1], "us-dallas") {
				return []byte("ActiveState=failed\nSubState=failed\nUnitFileState=enabled\nNRestarts=3\n"), nil
			}
			return []byte("ActiveState=active\nSubState=running\nUnitFileState=enabled\nNRestarts=0\n"), nil
		}
		if name == "journalctl" {
			return []byte("2026-10-27T15:00:01+0000 tunnel: relaying :40001 -> remote 40001\n2026-10-27T15:00:02+0000 tunnel: switched to endpoint 1 (tcp 127.0.0.1)\n"), nil
		}
		return nil, nil
	})
	// a mock kernel tunnel (ESP hub with two kharej peers) for the kernel page
	kpath := filepath.Join(dir, "kernel-config")
	_ = os.WriteFile(kpath, []byte("ROLE=IRAN\nTUNNEL_MODE=esp\nSELF_IP=203.0.113.20\nBALANCE_MODE=ports\nTUNNEL_MTU=1360\nTUNING_PROFILE=balanced\nSHAPE_MBIT=0\nKEEP_CLIENT_IP=1\nINSTALL_DATE='2026-10-22 10:50:44'\nPEER_COUNT=2\n"+
		"PEER_1_NAME='kharej1'\nPEER_1_IP='198.51.100.21'\nPEER_1_IFACE='midosMOI1'\nPEER_1_REMOTE='11.10.10.2'\nPEER_1_TCP='443,8443'\nPEER_1_UDP='443,8443'\nPEER_1_ENABLED='1'\n"+
		"PEER_2_NAME='kharej2'\nPEER_2_IP='198.51.100.23'\nPEER_2_IFACE='midosMOI2'\nPEER_2_REMOTE='11.10.10.6'\nPEER_2_TCP='2096'\nPEER_2_UDP=''\nPEER_2_ENABLED='0'\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "kernel-health"), []byte("1 up\n2 down\n"), 0o600)
	kernelConfigPathVar = kpath
	kernelHealthPathVar = filepath.Join(dir, "kernel-health")

	s.unitDir = filepath.Join(dir, "units")
	s.sys = &sysSampler{iface: "eth0"}
	now := time.Now().Unix()
	for i := 0; i < sysHistory; i++ {
		f := float64(i)
		s.sys.hist = append(s.sys.hist, sysPoint{T: now - int64(sysHistory-i)*2,
			CPU: 18 + 9*math.Sin(f/7), Rx: 2.6e6 + 1.1e6*math.Sin(f/5), Tx: 1.2e6 + 0.5e6*math.Sin(f/4+1)})
	}
	// a richer security state for the settings page
	next := *cfg
	next.TelegramToken, next.TelegramChat = "123456:demo-token", "987654321"
	s.cfg = &next
	// some audit history
	for i, e := range []struct{ ev, d string }{{"panel-start", "2.1.0"}, {"login", ""}, {"tunnel-create", "uk-london"}, {"login-failed", ""}, {"telegram", "settings changed"}} {
		_ = i
		s.audit(nil, e.ev, e.d)
	}

	previewFleet(s)

	session := strings.Repeat("ab", 32)
	s.sessions[session] = time.Now().Add(time.Hour)

	cert, err := selfSigned("localhost")
	if err != nil {
		t.Fatal(err)
	}
	h := s.routes()
	server := &http.Server{Addr: cfg.Listen, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if os.Getenv("MIDOS_PANEL_PREVIEW") != "login" {
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
			}
			h.ServeHTTP(w, r)
		})}
	go func() { _ = server.ListenAndServeTLS("", "") }()
	t.Logf("preview on https://%s%s/", cfg.Listen, cfg.BasePath)
	time.Sleep(2 * time.Hour)
}
