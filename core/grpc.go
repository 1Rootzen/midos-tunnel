package main

// grpc: the carrier rides one bidirectional gRPC call over HTTP/2 + TLS. On the
// wire it is a POST to /<service>/Tun with content-type application/grpc whose
// body is a stream of ordinary length-prefixed gRPC messages, so a CDN with
// gRPC support (and any middlebox that lets gRPC through) carries it like any
// other gRPC traffic. The pre-shared key challenge still runs inside it.
//
// One carrier is one TLS connection with one call on it; closing the carrier
// closes both. HTTP/2 flow control is sized up from its 64 KiB default so a
// single call is not starved on a long, fat path.

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

const (
	grpcContentType = "application/grpc"
	grpcMaxMessage  = 32 << 10 // payload per gRPC message we send
)

func grpcPath(cfg *Config) string {
	svc := strings.Trim(cfg.GRPCService, "/")
	if svc == "" {
		svc = "GunService"
	}
	return "/" + svc + "/Tun"
}

// killTimer stands in for deadlines on carriers that have none of their own (a
// gRPC call, an SSH channel): when a deadline passes the carrier is closed,
// which is what the handshake's deadline is there to achieve.
type killTimer struct {
	mu   sync.Mutex
	t    *time.Timer
	kill func()
}

func (k *killTimer) set(at time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.t != nil {
		k.t.Stop()
		k.t = nil
	}
	if at.IsZero() {
		return nil
	}
	d := time.Until(at)
	if d <= 0 {
		d = time.Millisecond
	}
	k.t = time.AfterFunc(d, k.kill)
	return nil
}

// grpcConn carries a byte stream as a sequence of gRPC messages.
type grpcConn struct {
	r             io.ReadCloser
	w             io.Writer
	flush         func()
	closeFn       func()
	local, remote net.Addr

	rmu  sync.Mutex
	left uint32 // bytes of the current incoming message not yet read
	hdr  [5]byte

	wmu    sync.Mutex
	wbuf   []byte
	closed bool

	once sync.Once
	dl   killTimer
}

func (c *grpcConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for c.left == 0 {
		if _, err := io.ReadFull(c.r, c.hdr[:]); err != nil {
			return 0, err
		}
		if c.hdr[0] != 0 {
			return 0, errors.New("grpc: compressed message not expected")
		}
		c.left = binary.BigEndian.Uint32(c.hdr[1:])
	}
	if uint32(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= uint32(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (c *grpcConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > grpcMaxMessage {
			chunk = chunk[:grpcMaxMessage]
		}
		// header and payload in one write, so each message is one DATA frame
		c.wbuf = append(c.wbuf[:0], 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(c.wbuf[1:], uint32(len(chunk)))
		c.wbuf = append(c.wbuf, chunk...)
		if _, err := c.w.Write(c.wbuf); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	if c.flush != nil {
		c.flush()
	}
	return written, nil
}

// shut stops all further writes; the server calls it before its handler
// returns, after which the response must not be touched.
func (c *grpcConn) shut() {
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
}

func (c *grpcConn) Close() error {
	c.once.Do(func() {
		_ = c.dl.set(time.Time{})
		c.closeFn()
	})
	return nil
}

func (c *grpcConn) LocalAddr() net.Addr                { return c.local }
func (c *grpcConn) RemoteAddr() net.Addr               { return c.remote }
func (c *grpcConn) SetDeadline(t time.Time) error      { return c.dl.set(t) }
func (c *grpcConn) SetReadDeadline(t time.Time) error  { return c.dl.set(t) }
func (c *grpcConn) SetWriteDeadline(t time.Time) error { return c.dl.set(t) }

// dialGRPC opens one TLS connection with a browser's ClientHello (ALPN h2) and
// makes the streaming call on it.
func dialGRPC(cfg *Config, ep Endpoint) (*grpcConn, net.Conn, error) {
	raw, err := net.DialTimeout("tcp", ep.Server, dialTimeout)
	if err != nil {
		return nil, nil, err
	}
	tuneTCP(raw)
	var hc net.Conn = raw
	if cfg.TLSFragment {
		hc = &fragConn{Conn: raw}
	}
	uc := utls.UClient(hc, &utls.Config{ServerName: cfg.ServerName, InsecureSkipVerify: cfg.Insecure}, helloID(cfg.Fingerprint))
	_ = raw.SetDeadline(time.Now().Add(tlsHandshakeTimeout))
	if err := uc.Handshake(); err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("tls handshake with %s: %w", ep.Server, err)
	}
	_ = raw.SetDeadline(time.Time{})
	if uc.ConnectionState().NegotiatedProtocol != "h2" {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("%s did not agree to HTTP/2 (is grpc_listen set there?)", ep.Server)
	}

	tr := &http2.Transport{ReadIdleTimeout: 30 * time.Second, PingTimeout: 15 * time.Second}
	cc, err := tr.NewClientConn(uc)
	if err != nil {
		_ = raw.Close()
		return nil, nil, err
	}
	host := cfg.Host
	if host == "" {
		host = cfg.ServerName
	}
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, "https://"+host+grpcPath(cfg), pr)
	if err != nil {
		_ = cc.Close()
		return nil, nil, err
	}
	req.ContentLength = -1
	req.Header.Set("Content-Type", grpcContentType)
	req.Header.Set("Te", "trailers")
	req.Header.Set("User-Agent", "grpc-go/1.71.0")

	type result struct {
		resp *http.Response
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := cc.RoundTrip(req)
		got <- result{resp, err}
	}()
	var res result
	select {
	case res = <-got:
	case <-time.After(dialTimeout):
		_ = pw.Close()
		_ = cc.Close()
		return nil, nil, fmt.Errorf("%s: no answer to the gRPC call", ep.Server)
	}
	if res.err != nil {
		_ = pw.Close()
		_ = cc.Close()
		return nil, nil, fmt.Errorf("%s: %w", ep.Server, res.err)
	}
	if res.resp.StatusCode != http.StatusOK || !strings.HasPrefix(res.resp.Header.Get("Content-Type"), grpcContentType) {
		_ = res.resp.Body.Close()
		_ = pw.Close()
		_ = cc.Close()
		return nil, nil, fmt.Errorf("%s answered the gRPC call with HTTP %d", ep.Server, res.resp.StatusCode)
	}
	c := &grpcConn{r: res.resp.Body, w: pw, local: raw.LocalAddr(), remote: raw.RemoteAddr()}
	c.closeFn = func() {
		c.shut()
		_ = pw.Close()
		_ = res.resp.Body.Close()
		_ = cc.Close()
	}
	c.dl.kill = func() { _ = c.Close() }
	return c, raw, nil
}

// serveGRPC answers one TLS connection: every call to our path becomes a
// carrier; anything else gets the answer a gRPC server gives to a call it
// does not serve.
func serveGRPC(cfg *Config, raw net.Conn, tc *tls.Config, h2 *http2.Server, accept func(*carrier)) {
	t := tls.Server(raw, tc)
	_ = t.SetDeadline(time.Now().Add(authTimeout))
	if err := t.Handshake(); err != nil {
		_ = raw.Close()
		return
	}
	_ = t.SetDeadline(time.Time{})
	if t.ConnectionState().NegotiatedProtocol != "h2" {
		_ = raw.Close()
		return
	}
	path := grpcPath(cfg)
	h2.ServeConn(t, &http2.ServeConnOpts{
		Context: context.Background(),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), grpcContentType) {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodPost || r.URL.Path != path {
				w.Header().Set("Content-Type", grpcContentType)
				w.Header().Set("Grpc-Status", "12") // UNIMPLEMENTED
				w.WriteHeader(http.StatusOK)
				return
			}
			fl, ok := w.(http.Flusher)
			if !ok {
				return
			}
			w.Header().Set("Content-Type", grpcContentType)
			w.Header().Set("Trailer", "Grpc-Status")
			w.WriteHeader(http.StatusOK)
			fl.Flush()

			done := make(chan struct{})
			c := &grpcConn{r: r.Body, w: w, flush: fl.Flush, local: raw.LocalAddr(), remote: raw.RemoteAddr()}
			c.closeFn = func() { close(done) }
			c.dl.kill = func() { _ = c.Close() }
			go accept(&carrier{Conn: c, ep: Endpoint{"grpc", raw.RemoteAddr().String()}, since: time.Now(), probe: tcpProbe(raw)})
			select {
			case <-done:
			case <-r.Context().Done():
				_ = c.Close()
			}
			c.shut()
			w.Header().Set("Grpc-Status", "0")
		}),
	})
	_ = raw.Close()
}

func grpcServer() *http2.Server {
	return &http2.Server{
		MaxUploadBufferPerStream:     8 << 20,
		MaxUploadBufferPerConnection: 16 << 20,
		MaxReadFrameSize:             1 << 20,
		ReadIdleTimeout:              30 * time.Second,
		PingTimeout:                  15 * time.Second,
	}
}
