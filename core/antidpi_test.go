package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestDNSWrapRoundTrip(t *testing.T) {
	tag := dnsTag("a-sufficiently-long-test-psk")
	for _, size := range []int{1, 60, 255, 256, 900} {
		payload := make([]byte, size)
		_, _ = rand.Read(payload)
		for _, response := range []bool{false, true} {
			b, err := dnsWrap(tag, 1234, response, payload)
			if err != nil {
				t.Fatalf("wrap size=%d resp=%v: %v", size, response, err)
			}
			got, ok := dnsUnwrap(tag, b)
			if !ok || !bytes.Equal(got, payload) {
				t.Fatalf("round trip failed size=%d resp=%v ok=%v", size, response, ok)
			}
		}
	}
	// a different key's tag must not match
	b, _ := dnsWrap(tag, 1, true, []byte("hi"))
	if _, ok := dnsUnwrap(dnsTag("another-long-enough-psk-value"), b); ok {
		t.Fatal("a packet for a different key was accepted")
	}
	// a real, unrelated DNS query must be ignored
	real, _ := dnsWrap("deadbeef", 7, false, []byte("x"))
	if _, ok := dnsUnwrap(tag, real); ok {
		t.Fatal("a foreign DNS packet was accepted")
	}
}

// sniSplit must find a cut point inside the SNI of a genuine uTLS ClientHello.
func TestSNISplitRealClientHello(t *testing.T) {
	// a pipe captures exactly the bytes uTLS writes as its first record
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		_ = srv.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, _ := srv.Read(buf)
		got <- append([]byte{}, buf[:n]...)
	}()
	uc := utls.UClient(cli, &utls.Config{ServerName: "www.cloudflare.com", InsecureSkipVerify: true}, utls.HelloChrome_Auto)
	_ = cli.SetDeadline(time.Now().Add(3 * time.Second))
	go func() { _ = uc.Handshake() }() // will not complete over the pipe; we only need the ClientHello
	hello := <-got
	at := sniSplit(hello)
	if at <= 0 || at >= len(hello) {
		t.Fatalf("no split point found in a %d-byte ClientHello (got %d)", len(hello), at)
	}
	// the cut must land inside the host name, so neither half contains it whole
	idx := bytes.Index(hello, []byte("www.cloudflare.com"))
	if idx < 0 {
		t.Skip("SNI not present verbatim (encrypted ClientHello?)")
	}
	if at <= idx || at >= idx+len("www.cloudflare.com") {
		t.Fatalf("split at %d is not inside the SNI at %d..%d", at, idx, idx+len("www.cloudflare.com"))
	}
}

// fragConn must split the first write and pass later writes through whole.
func TestFragConnSplitsFirstWrite(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	fc := &fragConn{Conn: a}
	// a minimal non-ClientHello first write falls back to the fixed split (32)
	first := make([]byte, 100)
	reads := make(chan int, 8)
	go func() {
		buf := make([]byte, 200)
		for {
			_ = b.SetReadDeadline(time.Now().Add(time.Second))
			n, err := b.Read(buf)
			if err != nil {
				close(reads)
				return
			}
			reads <- n
		}
	}()
	if _, err := fc.Write(first); err != nil {
		t.Fatal(err)
	}
	if n := <-reads; n != 32 {
		t.Fatalf("first segment = %d bytes, want 32", n)
	}
	if n := <-reads; n != 68 {
		t.Fatalf("second segment = %d bytes, want 68", n)
	}
	// a later write is not split
	if _, err := fc.Write(make([]byte, 50)); err != nil {
		t.Fatal(err)
	}
	if n := <-reads; n != 50 {
		t.Fatalf("later write = %d bytes, want 50 (unsplit)", n)
	}
}

// Stealth with shaping on must still carry data intact, and the receiver must
// strip the padding (works even if only one side shapes).
func TestStealthShapingRoundTrip(t *testing.T) {
	svc := freePort(t)
	local := freePort(t)
	echoServer(t, svc)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	srv := &Config{Mode: "server", PSK: testPSK, Targets: "127.0.0.1", Ports: []string{fmt.Sprint(svc)},
		StealthListen: addr}
	cli := &Config{Mode: "client", PSK: testPSK, Carriers: 2, Ports: []string{fmt.Sprintf("%d:%d", local, svc)},
		Endpoints: []Endpoint{{"stealth", addr}}, Shape: true}
	startPair(t, srv, cli)
	relayRoundTrip(t, local, 256*1024)
	relayRoundTrip(t, local, 1)
}
