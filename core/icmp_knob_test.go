package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two ICMP tunnels on one server: ping replies stay off until the last one
// stops, then go back to what they were before the first one started.
func TestKernelPingRestoredByLastListener(t *testing.T) {
	dir := t.TempDir()
	oldKnob, oldDir, oldAlive := pingKnob, icmpStateDir, processAlive
	pingKnob, icmpStateDir = filepath.Join(dir, "knob"), filepath.Join(dir, "state")
	processAlive = func(pid int) bool { return pid == os.Getpid() }
	t.Cleanup(func() { pingKnob, icmpStateDir, processAlive = oldKnob, oldDir, oldAlive; exitHooks.fns = nil })
	knob := func() string { b, _ := os.ReadFile(pingKnob); return strings.TrimSpace(string(b)) }
	_ = os.WriteFile(pingKnob, []byte("0\n"), 0o644)

	// a listener that died without cleaning up is not counted
	_ = os.MkdirAll(icmpStateDir, 0o700)
	_ = os.WriteFile(filepath.Join(icmpStateDir, "999999.1"), nil, 0o600)

	r1, err := silenceKernelPing()
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := silenceKernelPing()
	if knob() != "1" {
		t.Fatalf("knob while listening: %q", knob())
	}
	r1()
	if knob() != "1" {
		t.Fatal("the first listener to stop turned ping replies back on under the second")
	}
	r2()
	if knob() != "0" {
		t.Fatalf("knob after the last listener stopped: %q", knob())
	}
	if ents, _ := os.ReadDir(icmpStateDir); len(ents) != 0 {
		t.Fatalf("state left behind: %v", ents)
	}

	// stopping by signal runs the same undo
	_, _ = silenceKernelPing()
	runExitHooks()
	if knob() != "0" {
		t.Fatalf("knob after shutdown: %q", knob())
	}
}
