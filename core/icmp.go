package main

// The ICMP transport carries datagrams inside ICMP echo packets, so a network
// that passes ping but throttles or blocks much else still leaves a path open.
// ICMP is unreliable and unordered on its own - exactly like UDP - so KCP runs
// on top for reliability and ordering, the same way it does over UDP. The
// client sends Echo Requests and the server sends Echo Replies, so on the wire
// it looks like an ordinary ping conversation.
//
// icmpConn implements net.PacketConn, which is all kcp-go needs, so the KCP
// crypto, FEC and tuning are reused unchanged.
//
// Raw ICMP needs a raw socket (root). The server also asks the kernel to stop
// answering pings itself while the tunnel runs, because the kernel's automatic
// Echo Reply would otherwise race ours and corrupt the stream.

import (
	"crypto/sha256"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// icmpProtocol is the IANA protocol number for ICMP, passed to icmp.ParseMessage.
const icmpProtocol = 1

type icmpConn struct {
	c        net.PacketConn // a raw ip4:icmp socket
	sendType icmp.Type      // Echo on the client, EchoReply on the server
	recvType icmp.Type      // the type we accept (the other side's send type)
	id       int            // ICMP identifier from the PSK, so stray pings are ignored
	seq      uint32
	restore  func() // undoes icmp_echo_ignore_all on the server
}

// openRawICMP opens a raw ICMP socket and enlarges its buffers, because ICMP
// arrives in bursts and a raw socket's small default buffer drops packets under
// load (made worse on a single host, where a sent packet is also delivered back
// to the sender's own socket).
func openRawICMP(bind string) (net.PacketConn, error) {
	if bind == "" || bind == "icmp" {
		bind = "0.0.0.0"
	}
	c, err := net.ListenPacket("ip4:icmp", bind)
	if err != nil {
		return nil, err
	}
	if ip, ok := c.(*net.IPConn); ok {
		_ = ip.SetReadBuffer(4 << 20)
		_ = ip.SetWriteBuffer(4 << 20)
	}
	return c, nil
}

// icmpID derives the 16-bit ICMP identifier from the shared key so our packets
// are told apart from ordinary pings sharing the link.
func icmpID(psk string) int {
	h := sha256.Sum256([]byte("midos-icmp|" + psk))
	return int(h[0])<<8 | int(h[1])
}

func listenICMP(psk, bind string) (*icmpConn, error) {
	c, err := openRawICMP(bind)
	if err != nil {
		return nil, err
	}
	restore, err := silenceKernelPing()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return &icmpConn{c: c, sendType: ipv4.ICMPTypeEchoReply, recvType: ipv4.ICMPTypeEcho,
		id: icmpID(psk), restore: restore}, nil
}

// dialICMP opens the client side and returns the server address to give KCP as
// the remote peer.
func dialICMP(psk, server string) (*icmpConn, net.Addr, error) {
	ip, err := resolveIP4(server)
	if err != nil {
		return nil, nil, err
	}
	c, err := openRawICMP("0.0.0.0")
	if err != nil {
		return nil, nil, err
	}
	return &icmpConn{c: c, sendType: ipv4.ICMPTypeEcho, recvType: ipv4.ICMPTypeEchoReply,
		id: icmpID(psk)}, &net.IPAddr{IP: ip}, nil
}

func resolveIP4(host string) (net.IP, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h // tolerate a stray :port even though ICMP has none
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, errors.New("icmp: only IPv4 is supported")
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4, nil
		}
	}
	return nil, errors.New("icmp: no IPv4 address for " + host)
}

func (m *icmpConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, 1600)
	for {
		n, src, err := m.c.ReadFrom(buf)
		if err != nil {
			return 0, nil, err
		}
		b := buf[:n]
		// A raw ip4:icmp socket hands us the whole IP packet; skip its header
		// (the icmp helper socket would have stripped it, so cope with both).
		if n >= 20 && b[0]>>4 == 4 {
			ihl := int(b[0]&0x0f) * 4
			if ihl >= 20 && ihl <= n {
				b = b[ihl:]
			}
		}
		msg, err := icmp.ParseMessage(icmpProtocol, b)
		if err != nil || msg.Type != m.recvType {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || echo.ID != m.id {
			continue
		}
		return copy(p, echo.Data), src, nil
	}
}

func (m *icmpConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	ip, err := addrIP(addr)
	if err != nil {
		return 0, err
	}
	msg := &icmp.Message{Type: m.sendType, Code: 0,
		Body: &icmp.Echo{ID: m.id, Seq: int(atomic.AddUint32(&m.seq, 1) & 0xffff), Data: p}}
	b, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}
	if _, err := m.c.WriteTo(b, &net.IPAddr{IP: ip}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func addrIP(addr net.Addr) (net.IP, error) {
	switch a := addr.(type) {
	case *net.IPAddr:
		return a.IP, nil
	case *net.UDPAddr:
		return a.IP, nil
	default:
		return resolveIP4(addr.String())
	}
}

func (m *icmpConn) Close() error {
	if m.restore != nil {
		m.restore()
	}
	return m.c.Close()
}
func (m *icmpConn) LocalAddr() net.Addr                { return m.c.LocalAddr() }
func (m *icmpConn) SetDeadline(t time.Time) error      { return m.c.SetDeadline(t) }
func (m *icmpConn) SetReadDeadline(t time.Time) error  { return m.c.SetReadDeadline(t) }
func (m *icmpConn) SetWriteDeadline(t time.Time) error { return m.c.SetWriteDeadline(t) }

// icmpSession ties a KCP session to the ICMP socket it rides, so closing the
// carrier also frees the raw socket (kcp-go does not own a conn it was given).
type icmpSession struct {
	*kcp.UDPSession
	pc net.PacketConn
}

func (s *icmpSession) Close() error {
	err := s.UDPSession.Close()
	_ = s.pc.Close()
	return err
}

var (
	pingKnob     = "/proc/sys/net/ipv4/icmp_echo_ignore_all"
	icmpStateDir = "/run/midos-icmp" // one file per running ICMP listener, plus "orig"
	icmpSeq      atomic.Int32
	// processAlive reports whether a listener recorded under pid still runs
	processAlive = func(pid int) bool {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		return err == nil && strings.Contains(string(b), "midos-tunnel")
	}
)

// silenceKernelPing sets net.ipv4.icmp_echo_ignore_all=1 and returns a function
// that undoes it. Several ICMP tunnels can listen on one server, so each one
// records itself under icmpStateDir and only the last one to stop puts the
// knob back to the value it had before the first one started. The undo also
// runs when the process is told to stop. Where the knob does not exist (not
// Linux) it is a no-op - that only happens on a box that will not serve ICMP.
func silenceKernelPing() (func(), error) {
	prev, err := os.ReadFile(pingKnob)
	if err != nil {
		return func() {}, nil
	}
	if err := os.MkdirAll(icmpStateDir, 0o700); err != nil {
		return nil, errors.New("icmp: " + err.Error())
	}
	orig := filepath.Join(icmpStateDir, "orig")
	if !otherICMPListeners("") {
		_ = os.WriteFile(orig, prev, 0o600)
	}
	mine := filepath.Join(icmpStateDir, strconv.Itoa(os.Getpid())+"."+strconv.Itoa(int(icmpSeq.Add(1))))
	if err := os.WriteFile(mine, nil, 0o600); err != nil {
		return nil, errors.New("icmp: " + err.Error())
	}
	if err := os.WriteFile(pingKnob, []byte("1\n"), 0o644); err != nil {
		_ = os.Remove(mine)
		return nil, errors.New("icmp: cannot silence kernel ping replies (need root): " + err.Error())
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			_ = os.Remove(mine)
			if otherICMPListeners(mine) {
				return
			}
			if b, err := os.ReadFile(orig); err == nil {
				_ = os.WriteFile(pingKnob, b, 0o644)
			}
			_ = os.Remove(orig)
		})
	}
	onExit(restore)
	return restore, nil
}

// otherICMPListeners reports whether an ICMP listener other than skip is
// still running, clearing the records of listeners that died without
// cleaning up.
func otherICMPListeners(skip string) bool {
	ents, _ := os.ReadDir(icmpStateDir)
	alive := false
	for _, e := range ents {
		p := filepath.Join(icmpStateDir, e.Name())
		pid, _, ok := strings.Cut(e.Name(), ".")
		n, err := strconv.Atoi(pid)
		if !ok || err != nil || p == skip {
			continue
		}
		if processAlive(n) {
			alive = true
		} else {
			_ = os.Remove(p)
		}
	}
	return alive
}
