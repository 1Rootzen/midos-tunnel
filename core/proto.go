package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// The in-session protocol is intentionally tiny.
//
//	server -> client   magic | version | 32-byte challenge
//	client -> server   32-byte HMAC-SHA256(psk, challenge)
//	server -> client   1 byte: 0 = accepted
//
// After that the connection is handed to the multiplexer, and each stream opens
// with a 2-byte big-endian port number naming the destination on the server.
//
// The HMAC proves the client holds the pre-shared key without sending it, and
// the challenge is fresh per connection so a recorded handshake cannot be
// replayed. Everything here rides inside TLS; this layer exists to authenticate
// the peer, not to provide secrecy.
const (
	protoMagic   = "MIDOSV1"
	protoVersion = 2

	challengeLen = 32
	authTimeout  = 15 * time.Second

	statusOK       = 0
	statusBadAuth  = 1
	statusBadProto = 2
)

var errAuthFailed = errors.New("peer failed the pre-shared key challenge")

func deadline(c net.Conn, d time.Duration) func() {
	_ = c.SetDeadline(time.Now().Add(d))
	return func() { _ = c.SetDeadline(time.Time{}) }
}

func authResponse(psk string, clientNonce, serverNonce []byte) []byte {
	m := hmac.New(sha256.New, []byte(psk))
	m.Write([]byte(protoMagic))
	m.Write(clientNonce)
	m.Write(serverNonce)
	return m.Sum(nil)
}

// errProbe means the peer did not open with our magic: most likely a scanner
// or an active probe, not a tunnel client. It carries the bytes already read so
// the listener can hand the whole connection to a decoy site, which makes the
// port answer exactly like the ordinary website it pretends to be.
type errProbe struct{ prefix []byte }

func (errProbe) Error() string { return "not a tunnel client" }

// serverHandshake authenticates a peer that dialed us. The dialer speaks first,
// so we stay completely silent until we have seen our magic - a random scan or
// an HTTP probe gets no tunnel-shaped byte back, only the decoy. Both sides mix a
// fresh nonce in, so the proof cannot be replayed even on a plaintext carrier.
func serverHandshake(c net.Conn, psk string) error {
	defer deadline(c, authTimeout)()

	// read the magic first and bail the instant it is wrong, so a short probe
	// does not hang until the deadline
	magic := make([]byte, len(protoMagic))
	if _, err := io.ReadFull(c, magic); err != nil {
		return errProbe{magic}
	}
	if string(magic) != protoMagic {
		return errProbe{magic}
	}
	rest := make([]byte, 1+challengeLen) // version + client nonce
	if _, err := io.ReadFull(c, rest); err != nil {
		return errProbe{append(magic, rest...)}
	}
	if rest[0] != protoVersion {
		return fmt.Errorf("peer speaks protocol v%d, we speak v%d", rest[0], protoVersion)
	}
	clientNonce := rest[1:]

	serverNonce := make([]byte, challengeLen)
	if _, err := rand.Read(serverNonce); err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	if _, err := c.Write(serverNonce); err != nil {
		return err
	}
	got := make([]byte, sha256.Size)
	if _, err := io.ReadFull(c, got); err != nil {
		return err
	}
	// constant time: never leak how much of the answer was right
	if subtle.ConstantTimeCompare(got, authResponse(psk, clientNonce, serverNonce)) != 1 {
		_, _ = c.Write([]byte{statusBadAuth})
		return errAuthFailed
	}
	_, err := c.Write([]byte{statusOK})
	return err
}

// clientHandshake proves to the peer we dialed that we hold the pre-shared key.
func clientHandshake(c net.Conn, psk string) error {
	defer deadline(c, authTimeout)()

	clientNonce := make([]byte, challengeLen)
	if _, err := rand.Read(clientNonce); err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	hdr := make([]byte, 0, len(protoMagic)+1+challengeLen)
	hdr = append(hdr, protoMagic...)
	hdr = append(hdr, protoVersion)
	hdr = append(hdr, clientNonce...)
	if _, err := c.Write(hdr); err != nil {
		return err
	}

	serverNonce := make([]byte, challengeLen)
	if _, err := io.ReadFull(c, serverNonce); err != nil {
		return err
	}
	if _, err := c.Write(authResponse(psk, clientNonce, serverNonce)); err != nil {
		return err
	}
	var status [1]byte
	if _, err := io.ReadFull(c, status[:]); err != nil {
		return err
	}
	if status[0] != statusOK {
		return errAuthFailed
	}
	return nil
}

func writeStreamHeader(w io.Writer, port uint16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], port)
	_, err := w.Write(b[:])
	return err
}

func readStreamHeader(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}
