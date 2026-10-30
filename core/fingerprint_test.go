package main

import (
	"crypto/tls"
	"encoding/hex"
	"net"
	"testing"
	"time"
)

// captureHello records the first record a client sends: the TLS ClientHello.
func captureHello(t *testing.T, dial func(addr string)) []byte {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		got <- buf[:n]
	}()

	dial(ln.Addr().String())
	select {
	case b := <-got:
		return b
	case <-time.After(6 * time.Second):
		t.Fatal("no ClientHello captured")
		return nil
	}
}

// The reason this component exists: a stock Go TLS ClientHello is a stable,
// recognisable fingerprint. Ours must not look like it.
func TestClientHelloIsNotStockGo(t *testing.T) {
	stock := captureHello(t, func(addr string) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		defer c.Close()
		tc := tls.Client(c, &tls.Config{ServerName: "www.example.com", InsecureSkipVerify: true})
		_ = tc.SetDeadline(time.Now().Add(2 * time.Second))
		_ = tc.Handshake()
	})

	ours := captureHello(t, func(addr string) {
		cfg := &Config{ServerName: "www.example.com", Fingerprint: "chrome", Insecure: true}
		if c, _, err := dialTLS(cfg, Endpoint{"tls", addr}); err == nil {
			_ = c.Close()
		}
	})

	if len(stock) == 0 || len(ours) == 0 {
		t.Fatal("failed to capture both hellos")
	}
	if hex.EncodeToString(stock) == hex.EncodeToString(ours) {
		t.Fatal("our ClientHello is byte-identical to stock Go - no camouflage at all")
	}
	// A browser hello is materially larger: more extensions, GREASE, ALPN,
	// key shares, padding. If ours is not, we are probably not mimicking.
	if len(ours) <= len(stock) {
		t.Fatalf("our hello (%d bytes) is not richer than stock Go (%d bytes)", len(ours), len(stock))
	}
	t.Logf("stock Go hello: %d bytes | browser-shaped hello: %d bytes", len(stock), len(ours))
}

// Each supported fingerprint should actually produce a different hello.
func TestFingerprintsDiffer(t *testing.T) {
	seen := map[string]string{}
	for _, fp := range []string{"chrome", "firefox", "safari", "edge"} {
		h := captureHello(t, func(addr string) {
			cfg := &Config{ServerName: "www.example.com", Fingerprint: fp, Insecure: true}
			if c, _, err := dialTLS(cfg, Endpoint{"tls", addr}); err == nil {
				_ = c.Close()
			}
		})
		if len(h) == 0 {
			t.Fatalf("%s: no hello", fp)
		}
		key := hex.EncodeToString(h[:min(len(h), 64)])
		for other, k := range seen {
			if k == key {
				t.Fatalf("%s and %s produce the same hello prefix", fp, other)
			}
		}
		seen[fp] = key
		t.Logf("%-8s hello: %d bytes", fp, len(h))
	}
}
