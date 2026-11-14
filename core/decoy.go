package main

// What happens to a connection that completed the TLS handshake (or reached a
// plain listener) but is not a tunnel client - a scanner, or an active probe.
//
// With a decoy address configured, the connection is spliced to that address
// with the bytes it already sent replayed first, so the prober gets exactly the
// response of whatever runs there: an ordinary website. Without one, it is held
// briefly and dropped, which at least costs the scanner a timeout.

import (
	"log"
	"net"
	"time"
)

// serveDecoy sends an unauthenticated connection to the decoy, replaying prefix
// (the bytes already read from it). It never blocks the caller for long - a
// dead decoy just closes the connection.
func serveDecoy(cfg *Config, c net.Conn, prefix []byte) {
	defer c.Close()
	if cfg.Decoy == "" {
		// no decoy: waste a little of the scanner's time, then drop
		time.Sleep(time.Duration(300+len(prefix)%400) * time.Millisecond)
		return
	}
	up, err := net.DialTimeout("tcp", cfg.Decoy, 8*time.Second)
	if err != nil {
		log.Printf("decoy %s unreachable: %v", cfg.Decoy, err)
		return
	}
	defer up.Close()
	if len(prefix) > 0 {
		if _, err := up.Write(prefix); err != nil {
			return
		}
	}
	pipe(c, up)
}

// handleAuthResult runs the server handshake on an accepted carrier and, when
// it is not a tunnel client, hands it to the decoy. It returns true when the
// carrier authenticated and should be served as a tunnel.
func (s authSink) ok(cfg *Config, c *carrier) bool {
	err := serverHandshake(c, cfg.PSK)
	if err == nil {
		if err := padCarrier(cfg, c, false); err != nil {
			log.Printf("padding with %s: %v (both ends need \"pad\")", c.RemoteAddr(), err)
			_ = c.Close()
			return false
		}
		return true
	}
	if p, isProbe := err.(errProbe); isProbe {
		go serveDecoy(cfg, c, p.prefix)
		return false
	}
	rejectSlowly(c, err)
	return false
}

// authSink is a tiny receiver so the two accept paths share one entry point.
type authSink struct{}

var auth authSink
