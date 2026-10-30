package main

// ssh: the carrier is a forwarded connection ("direct-tcpip" channel) through
// the listening server's own sshd, to a relay listening on that server's
// loopback (ssh_listen). Where only SSH gets out, the tunnel needs nothing but
// the login every server already has: on the wire there is one ordinary SSH
// session, and the relay port is never exposed. Each carrier is its own SSH
// connection, so several carriers make a small pool of sessions.
//
// The sshd's host key is not pinned: the pre-shared key challenge inside the
// channel is what proves the relay on the far end is ours.

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshConn struct {
	net.Conn // the forwarded channel
	client   *ssh.Client
	once     sync.Once
	dl       killTimer
}

func (c *sshConn) Close() error {
	var err error
	c.once.Do(func() {
		_ = c.dl.set(time.Time{})
		err = c.Conn.Close()
		_ = c.client.Close()
	})
	return err
}

func (c *sshConn) SetDeadline(t time.Time) error      { return c.dl.set(t) }
func (c *sshConn) SetReadDeadline(t time.Time) error  { return c.dl.set(t) }
func (c *sshConn) SetWriteDeadline(t time.Time) error { return c.dl.set(t) }

func dialSSHCarrier(cfg *Config, ep Endpoint) (*sshConn, net.Conn, error) {
	auths, err := sshAuth(cfg.SSHPassword, cfg.SSHKey)
	if err != nil {
		return nil, nil, err
	}
	raw, err := net.DialTimeout("tcp", ep.Server, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	tuneTCP(raw)
	sc := &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
		// what a stock OpenSSH client announces
		ClientVersion: "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5",
	}
	_ = raw.SetDeadline(time.Now().Add(dialTimeout))
	conn, chans, reqs, err := ssh.NewClientConn(raw, ep.Server, sc)
	if err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("ssh login to %s: %w", ep.Server, err)
	}
	_ = raw.SetDeadline(time.Time{})
	client := ssh.NewClient(conn, chans, reqs)

	remote := cfg.SSHRemote
	if remote == "" {
		remote = defaultSSHRemote
	}
	ch, err := client.Dial("tcp", remote)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("ssh forward to %s via %s (is ssh_listen set there, and AllowTcpForwarding on?): %w", remote, ep.Server, err)
	}
	c := &sshConn{Conn: ch, client: client}
	c.dl.kill = func() { _ = c.Close() }
	// keep NATs and idle timers on the path from dropping a quiet session
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				_ = c.Close()
				return
			}
		}
	}()
	return c, raw, nil
}

// sshEndpoint is how a peer reaches this server's sshd.
func sshEndpoint(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
