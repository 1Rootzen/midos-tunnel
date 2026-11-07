package main

import (
	"net/http"
	"strings"
	"testing"
)

// A server whose provider gave it a new IP: the panel checks it at the new
// address, stores it, and rebuilds every tunnel it is part of on both ends.
func TestFleetReaddressRebuildsTunnels(t *testing.T) {
	f := useFakeFleet(t)
	p := newTestPanel(t)
	p.login(testPassword)
	ir := addServer(t, p, "iran", "198.51.100.10")
	k1 := addServer(t, p, "kharej", "198.51.100.21")
	k2 := addServer(t, p, "kharej", "198.51.100.22")
	_ = p.s.updateTopology(func(t *topology) error {
		t.Links = append(t.Links,
			topoLink{ID: "ldir", A: ir, B: k1, Kind: "direct", Transport: "tls", Port: 8443, Ports: "443"},
			topoLink{ID: "lker", A: ir, B: k2, Kind: "kernel", Mode: "esp", Ports: "2083"})
		return nil
	})
	for _, id := range []string{"ldir", "lker"} {
		if w := p.do("POST", "/api/fleet/apply", map[string]any{"id": id}, true); w.Code != http.StatusOK {
			t.Fatalf("apply %s: %d %s", id, w.Code, w.Body)
		}
	}

	// the Iran server moves: both cables are rebuilt against its new address
	w := p.do("POST", "/api/fleet/readdress", map[string]any{"id": ir, "ip": "203.0.113.10"}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("readdress: %d %s", w.Code, w.Body)
	}
	n, _ := nodeByID(p.s.loadTopology(), ir)
	if n.IP != "203.0.113.10" || n.HostKey != "SHA256:pinned" {
		t.Fatalf("node after move: %+v", n)
	}
	moved := f.host("203.0.113.10")
	if _, _, ok := moved.ran("hostname"); !ok {
		t.Error("the server was not checked at its new address")
	}
	if ic := lastConfig(t, moved); len(ic.Endpoints) != 1 || ic.Endpoints[0].Server != "198.51.100.21:8443" {
		t.Fatalf("iran relay end at the new address: %+v", ic)
	}
	if hub, _, ok := moved.ran(" provision"); !ok || !strings.Contains(hub, "MIDOS_SELF_IP='203.0.113.10'") {
		t.Fatalf("hub not rebuilt at the new address: %s", hub)
	}
	if peer, _, ok := f.host("198.51.100.22").ran("MIDOS_IRAN_IP='203.0.113.10'"); !ok {
		t.Fatalf("kharej kernel end still points at the old Iran IP: %s", peer)
	}

	// an address another server already has is refused, as is one where the
	// server does not answer; neither changes the stored address
	if w := p.do("POST", "/api/fleet/readdress", map[string]any{"id": ir, "ip": "198.51.100.21"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate address: %d", w.Code)
	}
	f.host("203.0.113.99").fail = "hostname"
	if w := p.do("POST", "/api/fleet/readdress", map[string]any{"id": k1, "ip": "203.0.113.99"}, true); w.Code != http.StatusBadGateway {
		t.Fatalf("unreachable address: %d %s", w.Code, w.Body)
	}
	if n, _ := nodeByID(p.s.loadTopology(), k1); n.IP != "198.51.100.21" {
		t.Fatalf("a failed move changed the address: %s", n.IP)
	}
	if w := p.do("POST", "/api/fleet/readdress", map[string]any{"id": k1, "ip": "not an ip!"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("bad address: %d", w.Code)
	}
}
