package main

// Passing the user's real address to the service.
//
// Behind a relay every connection reaches the service from 127.0.0.1, so a
// panel that limits devices or logs by client address sees one client. With
// proxy_protocol on, the Iran side sends the user's address along with each
// stream, and the server prefixes the connection to the service with a PROXY
// protocol v2 header - which xray and most proxies can read.
//
// On the wire, a stream that carries an address opens with
//
//	0x0000 | 0x0000 | 2-byte port | family (4 or 6) | address | 2-byte source port
//
// A plain stream starts with a non-zero port and a UDP flow with 0x0000 and a
// non-zero port, so 0x0000 0x0000 is free. A server too old to know it reads
// "UDP to port 0", refuses it, and logs why.

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
)

const (
	kindTCP = iota
	kindUDP
)

// writeTCPOpen opens a TCP stream, with the user's address when there is one.
func writeTCPOpen(w io.Writer, port uint16, user net.Addr) error {
	ta, ok := user.(*net.TCPAddr)
	if !ok || ta == nil {
		return writeStreamHeader(w, port)
	}
	ip := ta.IP.To4()
	fam := byte(4)
	if ip == nil {
		ip, fam = ta.IP.To16(), 6
	}
	b := make([]byte, 0, 4+2+1+len(ip)+2)
	b = append(b, 0, 0, 0, 0)
	b = binary.BigEndian.AppendUint16(b, port)
	b = append(b, fam)
	b = append(b, ip...)
	b = binary.BigEndian.AppendUint16(b, uint16(ta.Port))
	_, err := w.Write(b)
	return err
}

// readStreamOpen reads any of the three stream openings.
func readStreamOpen(r io.Reader) (kind int, port uint16, user *net.TCPAddr, err error) {
	if port, err = readStreamHeader(r); err != nil {
		return
	}
	if port != 0 {
		return kindTCP, port, nil, nil
	}
	if port, err = readStreamHeader(r); err != nil {
		return
	}
	if port != 0 {
		return kindUDP, port, nil, nil
	}
	if port, err = readStreamHeader(r); err != nil {
		return
	}
	var fam [1]byte
	if _, err = io.ReadFull(r, fam[:]); err != nil {
		return
	}
	n := 4
	switch fam[0] {
	case 4:
	case 6:
		n = 16
	default:
		return 0, 0, nil, errors.New("bad address family")
	}
	b := make([]byte, n+2)
	if _, err = io.ReadFull(r, b); err != nil {
		return
	}
	user = &net.TCPAddr{IP: net.IP(b[:n]), Port: int(binary.BigEndian.Uint16(b[n:]))}
	if port == 0 {
		return 0, 0, nil, errors.New("port 0")
	}
	return kindTCP, port, user, nil
}

var proxyV2Sig = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

// writeProxyV2 writes a PROXY protocol v2 header for a TCP connection from src
// to dst. Mixed families are sent as IPv6 with the IPv4 address mapped.
func writeProxyV2(w io.Writer, src, dst *net.TCPAddr) error {
	s4, d4 := src.IP.To4(), dst.IP.To4()
	b := append([]byte{}, proxyV2Sig...)
	b = append(b, 0x21) // version 2, command PROXY
	if s4 != nil && d4 != nil {
		b = append(b, 0x11) // TCP over IPv4
		b = binary.BigEndian.AppendUint16(b, 12)
		b = append(b, s4...)
		b = append(b, d4...)
	} else {
		b = append(b, 0x21) // TCP over IPv6
		b = binary.BigEndian.AppendUint16(b, 36)
		b = append(b, src.IP.To16()...)
		b = append(b, dst.IP.To16()...)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(src.Port))
	b = binary.BigEndian.AppendUint16(b, uint16(dst.Port))
	_, err := w.Write(b)
	return err
}
