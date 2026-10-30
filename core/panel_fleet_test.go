package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHost stands in for one server reached over SSH: it records every
// command with its stdin and answers the few that the fleet reads back.
type fakeHost struct {
	mu    sync.Mutex
	name  string
	cmds  []string
	stdin []string
	fail  string // a command containing this fails
}

func (h *fakeHost) Run(_ context.Context, cmd string, stdin io.Reader, out io.Writer) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	in := ""
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	h.cmds, h.stdin = append(h.cmds, cmd), append(h.stdin, in)
	if h.fail != "" && strings.Contains(cmd, h.fail) {
		return io.ErrUnexpectedEOF
	}
	switch {
	case cmd == "id -u":
		_, _ = io.WriteString(out, "0\n")
	case strings.Contains(cmd, `echo "hostname=`):
		_, _ = io.WriteString(out, "hostname="+h.name+"\nos=Ubuntu 24.04 LTS\nkernel=6.8.0\narch=x86_64\nvirt=kvm\ncpu=AMD EPYC\ncores=2\nmem=4096000000\ndisk=40000000000\nagent=\nuid=0\n")
	}
	return nil
}

func (h *fakeHost) ran(sub string) (string, string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, c := range h.cmds {
		if strings.Contains(c, sub) {
			return c, h.stdin[i], true
		}
	}
	return "", "", false
}

type fakeFleet struct {
	mu    sync.Mutex
	hosts map[string]*fakeHost // by IP, "self" for the mother
	boot  []sshTarget
}

func (f *fakeFleet) host(key string) *fakeHost {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hosts[key] == nil {
		f.hosts[key] = &fakeHost{name: key}
	}
	return f.hosts[key]
}

func useFakeFleet(t *testing.T) *fakeFleet {
	f := &fakeFleet{hosts: map[string]*fakeHost{}}
	oldExec, oldBoot, oldScript := fleetExecFor, fleetBootstrap, kernelScriptPathVar
	fleetExecFor = func(_ *panelServer, n topoNode) hostExec {
		if n.Kind == "self" {
			return f.host("self")
		}
		return f.host(n.IP)
	}
	fleetBootstrap = func(tgt sshTarget) (hostExec, string, func(), error) {
		f.mu.Lock()
		f.boot = append(f.boot, tgt)
		f.mu.Unlock()
		return f.host(tgt.Host), "SHA256:pinned", func() {}, nil
	}
	script := filepath.Join(t.TempDir(), "Midos-tunnel.sh")
	_ = os.WriteFile(script, []byte("#!/bin/bash\necho installer\n"), 0o755)
	kernelScriptPathVar = script
	t.Cleanup(func() { fleetExecFor, fleetBootstrap, kernelScriptPathVar = oldExec, oldBoot, oldScript })
	return f
}

func waitJob(t *testing.T, p *testPanel, id string) *fleetJobView {
	t.Helper()
	for i := 0; i < 200; i++ {
		p.s.fleetMu.Lock()
		j := p.s.fleetJobs[id]
		p.s.fleetMu.Unlock()
		if j != nil {
			if v := j.view(); v.Done {
				return v
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the install job did not finish")
	return nil
}

func addServer(t *testing.T, p *testPanel, kind, ip string) string {
	t.Helper()
	w := p.do("POST", "/api/fleet/add", map[string]any{"kind": kind, "ip": ip, "label": kind + "-" + ip[len(ip)-2:],
		"password": "pw", "upgrade": true}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("add %s: %d %s", ip, w.Code, w.Body)
	}
	var r struct{ ID string }
	_ = json.NewDecoder(w.Body).Decode(&r)
	if v := waitJob(t, p, r.ID); !v.OK {
		t.Fatalf("install of %s failed: %+v", ip, v.Steps)
	}
	return r.ID
}

func TestFleetAddInstallsAndPinsTheServer(t *testing.T) {
	f := useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)

	// no password, no key: refused before any login
	if w := p.do("POST", "/api/fleet/add", map[string]any{"kind": "kharej", "ip": "198.51.100.20"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("missing login accepted: %d", w.Code)
	}
	id := addServer(t, p, "kharej", "198.51.100.20")
	h := f.host("198.51.100.20")

	if len(f.boot) != 1 || f.boot[0].Password != "pw" || f.boot[0].User != "root" || f.boot[0].Port != 22 {
		t.Fatalf("first login: %+v", f.boot)
	}
	line, _ := p.s.fleetAuthorizedLine()
	if cmd, _, ok := h.ran("authorized_keys"); !ok || !strings.Contains(cmd, strings.Fields(line)[1]) {
		t.Fatalf("the mother's key was not installed: %q", cmd)
	}
	for _, want := range []string{"upgrade", "install -y", "midos-tunnel.new", "prepare-kernel"} {
		if _, _, ok := h.ran(want); !ok {
			t.Errorf("no %q step ran", want)
		}
	}
	// the binary copied over is this one, checked against its own checksum
	_, sum, _ := selfBinary()
	if cmd, in, _ := h.ran("midos-tunnel.new"); !strings.Contains(cmd, sum) || len(in) == 0 {
		t.Errorf("binary upload: sum in cmd=%v, %d bytes", strings.Contains(cmd, sum), len(in))
	}

	topo := p.s.loadTopology()
	n, _ := nodeByID(topo, id)
	if n.State != "ready" || n.HostKey != "SHA256:pinned" || n.Facts == nil || n.Facts.OS != "Ubuntu 24.04 LTS" {
		t.Fatalf("node after install: %+v", n)
	}
	b, _ := os.ReadFile(p.s.topologyPath())
	if strings.Contains(string(b), `"pw"`) {
		t.Fatal("the SSH password was stored")
	}

	// the same server again by IP is refused
	if w := p.do("POST", "/api/fleet/add", map[string]any{"kind": "kharej", "ip": "198.51.100.20", "password": "x"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate server accepted: %d", w.Code)
	}
	// re-installing an added server needs no password: the mother's key is used
	w := p.do("POST", "/api/fleet/add", map[string]any{"id": id, "kind": "kharej", "ip": "198.51.100.20"}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("reinstall: %d %s", w.Code, w.Body)
	}
	if v := waitJob(t, p, id); !v.OK || len(f.boot) != 1 {
		t.Fatalf("reinstall used a password login (%d) or failed: %+v", len(f.boot), v.Steps)
	}
}

func TestFleetAddFailsWithoutRoot(t *testing.T) {
	f := useFakeFleet(t)
	h := f.host("198.51.100.30")
	h.fail = "id -u"
	p := newTestPanel(t)
	p.login(testPassword)
	w := p.do("POST", "/api/fleet/add", map[string]any{"kind": "iran", "ip": "198.51.100.30", "password": "pw"}, true)
	var r struct{ ID string }
	_ = json.NewDecoder(w.Body).Decode(&r)
	if v := waitJob(t, p, r.ID); v.OK {
		t.Fatal("install succeeded without a working login")
	}
	n, _ := nodeByID(p.s.loadTopology(), r.ID)
	if n.State != "error" || n.Error == "" {
		t.Fatalf("failed node: %+v", n)
	}
}

func TestFleetBrowserCannotForgeServerFields(t *testing.T) {
	useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)
	id := addServer(t, p, "kharej", "198.51.100.20")

	// the page saves its layout with a moved device, a forged state and a
	// changed address: the move sticks, the rest does not
	topo := p.s.loadTopology()
	body := map[string]any{"self_role": "iran", "nodes": []map[string]any{
		{"id": "self", "kind": "self", "x": 0, "y": 0},
		{"id": id, "kind": "kharej", "ip": "203.0.113.99", "x": 300, "y": 40, "state": "ready", "host_key": "SHA256:evil"},
	}, "links": []map[string]any{{"id": "l1", "a": "self", "b": id, "kind": "direct", "tunnel": "forged"}}}
	if w := p.do("PUT", "/api/topology", body, true); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	after := p.s.loadTopology()
	n, _ := nodeByID(after, id)
	before, _ := nodeByID(topo, id)
	if n.X != 300 || n.IP != before.IP || n.HostKey != "SHA256:pinned" || after.Links[0].Tunnel != "" {
		t.Fatalf("server-owned fields changed: %+v links %+v", n, after.Links)
	}
}

func TestFleetDirectCableBetweenTwoServers(t *testing.T) {
	f := useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)
	ir := addServer(t, p, "iran", "198.51.100.10")
	kh := addServer(t, p, "kharej", "198.51.100.20")
	_ = p.s.updateTopology(func(t *topology) error {
		t.Links = append(t.Links, topoLink{ID: "lab123", A: ir, B: kh, Kind: "direct", Transport: "tls", Port: 8443, Ports: "443, 2053", Carriers: 2})
		return nil
	})
	w := p.do("POST", "/api/fleet/apply", map[string]any{"id": "lab123"}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	l, _ := linkByID(p.s.loadTopology(), "lab123")
	if l.Tunnel == "" || !tunnelNameRE.MatchString(l.Tunnel) {
		t.Fatalf("cable tunnel name %q", l.Tunnel)
	}
	read := func(h *fakeHost) (*Config, string) {
		cmd, in, ok := h.ran("midos-tunnel@$NAME")
		if !ok {
			t.Fatalf("%s: no deploy", h.name)
		}
		var c Config
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("%s: config: %v", h.name, err)
		}
		return &c, cmd
	}
	ic, icmd := read(f.host("198.51.100.10"))
	kc, kcmd := read(f.host("198.51.100.20"))
	if ic.Mode != "client" || len(ic.Endpoints) != 1 || ic.Endpoints[0].Server != "198.51.100.20:8443" {
		t.Fatalf("iran end: %+v", ic)
	}
	if kc.Mode != "server" || kc.Listen != "0.0.0.0:8443" || kc.PSK != ic.PSK || kc.Name != ic.Name {
		t.Fatalf("kharej end: %+v", kc)
	}
	// each end checks its own listening ports and opens its firewall
	if !strings.Contains(kcmd, "for p in 8443; do") || !strings.Contains(kcmd, "8443/tcp") {
		t.Errorf("kharej ports: %s", kcmd[:200])
	}
	if !strings.Contains(icmd, "443 2053") || !strings.Contains(icmd, "443/tcp 2053/tcp") {
		t.Errorf("iran ports missing")
	}

	// removing the cable removes the tunnel on both servers
	if w := p.do("POST", "/api/fleet/unlink", map[string]any{"id": "lab123"}, true); w.Code != http.StatusOK {
		t.Fatalf("unlink: %d %s", w.Code, w.Body)
	}
	for _, ip := range []string{"198.51.100.10", "198.51.100.20"} {
		if cmd, _, ok := f.host(ip).ran("rm -f"); !ok || !strings.Contains(cmd, l.Tunnel) {
			t.Errorf("%s: tunnel not removed", ip)
		}
	}
	if len(p.s.loadTopology().Links) != 0 {
		t.Fatal("cable still on the map")
	}
}

func TestFleetCableNeedsReadyServers(t *testing.T) {
	useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)
	_ = p.s.updateTopology(func(t *topology) error {
		t.SelfRole = "iran"
		t.Nodes = []topoNode{{ID: "self", Kind: "self"}, {ID: "k1", Kind: "kharej", IP: "198.51.100.20"}}
		t.Links = []topoLink{{ID: "l1", A: "self", B: "k1", Kind: "direct", Ports: "443"}}
		return nil
	})
	if w := p.do("POST", "/api/fleet/apply", map[string]any{"id": "l1"}, true); w.Code != http.StatusBadGateway {
		t.Fatalf("a server that was never added took a tunnel: %d", w.Code)
	}
}

func TestFleetKernelHubKeepsSlots(t *testing.T) {
	f := useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)
	ir := addServer(t, p, "iran", "198.51.100.10")
	k1 := addServer(t, p, "kharej", "198.51.100.21")
	k2 := addServer(t, p, "kharej", "198.51.100.22")
	_ = p.s.updateTopology(func(t *topology) error {
		t.Links = append(t.Links,
			topoLink{ID: "la", A: ir, B: k1, Kind: "kernel", Mode: "esp", Ports: "443"},
			topoLink{ID: "lb", A: k2, B: ir, Kind: "kernel", Mode: "esp", Ports: "443"})
		return nil
	})
	if w := p.do("POST", "/api/fleet/apply", map[string]any{"id": "la"}, true); w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	hub, _, _ := f.host("198.51.100.10").ran(" provision")
	if !strings.Contains(hub, "MIDOS_ROLE='IRAN'") || !strings.Contains(hub, "198.51.100.21:") || !strings.Contains(hub, "198.51.100.22:") {
		t.Fatalf("hub provision: %s", hub)
	}
	topo := p.s.loadTopology()
	la, _ := linkByID(topo, "la")
	lb, _ := linkByID(topo, "lb")
	if la.Tunnel != "kernel" || lb.Tunnel != "kernel" || la.PSK == "" || la.PSK != lb.PSK || la.Slot == lb.Slot {
		t.Fatalf("kernel cables: %+v %+v", la, lb)
	}

	// dropping the first Kharej rebuilds the hub with the second, slot unchanged
	f.host("198.51.100.10").cmds = nil
	if w := p.do("POST", "/api/fleet/unlink", map[string]any{"id": "la"}, true); w.Code != http.StatusOK {
		t.Fatalf("unlink: %d %s", w.Code, w.Body)
	}
	if _, _, ok := f.host("198.51.100.21").ran(" teardown"); !ok {
		t.Error("the removed kharej was not torn down")
	}
	hub, _, _ = f.host("198.51.100.10").ran(" provision")
	if strings.Contains(hub, "198.51.100.21:") || !strings.Contains(hub, "198.51.100.22:kharej-22:"+itoa(lb.Slot)) {
		t.Fatalf("hub rebuild: %s", hub)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestParseStats(t *testing.T) {
	out := `cpu cpu  100 0 100 800 0 0 0 0 0 0
mem 4000 1000
disk 40000 10000
load 0.10 0.20 0.30
up 3600.5
net 1000 2000
tun fl-a-b-1 active {"name":"fl-a-b-1","carriers":[{"transport":"tls","rtt_ms":42.5},{"transport":"tls","rtt_ms":40}],"ports":[]}
tun old inactive
kernel active
`
	t0 := time.Unix(1000, 0)
	_, prev := parseStats(out, nil, t0)
	next := strings.Replace(strings.Replace(out, "cpu  100 0 100 800", "cpu  150 0 150 900", 1), "net 1000 2000", "net 11000 4000", 1)
	st, _ := parseStats(next, &prev, t0.Add(10*time.Second))
	if st.CPU < 49 || st.CPU > 51 || st.Rx != 1000 || st.Tx != 200 {
		t.Fatalf("rates: cpu %.1f rx %.0f tx %.0f", st.CPU, st.Rx, st.Tx)
	}
	// a counter that went backwards (a reboot) gives no reading, not a wrap
	if back, _ := parseStats(out, &rawSample{busy: 1 << 40, total: 10, at: t0}, t0.Add(time.Second)); back.CPU != 0 {
		t.Fatalf("cpu after a counter reset: %.1f", back.CPU)
	}
	if st.MemUsed != 1000 || st.DiskTotal != 40000 || st.Uptime != 3600 || st.Kernel != "active" {
		t.Fatalf("stats: %+v", st)
	}
	if len(st.Tunnels) != 2 || st.Tunnels[0].Carriers != 2 || st.Tunnels[0].RTT != 40 || st.Tunnels[1].Active != "inactive" {
		t.Fatalf("tunnels: %+v", st.Tunnels)
	}
}

func TestFleetDirectConfigsReverse(t *testing.T) {
	l := topoLink{ID: "l1", Kind: "direct", Transport: "kcp", Ports: "443", Reverse: true}
	ic, kc, iOpen, kOpen, err := fleetDirectConfigs(l, "fl-x", "198.51.100.10", "198.51.100.20")
	if err != nil {
		t.Fatal(err)
	}
	// reverse: Iran listens for the carrier, Kharej dials it
	if ic.KCPListen != "0.0.0.0:4000" || len(kc.Endpoints) != 1 || kc.Endpoints[0].Server != "198.51.100.10:4000" {
		t.Fatalf("reverse ends: iran %+v kharej %+v", ic, kc.Endpoints)
	}
	if strings.Join(iOpen, " ") != "4000/udp 443/tcp" || len(kOpen) != 0 {
		t.Fatalf("firewall: iran %v kharej %v", iOpen, kOpen)
	}
	if _, _, _, _, err := fleetDirectConfigs(topoLink{Transport: "tls"}, "x", "a", "b"); err == nil {
		t.Fatal("a cable without ports was accepted")
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"2.3.1", "2.3.0", true}, {"2.3.0", "2.3.0", false}, {"2.10.0", "2.9.9", true}, {"2.2.9", "2.3.0", false}} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
