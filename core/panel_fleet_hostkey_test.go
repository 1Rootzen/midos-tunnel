package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"testing"

	"golang.org/x/crypto/ssh"
)

// sshServerWithKeys serves SSH on loopback with several host keys, accepting
// any client key, and returns its port and the keys by type.
func sshServerWithKeys(t *testing.T) (int, map[string]ssh.PublicKey) {
	t.Helper()
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	keys := map[string]ssh.PublicKey{}
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, k := range []any{edKey, ecKey, rsaKey} {
		sg, err := ssh.NewSignerFromKey(k)
		if err != nil {
			t.Fatal(err)
		}
		cfg.AddHostKey(sg)
		keys[sg.PublicKey().Type()] = sg.PublicKey()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "test")
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, keys
}

// A server pinned by its RSA key that now offers another key first is still
// reached, and only with the pinned key; the type is remembered.
func TestFleetClientFindsPinnedKeyType(t *testing.T) {
	port, keys := sshServerWithKeys(t)
	p := newTestPanel(t)
	node := topoNode{ID: "k1", Kind: "kharej", IP: "127.0.0.1", SSHPort: port, State: "ready",
		HostKey: ssh.FingerprintSHA256(keys[ssh.KeyAlgoRSA])}
	if err := p.s.saveTopology(&topology{SelfRole: "iran", Nodes: []topoNode{node}}); err != nil {
		t.Fatal(err)
	}

	c, err := p.s.fleetClient(node)
	if err != nil {
		t.Fatalf("pinned RSA key not found: %v", err)
	}
	p.s.dropFleetConn(node.ID)
	_ = c
	got, _ := nodeByID(p.s.loadTopology(), "k1")
	if got.HostKeyType != ssh.KeyAlgoRSA {
		t.Fatalf("key type not remembered: %q", got.HostKeyType)
	}

	// the remembered type is asked for first next time
	if c, err := p.s.fleetClient(got); err != nil {
		t.Fatalf("second dial: %v", err)
	} else {
		c.Close()
	}
	p.s.dropFleetConn(node.ID)

	// a key the server does not have is still refused
	stranger := node
	stranger.ID, stranger.HostKey = "k2", "SHA256:not-this-server"
	if _, err := p.s.fleetClient(stranger); !errors.Is(err, errHostKeyChanged) {
		t.Fatalf("an unknown key must be refused, got %v", err)
	}
}
