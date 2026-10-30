package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFleetLive adds a real server to a throwaway fleet over SSH, the way the
// panel does: password login, the mother's key, key-only login, facts,
// prerequisites, live stats, one loopback-only tunnel, then removes the server
// again and takes the key back off it. The system is not upgraded and no
// firewall rule is touched. The server must already run this release of
// midos-tunnel, so nothing is uploaded. Skipped unless MIDOS_FLEET_LIVE is set:
//
//	MIDOS_FLEET_LIVE=1 MIDOS_SSH_HOST=1.2.3.4 MIDOS_SSH_PASS=... go test -run TestFleetLive -v
func TestFleetLive(t *testing.T) {
	if os.Getenv("MIDOS_FLEET_LIVE") == "" {
		t.Skip("set MIDOS_FLEET_LIVE=1 (and MIDOS_SSH_HOST/PASS) to run the live fleet test")
	}
	host, pass := os.Getenv("MIDOS_SSH_HOST"), os.Getenv("MIDOS_SSH_PASS")
	old := kernelScriptPathVar
	kernelScriptPathVar = filepath.Join(t.TempDir(), "none") // skip the kernel base
	defer func() { kernelScriptPathVar = old }()

	// never push this test binary: the server must already be on this release
	bx, _, closeFn, err := fleetBootstrap(sshTarget{Host: host, Port: 22, User: "root", Password: pass})
	if err != nil {
		t.Fatal(err)
	}
	ver, _ := runOut(context.Background(), bx, remoteBin+" -version 2>&1 | awk '{print $2}'")
	if ver != version {
		closeFn()
		t.Fatalf("the server runs midos-tunnel %q, not %s: deploy the release first", ver, version)
	}

	p := newTestPanel(t)
	p.login(testPassword)
	w := p.do("POST", "/api/fleet/add", map[string]any{"kind": "kharej", "ip": host, "label": "live", "password": pass}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	var r struct{ ID string }
	_ = json.NewDecoder(w.Body).Decode(&r)
	var v *fleetJobView
	for i := 0; i < 600; i++ {
		p.s.fleetMu.Lock()
		j := p.s.fleetJobs[r.ID]
		p.s.fleetMu.Unlock()
		if v = j.view(); v.Done {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, s := range v.Steps {
		t.Logf("step %-5s %s  %s", s.State, s.Name, s.Note)
	}
	if !v.OK {
		t.Fatalf("install failed:\n%s", v.Tail)
	}
	n, _ := nodeByID(p.s.loadTopology(), r.ID)
	t.Logf("facts: %+v host key %s", *n.Facts, n.HostKey)

	// live stats over the pooled key connection (two readings for the rates)
	p.s.fleetSample()
	time.Sleep(2 * time.Second)
	p.s.fleetSample()
	st := p.s.fleetStats[r.ID]
	t.Logf("stats: cpu %.1f%% mem %d/%d disk %d/%d rx %.0f tx %.0f load %s up %ds err %q",
		st.CPU, st.MemUsed, st.MemTotal, st.DiskUsed, st.DiskTotal, st.Rx, st.Tx, st.Load, st.Uptime, st.Error)
	if st.Error != "" || st.MemTotal == 0 || st.DiskTotal == 0 || st.Uptime == 0 {
		t.Fatalf("bad stats: %+v", st)
	}

	// a tunnel end on loopback only: deployed, seen running, removed
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	end := fleetEnd{node: n, host: host, ex: fleetExecFor(p.s, n)}
	c, err := validated(&Config{Mode: "server", PSK: testPSK, Name: "fl-livetest", TCPListen: "127.0.0.1:47991",
		Targets: "127.0.0.1", Ports: []string{"47992"}, StatsListen: "127.0.0.1:38999"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := deployTunnel(ctx, end, "fl-livetest", c, nil)
	t.Logf("deploy:\n%s", out)
	if err != nil || !strings.Contains(out, "is running") {
		t.Fatalf("deploy: %v", err)
	}
	time.Sleep(2 * time.Second)
	p.s.fleetSample()
	found := false
	for _, x := range p.s.fleetStats[r.ID].Tunnels {
		if x.Name == "fl-livetest" {
			found = x.Active == "active"
			t.Logf("tunnel seen: %+v", x)
		}
	}
	if out, err := removeTunnelOn(ctx, end, "fl-livetest"); err != nil {
		t.Errorf("remove tunnel: %v %s", err, out)
	}
	if !found {
		t.Error("the deployed tunnel was not reported active")
	}

	// remove the server and take the key back off it
	if w := p.do("POST", "/api/fleet/remove", map[string]any{"id": r.ID, "purge": true}, true); w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	line, _ := p.s.fleetAuthorizedLine()
	left, _ := runOut(ctx, bx, "grep -cF "+shQuote(strings.Fields(line)[1])+" ~/.ssh/authorized_keys || true")
	stillThere, _ := runOut(ctx, bx, "test -e /etc/Midos-tunnel/tunnels/fl-livetest.json && echo yes || echo no")
	closeFn()
	if left != "0" || stillThere != "no" {
		t.Fatalf("cleanup: key lines left %q, tunnel file left %q", left, stillThere)
	}
}
