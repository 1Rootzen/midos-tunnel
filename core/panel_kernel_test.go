package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestShellConfigParse(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config"
	body := `# Midos Tunnel configuration - generated 2026-10-27
CONFIG_VERSION=3
ROLE=IRAN
TUNNEL_MODE=esp
IPSEC_PSK='a secret with spaces'
PEER_COUNT=2
# --- peer 1: kharej1 ---
PEER_1_NAME='kharej1'
PEER_1_REMOTE='11.10.10.2'
PEER_1_ENABLED='1'
PEER_2_NAME='kharej2'
PEER_2_ENABLED='0'
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := shellConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c["ROLE"] != "IRAN" || c["TUNNEL_MODE"] != "esp" || c["PEER_COUNT"] != "2" {
		t.Fatalf("basic keys wrong: %+v", c)
	}
	if c["IPSEC_PSK"] != "a secret with spaces" {
		t.Fatalf("quoted value: %q", c["IPSEC_PSK"])
	}
	if c["PEER_1_NAME"] != "kharej1" || c["PEER_2_ENABLED"] != "0" {
		t.Fatalf("peer keys wrong: %+v", c)
	}
}

func TestKernelDocAndActions(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// no kernel tunnel on this host: installed=false, no error
	path := p.dir + "/kernel-config"
	oldPath := kernelConfigPathVar
	kernelConfigPathVar = path
	defer func() { kernelConfigPathVar = oldPath }()

	// base prerequisites not prepared yet: base_ready must be false
	oldMarker, oldScript := kernelBaseMarkerVar, kernelScriptPathVar
	kernelBaseMarkerVar = p.dir + "/kernel-base-ready"
	kernelScriptPathVar = p.dir + "/Midos-tunnel.sh"
	defer func() { kernelBaseMarkerVar, kernelScriptPathVar = oldMarker, oldScript }()

	var doc kernelDoc
	_ = json.NewDecoder(p.do("GET", "/api/kernel", nil, false).Body).Decode(&doc)
	if doc.Installed {
		t.Fatal("reported a kernel tunnel where there is none")
	}
	if doc.BaseReady {
		t.Fatal("base_ready true before prerequisites were prepared")
	}
	// once the marker exists, base_ready flips to true
	os.WriteFile(kernelBaseMarkerVar, []byte("1"), 0o600)
	doc = kernelDoc{}
	_ = json.NewDecoder(p.do("GET", "/api/kernel", nil, false).Body).Decode(&doc)
	if !doc.BaseReady {
		t.Fatal("base_ready false after the marker was written")
	}
	if w := p.do("POST", "/api/kernel/action", map[string]string{"action": "restart"}, true); w.Code != 404 {
		t.Fatalf("action without a tunnel: %d", w.Code)
	}

	os.WriteFile(path, []byte("ROLE=IRAN\nTUNNEL_MODE=esp\nSELF_IP=203.0.113.20\nBALANCE_MODE=ports\nPEER_COUNT=1\nPEER_1_NAME='uk'\nPEER_1_IP='198.51.100.21'\nPEER_1_IFACE='midosMOI1'\nPEER_1_REMOTE='11.10.10.2'\nPEER_1_ENABLED='1'\n"), 0o600)
	os.WriteFile(p.dir+"/kernel-health", []byte("1 up\n"), 0o600)
	oldHealth := kernelHealthPathVar
	kernelHealthPathVar = p.dir + "/kernel-health"
	defer func() { kernelHealthPathVar = oldHealth }()

	doc = kernelDoc{}
	_ = json.NewDecoder(p.do("GET", "/api/kernel", nil, false).Body).Decode(&doc)
	if !doc.Installed || doc.Role != "IRAN" || doc.Mode != "esp" || len(doc.Peers) != 1 {
		t.Fatalf("kernel doc: %+v", doc)
	}
	if doc.Peers[0].Name != "uk" || doc.Peers[0].Health != "up" {
		t.Fatalf("peer: %+v", doc.Peers[0])
	}
	if _, ok := doc.Units["Midos-tunnel.service"]; !ok {
		t.Fatalf("units missing: %+v", doc.Units)
	}

	if w := p.do("POST", "/api/kernel/action", map[string]string{"action": "restart"}, true); w.Code != 200 {
		t.Fatalf("restart: %d %s", w.Code, w.Body)
	}
	if !p.sys.called("systemctl restart Midos-tunnel.service") {
		t.Fatalf("main service not restarted: %v", p.sys.calls)
	}
	if w := p.do("POST", "/api/kernel/action", map[string]string{"action": "bogus"}, true); w.Code != 400 {
		t.Fatalf("bogus action: %d", w.Code)
	}
}
