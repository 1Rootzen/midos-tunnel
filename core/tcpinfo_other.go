//go:build !linux

package main

import "net"

// tcpProbe has no portable way to read TCP state outside Linux; the relay only
// runs on Linux servers, this exists so it still builds for tests elsewhere.
func tcpProbe(net.Conn) func() linkInfo { return nil }
