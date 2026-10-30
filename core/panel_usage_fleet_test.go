package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// The kernel interface counters and relay byte counts in a fleet sample reach
// the stats.
func TestParseStatsKernelPeers(t *testing.T) {
	out := "kernel active\nkif 198.51.100.2 1000 234\nkif 198.51.100.3 7 8\n" +
		`tun fl-x active {"carriers":[{"rtt_ms":40}],"ports":[{"bytes_up":300,"bytes_down":200}]}` + "\n"
	st, _ := parseStats(out, nil, time.Now())
	if st.KernelPeers["198.51.100.2"] != 1234 || st.KernelPeers["198.51.100.3"] != 15 {
		t.Fatalf("kernel peers: %v", st.KernelPeers)
	}
	if len(st.Tunnels) != 1 || st.Tunnels[0].Bytes != 500 {
		t.Fatalf("tunnels: %+v", st.Tunnels)
	}
}

// Cables between fleet servers are counted from the samples and listed on the
// traffic page; a relay with this server at one end is left to the local count.
func TestFleetCableUsage(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	p.s.usage = newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	topo := topology{SelfRole: "iran",
		Nodes: []topoNode{
			{ID: "self", Kind: "self"},
			{ID: "hub", Kind: "iran", Label: "Tehran", IP: "198.51.100.1", State: "ready"},
			{ID: "k1", Kind: "kharej", Label: "London", IP: "198.51.100.2", State: "ready"},
			{ID: "k2", Kind: "kharej", Label: "Dallas", IP: "198.51.100.3", State: "ready"},
		},
		Links: []topoLink{
			{ID: "l1", A: "k1", B: "hub", Kind: "kernel", Tunnel: "kernel"},
			{ID: "l2", A: "hub", B: "k2", Kind: "direct", Tunnel: "fl-x"},
			{ID: "l3", A: "self", B: "k2", Kind: "direct", Tunnel: "local"},
			{ID: "l4", A: "hub", B: "k2", Kind: "direct"}, // drawn, not applied
		}}
	if err := p.s.saveTopology(&topo); err != nil {
		t.Fatal(err)
	}
	sample := func(kernel, relay uint64) {
		p.s.fleetMu.Lock()
		p.s.fleetStats["hub"] = nodeStats{Kernel: "active", KernelPeers: map[string]uint64{"198.51.100.2": kernel},
			Tunnels: []nodeTunnel{{Name: "fl-x", Active: "active", Bytes: relay}}}
		p.s.fleetMu.Unlock()
		p.s.recordFleetUsage(p.s.loadTopology())
	}
	sample(1000, 50) // the first reading only sets the baseline
	sample(1600, 80)
	sample(200, 100) // the interface was rebuilt: its counter started over

	var rows []struct {
		ID         string `json:"id"`
		Fleet      bool   `json:"fleet"`
		From, To   string
		Kind       string `json:"kind"`
		Active     bool   `json:"active"`
		MonthBytes uint64 `json:"month_bytes"`
		TotalBytes uint64 `json:"total_bytes"`
	}
	if err := json.NewDecoder(p.do("GET", "/api/usage", nil, false).Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for i, r := range rows {
		got[r.ID] = i
	}
	if len(rows) != 2 {
		t.Fatalf("want the two fleet cables only, got %+v", rows)
	}
	k := rows[got["net:l1"]]
	if !k.Fleet || k.From != "Tehran" || k.To != "London" || k.Kind != "kernel" || !k.Active || k.MonthBytes != 800 || k.TotalBytes != 800 {
		t.Fatalf("kernel cable: %+v", k)
	}
	if d := rows[got["net:l2"]]; d.MonthBytes != 50 || d.To != "Dallas" {
		t.Fatalf("relay cable: %+v", d)
	}

	if w := p.do("POST", "/api/usage/reset", map[string]string{"id": "net:l1"}, true); w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if p.s.usage.get("net:l1").TotalBytes != 0 {
		t.Fatal("reset did not clear the cable")
	}
	if w := p.do("POST", "/api/usage/reset", map[string]string{"id": "net:l4"}, true); w.Code != 404 {
		t.Fatalf("an unapplied cable has no counter: %d", w.Code)
	}
}
