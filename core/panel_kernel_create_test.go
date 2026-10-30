package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func envMap(e []envKV) map[string]string {
	m := map[string]string{}
	for _, kv := range e {
		m[kv.K] = kv.V
	}
	return m
}

func TestBuildKernelJobsIranHub(t *testing.T) {
	req := kernelCreateReq{
		Role: "iran", Mode: "esp", PSK: "a-strong-passphrase", SelfIP: "203.0.113.10", Watchdog: true,
		Peers: []kernelPeerReq{
			{SelfIP: "198.51.100.21", Label: "uk", Slot: "1", SSH: sshTarget{Host: "198.51.100.21", User: "root", Password: "x"}},
			{SelfIP: "198.51.100.22", Label: "us", SSH: sshTarget{Host: "198.51.100.22", User: "root", Password: "y"}},
		},
	}
	local, remotes, err := buildKernelJobs("1.1.1.1", req)
	if err != nil {
		t.Fatal(err)
	}
	lm := envMap(local)
	if lm["MIDOS_ROLE"] != "IRAN" || lm["MIDOS_SELF_IP"] != "203.0.113.10" || lm["MIDOS_PSK"] != "a-strong-passphrase" {
		t.Fatalf("local env wrong: %v", lm)
	}
	if lm["MIDOS_PEERS"] != "198.51.100.21:uk:1,198.51.100.22:us:2" {
		t.Fatalf("peers spec wrong: %q", lm["MIDOS_PEERS"])
	}
	if len(remotes) != 2 {
		t.Fatalf("want 2 remote jobs, got %d", len(remotes))
	}
	r0 := envMap(remotes[0].env)
	if r0["MIDOS_ROLE"] != "KHAREJ" || r0["MIDOS_IRAN_IP"] != "203.0.113.10" || r0["MIDOS_SLOT"] != "1" || r0["MIDOS_SELF_IP"] != "198.51.100.21" {
		t.Fatalf("remote 0 env wrong: %v", r0)
	}
	if envMap(remotes[1].env)["MIDOS_SLOT"] != "2" {
		t.Fatal("second peer should default to slot 2")
	}
}

func TestBuildKernelJobsKharejSingle(t *testing.T) {
	req := kernelCreateReq{
		Role: "kharej", Mode: "gre", SelfIP: "198.51.100.22", Slot: "3",
		Peers: []kernelPeerReq{{SelfIP: "203.0.113.10", Label: "hub", SSH: sshTarget{Host: "203.0.113.10", User: "root", Key: "x"}}},
	}
	local, remotes, err := buildKernelJobs("2.2.2.2", req)
	if err != nil {
		t.Fatal(err)
	}
	lm := envMap(local)
	if lm["MIDOS_ROLE"] != "KHAREJ" || lm["MIDOS_IRAN_IP"] != "203.0.113.10" || lm["MIDOS_SLOT"] != "3" {
		t.Fatalf("local env wrong: %v", lm)
	}
	if len(remotes) != 1 {
		t.Fatalf("want 1 remote, got %d", len(remotes))
	}
	rm := envMap(remotes[0].env)
	if rm["MIDOS_ROLE"] != "IRAN" || rm["MIDOS_PEERS"] != "198.51.100.22:hub:3" {
		t.Fatalf("remote iran env wrong: %v", rm)
	}
}

func TestBuildKernelJobsValidation(t *testing.T) {
	cases := []kernelCreateReq{
		{Role: "iran", Mode: "esp", PSK: "short", SelfIP: "1.2.3.4", Peers: []kernelPeerReq{{SSH: sshTarget{Host: "5.6.7.8", User: "root", Password: "p"}}}},                                 // psk too short
		{Role: "iran", Mode: "esp", PSK: "a-strong-passphrase", SelfIP: "1.2.3.4"},                                                                                                           // no peers
		{Role: "iran", Mode: "bogus", SelfIP: "1.2.3.4", Peers: []kernelPeerReq{{SSH: sshTarget{Host: "5.6.7.8", User: "root", Password: "p"}}}},                                             // bad mode
		{Role: "iran", Mode: "gre", SelfIP: "1.2.3.4", Peers: []kernelPeerReq{{SelfIP: "5.6.7.8"}}},                                                                                          // peer has no ssh creds
		{Role: "kharej", Mode: "ipsec", PSK: "a-strong-passphrase", SelfIP: "1.2.3.4", Slot: "auto", Peers: []kernelPeerReq{{SSH: sshTarget{Host: "9.9.9.9", User: "root", Password: "p"}}}}, // ipsec+auto slot
	}
	for i, c := range cases {
		if _, _, err := buildKernelJobs("1.1.1.1", c); err == nil {
			t.Errorf("case %d should have failed", i)
		}
	}
}

func TestShQuoteEnvPrefix(t *testing.T) {
	env := []envKV{{"MIDOS_PSK", "pa ss'word"}, {"MIDOS_ROLE", "IRAN"}}
	got := envPrefix(env)
	want := `MIDOS_PSK='pa ss'\''word' MIDOS_ROLE='IRAN'`
	if got != want {
		t.Fatalf("envPrefix =\n %q\nwant\n %q", got, want)
	}
}

// the handler drives provision through overridable hooks; this exercises the
// whole path with those hooks faked, so no shell or network is touched.
func TestHandleKernelCreateFaked(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)

	// fake the installer file so kernelScript() succeeds
	kernelScriptPathVar = p.dir + "/Midos-tunnel.sh"
	if err := writeFileAtomic(kernelScriptPathVar, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var localEnv []envKV
	var remoteEnvs [][]envKV
	origL, origR := provisionLocal, provisionRemote
	defer func() { provisionLocal, provisionRemote = origL, origR }()
	provisionLocal = func(_ context.Context, env []envKV) ([]byte, error) { localEnv = env; return []byte("local ok"), nil }
	provisionRemote = func(_ context.Context, _ sshTarget, _ []byte, env []envKV) ([]byte, error) {
		remoteEnvs = append(remoteEnvs, env)
		return []byte("remote ok"), nil
	}

	body := map[string]any{
		"role": "IRAN", "mode": "gre", "self_ip": "203.0.113.10",
		"peers": []map[string]any{
			{"self_ip": "198.51.100.21", "label": "uk", "slot": "1", "ssh": map[string]any{"host": "198.51.100.21", "user": "root", "password": "p"}},
		},
	}
	w := p.do("POST", "/api/kernel/create", body, true)
	if w.Code != http.StatusOK {
		t.Fatalf("create answered %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool         `json:"ok"`
		Results []stepResult `json:"results"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.OK || len(resp.Results) != 2 || !resp.Results[0].OK || !resp.Results[1].OK {
		t.Fatalf("unexpected results: %+v", resp)
	}
	if envMap(localEnv)["MIDOS_ROLE"] != "IRAN" {
		t.Fatal("local provision not called with IRAN role")
	}
	if len(remoteEnvs) != 1 || envMap(remoteEnvs[0])["MIDOS_ROLE"] != "KHAREJ" {
		t.Fatalf("remote provision not driven correctly: %v", remoteEnvs)
	}

	// a validation failure should never reach provision
	bad := map[string]any{"role": "IRAN", "mode": "esp", "psk": "short", "self_ip": "1.2.3.4",
		"peers": []map[string]any{{"ssh": map[string]any{"host": "5.6.7.8", "user": "root", "password": "p"}}}}
	if w := p.do("POST", "/api/kernel/create", bad, true); w.Code != http.StatusBadRequest {
		t.Fatalf("short psk answered %d, want 400", w.Code)
	}
}
