package main

import "testing"

// data>0 with parity=0 is "lean UDP": FEC off, and normalize must not silently
// turn it back on.
func TestLeanUDPKeepsFECOff(t *testing.T) {
	c := &Config{Mode: "client", PSK: testPSK, Server: "1.2.3.4:4000", Transport: "kcp",
		KCPDataShards: 10, KCPParityShards: 0, Ports: []string{"443"}}
	if err := c.normalize(); err != nil {
		t.Fatal(err)
	}
	if c.KCPDataShards != 10 || c.KCPParityShards != 0 {
		t.Fatalf("FEC-off was changed to %d/%d", c.KCPDataShards, c.KCPParityShards)
	}
}
