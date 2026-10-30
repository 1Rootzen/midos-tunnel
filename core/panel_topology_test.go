package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func validTopo() topology {
	return topology{
		SelfRole: "iran",
		Nodes: []topoNode{
			{ID: "self", Kind: "self", Label: "ir"},
			{ID: "k1", Kind: "kharej", Label: "de-1", IP: "198.51.100.20", SSHUser: "root", SSHPort: 22},
		},
		Links: []topoLink{{ID: "l1", A: "self", B: "k1", Kind: "direct", Transport: "tls", Port: 443, Ports: "443"}},
	}
}

func TestCheckTopology(t *testing.T) {
	ok := validTopo()
	if err := checkTopology(&ok); err != nil {
		t.Fatalf("valid layout rejected: %v", err)
	}
	cases := map[string]func(*topology){
		"bad role":       func(x *topology) { x.SelfRole = "moon" },
		"unknown kind":   func(x *topology) { x.Nodes[1].Kind = "router" },
		"missing device": func(x *topology) { x.Links[0].B = "nope" },
		"unknown cable":  func(x *topology) { x.Links[0].Kind = "fiber" },
		"kharej-to-kharej": func(x *topology) {
			x.Nodes = append(x.Nodes, topoNode{ID: "k2", Kind: "kharej"})
			x.Links[0].A = "k2"
		},
		"manager with a cable": func(x *topology) { x.SelfRole = "none" },
		"two cables, one pair": func(x *topology) {
			x.Links = append(x.Links, topoLink{ID: "l2", A: "k1", B: "self", Kind: "kernel"})
		},
		"kharej with two kernel cables": func(x *topology) {
			x.Nodes = append(x.Nodes, topoNode{ID: "i2", Kind: "iran"})
			x.Links = []topoLink{{ID: "l1", A: "self", B: "k1", Kind: "kernel"}, {ID: "l2", A: "i2", B: "k1", Kind: "kernel"}}
		},
	}
	// two managed servers may be cabled without the mother taking part
	fleet := validTopo()
	fleet.SelfRole = "none"
	fleet.Nodes = append(fleet.Nodes, topoNode{ID: "i1", Kind: "iran"}, topoNode{ID: "k2", Kind: "kharej"})
	fleet.Links = []topoLink{{ID: "l1", A: "i1", B: "k1", Kind: "direct"}, {ID: "l2", A: "i1", B: "k2", Kind: "kernel"}}
	if err := checkTopology(&fleet); err != nil {
		t.Fatalf("one-to-many between managed servers rejected: %v", err)
	}
	for name, mutate := range cases {
		x := validTopo()
		mutate(&x)
		if err := checkTopology(&x); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTopologyStoreNeverKeepsSecrets(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// nothing stored yet: a default layout comes back
	w := p.do("GET", "/api/topology", nil, false)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	var got struct {
		Topology topology `json:"topology"`
		SelfIP   string   `json:"self_ip"`
	}
	_ = json.NewDecoder(w.Body).Decode(&got)
	if got.Topology.SelfRole != "none" || got.SelfIP != "203.0.113.7" {
		t.Fatalf("default layout: %+v", got)
	}

	if w := p.do("PUT", "/api/topology", validTopo(), true); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	w = p.do("GET", "/api/topology", nil, false)
	_ = json.NewDecoder(w.Body).Decode(&got)
	if len(got.Topology.Nodes) != 2 || len(got.Topology.Links) != 1 || got.Topology.Nodes[1].IP != "198.51.100.20" {
		t.Fatalf("round trip lost data: %+v", got.Topology)
	}

	// a password smuggled into a device must be refused, not stored
	bad := map[string]any{"self_role": "iran", "nodes": []map[string]any{
		{"id": "self", "kind": "self", "x": 0, "y": 0},
		{"id": "k1", "kind": "kharej", "x": 0, "y": 0, "ssh_password": "hunter2"},
	}, "links": []any{}}
	if w := p.do("PUT", "/api/topology", bad, true); w.Code != http.StatusBadRequest {
		t.Fatalf("a layout carrying a password was accepted: %d", w.Code)
	}
}

func TestTopologyLinkRunsPeerCommandOverSSH(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	cfg := map[string]any{"mode": "client", "psk": testPSK,
		"endpoints": []map[string]string{{"transport": "tls", "server": "198.51.100.20:443"}},
		"ports":     []string{"40021"}}
	if w := p.do("POST", "/api/tunnels", map[string]any{"name": "tp-de", "config": cfg}, true); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}

	var ranOn sshTarget
	var ranCmd string
	old := linkRemote
	linkRemote = func(_ context.Context, tgt sshTarget, cmd string) ([]byte, error) {
		ranOn, ranCmd = tgt, cmd
		return []byte("installed tp-de\n"), nil
	}
	defer func() { linkRemote = old }()

	// no password and no key: refused before anything runs
	w := p.do("POST", "/api/topology/link", map[string]any{"id": "tp-de", "ssh": map[string]any{"host": "198.51.100.20", "user": "root"}}, true)
	if w.Code != http.StatusBadRequest || ranCmd != "" {
		t.Fatalf("missing credentials: %d, ran %q", w.Code, ranCmd)
	}

	w = p.do("POST", "/api/topology/link", map[string]any{"id": "tp-de",
		"ssh": map[string]any{"host": "198.51.100.20", "port": 22, "user": "root", "password": "pw"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	if ranOn.Host != "198.51.100.20" || !strings.HasPrefix(ranCmd, "curl -fsSLk 'https://203.0.113.7:1/s3cretpath/peer/") || !strings.HasSuffix(ranCmd, "| bash") {
		t.Fatalf("ran %q on %+v", ranCmd, ranOn)
	}

	// an unknown tunnel is a 404, not an SSH attempt
	ranCmd = ""
	w = p.do("POST", "/api/topology/link", map[string]any{"id": "nope", "ssh": map[string]any{"host": "h", "user": "root", "password": "pw"}}, true)
	if w.Code != http.StatusNotFound || ranCmd != "" {
		t.Fatalf("unknown tunnel: %d, ran %q", w.Code, ranCmd)
	}
}

func TestPeerConfigICMP(t *testing.T) {
	// this side dials over ICMP: the peer must answer ICMP
	c := &Config{Mode: "client", PSK: testPSK, Name: "ic",
		Endpoints: []Endpoint{{"icmp", "198.51.100.20"}}, Ports: []string{"40031"}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	p, _, err := peerConfig(c, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if p.ICMPListen != "0.0.0.0" || p.dials() {
		t.Fatalf("peer should listen on ICMP: %+v", p)
	}

	// this side listens on ICMP (reverse): the peer must dial it by bare host
	r := &Config{Mode: "client", PSK: testPSK, Name: "ic", Reverse: true, ICMPListen: "0.0.0.0", Ports: []string{"40032"}}
	if err := r.normalize(); err != nil {
		t.Fatal(err)
	}
	p, _, err = peerConfig(r, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Endpoints) != 1 || p.Endpoints[0].Transport != "icmp" || p.Endpoints[0].Server != "203.0.113.7" {
		t.Fatalf("peer should dial ICMP: %+v", p.Endpoints)
	}
}
