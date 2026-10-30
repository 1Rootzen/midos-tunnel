package main

import (
	"net"
	"os/exec"
	"testing"
)

// The script a fleet server runs for the route test: an open port answers all
// five handshakes with a timing, a closed one none.
func TestRemoteBenchmarkScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("needs bash")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	open := ln.Addr().(*net.TCPAddr).Port
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	shut := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	_, probes, err := remoteBenchmark(localExec{}, "127.0.0.1", []int{open, shut, 0})
	if err != nil {
		t.Fatal(err)
	}
	if p := probes[0]; !p.Reachable || p.LossPct != 0 || p.AvgMs <= 0 || p.AvgMs > 1000 {
		t.Fatalf("open port: %+v", p)
	}
	if p := probes[1]; p.Reachable || p.LossPct != 100 {
		t.Fatalf("closed port: %+v", p)
	}
	if p := probes[2]; p.Reachable || p.Error == "" {
		t.Fatalf("bad port: %+v", p)
	}
}
