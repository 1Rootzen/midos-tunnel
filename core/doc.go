// Command midos-tunnel carries Midos Tunnel traffic over a TLS connection that is
// built to look like an ordinary browser session.
//
// # Why this exists
//
// The kernel transports (MOI, ESP, ESP-in-UDP) are fast because the data path
// never leaves the kernel, but they are still recognisable: a bare IP protocol,
// or a long-lived UDP flow to one foreign address. Where a network blocks those
// outright, nothing tunes around it.
//
// This component is the fallback for exactly that case. It is deliberately NOT
// the fast path: every byte is copied through user space, so it costs more CPU
// and latency than the kernel transports. It is meant to be switched on when the
// fast path is blocked, not to replace it.
//
// # What makes it hard to fingerprint
//
// A Go program using crypto/tls produces a TLS ClientHello whose extension set,
// cipher order and curve list are distinctive - a single JA3 rule matches every
// connection it ever makes. This program therefore builds its ClientHello to
// match a real browser's, so the handshake is indistinguishable from ordinary
// web traffic. The payload rides inside that TLS session, multiplexed so a
// hundred user connections do not open a hundred suspicious sockets.
//
// # Trust model
//
// TLS authenticates and encrypts the channel, but the server may legitimately
// present a self-signed or CDN-terminated certificate, so the channel alone is
// not proof of identity. A pre-shared key is therefore verified inside the
// session: the server sends a random challenge and the client must answer with
// an HMAC of it. This is a proof of possession, not a new cipher - no home-made
// cryptography is involved anywhere in this program.
package main
