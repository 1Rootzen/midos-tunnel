package main

// Carriers are the long-lived connections the multiplexer runs over. Every
// transport hands back a plain net.Conn, so the pre-shared key challenge, the
// multiplexer and the port relaying above it are identical whichever is used:
//
//	tls   TLS over TCP with a real browser's ClientHello (the default)
//	ws    a WebSocket inside that TLS session, so it can cross a CDN
//	tcp   bare TCP: the least overhead, but nothing disguises the traffic
//	kcp   a reliable stream over UDP with forward error correction. On a lossy
//	      route TCP stalls a whole round trip on every lost segment; parity
//	      shards let the receiver rebuild most losses on the spot instead.
//	quic  QUIC over UDP (TLS 1.3, ALPN h3), which on the wire is HTTP/3
//	icmp  kcp inside ICMP echo packets, for paths where only ping gets through
//	grpc  one bidirectional gRPC call over HTTP/2 + TLS (CDN-friendly)
//	ssh   a forwarded channel through the listening server's own sshd
//
// Which side dials and which side listens is independent of the transport (see
// Config.Reverse), so each transport needs exactly one dialer and one listener.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

const dialTimeout = 15 * time.Second

// linkInfo is what a transport can tell about the path under one carrier.
type linkInfo struct {
	RTTms   float64 `json:"rtt_ms"`
	RTTVar  float64 `json:"rtt_var_ms"`
	Retrans uint64  `json:"retrans"`
	known   bool
}

// carrier is one transport connection plus what is known about its path.
type carrier struct {
	net.Conn
	ep    Endpoint
	since time.Time
	probe func() linkInfo // nil when the transport cannot measure its path
}

func (c *carrier) link() linkInfo {
	if c.probe == nil {
		return linkInfo{}
	}
	return c.probe()
}

// ---------------------------------------------------------------------------
// dialing
// ---------------------------------------------------------------------------

func dialCarrier(cfg *Config, ep Endpoint) (*carrier, error) {
	c := &carrier{ep: ep, since: time.Now()}
	switch ep.Transport {
	case "tls", "ws":
		conn, raw, err := dialTLS(cfg, ep)
		if err != nil {
			return nil, err
		}
		c.Conn, c.probe = conn, tcpProbe(raw)
	case "tcp":
		raw, err := net.DialTimeout("tcp", ep.Server, dialTimeout)
		if err != nil {
			return nil, err
		}
		tuneTCP(raw)
		c.Conn, c.probe = raw, tcpProbe(raw)
	case "stealth":
		raw, err := net.DialTimeout("tcp", ep.Server, dialTimeout)
		if err != nil {
			return nil, err
		}
		tuneTCP(raw)
		sc, err := newStealthConn(raw, cfg.PSK, true, cfg.Shape)
		if err != nil {
			_ = raw.Close()
			return nil, err
		}
		c.Conn, c.probe = sc, tcpProbe(raw)
	case "kcp":
		s, err := kcp.DialWithOptions(ep.Server, kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards)
		if err != nil {
			return nil, err
		}
		tuneKCP(cfg, s)
		if _, err := s.Write([]byte{dialerHello}); err != nil {
			_ = s.Close()
			return nil, err
		}
		c.Conn, c.probe = s, kcpProbe(s)
	case "icmp":
		pc, raddr, err := dialICMP(cfg.PSK, ep.Server)
		if err != nil {
			return nil, err
		}
		s, err := kcp.NewConn2(raddr, kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards, pc)
		if err != nil {
			_ = pc.Close()
			return nil, err
		}
		tuneKCP(cfg, s)
		if _, err := s.Write([]byte{dialerHello}); err != nil {
			_ = s.Close()
			_ = pc.Close()
			return nil, err
		}
		c.Conn, c.probe = &icmpSession{UDPSession: s, pc: pc}, kcpProbe(s)
	case "dns":
		pc, raddr, err := dialDNS(cfg.PSK, ep.Server)
		if err != nil {
			return nil, err
		}
		s, err := kcp.NewConn2(raddr, kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards, pc)
		if err != nil {
			_ = pc.Close()
			return nil, err
		}
		_ = s.SetMtu(dnsMTU)
		tuneKCP(cfg, s)
		if _, err := s.Write([]byte{dialerHello}); err != nil {
			_ = s.Close()
			_ = pc.Close()
			return nil, err
		}
		c.Conn, c.probe = &dnsSession{UDPSession: s, pc: pc}, kcpProbe(s)
	case "quic":
		conn, err := dialQUIC(cfg, ep)
		if err != nil {
			return nil, err
		}
		c.Conn, c.probe = conn, conn.probe
	case "grpc":
		conn, raw, err := dialGRPC(cfg, ep)
		if err != nil {
			return nil, err
		}
		c.Conn, c.probe = conn, tcpProbe(raw)
	case "ssh":
		conn, raw, err := dialSSHCarrier(cfg, ep)
		if err != nil {
			return nil, err
		}
		c.Conn, c.probe = conn, tcpProbe(raw)
	default:
		return nil, fmt.Errorf("unknown transport %q", ep.Transport)
	}
	return c, nil
}

// dialAuthenticated opens a carrier to the first endpoint that answers and
// proves the pre-shared key over it. good remembers the endpoint that last
// worked, so a failover sticks instead of retrying the dead path every time.
func dialAuthenticated(cfg *Config, good *atomic.Int32) (*carrier, error) {
	eps := cfg.Endpoints
	if len(eps) == 0 {
		return nil, errors.New("no endpoint configured")
	}
	tries := 2
	if len(eps) == 1 {
		// some networks reset a random share of handshakes; with nowhere else
		// to go, one unlucky reset should not fail the user's connection
		tries = 3
	}
	start := int(good.Load())
	var lastErr error
	for i := range eps {
		idx := (start + i) % len(eps)
		ep := eps[idx]
		for attempt := 0; attempt < tries; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
			}
			c, err := dialCarrier(cfg, ep)
			if err != nil {
				lastErr = fmt.Errorf("%s %s: %w", ep.Transport, ep.Server, err)
				continue
			}
			if err := clientHandshake(c, cfg.PSK); err != nil {
				_ = c.Close()
				lastErr = fmt.Errorf("%s %s: %w", ep.Transport, ep.Server, err)
				continue
			}
			if err := padCarrier(cfg, c, true); err != nil {
				_ = c.Close()
				lastErr = fmt.Errorf("%s %s: padding: %w", ep.Transport, ep.Server, err)
				continue
			}
			if old := good.Swap(int32(idx)); int(old) != idx {
				log.Printf("switched to endpoint %d (%s %s)", idx+1, ep.Transport, ep.Server)
			}
			return c, nil
		}
	}
	return nil, lastErr
}

func tuneTCP(c net.Conn) {
	// TCP_NODELAY matters here: the multiplexer sends many small frames and
	// Nagle would add a round trip of latency to each interactive stream.
	if t, ok := c.(*net.TCPConn); ok {
		_ = t.SetNoDelay(true)
		_ = t.SetKeepAlive(true)
		_ = t.SetKeepAlivePeriod(30 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// listening
// ---------------------------------------------------------------------------

// listenCarriers starts every listener the configuration names and hands each
// new carrier to accept. It returns when the first listener fails.
func listenCarriers(cfg *Config, accept func(*carrier)) error {
	errCh := make(chan error, 10)
	n := 0
	start := func(what string, run func() error) {
		n++
		go func() { errCh <- fmt.Errorf("%s listener: %w", what, run()) }()
	}

	if cfg.Listen != "" {
		tc, err := serverTLSConfig(cfg)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", cfg.Listen)
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("tls carriers on %s", cfg.Listen)
		if cfg.WSPath != "" {
			log.Printf("websocket sessions accepted on the secret path")
		}
		if cfg.Camouflage {
			log.Printf("camouflage: other connections go to %s", cfg.camoTarget())
		}
		start("tls", func() error {
			return acceptLoop(ln, func(raw net.Conn) {
				tlsRaw := raw
				if cfg.Camouflage {
					if tlsRaw = camoGate(cfg, raw); tlsRaw == nil {
						return // not a tunnel client: handed to the real site
					}
				}
				conn, err := openTLSSession(cfg, tlsRaw, tc)
				if err != nil {
					_ = raw.Close()
					return
				}
				accept(&carrier{Conn: conn, ep: Endpoint{"tls", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			})
		})
	}

	if cfg.HTTPListen != "" {
		ln, err := net.Listen("tcp", cfg.HTTPListen)
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("plain-http websocket listener on %s", cfg.HTTPListen)
		start("http", func() error {
			return acceptLoop(ln, func(raw net.Conn) {
				conn, err := wsServerUpgrade(raw, newBufReader(raw), cfg.WSPath)
				if err != nil {
					_ = raw.Close()
					return
				}
				accept(&carrier{Conn: conn, ep: Endpoint{"ws", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			})
		})
	}

	if cfg.TCPListen != "" {
		ln, err := net.Listen("tcp", cfg.TCPListen)
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("tcp carriers on %s", cfg.TCPListen)
		start("tcp", func() error {
			return acceptLoop(ln, func(raw net.Conn) {
				accept(&carrier{Conn: raw, ep: Endpoint{"tcp", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			})
		})
	}

	if cfg.StealthListen != "" {
		ln, err := net.Listen("tcp", cfg.StealthListen)
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("stealth carriers on %s", cfg.StealthListen)
		start("stealth", func() error {
			return acceptLoop(ln, func(raw net.Conn) {
				sc, err := newStealthConn(raw, cfg.PSK, false, cfg.Shape)
				if err != nil {
					_ = raw.Close()
					return
				}
				accept(&carrier{Conn: sc, ep: Endpoint{"stealth", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			})
		})
	}

	if cfg.KCPListen != "" {
		ln, err := kcp.ListenWithOptions(cfg.KCPListen, kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards)
		if err != nil {
			return err
		}
		defer ln.Close()
		_ = ln.SetReadBuffer(4 << 20)
		_ = ln.SetWriteBuffer(4 << 20)
		log.Printf("kcp carriers on %s (fec %d+%d)", cfg.KCPListen, cfg.KCPDataShards, cfg.KCPParityShards)
		start("kcp", func() error {
			for {
				s, err := ln.AcceptKCP()
				if err != nil {
					return err
				}
				tuneKCP(cfg, s)
				go func() {
					if readDialerHello(s) != nil {
						_ = s.Close()
						return
					}
					accept(&carrier{Conn: s, ep: Endpoint{"kcp", s.RemoteAddr().String()}, since: time.Now(), probe: kcpProbe(s)})
				}()
			}
		})
	}

	if cfg.ICMPListen != "" {
		pc, err := listenICMP(cfg.PSK, cfg.ICMPListen)
		if err != nil {
			return err
		}
		ln, err := kcp.ServeConn(kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards, pc)
		if err != nil {
			_ = pc.Close()
			return err
		}
		defer ln.Close()
		defer pc.Close()
		log.Printf("icmp carriers on %s (fec %d+%d)", cfg.ICMPListen, cfg.KCPDataShards, cfg.KCPParityShards)
		start("icmp", func() error {
			for {
				s, err := ln.AcceptKCP()
				if err != nil {
					return err
				}
				tuneKCP(cfg, s)
				go func() {
					if readDialerHello(s) != nil {
						_ = s.Close()
						return
					}
					accept(&carrier{Conn: s, ep: Endpoint{"icmp", s.RemoteAddr().String()}, since: time.Now(), probe: kcpProbe(s)})
				}()
			}
		})
	}

	if cfg.GRPCListen != "" {
		tc, err := serverTLSConfig(cfg)
		if err != nil {
			return err
		}
		tc = tc.Clone()
		tc.NextProtos = []string{"h2"}
		ln, err := net.Listen("tcp", cfg.GRPCListen)
		if err != nil {
			return err
		}
		defer ln.Close()
		h2 := grpcServer()
		log.Printf("grpc carriers on %s (%s)", cfg.GRPCListen, grpcPath(cfg))
		start("grpc", func() error {
			return acceptLoop(ln, func(raw net.Conn) { serveGRPC(cfg, raw, tc, h2, accept) })
		})
	}

	if cfg.SSHListen != "" {
		// sshd forwards each carrier here as a plain loopback connection
		ln, err := net.Listen("tcp", cfg.SSHListen)
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("ssh-forwarded carriers on %s", cfg.SSHListen)
		start("ssh", func() error {
			return acceptLoop(ln, func(raw net.Conn) {
				accept(&carrier{Conn: raw, ep: Endpoint{"ssh", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			})
		})
	}

	if cfg.DNSListen != "" {
		pc, err := listenDNS(cfg.PSK, cfg.DNSListen)
		if err != nil {
			return err
		}
		ln, err := kcp.ServeConn(kcpBlock(cfg.PSK), cfg.KCPDataShards, cfg.KCPParityShards, pc)
		if err != nil {
			_ = pc.Close()
			return err
		}
		defer ln.Close()
		defer pc.Close()
		log.Printf("dns carriers on %s (fec %d+%d)", cfg.DNSListen, cfg.KCPDataShards, cfg.KCPParityShards)
		start("dns", func() error {
			for {
				s, err := ln.AcceptKCP()
				if err != nil {
					return err
				}
				_ = s.SetMtu(dnsMTU)
				tuneKCP(cfg, s)
				go func() {
					if readDialerHello(s) != nil {
						_ = s.Close()
						return
					}
					accept(&carrier{Conn: s, ep: Endpoint{"dns", s.RemoteAddr().String()}, since: time.Now(), probe: kcpProbe(s)})
				}()
			}
		})
	}

	if cfg.QUICListen != "" {
		tc, err := serverTLSConfig(cfg)
		if err != nil {
			return err
		}
		tc = tc.Clone()
		tc.NextProtos = []string{"h3"}
		tc.MinVersion = tls.VersionTLS13
		ln, err := quic.ListenAddr(cfg.QUICListen, tc, quicConfig())
		if err != nil {
			return err
		}
		defer ln.Close()
		log.Printf("quic carriers on %s", cfg.QUICListen)
		start("quic", func() error {
			for {
				qc, err := ln.Accept(context.Background())
				if err != nil {
					return err
				}
				go func() {
					c, err := acceptQUIC(qc)
					if err != nil {
						_ = qc.CloseWithError(0, "")
						return
					}
					accept(&carrier{Conn: c, ep: Endpoint{"quic", qc.RemoteAddr().String()}, since: time.Now(), probe: c.probe})
				}()
			}
		})
	}

	if n == 0 {
		return errors.New("no listener configured")
	}
	return <-errCh
}

func acceptLoop(ln net.Listener, serve func(net.Conn)) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return err
		}
		tuneTCP(c)
		go serve(c)
	}
}

// ---------------------------------------------------------------------------
// kcp
// ---------------------------------------------------------------------------

// kcpBlock derives the packet cipher from the pre-shared key. KCP datagrams
// are encrypted individually, so without this the payload and the KCP header
// would be readable on the wire.
func kcpBlock(psk string) kcp.BlockCrypt {
	k := sha256.Sum256([]byte("midos-kcp|" + psk))
	b, err := kcp.NewAESBlockCrypt(k[:])
	if err != nil {
		panic(err) // a 32-byte key is always valid
	}
	return b
}

func tuneKCP(cfg *Config, s *kcp.UDPSession) {
	s.SetStreamMode(true)
	s.SetWriteDelay(false)
	// fast mode: 20 ms internal clock, fast resend after 2 duplicate ACKs, no
	// congestion window backing off on random loss - loss on these routes is
	// mostly policing and noise, not a full queue
	t := cfg.tune()
	s.SetNoDelay(1, 20, t.kcpResend, 1)
	s.SetWindowSize(t.kcpWindow, t.kcpWindow)
	s.SetMtu(1350)
	s.SetACKNoDelay(true)
	_ = s.SetReadBuffer(4 << 20)
	_ = s.SetWriteBuffer(4 << 20)
}

func kcpProbe(s *kcp.UDPSession) func() linkInfo {
	return func() linkInfo {
		return linkInfo{RTTms: float64(s.GetSRTT()), RTTVar: float64(s.GetSRTTVar()), known: true}
	}
}

// ---------------------------------------------------------------------------
// quic
// ---------------------------------------------------------------------------

func quicConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout: dialTimeout,
		MaxIdleTimeout:       60 * time.Second,
		KeepAlivePeriod:      15 * time.Second,
		MaxIncomingStreams:   4,
	}
}

// quicConn is one QUIC stream presented as a net.Conn. The multiplexer above
// needs a single byte stream, so each QUIC connection carries exactly one.
type quicConn struct {
	*quic.Stream
	conn *quic.Conn
}

func (c *quicConn) Close() error {
	_ = c.Stream.Close()
	return c.conn.CloseWithError(0, "")
}
func (c *quicConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *quicConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

func (c *quicConn) probe() linkInfo {
	s := c.conn.ConnectionStats()
	return linkInfo{
		RTTms:   float64(s.SmoothedRTT.Microseconds()) / 1000,
		RTTVar:  float64(s.MeanDeviation.Microseconds()) / 1000,
		Retrans: s.PacketsLost,
		known:   true,
	}
}

// dialerHello is the first byte a dialer sends on the connectionless
// transports. A KCP session, like a QUIC stream, does not exist for the
// listener until data arrives on it, and in this protocol the listening side
// speaks first (the challenge) - so without this byte the two would wait for
// each other.
const dialerHello = 0x01

func readDialerHello(c net.Conn) error {
	var b [1]byte
	_ = c.SetReadDeadline(time.Now().Add(authTimeout))
	defer c.SetReadDeadline(time.Time{})
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return err
	}
	if b[0] != dialerHello {
		return errors.New("unexpected first byte")
	}
	return nil
}

func dialQUIC(cfg *Config, ep Endpoint) (*quicConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	tc := &tls.Config{
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.Insecure,
		NextProtos:         []string{"h3"},
		MinVersion:         tls.VersionTLS13,
	}
	qc, err := quic.DialAddr(ctx, ep.Server, tc, quicConfig())
	if err != nil {
		return nil, err
	}
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		_ = qc.CloseWithError(0, "")
		return nil, err
	}
	if _, err := st.Write([]byte{dialerHello}); err != nil {
		_ = qc.CloseWithError(0, "")
		return nil, err
	}
	return &quicConn{Stream: st, conn: qc}, nil
}

func acceptQUIC(qc *quic.Conn) (*quicConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	st, err := qc.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	c := &quicConn{Stream: st, conn: qc}
	if err := readDialerHello(c); err != nil {
		return nil, fmt.Errorf("quic: %w", err)
	}
	return c, nil
}
