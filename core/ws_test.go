package main

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const wsTestPSK = "a-sufficiently-long-test-psk"

// startRelay runs a server and a client and returns the client's local port,
// which relays to an echo service behind the server.
func startRelay(t *testing.T, srv, cli *Config) int {
	t.Helper()
	svcPort := freePort(t)
	localPort := freePort(t)
	echoServer(t, svcPort)
	srv.Ports = []string{fmt.Sprintf("%d", svcPort)}
	cli.Ports = []string{fmt.Sprintf("%d:%d", localPort, svcPort)}
	go func() {
		if err := runServer(srv); err != nil {
			t.Log("server stopped:", err)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	go func() {
		if err := runClient(cli); err != nil {
			t.Log("client stopped:", err)
		}
	}()
	time.Sleep(500 * time.Millisecond)
	return localPort
}

func roundTrip(t *testing.T, localPort int) {
	t.Helper()
	payload := make([]byte, 256*1024)
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

// A CDN that reaches the origin over TLS (Cloudflare "Full").
func TestWebSocketOverTLS(t *testing.T) {
	tunnelPort := freePort(t)
	srv := &Config{Mode: "server", PSK: wsTestPSK, Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		Targets: "127.0.0.1", ServerName: "cdn.example.com", WSPath: "/s3cr3t"}
	cli := &Config{Mode: "client", PSK: wsTestPSK, Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		ServerName: "cdn.example.com", Host: "cdn.example.com", Fingerprint: "chrome",
		Insecure: true, Carriers: 2, Transport: "ws", WSPath: "/s3cr3t"}
	roundTrip(t, startRelay(t, srv, cli))
}

// A CDN that reaches the origin over plain HTTP (Cloudflare "Flexible"); the
// client still speaks TLS, to the CDN, here played by a TLS-terminating proxy.
func TestWebSocketOverPlainHTTPOrigin(t *testing.T) {
	tunnelPort := freePort(t)
	httpPort := freePort(t)
	edgePort := freePort(t)
	srv := &Config{Mode: "server", PSK: wsTestPSK, Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		HTTPListen: fmt.Sprintf("127.0.0.1:%d", httpPort), Targets: "127.0.0.1",
		ServerName: "cdn.example.com", WSPath: "/s3cr3t"}

	// the "edge": terminate TLS, forward bytes to the plain-HTTP origin
	cert, err := selfSigned("cdn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	edge, err := tls.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", edgePort),
		&tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { edge.Close() })
	go func() {
		for {
			c, err := edge.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				o, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", httpPort))
				if err != nil {
					c.Close()
					return
				}
				pipe(c, o)
			}(c)
		}
	}()

	cli := &Config{Mode: "client", PSK: wsTestPSK, Server: fmt.Sprintf("127.0.0.1:%d", edgePort),
		ServerName: "cdn.example.com", Host: "cdn.example.com", Fingerprint: "chrome",
		Insecure: true, Carriers: 2, Transport: "ws", WSPath: "/s3cr3t"}
	roundTrip(t, startRelay(t, srv, cli))
}

// Enabling WebSocket sessions must not break plain tunnel clients on the same port.
func TestPlainClientWithWebSocketServer(t *testing.T) {
	tunnelPort := freePort(t)
	srv := &Config{Mode: "server", PSK: wsTestPSK, Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		Targets: "127.0.0.1", ServerName: "www.example.com", WSPath: "/s3cr3t"}
	cli := &Config{Mode: "client", PSK: wsTestPSK, Server: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		ServerName: "www.example.com", Fingerprint: "chrome", Insecure: true, Carriers: 2}
	roundTrip(t, startRelay(t, srv, cli))
}

// A visitor who does not know the path sees an ordinary web server's 404.
func TestWrongWebSocketPathLooksLikeNginx(t *testing.T) {
	tunnelPort := freePort(t)
	srv := &Config{Mode: "server", PSK: wsTestPSK, Listen: fmt.Sprintf("127.0.0.1:%d", tunnelPort),
		Targets: "127.0.0.1", ServerName: "www.example.com", WSPath: "/s3cr3t", Ports: []string{"9"}}
	go func() { _ = runServer(srv) }()
	time.Sleep(300 * time.Millisecond)

	c, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", tunnelPort), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(c, "GET /index.html HTTP/1.1\r\nHost: www.example.com\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || resp.Header.Get("Server") != "nginx" || !strings.Contains(string(body), "nginx") {
		t.Fatalf("got %s server=%q body=%q", resp.Status, resp.Header.Get("Server"), body)
	}
}

func TestWebSocketConfigValidation(t *testing.T) {
	if _, err := loadConfig(writeCfg(t, Config{Mode: "client", PSK: wsTestPSK, Server: "x:443", Transport: "ws"})); err == nil {
		t.Fatal("ws without ws_path accepted")
	}
	c, err := loadConfig(writeCfg(t, Config{Mode: "client", PSK: wsTestPSK, Server: "cdn.example.com:443", Transport: "ws", WSPath: "/p"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "cdn.example.com" {
		t.Fatalf("host defaulted to %q", c.Host)
	}
	if _, err := loadConfig(writeCfg(t, Config{Mode: "server", PSK: wsTestPSK, Listen: ":443", HTTPListen: ":80"})); err == nil {
		t.Fatal("http_listen without ws_path accepted")
	}
}
