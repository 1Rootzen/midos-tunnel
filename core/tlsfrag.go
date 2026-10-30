package main

// ClientHello fragmentation: an anti-DPI trick for the TLS-based transports.
// A censor that blocks by SNI reads the server name out of the first TLS record
// (the ClientHello). If that record is split across two TCP segments - with the
// cut landing inside the server name - a DPI box that does not reassemble the
// stream never sees the whole name and lets the flow pass. The server's TCP
// stack reassembles it normally, so nothing downstream changes.
//
// fragConn splits only the first write (the ClientHello); everything after it
// is written straight through.

import (
	"encoding/binary"
	"net"
	"sync"
)

type fragConn struct {
	net.Conn
	once sync.Once
	done bool
}

func (f *fragConn) Write(p []byte) (int, error) {
	first := false
	f.once.Do(func() { first = true })
	if !first || f.done {
		return f.Conn.Write(p)
	}
	f.done = true
	at := sniSplit(p)
	if at <= 0 || at >= len(p) {
		// not a ClientHello we could parse: split near the front anyway, which
		// still breaks a naive fixed-offset SNI match
		at = 32
		if at >= len(p) {
			return f.Conn.Write(p)
		}
	}
	if n, err := f.Conn.Write(p[:at]); err != nil {
		return n, err
	}
	n, err := f.Conn.Write(p[at:])
	return at + n, err
}

// sniSplit returns a byte offset inside the ClientHello's SNI host name, so a
// write split there cuts the name in two. It returns -1 if the bytes are not a
// ClientHello carrying an SNI it can locate.
func sniSplit(b []byte) int {
	// TLS record: type(1) version(2) length(2), then the handshake message
	if len(b) < 43 || b[0] != 0x16 {
		return -1
	}
	rec := int(binary.BigEndian.Uint16(b[3:5]))
	end := 5 + rec
	if end > len(b) {
		end = len(b)
	}
	h := b[5:end]
	// handshake: type(1)=ClientHello length(3) version(2) random(32)
	if len(h) < 38 || h[0] != 0x01 {
		return -1
	}
	p := 38
	// session id
	if p >= len(h) {
		return -1
	}
	p += 1 + int(h[p])
	// cipher suites
	if p+2 > len(h) {
		return -1
	}
	p += 2 + int(binary.BigEndian.Uint16(h[p:]))
	// compression methods
	if p+1 > len(h) {
		return -1
	}
	p += 1 + int(h[p])
	// extensions
	if p+2 > len(h) {
		return -1
	}
	extEnd := p + 2 + int(binary.BigEndian.Uint16(h[p:]))
	p += 2
	for p+4 <= len(h) && p+4 <= extEnd {
		etype := binary.BigEndian.Uint16(h[p:])
		elen := int(binary.BigEndian.Uint16(h[p+2:]))
		body := p + 4
		if body+elen > len(h) {
			return -1
		}
		if etype == 0x0000 { // server_name
			// server_name_list(2) then entries: type(1) len(2) name
			q := body
			if q+2 > len(h) {
				return -1
			}
			q += 2 // list length
			if q+3 > len(h) {
				return -1
			}
			nameLen := int(binary.BigEndian.Uint16(h[q+1:]))
			nameStart := q + 3
			if nameStart+nameLen > len(h) || nameLen < 2 {
				return -1
			}
			// offset is relative to b: the record payload began at index 5
			return 5 + nameStart + nameLen/2
		}
		p = body + elen
	}
	return -1
}
