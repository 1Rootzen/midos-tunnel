package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type fakeSystem struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeSystem) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	f.mu.Unlock()
	if name == "systemctl" && len(args) > 0 && args[0] == "show" {
		return []byte("ActiveState=active\nSubState=running\nUnitFileState=enabled\nNRestarts=0\n"), nil
	}
	return nil, nil
}

func (f *fakeSystem) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type testPanel struct {
	t      *testing.T
	s      *panelServer
	sys    *fakeSystem
	h      http.Handler
	cookie string
	dir    string
}

const testPassword = "correct horse battery"

func newTestPanel(t *testing.T) *testPanel {
	t.Helper()
	dir := t.TempDir()
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	cfg := &PanelConfig{Listen: "127.0.0.1:1", BasePath: "/s3cretpath", Username: "admin",
		PasswordHash: string(hash), TunnelsDir: filepath.Join(dir, "tunnels"), PublicHost: "203.0.113.7",
		CertPEM: filepath.Join(dir, "panel.crt"), KeyPEM: filepath.Join(dir, "panel.key")}
	cfgPath := filepath.Join(dir, "panel.json")
	if err := savePanelConfig(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(cfg.TunnelsDir, 0o700)
	sys := &fakeSystem{}
	s := newPanelServer(cfgPath, cfg, sys.run)
	s.unitDir = filepath.Join(dir, "units")
	_ = os.MkdirAll(s.unitDir, 0o755)
	return &testPanel{t: t, s: s, sys: sys, h: s.routes(), dir: dir}
}

func (p *testPanel) do(method, path string, body any, header bool) *httptest.ResponseRecorder {
	p.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, "/s3cretpath"+path, r)
	req.RemoteAddr = "198.51.100.9:5000"
	if header {
		req.Header.Set("X-Midos", "1")
	}
	if p.cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: p.cookie})
	}
	w := httptest.NewRecorder()
	p.h.ServeHTTP(w, req)
	return w
}

func (p *testPanel) captcha() (string, string) {
	w := p.do("GET", "/api/captcha", nil, false)
	var c struct{ ID string }
	_ = json.NewDecoder(w.Body).Decode(&c)
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	return c.ID, p.s.captchas[c.ID].answer
}

func (p *testPanel) login(pass string) *httptest.ResponseRecorder {
	id, ans := p.captcha()
	w := p.do("POST", "/api/login", map[string]string{"username": "admin", "password": pass, "captcha_id": id, "captcha": ans}, true)
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			p.cookie = c.Value
		}
	}
	return w
}

func TestPanelHidesOutsideSecretPath(t *testing.T) {
	p := newTestPanel(t)
	for _, path := range []string{"/", "/api/session", "/admin", "/s3cretpat/"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		p.h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s answered %d, want 404", path, w.Code)
		}
	}
	if w := p.do("GET", "/", nil, false); w.Code != http.StatusOK {
		t.Fatalf("panel page: %d", w.Code)
	}
}

func TestPanelLogin(t *testing.T) {
	p := newTestPanel(t)
	if w := p.do("GET", "/api/session", nil, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("session without login: %d", w.Code)
	}
	// a wrong captcha fails even with the right password
	id, _ := p.captcha()
	if w := p.do("POST", "/api/login", map[string]string{"username": "admin", "password": testPassword, "captcha_id": id, "captcha": "XXXXX"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("wrong captcha: %d", w.Code)
	}
	if w := p.login("wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if w := p.login(testPassword); w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	if w := p.do("GET", "/api/session", nil, false); w.Code != http.StatusOK {
		t.Fatalf("session after login: %d", w.Code)
	}
	// a state change without the panel's header is refused even when logged in
	if w := p.do("POST", "/api/logout", nil, false); w.Code != http.StatusForbidden {
		t.Fatalf("logout without header: %d", w.Code)
	}
}

func TestPanelLockout(t *testing.T) {
	p := newTestPanel(t)
	for i := 0; i < maxLoginFail; i++ {
		p.login("wrong")
	}
	// the right password is refused while the address is locked out
	if w := p.login(testPassword); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: %d", maxLoginFail, w.Code)
	}
}

func TestPanelTunnelLifecycle(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// a bad name and a broken configuration are both refused
	if w := p.do("POST", "/api/tunnels", map[string]any{"name": "../x", "config": map[string]any{}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name: %d", w.Code)
	}
	if w := p.do("POST", "/api/tunnels", map[string]any{"name": "uk", "config": map[string]any{"mode": "client", "psk": "short"}}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bad config: %d", w.Code)
	}

	cfg := map[string]any{"mode": "client", "psk": testPSK,
		"endpoints": []map[string]string{{"transport": "tls", "server": "198.51.100.21:443"}, {"transport": "kcp", "server": "198.51.100.21:4000"}},
		"ports":     []string{"40001", "40002:50002"}, "udp_ports": []string{"40002:50002"}}
	w := p.do("POST", "/api/tunnels", map[string]any{"name": "uk", "config": cfg}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	path := filepath.Join(p.dir, "tunnels", "uk.json")
	saved, err := readTunnelConfig(path)
	if err != nil || saved.StatsListen == "" || saved.Name != "uk" {
		t.Fatalf("saved config: %+v %v", saved, err)
	}
	if !p.sys.called("systemctl enable --now midos-tunnel@uk.service") {
		t.Fatalf("unit not started: %v", p.sys.calls)
	}
	if w := p.do("POST", "/api/tunnels", map[string]any{"name": "uk", "config": cfg}, true); w.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", w.Code)
	}

	// a tunnel from before the panel shows up, but cannot be deleted
	legacyCfg := filepath.Join(p.dir, "veil-us.json")
	_ = os.WriteFile(legacyCfg, []byte(`{"mode":"server","psk":"`+testPSK+`","listen":"0.0.0.0:443","ports":["57144"]}`), 0o600)
	_ = os.WriteFile(filepath.Join(p.s.unitDir, "Midos-tunnel-veil-us.service"),
		[]byte("[Service]\nExecStart=/usr/local/sbin/midos-tunnel -config "+legacyCfg+"\n"), 0o644)
	var list []tunnelView
	_ = json.NewDecoder(p.do("GET", "/api/tunnels", nil, false).Body).Decode(&list)
	if len(list) != 2 {
		t.Fatalf("want 2 tunnels, got %d", len(list))
	}
	if w := p.do("DELETE", "/api/tunnels/Midos-tunnel-veil-us", nil, true); w.Code != http.StatusForbidden {
		t.Fatalf("delete legacy: %d", w.Code)
	}

	if w := p.do("POST", "/api/tunnels/uk/action", map[string]string{"action": "restart"}, true); w.Code != http.StatusOK {
		t.Fatalf("restart: %d", w.Code)
	}
	if w := p.do("POST", "/api/tunnels/uk/action", map[string]string{"action": "rm -rf"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bogus action: %d", w.Code)
	}

	// backup, delete, restore
	bw := p.do("GET", "/api/backup", nil, false)
	if bw.Code != http.StatusOK || bw.Body.Len() == 0 {
		t.Fatalf("backup: %d", bw.Code)
	}
	backup := bw.Body.Bytes()
	if w := p.do("DELETE", "/api/tunnels/uk", nil, true); w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config still on disk after delete")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "b.tar.gz")
	_, _ = fw.Write(backup)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/s3cretpath/api/restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Midos", "1")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: p.cookie})
	rw := httptest.NewRecorder()
	p.h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK || !strings.Contains(rw.Body.String(), `"uk","status":"restored"`) {
		t.Fatalf("restore: %d %s", rw.Code, rw.Body)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("restored tunnel missing")
	}
}

func TestPeerConfigForward(t *testing.T) {
	c := &Config{Mode: "client", PSK: testPSK, Name: "uk",
		Endpoints: []Endpoint{{"tls", "198.51.100.21:443"}, {"kcp", "198.51.100.21:4000"}},
		Ports:     []string{"40001", "40002:50002"}, UDPPorts: []string{"40002:50002"}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	p, open, err := peerConfig(c, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "server" || p.Listen != "0.0.0.0:443" || p.KCPListen != "0.0.0.0:4000" || p.dials() {
		t.Fatalf("peer listeners wrong: %+v", p)
	}
	if strings.Join(p.Ports, ",") != "40001,50002" || strings.Join(p.UDPPorts, ",") != "50002" {
		t.Fatalf("peer ports %v / %v", p.Ports, p.UDPPorts)
	}
	if strings.Join(open, " ") != "443/tcp 4000/udp" {
		t.Fatalf("firewall ports %v", open)
	}
}

func TestPeerConfigReverse(t *testing.T) {
	c := &Config{Mode: "client", PSK: testPSK, Name: "uk", Reverse: true,
		Listen: "0.0.0.0:8443", QUICListen: "0.0.0.0:8443", Ports: []string{"40001"}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	p, open, err := peerConfig(c, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if !p.dials() || len(p.Endpoints) != 2 || p.Endpoints[0].Server != "203.0.113.7:8443" || p.Endpoints[1].Transport != "quic" {
		t.Fatalf("reverse peer endpoints: %+v", p.Endpoints)
	}
	if len(open) != 0 {
		t.Fatalf("a dialing peer needs no open port, got %v", open)
	}
}

func TestPeerSetupEndpoints(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	cfg := map[string]any{"mode": "client", "psk": testPSK,
		"endpoints": []map[string]string{{"transport": "tls", "server": "198.51.100.21:443"}},
		"ports":     []string{"40011"}}
	if w := p.do("POST", "/api/tunnels", map[string]any{"name": "t2", "config": cfg}, true); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w := p.do("POST", "/api/tunnels/t2/peer", nil, true)
	var resp struct{ Command, Config string }
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !strings.Contains(resp.Command, "https://203.0.113.7:1/s3cretpath/peer/") {
		t.Fatalf("command: %q", resp.Command)
	}
	token := resp.Command[strings.Index(resp.Command, "/peer/")+6 : strings.LastIndex(resp.Command, "'")]

	// the token works without a session - the other server has none
	p.cookie = ""
	sw := p.do("GET", "/peer/"+token, nil, false)
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), "NAME='t2'") || !strings.Contains(sw.Body.String(), "sha256sum -c") {
		t.Fatalf("script: %d %s", sw.Code, sw.Body)
	}
	cw := p.do("GET", "/peer/"+token+"/config", nil, false)
	var pc Config
	if err := json.NewDecoder(cw.Body).Decode(&pc); err != nil || pc.Mode != "server" || pc.PSK != testPSK {
		t.Fatalf("peer config: %+v %v", pc, err)
	}
	if w := p.do("GET", "/peer/0000/config", nil, false); w.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", w.Code)
	}
}

func TestChangePasswordEndsOtherSessions(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	first := p.cookie
	p.login(testPassword)
	second := p.cookie
	if w := p.do("POST", "/api/settings/password", map[string]string{"current": testPassword, "new": "a much better password"}, true); w.Code != http.StatusOK {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	p.cookie = first
	if w := p.do("GET", "/api/session", nil, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("old session survived: %d", w.Code)
	}
	p.cookie = second
	if w := p.do("GET", "/api/session", nil, false); w.Code != http.StatusOK {
		t.Fatalf("own session ended: %d", w.Code)
	}
}
