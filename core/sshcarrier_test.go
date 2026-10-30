package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshTestServer is a minimal sshd: password login and "direct-tcpip"
// forwarding, which is all the ssh transport uses.
func sshTestServer(t *testing.T, user, pass string) string {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == user && string(p) == pass {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
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
					_ = c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						_ = nc.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					var to struct {
						Host     string
						Port     uint32
						OrigHost string
						OrigPort uint32
					}
					if err := ssh.Unmarshal(nc.ExtraData(), &to); err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, "bad request")
						continue
					}
					dst, err := net.Dial("tcp", net.JoinHostPort(to.Host, strconv.Itoa(int(to.Port))))
					if err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, err.Error())
						continue
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						_ = dst.Close()
						continue
					}
					go ssh.DiscardRequests(creqs)
					go func() {
						defer ch.Close()
						defer dst.Close()
						go func() { _, _ = io.Copy(dst, ch); _ = dst.(*net.TCPConn).CloseWrite() }()
						_, _ = io.Copy(ch, dst)
					}()
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// The ssh transport must carry streams through sshd, in both directions of
// dialing: normally Iran logs in to the server abroad; with reverse the server
// abroad logs in to Iran.
func TestSSHTransportBothDirections(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "forward"
		if reverse {
			name = "reverse"
		}
		t.Run(name, func(t *testing.T) {
			sshd := sshTestServer(t, "tunnel", "s3cret-pass")
			svc := freePort(t)
			local := freePort(t)
			echoServer(t, svc)
			relay := fmt.Sprintf("127.0.0.1:%d", freePort(t))
			ep := []Endpoint{{"ssh", sshd}}

			srv := &Config{Mode: "server", PSK: testPSK, Targets: "127.0.0.1", Ports: []string{fmt.Sprint(svc)}, Reverse: reverse}
			cli := &Config{Mode: "client", PSK: testPSK, Carriers: 2, Ports: []string{fmt.Sprintf("%d:%d", local, svc)}, Reverse: reverse}
			login := func(c *Config) {
				c.Endpoints, c.SSHUser, c.SSHPassword, c.SSHRemote = ep, "tunnel", "s3cret-pass", relay
			}
			if reverse {
				cli.SSHListen = relay
				login(srv)
				go func() { _ = runClient(cli) }()
				time.Sleep(400 * time.Millisecond)
				go func() { _ = runServer(srv) }()
				time.Sleep(1500 * time.Millisecond)
			} else {
				srv.SSHListen = relay
				login(cli)
				startPair(t, srv, cli)
			}
			relayRoundTrip(t, local, 256*1024)
			relayRoundTrip(t, local, 1)
		})
	}
}

func TestSSHCarrierWrongPassword(t *testing.T) {
	sshd := sshTestServer(t, "tunnel", "right")
	cfg := &Config{SSHUser: "tunnel", SSHPassword: "wrong", SSHRemote: "127.0.0.1:1"}
	if _, _, err := dialSSHCarrier(cfg, Endpoint{"ssh", sshd}); err == nil || !strings.Contains(err.Error(), "ssh login") {
		t.Fatalf("a wrong password must fail the login, got %v", err)
	}
}

func TestConfigSSHValidation(t *testing.T) {
	base := func() *Config {
		return &Config{Mode: "client", PSK: testPSK, Ports: []string{"443"}, Endpoints: []Endpoint{{"ssh", "198.51.100.20:22"}}}
	}
	c := base()
	if err := c.normalize(); err == nil {
		t.Fatal("an ssh endpoint without a login must be refused")
	}
	c = base()
	c.SSHUser, c.SSHPassword = "root", "pw"
	if err := c.normalize(); err != nil || c.SSHRemote != defaultSSHRemote || c.SSHPort != 22 {
		t.Fatalf("valid ssh config: err=%v remote=%q port=%d", err, c.SSHRemote, c.SSHPort)
	}
	l := &Config{Mode: "server", PSK: testPSK, Ports: []string{"443"}, SSHListen: "0.0.0.0:8710"}
	if err := l.normalize(); err == nil {
		t.Fatal("ssh_listen on a public address must be refused")
	}
}

// A plain HTTPS request to the gRPC listener must look like an ordinary web
// server's 404, and a gRPC call to another method like an unimplemented one.
func TestGRPCListenerAnswersOthers(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	srv := &Config{Mode: "server", PSK: testPSK, Targets: "127.0.0.1", Ports: []string{"1"}, GRPCListen: addr}
	go func() { _ = runServer(srv) }()
	time.Sleep(400 * time.Millisecond)

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}}
	resp, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || resp.ProtoMajor != 2 {
		t.Fatalf("plain request: %d over HTTP/%d", resp.StatusCode, resp.ProtoMajor)
	}

	req, _ := http.NewRequest(http.MethodPost, "https://"+addr+"/other.Service/Call", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/grpc")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if got := resp.Header.Get("Grpc-Status") + resp.Trailer.Get("Grpc-Status"); got != "12" {
		t.Fatalf("unknown method: grpc-status %q, want 12", got)
	}
}
