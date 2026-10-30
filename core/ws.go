package main

// WebSocket transport, for reaching the server through a CDN.
//
// Some Iranian data centres let in data only from whitelisted foreign ranges:
// the TCP handshake to any address succeeds, but the server's first data
// packet never arrives unless it comes from, say, Cloudflare. No disguise of
// the connection itself helps there; the bytes have to come from an allowed
// address. Carrying the session as a WebSocket through a CDN does that:
//
//	client --TLS(SNI=domain)--> CDN edge --TLS or HTTP--> server, path ws_path
//
// The PSK challenge and the multiplexer run unchanged inside the WebSocket, so
// the CDN sees only opaque binary messages. Only what is needed of RFC 6455 is
// here: binary messages, ping/pong and close. Client frames are masked, as
// every CDN requires.

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	// a frame larger than this is refused; the multiplexer never sends one
	maxWSFrame = 1 << 20
	// written data is split into frames of at most this size
	wsWriteChunk = 32 << 10
	// browser user agent sent with the upgrade request
	wsUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var errWSFrameTooLarge = errors.New("websocket frame too large")

func wsAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// wsConn is a net.Conn whose bytes travel as binary WebSocket messages.
type wsConn struct {
	net.Conn
	br     *bufio.Reader
	client bool // client frames are masked

	wmu sync.Mutex // frames from Write and control replies must not interleave

	remaining int64 // unread payload bytes of the current data frame
	mask      [4]byte
	masked    bool
	maskPos   int
	closed    bool
}

func (c *wsConn) Read(p []byte) (int, error) {
	for c.remaining == 0 {
		if c.closed {
			return 0, io.EOF
		}
		if err := c.nextFrame(); err != nil {
			return 0, err
		}
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.br.Read(p)
	if c.masked {
		for i := 0; i < n; i++ {
			p[i] ^= c.mask[c.maskPos&3]
			c.maskPos++
		}
	}
	c.remaining -= int64(n)
	return n, err
}

// nextFrame reads frame headers until a data frame with a payload starts,
// answering control frames on the way.
func (c *wsConn) nextFrame() error {
	var h [2]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return err
	}
	op := h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := int64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return err
		}
		n = int64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(c.br, b[:]); err != nil {
			return err
		}
		n = int64(binary.BigEndian.Uint64(b[:]))
	}
	if n < 0 || n > maxWSFrame {
		return errWSFrameTooLarge
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return err
		}
	}

	switch op {
	case opBinary, opText, opContinuation:
		c.remaining, c.mask, c.masked, c.maskPos = n, mask, masked, 0
		return nil
	}

	// control frame: payload is at most 125 bytes
	if n > 125 {
		return errors.New("websocket control frame too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	switch op {
	case opPing:
		return c.writeFrame(opPong, payload)
	case opClose:
		_ = c.writeFrame(opClose, nil)
		c.closed = true
	}
	return nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > wsWriteChunk {
			chunk = chunk[:wsWriteChunk]
		}
		if err := c.writeFrame(opBinary, chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (c *wsConn) writeFrame(op byte, p []byte) error {
	hdr := make([]byte, 0, 14+len(p))
	hdr = append(hdr, 0x80|op)
	var maskBit byte
	if c.client {
		maskBit = 0x80
	}
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, maskBit|byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, maskBit|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, maskBit|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	if c.client {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		hdr = append(hdr, mask[:]...)
		start := len(hdr)
		hdr = append(hdr, p...)
		for i := start; i < len(hdr); i++ {
			hdr[i] ^= mask[(i-start)&3]
		}
	} else {
		hdr = append(hdr, p...)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.Conn.Write(hdr)
	return err
}

// ---------------------------------------------------------------------------
// client: upgrade an established (TLS) connection
// ---------------------------------------------------------------------------

func wsClientUpgrade(c net.Conn, host, path string) (net.Conn, error) {
	var k [16]byte
	if _, err := rand.Read(k[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(k[:])
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"User-Agent: " + wsUserAgent + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Origin: https://" + host + "\r\n" +
		"Cache-Control: no-cache\r\n" +
		"Pragma: no-cache\r\n\r\n"

	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := io.WriteString(c, req); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(c, 64<<10)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, fmt.Errorf("websocket upgrade: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("websocket upgrade: server answered %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		return nil, errors.New("websocket upgrade: bad Sec-WebSocket-Accept")
	}
	return &wsConn{Conn: c, br: br, client: true}, nil
}

// ---------------------------------------------------------------------------
// server: answer an upgrade on the secret path, look like a web server otherwise
// ---------------------------------------------------------------------------

const notFoundPage = "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"

func wsServerUpgrade(c net.Conn, br *bufio.Reader, path string) (net.Conn, error) {
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.SetDeadline(time.Time{})
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, err
	}
	if req.URL.Path != path || !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") ||
		req.Header.Get("Sec-WebSocket-Key") == "" {
		_, _ = fmt.Fprintf(c, "HTTP/1.1 404 Not Found\r\nServer: nginx\r\nDate: %s\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			time.Now().UTC().Format(http.TimeFormat), len(notFoundPage), notFoundPage)
		return nil, fmt.Errorf("not a websocket request for the tunnel path (%s %s)", req.Method, req.URL.Path)
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAccept(req.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"
	if _, err := io.WriteString(c, resp); err != nil {
		return nil, err
	}
	return &wsConn{Conn: c, br: br}, nil
}
