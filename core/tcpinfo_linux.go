//go:build linux

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// tcpProbe reads the kernel's own view of a TCP connection: its smoothed RTT
// and how many segments it had to send again. That is the real health of the
// path under a carrier, measured without sending a single extra packet.
func tcpProbe(c net.Conn) func() linkInfo {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return nil
	}
	rc, err := tc.SyscallConn()
	if err != nil {
		return nil
	}
	return func() linkInfo {
		var info *unix.TCPInfo
		var gerr error
		if err := rc.Control(func(fd uintptr) {
			info, gerr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
		}); err != nil || gerr != nil || info == nil {
			return linkInfo{}
		}
		return linkInfo{
			RTTms:   float64(info.Rtt) / 1000,
			RTTVar:  float64(info.Rttvar) / 1000,
			Retrans: uint64(info.Total_retrans),
			known:   true,
		}
	}
}
