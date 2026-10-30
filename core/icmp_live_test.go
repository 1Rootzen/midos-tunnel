package main

import (
	"fmt"
	"os"
	"strconv"
	"testing"
)

// TestICMPRoundTripLive runs a full relay over the ICMP transport on the
// loopback: a server listening on ICMP, a client dialing it, and a byte stream
// pushed through and checked. It needs a raw socket (root) and a Linux kernel
// whose ping-reply knob it can toggle, so it is skipped unless MIDOS_ICMP_LIVE
// is set and is meant to run on a Linux test box, not the Windows dev machine.
func TestICMPRoundTripLive(t *testing.T) {
	if os.Getenv("MIDOS_ICMP_LIVE") == "" {
		t.Skip("set MIDOS_ICMP_LIVE=1 and run as root on Linux to exercise the ICMP transport")
	}
	svc, local := freePort(t), freePort(t)
	echoServer(t, svc)
	srv := &Config{Mode: "server", PSK: testPSK, ICMPListen: "127.0.0.1",
		Targets: "127.0.0.1", Ports: []string{fmt.Sprint(svc)}}
	cli := &Config{Mode: "client", PSK: testPSK,
		Endpoints: []Endpoint{{"icmp", "127.0.0.1"}},
		Ports:     []string{fmt.Sprintf("%d:%d", local, svc)}}
	startPair(t, srv, cli)
	size := 4096
	if v, err := strconv.Atoi(os.Getenv("MIDOS_ICMP_SIZE")); err == nil && v > 0 {
		size = v
	}
	for i := 0; i < 3; i++ {
		relayRoundTrip(t, local, size)
		t.Logf("round trip %d of %d bytes ok", i+1, size)
	}
}
