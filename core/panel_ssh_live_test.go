package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSSHProvisionLive exercises the real SSH orchestration against a server
// given in the environment: it copies the installer over SSH and runs the
// non-interactive provision, exactly as the panel does. It is skipped unless
// MIDOS_SSH_LIVE is set, so it never runs in the normal suite.
//
//	MIDOS_SSH_LIVE=1 MIDOS_SSH_HOST=1.2.3.4 MIDOS_SSH_USER=root \
//	MIDOS_SSH_PASS=... go test -run TestSSHProvisionLive -v
func TestSSHProvisionLive(t *testing.T) {
	if os.Getenv("MIDOS_SSH_LIVE") == "" {
		t.Skip("set MIDOS_SSH_LIVE=1 (and MIDOS_SSH_HOST/USER/PASS) to run the live SSH test")
	}
	script, err := os.ReadFile("../Midos-tunnel.sh")
	if err != nil {
		t.Fatalf("read installer: %v", err)
	}
	tgt := sshTarget{
		Host:     os.Getenv("MIDOS_SSH_HOST"),
		User:     os.Getenv("MIDOS_SSH_USER"),
		Password: os.Getenv("MIDOS_SSH_PASS"),
	}
	env := []envKV{
		{"MIDOS_ROLE", "KHAREJ"}, {"MIDOS_MODE", "gre"},
		{"MIDOS_SELF_IP", "198.51.100.60"}, {"MIDOS_IRAN_IP", "198.51.100.61"},
		{"MIDOS_SLOT", "1"}, {"MIDOS_WATCHDOG", "0"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out, err := sshProvision(ctx, tgt, script, env)
	t.Logf("remote output:\n%s", out)
	if err != nil {
		t.Fatalf("sshProvision: %v", err)
	}
	if !strings.Contains(string(out), "provision: KHAREJ ready") {
		t.Fatalf("provision did not report success")
	}
}
