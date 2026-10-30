package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func writeCfg(t *testing.T, c Config) string {
	t.Helper()
	b, _ := json.Marshal(c)
	p := filepath.Join(t.TempDir(), "tunnel.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// echoServer stands in for the real service behind the relay.
func echoServer(t *testing.T, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
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
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
}

// The whole point: bytes handed to the local port must come back from the
// service behind the server, through a TLS session wearing a browser hello.
func TestEndToEndRelay(t *testing.T) {
	psk := "a-sufficiently-long-test-psk"
	svcPort := freePort(t)
	tunnelPort := freePort(t)
	localPort := freePort(t)
	echoServer(t, svcPort)

	srv := &Config{Mode: "server", PSK: psk,
		Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort), Targets: "127.0.0.1",
		ServerName: "www.example.com",
		Ports:      []string{fmt.Sprintf("%d", svcPort)}}
	go func() {
		if err := runServer(srv); err != nil {
			t.Log("server stopped:", err)
		}
	}()
	time.Sleep(300 * time.Millisecond)

	cli := &Config{Mode: "client", PSK: psk,
		Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort), ServerName: "www.example.com",
		Fingerprint: "chrome", Insecure: true, Carriers: 2,
		Ports: []string{fmt.Sprintf("%d:%d", localPort, svcPort)}}
	go func() {
		if err := runClient(cli); err != nil {
			t.Log("client stopped:", err)
		}
	}()
	time.Sleep(500 * time.Millisecond)

	payload := make([]byte, 64*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))

	go func() { _, _ = c.Write(payload) }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	for i := range payload {
		if payload[i] != got[i] {
			t.Fatalf("payload corrupted at byte %d", i)
		}
	}
}

// A wrong pre-shared key must not get a usable session.
func TestWrongPSKIsRejected(t *testing.T) {
	svcPort := freePort(t)
	tunnelPort := freePort(t)
	localPort := freePort(t)
	echoServer(t, svcPort)

	go func() {
		_ = runServer(&Config{Mode: "server", PSK: "the-real-pre-shared-key",
			Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort), Targets: "127.0.0.1",
			ServerName: "www.example.com", Ports: []string{fmt.Sprintf("%d", svcPort)}})
	}()
	time.Sleep(300 * time.Millisecond)

	go func() {
		_ = runClient(&Config{Mode: "client", PSK: "a-completely-wrong-key",
			Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort), ServerName: "www.example.com",
			Fingerprint: "chrome", Insecure: true, Carriers: 1,
			Ports: []string{fmt.Sprintf("%d:%d", localPort, svcPort)}})
	}()
	time.Sleep(500 * time.Millisecond)

	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
	if err != nil {
		return // refusing to listen at all is an acceptable outcome
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(6 * time.Second))
	if _, err := c.Write([]byte("hello")); err != nil {
		return
	}
	buf := make([]byte, 16)
	if n, err := c.Read(buf); err == nil && n > 0 {
		t.Fatalf("relay carried %d bytes despite a wrong PSK", n)
	}
}

// A port the operator did not list must not be reachable through the relay.
func TestUnlistedPortRefused(t *testing.T) {
	psk := "a-sufficiently-long-test-psk"
	allowed := freePort(t)
	secret := freePort(t)
	tunnelPort := freePort(t)
	echoServer(t, secret)

	srv := &Config{Mode: "server", PSK: psk,
		Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort), Targets: "127.0.0.1",
		ServerName: "www.example.com", Ports: []string{fmt.Sprintf("%d", allowed)}}
	go func() { _ = runServer(srv) }()
	time.Sleep(300 * time.Millisecond)

	cfg := &Config{Mode: "client", PSK: psk,
		Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort), ServerName: "www.example.com",
		Fingerprint: "chrome", Insecure: true, Carriers: 1,
		Ports: []string{fmt.Sprintf("%d", allowed)}}
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	pool := newCarrierPool(cfg)
	defer pool.closeAll()

	st, err := pool.stream()
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer st.Close()
	// ask for the port that was never listed
	if err := writeStreamHeader(st, uint16(secret)); err != nil {
		t.Fatal(err)
	}
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := st.Write([]byte("probe")); err != nil {
		return
	}
	buf := make([]byte, 8)
	if n, err := st.Read(buf); err == nil && n > 0 {
		t.Fatalf("reached an unlisted port: got %d bytes", n)
	}
}

func TestPortMapParsing(t *testing.T) {
	c := &Config{Ports: []string{"443", "8443:443", " 2053 "}}
	m, err := c.parsePorts()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 || m[1].local != 8443 || m[1].remote != 443 {
		t.Fatalf("unexpected mapping: %+v", m)
	}
	if _, err := (&Config{Ports: []string{"443", "443:8443"}}).parsePorts(); err == nil {
		t.Fatal("duplicate local port should be rejected")
	}
	if _, err := (&Config{Ports: []string{"70000"}}).parsePorts(); err == nil {
		t.Fatal("out-of-range port should be rejected")
	}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// UDP datagrams handed to the client's UDP port must come back from a UDP
// service behind the server with their boundaries intact - this is how
// WireGuard and OpenVPN-over-UDP cross a network that lets only TCP out.
func TestEndToEndUDP(t *testing.T) {
	psk := "a-sufficiently-long-test-psk"
	svcPort := freeUDPPort(t)
	tunnelPort := freePort(t)
	localPort := freeUDPPort(t)

	// UDP echo service
	svc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: svcPort})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, a, err := svc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = svc.WriteToUDP(buf[:n], a)
		}
	}()

	go func() {
		_ = runServer(&Config{Mode: "server", PSK: psk,
			Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort), Targets: "127.0.0.1",
			ServerName: "www.example.com", UDPPorts: []string{fmt.Sprintf("%d", svcPort)}})
	}()
	time.Sleep(300 * time.Millisecond)
	go func() {
		_ = runClient(&Config{Mode: "client", PSK: psk,
			Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort), ServerName: "www.example.com",
			Fingerprint: "chrome", Insecure: true, Carriers: 1,
			UDPPorts: []string{fmt.Sprintf("%d:%d", localPort, svcPort)}})
	}()
	time.Sleep(500 * time.Millisecond)

	dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: localPort}
	// two independent clients: each must get only its own replies
	for c := 0; c < 2; c++ {
		conn, err := net.DialUDP("udp", nil, dst)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, size := range []int{1, 1400, 60000} {
			p := make([]byte, size)
			_, _ = rand.Read(p)
			if _, err := conn.Write(p); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			got := make([]byte, 65535)
			n, err := conn.Read(got)
			if err != nil {
				t.Fatalf("client %d size %d: %v", c, size, err)
			}
			if n != size || string(got[:n]) != string(p) {
				t.Fatalf("client %d size %d: got %d bytes, boundary or payload broken", c, size, n)
			}
		}
	}
}

// A UDP port the server did not list must not be reachable, even if the same
// number is allowed for TCP.
func TestUDPPortNotListedIsRefused(t *testing.T) {
	psk := "a-sufficiently-long-test-psk"
	svcPort := freeUDPPort(t)
	tunnelPort := freePort(t)
	localPort := freeUDPPort(t)
	go func() {
		_ = runServer(&Config{Mode: "server", PSK: psk,
			Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort), Targets: "127.0.0.1",
			ServerName: "www.example.com", Ports: []string{fmt.Sprintf("%d", svcPort)}})
	}()
	time.Sleep(300 * time.Millisecond)
	go func() {
		_ = runClient(&Config{Mode: "client", PSK: psk,
			Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort), ServerName: "www.example.com",
			Fingerprint: "chrome", Insecure: true, Carriers: 1,
			UDPPorts: []string{fmt.Sprintf("%d:%d", localPort, svcPort)}})
	}()
	time.Sleep(500 * time.Millisecond)
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: localPort})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("ping"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 64)); err == nil {
		t.Fatal("got a reply through a UDP port the server never allowed")
	}
}

// A path that completes the TCP handshake but swallows the ClientHello must
// make the dial fail in bounded time, not hang and strand the socket.
func TestBlackholedHandshakeTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // accept, then never answer
		}
	}()

	old := tlsHandshakeTimeout
	tlsHandshakeTimeout = 500 * time.Millisecond
	defer func() { tlsHandshakeTimeout = old }()

	start := time.Now()
	_, _, err = dialTLS(&Config{ServerName: "www.example.com", Fingerprint: "chrome", Insecure: true}, Endpoint{"tls", ln.Addr().String()})
	if err == nil {
		t.Fatal("dial to a silent peer succeeded")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("dial took %v; the handshake deadline did not fire", d)
	}
}

func TestPortRangesAndDestinations(t *testing.T) {
	c := &Config{Ports: []string{"443=10.0.0.5:2096", "9000=backend", "8000-8002", "5000:6000"}}
	m, err := c.parsePorts()
	if err != nil {
		t.Fatal(err)
	}
	// 443 host+port, 9000 host only, 8000-8002 range (3), 5000:6000
	if len(m) != 6 {
		t.Fatalf("want 6 mappings, got %d: %+v", len(m), m)
	}
	if m[0].dest("127.0.0.1") != "10.0.0.5:2096" {
		t.Fatalf("per-port dest: %s", m[0].dest("127.0.0.1"))
	}
	if m[1].dest("127.0.0.1") != "backend:9000" { // host only, port defaults to remote
		t.Fatalf("host-only dest: %s", m[1].dest("127.0.0.1"))
	}
	if m[2].local != 8000 || m[4].local != 8002 || m[2].dest("127.0.0.1") != "127.0.0.1:8000" {
		t.Fatalf("range expansion wrong: %+v", m[2:5])
	}
	if m[5].local != 5000 || m[5].remote != 6000 || m[5].dest("1.2.3.4") != "1.2.3.4:6000" {
		t.Fatalf("local:remote dest: %+v", m[5])
	}
	// a range may not also pin a destination port
	if _, err := (&Config{Ports: []string{"8000-8002=1.2.3.4:9999"}}).parsePorts(); err == nil {
		t.Fatal("range with fixed dest port should be rejected")
	}
	// a reversed range is invalid
	if _, err := (&Config{Ports: []string{"8010-8000"}}).parsePorts(); err == nil {
		t.Fatal("reversed range should be rejected")
	}
}

func TestPresetTuning(t *testing.T) {
	c := &Config{Mode: "client", PSK: testPSK, Server: "1.2.3.4:443", Preset: "throughput", Ports: []string{"443"}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Carriers != 8 { // throughput default carriers
		t.Fatalf("throughput carriers = %d, want 8", c.Carriers)
	}
	if smuxConfig(c).MaxReceiveBuffer != 64<<20 {
		t.Fatalf("throughput recv buffer = %d", smuxConfig(c).MaxReceiveBuffer)
	}
	// an explicit carrier count overrides the preset default
	c2 := &Config{Mode: "client", PSK: testPSK, Server: "1.2.3.4:443", Preset: "turbo", Carriers: 3, Ports: []string{"443"}}
	if err := c2.normalize(); err != nil {
		t.Fatal(err)
	}
	if c2.Carriers != 3 {
		t.Fatalf("explicit carriers overridden: %d", c2.Carriers)
	}
	if err := (&Config{Mode: "client", PSK: testPSK, Server: "1.2.3.4:443", Preset: "ludicrous", Ports: []string{"443"}}).normalize(); err == nil {
		t.Fatal("unknown preset should be rejected")
	}
}
