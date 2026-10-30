package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemd answers systemctl queries from a table and records every call.
type fakeSystemd struct {
	enabled, active map[string]bool
	calls           []string
}

func (f *fakeSystemd) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name != "systemctl" || len(args) < 2 {
		return nil, nil
	}
	switch args[0] {
	case "is-enabled":
		if f.enabled[args[1]] {
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), nil
	case "is-active":
		if f.active[args[1]] {
			return []byte("active\n"), nil
		}
		return []byte("inactive\n"), nil
	}
	return nil, nil
}

func (f *fakeSystemd) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// an older install: old binary, old panel unit, two tunnels (one running, one stopped)
func legacyServer(t *testing.T) (m *migration, sd *fakeSystemd, linked *string) {
	t.Helper()
	root := t.TempDir()
	units := filepath.Join(root, "units")
	tunnels := filepath.Join(root, "tunnels")
	sbin := filepath.Join(root, "sbin")
	for _, d := range []string{units, tunnels, sbin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(sbin, "midos-veil"), "OLD-BINARY")
	write(filepath.Join(units, legacyPanelUnit), "[Service]\nExecStart=/usr/local/sbin/midos-veil panel -config /etc/custom/panel.json\n")
	write(filepath.Join(units, legacyTunnelUnit), "[Service]\nExecStart=/usr/local/sbin/midos-veil -config /etc/Midos-tunnel/tunnels/%i.json\n")
	write(filepath.Join(tunnels, "uk.json"), "{}")
	write(filepath.Join(tunnels, "spare.json"), "{}")

	sd = &fakeSystemd{
		enabled: map[string]bool{"midos-veil@uk.service": true},
		active:  map[string]bool{"midos-veil@uk.service": true, legacyPanelUnit: true},
	}
	m = &migration{run: sd.run, unitDir: units, tunnelsDir: tunnels,
		bin: filepath.Join(sbin, "midos-tunnel"), legacyBin: filepath.Join(sbin, "midos-veil")}
	var target string
	linked = &target
	old := symlink
	symlink = func(oldname, newname string) error { target = oldname + " <- " + newname; return nil }
	t.Cleanup(func() { symlink = old })
	return m, sd, linked
}

func TestMigrateFromLegacy(t *testing.T) {
	m, sd, linked := legacyServer(t)
	if !m.needed() {
		t.Fatal("an older install must need migrating")
	}
	if err := m.runAll(); err != nil {
		t.Fatalf("migrate: %v\n%s", err, strings.Join(m.log, "\n"))
	}

	// the binary: the old file's bytes at the new path, the old path a link to it
	if b, err := os.ReadFile(m.bin); err != nil || string(b) != "OLD-BINARY" {
		t.Fatalf("new binary: %q %v", b, err)
	}
	if *linked != m.bin+" <- "+m.legacyBin {
		t.Fatalf("legacy link: %q", *linked)
	}

	// the running tunnel moves and keeps running; the stopped one is left alone
	if !sd.called("systemctl disable --now midos-veil@uk.service") || !sd.called("systemctl enable --now midos-tunnel@uk.service") {
		t.Fatalf("running tunnel not moved: %v", sd.calls)
	}
	if sd.called("systemctl enable --now midos-tunnel@spare.service") || sd.called("systemctl enable midos-tunnel@spare.service") {
		t.Fatalf("a stopped tunnel must stay stopped: %v", sd.calls)
	}
	if _, err := os.Stat(filepath.Join(m.unitDir, tunnelUnitName)); err != nil {
		t.Fatal("new tunnel template not written")
	}
	if _, err := os.Stat(filepath.Join(m.unitDir, legacyTunnelUnit)); err == nil {
		t.Fatal("old tunnel template left behind")
	}

	// the panel: new unit on the same config, old unit gone, started because it ran
	nb, err := os.ReadFile(filepath.Join(m.unitDir, panelUnitName))
	if err != nil || !strings.Contains(string(nb), m.bin+" panel -config /etc/custom/panel.json") {
		t.Fatalf("new panel unit: %q %v", nb, err)
	}
	if _, err := os.Stat(filepath.Join(m.unitDir, legacyPanelUnit)); err == nil {
		t.Fatal("old panel unit left behind")
	}
	if !sd.called("systemctl disable --now "+legacyPanelUnit) || !sd.called("systemctl start "+panelUnitName) {
		t.Fatalf("panel not swapped: %v", sd.calls)
	}
}

// From inside a running older panel the unit swap is handed to systemd, and the
// old unit file stays until that job runs.
func TestMigrateFromInsidePanel(t *testing.T) {
	m, sd, _ := legacyServer(t)
	m.inPanel = true
	if err := m.runAll(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !sd.called("systemd-run --unit=midos-tunnel-migrate") {
		t.Fatalf("swap not scheduled: %v", sd.calls)
	}
	if sd.called("systemctl disable --now " + legacyPanelUnit) {
		t.Fatal("the panel must not stop its own unit directly")
	}
	if !sd.called("systemctl enable " + panelUnitName) {
		t.Fatalf("new panel unit not enabled: %v", sd.calls)
	}
}

// A second run, or a server installed fresh, has nothing to do.
func TestMigrateIdempotent(t *testing.T) {
	m, sd, _ := legacyServer(t)
	if err := m.runAll(); err != nil {
		t.Fatal(err)
	}
	n := len(sd.calls)
	if m.needed() {
		t.Fatal("nothing should be left to migrate")
	}
	if err := m.runAll(); err != nil || len(sd.calls) != n {
		t.Fatalf("second run did work: %v %v", err, sd.calls[n:])
	}
}
