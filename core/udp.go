package main

// UDP over the tunnel.
//
// Some networks let only TCP leave (an Iranian data centre that drops outbound
// UDP, ESP and GRE to foreign addresses is the case this was written for), so a
// UDP service - WireGuard, OpenVPN over UDP - can only cross inside the same TLS
// session the TCP ports use.
//
// Each UDP flow (one client address on one relayed port) becomes one stream:
//
//	stream header   0x0000 | 2-byte port      (port 0 never names a TCP stream,
//	                                           so an older server simply refuses)
//	then frames     2-byte length | datagram   in both directions
//
// Datagram boundaries survive because every datagram is framed; ordering is
// whatever TCP gives, which UDP applications already tolerate. A flow that is
// quiet for udpIdle in both directions is closed on both ends.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

const (
	udpIdle     = 3 * time.Minute
	maxDatagram = 65535
)

var errDatagramTooLarge = errors.New("datagram larger than 65535 bytes")

// writeUDPHeader opens a UDP stream: the reserved port 0, then the real port.
func writeUDPHeader(w io.Writer, port uint16) error {
	var b [4]byte
	binary.BigEndian.PutUint16(b[2:], port)
	_, err := w.Write(b[:])
	return err
}

// writeFrame sends one datagram as a single write, so it is never interleaved
// with another writer's bytes.
func writeFrame(w io.Writer, p []byte) error {
	if len(p) > maxDatagram {
		return errDatagramTooLarge
	}
	buf := make([]byte, 2+len(p))
	binary.BigEndian.PutUint16(buf, uint16(len(p)))
	copy(buf[2:], p)
	_, err := w.Write(buf)
	return err
}

// readFrame reads one datagram into buf and returns its length.
func readFrame(r io.Reader, buf []byte) (int, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(h[:]))
	if n > len(buf) {
		return 0, errDatagramTooLarge
	}
	_, err := io.ReadFull(r, buf[:n])
	return n, err
}

// ---------------------------------------------------------------------------
// client side (Iran): listen on UDP, one stream per client address
// ---------------------------------------------------------------------------

// udpQueue is how many datagrams a flow holds while its stream is being opened
// or its carrier is slow; beyond that datagrams are dropped, as UDP would.
const udpQueue = 256

type udpFlow struct {
	out  chan []byte
	last atomic.Int64 // unix nanoseconds of the last datagram either way

	closeOnce sync.Once
	done      chan struct{}
	mu        sync.Mutex
	st        *smux.Stream // nil until the stream is open
}

func newUDPFlow() *udpFlow {
	f := &udpFlow{out: make(chan []byte, udpQueue), done: make(chan struct{})}
	f.touch()
	return f
}

func (f *udpFlow) touch() { f.last.Store(time.Now().UnixNano()) }

func (f *udpFlow) idle(now time.Time) bool {
	return now.Sub(time.Unix(0, f.last.Load())) > udpIdle
}

func (f *udpFlow) close() {
	f.closeOnce.Do(func() {
		close(f.done)
		f.mu.Lock()
		if f.st != nil {
			_ = f.st.Close()
		}
		f.mu.Unlock()
	})
}

// run opens the flow's stream and pumps datagrams both ways. It runs in its
// own goroutine, so a carrier that cannot connect stalls only this flow and
// never the socket's read loop (which would starve every other client).
func (f *udpFlow) run(pool *carrierPool, m portMap, pc *net.UDPConn, addr *net.UDPAddr) {
	defer f.close()
	st, err := pool.stream()
	if err != nil {
		log.Printf("no carrier for udp :%d: %v", m.remote, err)
		return
	}
	f.mu.Lock()
	select {
	case <-f.done: // dropped while the stream was being opened
		f.mu.Unlock()
		_ = st.Close()
		return
	default:
	}
	f.st = st
	f.mu.Unlock()
	if writeUDPHeader(st, m.remote) != nil {
		return
	}
	ctr := stats.port("udp", m.remote)
	ctr.total.Add(1)
	ctr.active.Add(1)
	defer ctr.active.Add(-1)

	go func() {
		defer f.close()
		in := make([]byte, maxDatagram)
		for {
			k, err := readFrame(st, in)
			if err != nil {
				return
			}
			f.touch()
			if _, err := pc.WriteToUDP(in[:k], addr); err != nil {
				return
			}
			ctr.down.Add(uint64(k))
		}
	}()

	for {
		select {
		case <-f.done:
			return
		case p := <-f.out:
			if writeFrame(st, p) != nil {
				return
			}
			ctr.up.Add(uint64(len(p)))
		}
	}
}

func serveUDPPort(pool *carrierPool, m portMap) error {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{Port: int(m.local)})
	if err != nil {
		return fmt.Errorf("listen udp %d: %w", m.local, err)
	}
	defer pc.Close()
	log.Printf("relaying udp :%d -> remote udp %d", m.local, m.remote)

	var mu sync.Mutex
	flows := map[string]*udpFlow{}
	drop := func(key string, f *udpFlow) {
		mu.Lock()
		if flows[key] == f {
			delete(flows, key)
		}
		mu.Unlock()
		f.close()
	}

	// Idle flows are closed so a client that went away does not hold a stream
	// (and a socket on the server) forever.
	go func() {
		for range time.Tick(30 * time.Second) {
			now := time.Now()
			mu.Lock()
			var stale []string
			for k, f := range flows {
				if f.idle(now) {
					stale = append(stale, k)
				}
			}
			mu.Unlock()
			for _, k := range stale {
				mu.Lock()
				f := flows[k]
				mu.Unlock()
				if f != nil {
					drop(k, f)
				}
			}
		}
	}()

	buf := make([]byte, maxDatagram)
	for {
		n, addr, err := pc.ReadFromUDP(buf)
		if err != nil {
			return err
		}
		key := addr.String()
		mu.Lock()
		f := flows[key]
		if f == nil {
			f = newUDPFlow()
			flows[key] = f
			go func(key string, f *udpFlow, addr *net.UDPAddr) {
				defer drop(key, f)
				f.run(pool, m, pc, addr)
			}(key, f, addr)
		}
		mu.Unlock()
		f.touch()
		p := make([]byte, n)
		copy(p, buf[:n])
		select {
		case f.out <- p:
		default: // queue full: drop, as a congested UDP path would
		}
	}
}

// ---------------------------------------------------------------------------
// server side (abroad): one UDP socket to the local service per stream
// ---------------------------------------------------------------------------

func handleUDPStream(target string, st *smux.Stream, port uint16) {
	conn, err := net.Dial("udp", target)
	if err != nil {
		log.Printf("dial udp %s: %v", target, err)
		return
	}
	defer conn.Close()
	ctr := stats.port("udp", port)
	ctr.total.Add(1)
	ctr.active.Add(1)
	defer ctr.active.Add(-1)

	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done); _ = st.Close(); _ = conn.Close() }) }
	defer stop()

	// service -> client
	go func() {
		defer stop()
		buf := make([]byte, maxDatagram)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(udpIdle))
			n, err := conn.Read(buf)
			if err != nil {
				// A read timeout alone is not idleness: traffic may be flowing
				// the other way. Only give up when both directions were quiet.
				if ne, ok := err.(net.Error); ok && ne.Timeout() &&
					time.Since(time.Unix(0, last.Load())) < udpIdle {
					continue
				}
				return
			}
			last.Store(time.Now().UnixNano())
			if writeFrame(st, buf[:n]) != nil {
				return
			}
			ctr.down.Add(uint64(n))
		}
	}()

	// client -> service
	buf := make([]byte, maxDatagram)
	for {
		n, err := readFrame(st, buf)
		if err != nil {
			return
		}
		last.Store(time.Now().UnixNano())
		if _, err := conn.Write(buf[:n]); err != nil {
			return
		}
		ctr.up.Add(uint64(n))
		select {
		case <-done:
			return
		default:
		}
	}
}
