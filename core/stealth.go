package main

// The stealth transport: an encrypted carrier over plain TCP with nothing fixed
// on the wire. It exists for networks that fingerprint the TLS ClientHello
// itself - here there is no ClientHello and no magic to match, only a random
// salt followed by AEAD-encrypted frames, so a passive rule has nothing to
// anchor on.
//
// The construction is a standard salt-then-AEAD stream, built only from the
// standard library's primitives (ChaCha20-Poly1305 and HKDF-SHA256) - no
// home-made cryptography:
//
//	each direction: 32-byte random salt + 32-byte ephemeral X25519 public key,
//	then frames of [encrypted 2-byte length][encrypted payload]
//	shared = X25519(my ephemeral private, peer ephemeral public)
//	key    = HKDF-SHA256(psk, salt, shared)   (info = the shared secret)
//	nonce  = 96-bit little-endian counter, incremented per frame
//
// The ephemeral keys give forward secrecy: the per-session key depends on a
// secret neither side ever transmits, so even if the pre-shared key leaks
// later, traffic captured earlier stays unreadable.
//
// The tunnel's pre-shared-key handshake and the multiplexer then run unchanged
// inside this encrypted channel, exactly as they do inside TLS.

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	stealthSaltLen  = 32
	stealthPubLen   = 32 // X25519 public key
	stealthTagLen   = 16
	stealthMaxFrame = 16 << 10 // encrypted plaintext per frame, after framing
	stealthMaxData  = stealthMaxFrame - 2 - 512
)

// stealthPad picks how many random bytes to append to a frame. Small frames
// (the interactive, fingerprintable ones) are padded the most; large frames
// are already near the MTU, so only a little is added.
func stealthPad(dataLen int) int {
	var b [2]byte
	_, _ = rand.Read(b[:])
	r := int(b[0])<<8 | int(b[1])
	if dataLen < 256 {
		return r % 512 // 0..511
	}
	return r % 128
}

func stealthKey(psk string, salt, shared []byte) ([]byte, error) {
	info := append([]byte("midos-stealth"), shared...)
	r := hkdf.New(sha256.New, []byte("midos-stealth|"+psk), salt, info)
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// stealthConn wraps a TCP connection with a read half and a write half, each
// with its own AEAD and nonce counter.
type stealthConn struct {
	net.Conn
	shape  bool
	rMu    sync.Mutex
	wMu    sync.Mutex
	rAEAD  cipher.AEAD
	wAEAD  cipher.AEAD
	rNonce []byte
	wNonce []byte
	rBuf   []byte // decrypted bytes not yet handed to Read
}

func nextNonce(n []byte) {
	for i := 0; i < len(n); i++ {
		n[i]++
		if n[i] != 0 {
			return
		}
	}
}

// newStealthConn runs the salt exchange. The dialer sends its salt first; the
// order is fixed by who dials, so the two ends never deadlock.
func newStealthConn(raw net.Conn, psk string, dialer, shape bool) (*stealthConn, error) {
	_ = raw.SetDeadline(time.Now().Add(dialTimeout))
	defer raw.SetDeadline(time.Time{})

	mySalt := make([]byte, stealthSaltLen)
	if _, err := rand.Read(mySalt); err != nil {
		return nil, err
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	// on the wire each side sends salt(32) || ephemeral public key(32)
	myHello := append(append([]byte{}, mySalt...), priv.PublicKey().Bytes()...)
	peerHello := make([]byte, stealthSaltLen+stealthPubLen)
	send := func() error { _, e := raw.Write(myHello); return e }
	recv := func() error { _, e := io.ReadFull(raw, peerHello); return e }

	if dialer {
		if err = send(); err == nil {
			err = recv()
		}
	} else {
		if err = recv(); err == nil {
			err = send()
		}
	}
	if err != nil {
		return nil, err
	}
	peerSalt := peerHello[:stealthSaltLen]
	peerPub, err := ecdh.X25519().NewPublicKey(peerHello[stealthSaltLen:])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, err
	}

	myKey, err := stealthKey(psk, mySalt, shared)
	if err != nil {
		return nil, err
	}
	peerKey, err := stealthKey(psk, peerSalt, shared)
	if err != nil {
		return nil, err
	}
	wAEAD, err := chacha20poly1305.New(myKey)
	if err != nil {
		return nil, err
	}
	rAEAD, err := chacha20poly1305.New(peerKey)
	if err != nil {
		return nil, err
	}
	return &stealthConn{
		Conn: raw, shape: shape, wAEAD: wAEAD, rAEAD: rAEAD,
		wNonce: make([]byte, chacha20poly1305.NonceSize),
		rNonce: make([]byte, chacha20poly1305.NonceSize),
	}, nil
}

func (s *stealthConn) Write(p []byte) (int, error) {
	s.wMu.Lock()
	defer s.wMu.Unlock()
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > stealthMaxData {
			chunk = chunk[:stealthMaxData]
		}
		// the plaintext frame is [real length][data][random padding]; the
		// padding (shaping) hides the true size, and the receiver drops it
		pad := 0
		if s.shape {
			pad = stealthPad(len(chunk))
		}
		plain := make([]byte, 2+len(chunk)+pad)
		binary.BigEndian.PutUint16(plain[:2], uint16(len(chunk)))
		copy(plain[2:], chunk)
		if pad > 0 {
			_, _ = rand.Read(plain[2+len(chunk):])
		}
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(plain)))
		encLen := s.wAEAD.Seal(nil, s.wNonce, lenBuf[:], nil)
		nextNonce(s.wNonce)
		encPay := s.wAEAD.Seal(nil, s.wNonce, plain, nil)
		nextNonce(s.wNonce)
		if _, err := s.Conn.Write(encLen); err != nil {
			return total, err
		}
		if _, err := s.Conn.Write(encPay); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

func (s *stealthConn) Read(p []byte) (int, error) {
	s.rMu.Lock()
	defer s.rMu.Unlock()
	if len(s.rBuf) == 0 {
		if err := s.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, s.rBuf)
	s.rBuf = s.rBuf[n:]
	return n, nil
}

func (s *stealthConn) readFrame() error {
	encLen := make([]byte, 2+stealthTagLen)
	if _, err := io.ReadFull(s.Conn, encLen); err != nil {
		return err
	}
	lenBuf, err := s.rAEAD.Open(nil, s.rNonce, encLen, nil)
	if err != nil {
		return err
	}
	nextNonce(s.rNonce)
	n := int(binary.BigEndian.Uint16(lenBuf))
	if n < 2 || n > stealthMaxFrame {
		return errors.New("stealth: bad frame length")
	}
	encPay := make([]byte, n+stealthTagLen)
	if _, err := io.ReadFull(s.Conn, encPay); err != nil {
		return err
	}
	pay, err := s.rAEAD.Open(nil, s.rNonce, encPay, nil)
	if err != nil {
		return err
	}
	nextNonce(s.rNonce)
	// drop the padding: the first two bytes are the real data length
	real := int(binary.BigEndian.Uint16(pay[:2]))
	if real > len(pay)-2 {
		return errors.New("stealth: bad inner length")
	}
	s.rBuf = pay[2 : 2+real]
	return nil
}
