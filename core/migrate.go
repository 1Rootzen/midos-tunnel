package main

// Moving an older install onto the current names. Earlier releases installed the binary
// as /usr/local/sbin/midos-veil and ran it as midos-veil-panel.service and
// midos-veil@<tunnel>.service. A migration moves an existing server onto the
// current names without losing anything:
//
//   - the new binary ends up at installedBinary and the old path becomes a
//     symlink to it, so scripts and units written by older releases keep working;
//   - every tunnel moves to its new unit in the state it was in (a running
//     tunnel is stopped under the old name and started under the new one, a
//     second or so apart; a stopped one stays stopped);
//   - the panel's own unit is swapped last.
//
// It runs from "midos-tunnel migrate" (the installer calls it) and on every
// panel start, which covers an older panel that updated itself through its Update
// button: that path writes the new binary over the old one and restarts the old
// panel unit. On a server with nothing old left it does nothing.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	legacyBinary     = "/usr/local/sbin/midos-veil"
	legacyPanelUnit  = "midos-veil-panel.service"
	legacyTunnelUnit = "midos-veil@.service"
	panelUnitName    = "midos-tunnel-panel.service"
	tunnelUnitName   = "midos-tunnel@.service"
)

// symlink is os.Symlink; a variable so tests can run where links need privileges.
var symlink = os.Symlink

type migration struct {
	run        runner
	unitDir    string
	tunnelsDir string
	bin        string // where the binary belongs now
	legacyBin  string
	inPanel    bool // the caller is the panel, possibly running under the old unit
	log        []string
}

func newMigration(run runner, unitDir, tunnelsDir string, inPanel bool) *migration {
	return &migration{run: run, unitDir: unitDir, tunnelsDir: tunnelsDir, bin: installedBinary, legacyBin: legacyBinary, inPanel: inPanel}
}

func (m *migration) note(format string, a ...any) { m.log = append(m.log, fmt.Sprintf(format, a...)) }

func (m *migration) systemctl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := m.run(ctx, "systemctl", args...)
	return strings.TrimSpace(string(out)), err
}

// needed reports whether anything from an older install is still on the server.
func (m *migration) needed() bool {
	if fi, err := os.Lstat(m.legacyBin); err == nil && fi.Mode().IsRegular() {
		return true
	}
	for _, u := range []string{legacyPanelUnit, legacyTunnelUnit} {
		if _, err := os.Stat(filepath.Join(m.unitDir, u)); err == nil {
			return true
		}
	}
	return false
}

func (m *migration) runAll() error {
	if !m.needed() {
		return nil
	}
	if err := m.binary(); err != nil {
		return fmt.Errorf("binary: %w", err)
	}
	if err := m.tunnels(); err != nil {
		return fmt.Errorf("tunnels: %w", err)
	}
	if err := m.panel(); err != nil {
		return fmt.Errorf("panel: %w", err)
	}
	return nil
}

// binary leaves exactly one real binary, at the new path, with the old path
// linking to it. Which copy is the new one depends on how the update arrived:
// the installer writes the new path, an older panel's Update button the old one.
func (m *migration) binary() error {
	fi, err := os.Lstat(m.legacyBin)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	self, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if _, err := os.Stat(m.bin); err != nil || self == m.legacyBin {
		b, err := os.ReadFile(m.legacyBin)
		if err != nil {
			return err
		}
		if err := os.WriteFile(m.bin+".new", b, 0o755); err != nil {
			return err
		}
		if err := os.Rename(m.bin+".new", m.bin); err != nil {
			return err
		}
		m.note("binary moved to %s", m.bin)
	}
	if err := os.Remove(m.legacyBin); err != nil {
		return err
	}
	if err := symlink(m.bin, m.legacyBin); err != nil {
		return err
	}
	m.note("%s now links to %s", m.legacyBin, m.bin)
	return nil
}

// tunnels moves every managed tunnel to the new unit, keeping its state.
func (m *migration) tunnels() error {
	oldTemplate := filepath.Join(m.unitDir, legacyTunnelUnit)
	if _, err := os.Stat(oldTemplate); err != nil {
		return nil
	}
	if err := writeFileAtomic(filepath.Join(m.unitDir, tunnelUnitName), []byte(tunnelUnitTemplate), 0o644); err != nil {
		return err
	}
	_, _ = m.systemctl("daemon-reload")
	configs, _ := filepath.Glob(filepath.Join(m.tunnelsDir, "*.json"))
	for _, f := range configs {
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		old := "midos-veil@" + name + ".service"
		enabled, _ := m.systemctl("is-enabled", old)
		active, _ := m.systemctl("is-active", old)
		if enabled != "enabled" && active != "active" {
			continue
		}
		_, _ = m.systemctl("disable", "--now", old)
		unit := "midos-tunnel@" + name + ".service"
		args := []string{"enable", unit}
		if active == "active" {
			args = []string{"enable", "--now", unit}
		}
		if out, err := m.systemctl(args...); err != nil {
			return fmt.Errorf("%s: %v %s", unit, err, out)
		}
		m.note("tunnel %s moved to %s (%s)", name, unit, map[bool]string{true: "running", false: "stopped"}[active == "active"])
	}
	if err := os.Remove(oldTemplate); err != nil {
		return err
	}
	_, _ = m.systemctl("daemon-reload")
	return nil
}

var unitConfigRE = regexp.MustCompile(`-config\s+(\S+)`)

// panel swaps the panel's unit. From inside the panel the swap runs as a
// transient systemd job a few seconds later: stopping the old unit here would
// end this process before the new unit is started.
func (m *migration) panel() error {
	oldPath := filepath.Join(m.unitDir, legacyPanelUnit)
	b, err := os.ReadFile(oldPath)
	if err != nil {
		return nil
	}
	cfg := defaultPanelConfig
	if mm := unitConfigRE.FindSubmatch(b); mm != nil {
		cfg = string(mm[1])
	}
	if err := writeFileAtomic(filepath.Join(m.unitDir, panelUnitName), []byte(panelUnitText(m.bin, cfg)), 0o644); err != nil {
		return err
	}
	_, _ = m.systemctl("daemon-reload")
	if out, err := m.systemctl("enable", panelUnitName); err != nil {
		return fmt.Errorf("enable %s: %v %s", panelUnitName, err, out)
	}
	swap := "systemctl disable --now " + legacyPanelUnit + "; rm -f " + oldPath + "; systemctl daemon-reload; systemctl start " + panelUnitName
	if m.inPanel {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if out, err := m.run(ctx, "systemd-run", "--unit=midos-tunnel-migrate", "--collect", "--on-active=3", "/bin/sh", "-c", swap); err != nil {
			return fmt.Errorf("systemd-run: %v %s", err, strings.TrimSpace(string(out)))
		}
		m.note("panel moves to %s in a few seconds", panelUnitName)
		return nil
	}
	active, _ := m.systemctl("is-active", legacyPanelUnit)
	_, _ = m.systemctl("disable", "--now", legacyPanelUnit)
	if err := os.Remove(oldPath); err != nil {
		return err
	}
	_, _ = m.systemctl("daemon-reload")
	if active == "active" {
		if out, err := m.systemctl("start", panelUnitName); err != nil {
			return fmt.Errorf("start %s: %v %s", panelUnitName, err, out)
		}
	}
	m.note("panel moved to %s", panelUnitName)
	return nil
}

// migrateMain is "midos-tunnel migrate".
func migrateMain() {
	m := newMigration(execRunner, defaultUnitDir, defaultTunnelsDir, false)
	if !m.needed() {
		fmt.Println("nothing to migrate")
		return
	}
	err := m.runAll()
	for _, l := range m.log {
		fmt.Println("  " + l)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}
