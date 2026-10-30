package main

// The udp-dns transport carries datagrams inside UDP packets shaped like DNS
// messages on port 53. On a network that lets DNS out but little else, this
// leaves a path open: each packet is a well-formed DNS query (from the client)
// or response (from the server), built with the standard library's DNS encoder
// so it parses as real DNS. Our bytes ride in a TXT record; a label derived
// from the pre-shared key marks our own packets so stray real DNS is ignored.
//
// Like ICMP, DNS gives no reliability or ordering, so KCP runs on top exactly
// as it does over plain UDP - dnsConn implements net.PacketConn, which is all
// kcp-go needs. Unlike ICMP, UDP has ports, so several carriers from one client
// are told apart by source port and no single-carrier cap is needed.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"golang.org/x/net/dns/dnsmessage"
)

// dnsMTU keeps a wrapped packet comfortably inside one UDP datagram after the
// DNS header, question and TXT framing are added.
const dnsMTU = 1100

// dnsTag is an 8-character label derived from the key; our packets carry it as
// the first label of the question name, so we recognise our own traffic.
func dnsTag(psk string) string {
	h := sha256.Sum256([]byte("midos-dns|" + psk))
	return hex.EncodeToString(h[:4])
}

func dnsName(tag string) dnsmessage.Name {
	n, err := dnsmessage.NewName(tag + ".dns.") // e.g. "1a2b3c4d.dns."
	if err != nil {
		// the tag is hex, so this never fails; fall back to a safe constant
		n, _ = dnsmessage.NewName("tunnel.dns.")
	}
	return n
}

// chunkTXT splits the payload into TXT character-strings (max 255 bytes each).
func chunkTXT(p []byte) []string {
	var out []string
	for len(p) > 0 {
		n := len(p)
		if n > 255 {
			n = 255
		}
		out = append(out, string(p[:n]))
		p = p[n:]
	}
	if out == nil {
		out = []string{""}
	}
	return out
}

func dnsWrap(tag string, id uint16, response bool, payload []byte) ([]byte, error) {
	name := dnsName(tag)
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{ID: id, Response: response, RecursionDesired: true, RecursionAvailable: response},
		Questions: []dnsmessage.Question{{
			Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET,
		}},
	}
	rh := dnsmessage.ResourceHeader{Name: name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: 60}
	res := dnsmessage.Resource{Header: rh, Body: &dnsmessage.TXTResource{TXT: chunkTXT(payload)}}
	// a query carries the bytes in the additional section, a response in the
	// answer section - the shape each normally uses
	if response {
		msg.Answers = append(msg.Answers, res)
	} else {
		msg.Additionals = append(msg.Additionals, res)
	}
	return msg.Pack()
}

// dnsUnwrap pulls our payload back out, or reports ok=false for anything that
// is not one of our packets (a real DNS query to the same socket, say).
func dnsUnwrap(tag string, b []byte) ([]byte, bool) {
	var p dnsmessage.Parser
	hdr, err := p.Start(b)
	if err != nil {
		return nil, false
	}
	q, err := p.Question()
	if err != nil {
		return nil, false
	}
	// the first label is our marker
	qn := q.Name.String()
	if len(qn) < len(tag) || qn[:len(tag)] != tag {
		return nil, false
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, false
	}
	// the payload rides in the answer (response) or additional (query) section
	var txt *dnsmessage.TXTResource
	if hdr.Response {
		for {
			r, err := p.AnswerHeader()
			if err == dnsmessage.ErrSectionDone {
				break
			}
			if err != nil {
				return nil, false
			}
			if r.Type == dnsmessage.TypeTXT {
				t, err := p.TXTResource()
				if err != nil {
					return nil, false
				}
				txt = &t
				break
			}
			if err := p.SkipAnswer(); err != nil {
				return nil, false
			}
		}
	} else {
		_ = p.SkipAllAnswers()
		_ = p.SkipAllAuthorities()
		for {
			r, err := p.AdditionalHeader()
			if err == dnsmessage.ErrSectionDone {
				break
			}
			if err != nil {
				return nil, false
			}
			if r.Type == dnsmessage.TypeTXT {
				t, err := p.TXTResource()
				if err != nil {
					return nil, false
				}
				txt = &t
				break
			}
			if err := p.SkipAdditional(); err != nil {
				return nil, false
			}
		}
	}
	if txt == nil {
		return nil, false
	}
	var out []byte
	for _, s := range txt.TXT {
		out = append(out, s...)
	}
	return out, true
}

// dnsConn presents a UDP socket that wraps every datagram as DNS, as a
// net.PacketConn for kcp-go.
type dnsConn struct {
	c        net.PacketConn
	tag      string
	response bool // the server answers with responses; the client sends queries
	id       uint32
}

func listenDNS(psk, bind string) (*dnsConn, error) {
	c, err := net.ListenPacket("udp", bind)
	if err != nil {
		return nil, err
	}
	if u, ok := c.(*net.UDPConn); ok {
		_ = u.SetReadBuffer(4 << 20)
		_ = u.SetWriteBuffer(4 << 20)
	}
	return &dnsConn{c: c, tag: dnsTag(psk), response: true}, nil
}

func dialDNS(psk, server string) (*dnsConn, net.Addr, error) {
	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, nil, err
	}
	c, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		return nil, nil, err
	}
	if u, ok := c.(*net.UDPConn); ok {
		_ = u.SetReadBuffer(4 << 20)
		_ = u.SetWriteBuffer(4 << 20)
	}
	return &dnsConn{c: c, tag: dnsTag(psk), response: false}, raddr, nil
}

func (d *dnsConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, 2048)
	for {
		n, src, err := d.c.ReadFrom(buf)
		if err != nil {
			return 0, nil, err
		}
		payload, ok := dnsUnwrap(d.tag, buf[:n])
		if !ok {
			continue // not ours: a stray or real DNS packet
		}
		return copy(p, payload), src, nil
	}
}

func (d *dnsConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	id := uint16(atomic.AddUint32(&d.id, 1))
	b, err := dnsWrap(d.tag, id, d.response, p)
	if err != nil {
		return 0, err
	}
	if _, err := d.c.WriteTo(b, addr); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (d *dnsConn) Close() error                       { return d.c.Close() }
func (d *dnsConn) LocalAddr() net.Addr                { return d.c.LocalAddr() }
func (d *dnsConn) SetDeadline(t time.Time) error      { return d.c.SetDeadline(t) }
func (d *dnsConn) SetReadDeadline(t time.Time) error  { return d.c.SetReadDeadline(t) }
func (d *dnsConn) SetWriteDeadline(t time.Time) error { return d.c.SetWriteDeadline(t) }

// dnsSession ties a KCP session to the UDP socket it rides, so closing the
// carrier frees the socket (kcp-go does not own a conn it was handed).
type dnsSession struct {
	*kcp.UDPSession
	pc net.PacketConn
}

func (s *dnsSession) Close() error {
	err := s.UDPSession.Close()
	_ = s.pc.Close()
	return err
}

var errDNSResolve = errors.New("udp-dns: could not resolve server")
