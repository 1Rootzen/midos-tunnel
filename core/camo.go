package main

// Camouflage for the tls transport. Without it, anyone can open a TLS
// connection to the listening side and meet the tunnel's own (self-signed)
// certificate before the decoy takes over - which is itself a tell. With it,
// the listening side decides before any TLS is spoken:
//
//   - the dialing side writes a signature into its ClientHello's 32-byte
//     legacy session id, a field browsers fill with random bytes anyway:
//     16 random bytes followed by an HMAC of them and the current minute,
//     keyed from the PSK. On the wire it is indistinguishable from random.
//   - the listening side reads the first TLS record, checks that field and
//     only then runs its TLS server on the connection (replaying the bytes it
//     read). Any other connection - a scanner, an active probe, a browser - is
//     spliced, untouched and before any handshake, to the real site being
//     imitated, so it gets that site's genuine certificate and pages.
//
// A signature is accepted within two minutes of the listener's clock and only
// once, so a recorded hello cannot be replayed to reach the tunnel.

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"
)

const (
	camoTokenLen = 32
	camoSkew     = 2 // minutes either side of the listener's clock
)

// camoTarget is the real site a non-tunnel connection is handed to.
func (c *Config) camoTarget() string {
	if c.Decoy != "" {
		return c.Decoy
	}
	if c.ServerName != "" {
		return net.JoinHostPort(c.ServerName, "443")
	}
	return ""
}

func camoMAC(psk string, nonce []byte, minute int64) []byte {
	key := sha256.Sum256([]byte("midos-camo|" + psk))
	m := hmac.New(sha256.New, key[:])
	m.Write(nonce)
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(minute))
	m.Write(t[:])
	return m.Sum(nil)[:camoTokenLen-16]
}

// camoToken is the session id a dialing side sends at time now.
func camoToken(psk string, now time.Time) []byte {
	tok := make([]byte, camoTokenLen)
	_, _ = rand.Read(tok[:16])
	copy(tok[16:], camoMAC(psk, tok[:16], now.Unix()/60))
	return tok
}

// camoReplay remembers the nonces seen in the last few minutes.
type camoReplay struct {
	mu   sync.Mutex
	seen map[[16]byte]time.Time
}

var camoSeen = &camoReplay{seen: map[[16]byte]time.Time{}}

// fresh records nonce and reports whether it was not seen before.
func (r *camoReplay) fresh(nonce []byte, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) > 4096 {
		for k, t := range r.seen {
			if now.Sub(t) > time.Duration(2*camoSkew+1)*time.Minute {
				delete(r.seen, k)
			}
		}
	}
	var k [16]byte
	copy(k[:], nonce)
	if _, ok := r.seen[k]; ok {
		return false
	}
	r.seen[k] = now
	return true
}

// camoValid checks a session id against the PSK at time now.
func camoValid(psk string, sid []byte, now time.Time) bool {
	if len(sid) != camoTokenLen {
		return false
	}
	m := now.Unix() / 60
	for d := int64(-camoSkew); d <= camoSkew; d++ {
		if hmac.Equal(sid[16:], camoMAC(psk, sid[:16], m+d)) {
			return camoSeen.fresh(sid[:16], now)
		}
	}
	return false
}

// helloSessionID returns the legacy session id of a TLS record holding a
// ClientHello, or nil when the record is anything else.
func helloSessionID(rec []byte) []byte {
	// record: type(1)=22 version(2) length(2), then the handshake message:
	// type(1)=1 length(3) version(2) random(32) session_id_len(1) session_id
	if len(rec) < 5+4+2+32+1 || rec[0] != 0x16 || rec[5] != 0x01 {
		return nil
	}
	n := int(rec[5+4+2+32])
	start := 5 + 4 + 2 + 32 + 1
	if n > 32 || start+n > len(rec) {
		return nil
	}
	return rec[start : start+n]
}

// prefixConn is a connection whose first reads replay bytes already taken
// from it.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (p *prefixConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// camoGate reads the first TLS record of an accepted connection. It returns a
// connection that replays that record for the tunnel's TLS server, or nil when
// the connection is not a tunnel client - in which case it has already been
// handed to the real site.
func camoGate(cfg *Config, raw net.Conn) net.Conn {
	_ = raw.SetReadDeadline(time.Now().Add(authTimeout))
	head := make([]byte, 5)
	n, err := io.ReadFull(raw, head)
	rec := head[:n]
	if err == nil && head[0] == 0x16 {
		size := int(binary.BigEndian.Uint16(head[3:5]))
		if size <= 1<<14+256 {
			body := make([]byte, size)
			m, _ := io.ReadFull(raw, body)
			rec = append(rec, body[:m]...)
		}
	}
	_ = raw.SetReadDeadline(time.Time{})
	if sid := helloSessionID(rec); sid != nil && camoValid(cfg.PSK, sid, time.Now()) {
		return &prefixConn{Conn: raw, r: io.MultiReader(bytes.NewReader(rec), raw)}
	}
	go camoPassThrough(cfg, raw, rec)
	return nil
}

// padCarrier puts random padding under the multiplexer of an authenticated
// tls or ws carrier when "pad" is on. It reuses the stealth framing with
// shaping, run inside the TLS session: every write becomes a frame with
// random padding, so the TLS record sizes no longer follow the user's
// packets. It starts after the tunnel handshake, so probes still meet the
// decoy; the dialing side speaks first, so the two ends never wait on each
// other.
func padCarrier(cfg *Config, c *carrier, dialer bool) error {
	if !cfg.Pad || (c.ep.Transport != "tls" && c.ep.Transport != "ws") {
		return nil
	}
	sc, err := newStealthConn(c.Conn, cfg.PSK, dialer, true)
	if err != nil {
		return err
	}
	c.Conn = sc
	return nil
}

// camoPassThrough splices a connection that is not a tunnel client to the
// real site, replaying what it already sent.
func camoPassThrough(cfg *Config, raw net.Conn, prefix []byte) {
	defer raw.Close()
	up, err := net.DialTimeout("tcp", cfg.camoTarget(), 8*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	if len(prefix) > 0 {
		if _, err := up.Write(prefix); err != nil {
			return
		}
	}
	pipe(raw, up)
}
