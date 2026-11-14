package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestCamoToken(t *testing.T) {
	now := time.Now()
	fresh := func() []byte { return camoToken(testPSK, now) }
	if !camoValid(testPSK, fresh(), now) {
		t.Fatal("a fresh signature was refused")
	}
	if !camoValid(testPSK, camoToken(testPSK, now.Add(-2*time.Minute)), now) || !camoValid(testPSK, camoToken(testPSK, now.Add(2*time.Minute)), now) {
		t.Fatal("a signature within the clock window was refused")
	}
	if camoValid(testPSK, camoToken(testPSK, now.Add(-4*time.Minute)), now) {
		t.Fatal("a stale signature was accepted")
	}
	if camoValid("another key entirely", fresh(), now) {
		t.Fatal("a signature from another key was accepted")
	}
	tok := fresh()
	if !camoValid(testPSK, tok, now) || camoValid(testPSK, tok, now) {
		t.Fatal("a replayed signature must be refused the second time")
	}
	if camoValid(testPSK, make([]byte, camoTokenLen), now) || camoValid(testPSK, tok[:16], now) {
		t.Fatal("zero or short session ids must be refused")
	}
}

// realSite is an ordinary HTTPS site standing in for the one a camouflaged
// listener imitates.
func realSite(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "REAL SITE")
	}))
	t.Cleanup(s.Close)
	return s, s.Certificate().Raw
}

// probeSees opens TLS to addr the way a scanner or browser would and returns
// the certificate it was shown and the page it got.
func probeSees(t *testing.T, addr string) ([]byte, string) {
	t.Helper()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "www.example.com", NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatalf("probe tls: %v", err)
	}
	defer c.Close()
	cert := c.ConnectionState().PeerCertificates[0].Raw
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: www.example.com\r\nConnection: close\r\n\r\n")
	body, _ := io.ReadAll(c)
	return cert, string(body)
}

func TestCamouflageEndToEnd(t *testing.T) {
	site, siteCert := realSite(t)
	target := strings.TrimPrefix(site.URL, "https://")
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			svc, local, port := freePort(t), freePort(t), freePort(t)
			echoServer(t, svc)
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			srv := &Config{Mode: "server", PSK: testPSK, Targets: "127.0.0.1", ServerName: "www.example.com",
				Ports: []string{fmt.Sprint(svc)}, Reverse: reverse, Camouflage: true, Decoy: target}
			cli := &Config{Mode: "client", PSK: testPSK, ServerName: "www.example.com", Fingerprint: "chrome", Insecure: true,
				Carriers: 2, Ports: []string{fmt.Sprintf("%d:%d", local, svc)}, Reverse: reverse, Camouflage: true, Decoy: target}
			if reverse {
				cli.Listen = addr
				srv.Endpoints, srv.Insecure, srv.Fingerprint = []Endpoint{{"tls", addr}}, true, "chrome"
				go func() { _ = runClient(cli) }()
				time.Sleep(400 * time.Millisecond)
				go func() { _ = runServer(srv) }()
				time.Sleep(1500 * time.Millisecond)
			} else {
				srv.Listen = addr
				cli.Endpoints = []Endpoint{{"tls", addr}}
				startPair(t, srv, cli)
			}
			relayRoundTrip(t, local, 256*1024)

			// a prober is shown the real site's own certificate and page
			cert, page := probeSees(t, addr)
			if !bytes.Equal(cert, siteCert) {
				t.Fatal("a prober saw the tunnel's certificate instead of the real site's")
			}
			if !strings.Contains(page, "REAL SITE") {
				t.Fatalf("a prober did not get the real site's page: %q", page)
			}
		})
	}
}

// Every browser fingerprint the panel offers carries a signable session id,
// and the signature lands in the bytes that go on the wire.
func TestCamoSignEveryFingerprint(t *testing.T) {
	for _, fp := range []string{"chrome", "firefox", "safari", "edge", "random"} {
		a, b := net.Pipe()
		c := utls.UClient(a, &utls.Config{ServerName: "www.example.com", InsecureSkipVerify: true}, helloID(fp))
		if err := camoSign(c, testPSK); err != nil {
			t.Errorf("%s: %v", fp, err)
		} else {
			raw := []byte{0x16, 3, 1, 0, 0}
			raw = append(raw, c.HandshakeState.Hello.Raw...)
			if sid := helloSessionID(raw); !camoValid(testPSK, sid, time.Now()) {
				t.Errorf("%s: the signature is not in the hello on the wire", fp)
			}
		}
		_ = a.Close()
		_ = b.Close()
	}
}

// A camouflaged listener without a site to imitate is refused at start.
func TestCamouflageNeedsTarget(t *testing.T) {
	c := &Config{Mode: "server", PSK: testPSK, Listen: "127.0.0.1:1", Targets: "127.0.0.1", Ports: []string{"443"}, Camouflage: true}
	if err := c.normalize(); err == nil {
		t.Fatal("camouflage without decoy or server_name was accepted")
	}
	c.ServerName = "www.example.com"
	if err := c.normalize(); err != nil || c.camoTarget() != "www.example.com:443" {
		t.Fatalf("server_name should be the target: %v %q", err, c.camoTarget())
	}
}

func TestPadEndToEnd(t *testing.T) {
	for _, tr := range []string{"tls", "ws"} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", tr, reverse), func(t *testing.T) {
				svc, local, port := freePort(t), freePort(t), freePort(t)
				echoServer(t, svc)
				addr := fmt.Sprintf("127.0.0.1:%d", port)
				srv := &Config{Mode: "server", PSK: testPSK, Targets: "127.0.0.1", ServerName: "www.example.com",
					Ports: []string{fmt.Sprint(svc)}, Reverse: reverse, WSPath: "/w", Pad: true}
				cli := &Config{Mode: "client", PSK: testPSK, ServerName: "www.example.com", Fingerprint: "chrome", Insecure: true,
					Carriers: 2, Ports: []string{fmt.Sprintf("%d:%d", local, svc)}, Reverse: reverse, WSPath: "/w", Pad: true}
				if reverse {
					cli.Listen = addr
					srv.Endpoints, srv.Insecure, srv.Fingerprint = []Endpoint{{tr, addr}}, true, "chrome"
					go func() { _ = runClient(cli) }()
					time.Sleep(400 * time.Millisecond)
					go func() { _ = runServer(srv) }()
					time.Sleep(1500 * time.Millisecond)
				} else {
					srv.Listen = addr
					cli.Endpoints = []Endpoint{{tr, addr}}
					startPair(t, srv, cli)
				}
				relayRoundTrip(t, local, 256*1024)
				relayRoundTrip(t, local, 1)
			})
		}
	}
}
