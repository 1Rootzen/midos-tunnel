package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// a trimmed bench.sh run, colour codes and all
const sampleBench = "-------------------- A Bench.sh Script By Teddysun -------------------\n" +
	" Version            : \x1b[0;32mv2024-11-11\x1b[0m\n" +
	" Usage              : \x1b[1;31mwget -qO- bench.sh | bash\x1b[0m\n" +
	"----------------------------------------------------------------------\n" +
	" CPU Model          : \x1b[0;36mAMD EPYC 7B13\x1b[0m\n" +
	" CPU Cores          : \x1b[0;36m2 @ 2449.998 MHz\x1b[0m\n" +
	" AES-NI             : \x1b[0;32m✓ Enabled\x1b[0m\n" +
	" Total Disk         : \x1b[1;33m24.5 GB\x1b[0m \x1b[0;36m(3.1 GB Used)\x1b[0m\n" +
	" Total Mem          : \x1b[1;33m1.9 GB\x1b[0m \x1b[0;36m(283.6 MB Used)\x1b[0m\n" +
	" Load average       : \x1b[0;36m0.27, 0.15, 0.06\x1b[0m\n" +
	" Virtualization     : \x1b[0;36mKVM\x1b[0m\n" +
	" Location           : \x1b[0;36mFrankfurt / DE\x1b[0m\n" +
	"----------------------------------------------------------------------\n" +
	" I/O Speed(1st run) : \x1b[1;33m145 MB/s\x1b[0m\n" +
	" I/O Speed(average) : \x1b[1;33m144.0 MB/s\x1b[0m\n" +
	"----------------------------------------------------------------------\n" +
	" Node Name        Upload Speed      Download Speed      Latency     \n" +
	"\x1b[0;33m Speedtest.net    \x1b[0;32m1803.25 Mbps      \x1b[0;31m6187.47 Mbps        \x1b[0;36m0.88 ms     \x1b[0m\n" +
	"\x1b[0;33m Los Angeles, US  \x1b[0;32m512.10 Mbps       \x1b[0;31m940.00 Mbps         \x1b[0;36m140.20 ms   \x1b[0m\n" +
	"----------------------------------------------------------------------\n" +
	" Finished in        : 4 min 2 sec\n"

func TestParseBench(t *testing.T) {
	info, ioRuns, speed := parseBench(sampleBench)
	get := func(k string) string {
		for _, kv := range info {
			if kv.Key == k {
				return kv.Value
			}
		}
		return ""
	}
	if get("CPU Model") != "AMD EPYC 7B13" || get("Total Mem") != "1.9 GB (283.6 MB Used)" || get("Location") != "Frankfurt / DE" {
		t.Fatalf("info parsed wrong: %+v", info)
	}
	if get("Usage") != "" || get("Version") != "" {
		t.Fatal("banner lines must be skipped")
	}
	if get("Load average") != "0.27, 0.15, 0.06" || get("Finished in") != "4 min 2 sec" {
		t.Fatalf("values with commas/colons: %+v", info)
	}
	if len(ioRuns) != 2 || ioRuns[1].Value != "144.0 MB/s" {
		t.Fatalf("io runs: %+v", ioRuns)
	}
	if len(speed) != 2 || speed[0].Node != "Speedtest.net" || speed[0].Download != "6187.47 Mbps" ||
		speed[1].Node != "Los Angeles, US" || speed[1].Latency != "140.20 ms" {
		t.Fatalf("speed table: %+v", speed)
	}
}

func TestBenchJobRunsOnTheCardsServer(t *testing.T) {
	p := newTestPanel(t)
	p.login(testPassword)
	if w := p.do("PUT", "/api/topology", validTopo(), true); w.Code != http.StatusOK {
		t.Fatalf("put topology: %d", w.Code)
	}

	ran := make(chan string, 2)
	oldR, oldL := benchRunRemote, benchRunLocal
	benchRunRemote = func(_ context.Context, tgt sshTarget, w io.Writer) error {
		ran <- "remote:" + tgt.Host
		_, _ = io.WriteString(w, sampleBench)
		return nil
	}
	benchRunLocal = func(_ context.Context, w io.Writer) error {
		ran <- "local"
		_, _ = io.WriteString(w, sampleBench)
		return nil
	}
	defer func() { benchRunRemote, benchRunLocal = oldR, oldL }()

	// a remote server needs a login
	if w := p.do("POST", "/api/topology/bench", map[string]any{"id": "k1"}, true); w.Code != http.StatusBadRequest {
		t.Fatalf("remote without ssh: %d", w.Code)
	}
	// the host comes from the card, not from the request
	w := p.do("POST", "/api/topology/bench", map[string]any{"id": "k1",
		"ssh": map[string]any{"host": "203.0.113.250", "user": "root", "password": "pw"}}, true)
	if w.Code != http.StatusOK {
		t.Fatalf("start remote: %d %s", w.Code, w.Body)
	}
	if got := <-ran; got != "remote:198.51.100.20" {
		t.Fatalf("ran on %q, want the card's IP", got)
	}
	// this server runs locally, no login
	if w := p.do("POST", "/api/topology/bench", map[string]any{"id": "self"}, true); w.Code != http.StatusOK {
		t.Fatalf("start local: %d %s", w.Code, w.Body)
	}
	if got := <-ran; got != "local" {
		t.Fatalf("self ran %q", got)
	}
	// an unknown server is refused
	if w := p.do("POST", "/api/topology/bench", map[string]any{"id": "nope"}, true); w.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", w.Code)
	}

	// wait for both results to land
	var got struct {
		Running map[string]any         `json:"running"`
		Results map[string]benchResult `json:"results"`
	}
	for i := 0; i < 50; i++ {
		w := p.do("GET", "/api/topology/bench", nil, false)
		_ = json.NewDecoder(w.Body).Decode(&got)
		if len(got.Results) == 2 && len(got.Running) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := got.Results["k1"]
	if !r.OK || r.Host != "198.51.100.20" || len(r.Speed) != 2 || strings.Contains(r.Raw, "\x1b") {
		t.Fatalf("stored result: %+v", r)
	}
	if !got.Results["self"].OK {
		t.Fatalf("self result: %+v", got.Results["self"])
	}
}

// A real run from an Iran server (bench.sh v2026-01-31): newer key names, a
// rate-limited geo-IP lookup that prints a JSON blob, and failed speed nodes.
func TestParseBenchRealOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/bench_real.txt")
	if err != nil {
		t.Fatal(err)
	}
	info, ioRuns, speed := parseBench(string(raw))
	keys := map[string]string{}
	for _, kv := range info {
		if strings.ContainsAny(kv.Key, `"{}`) || strings.HasPrefix(kv.Value, "{") {
			t.Fatalf("JSON blob leaked into the info: %+v", kv)
		}
		keys[kv.Key] = kv.Value
	}
	for k, want := range map[string]string{
		"CPU Model":           "Intel(R) Xeon(R) CPU E5-2697 v4 @ 2.30GHz",
		"Total RAM":           "3.8 GB (585.8 MB Used)",
		"TCP Congestion Ctrl": "bbr",
		"Virtualization":      "KVM",
	} {
		if keys[k] != want {
			t.Errorf("%s = %q, want %q", k, keys[k], want)
		}
	}
	if _, ok := keys["Organization"]; ok {
		t.Error("a rate-limited Organization must be left out, not shown as JSON")
	}
	if len(ioRuns) != 4 || ioRuns[3].Value != "1218.9 MB/s" {
		t.Fatalf("io runs: %+v", ioRuns)
	}
	// 12 nodes, 4 of them "Test failed", all kept in order
	if len(speed) != 12 || speed[0].Node != "Speedtest.net" || speed[9].Node != "Singapore, SG" {
		t.Fatalf("speed rows: %d %+v", len(speed), speed)
	}
	failed := 0
	for _, x := range speed {
		if x.Failed {
			failed++
			if x.Download != "" {
				t.Errorf("failed node with numbers: %+v", x)
			}
		}
	}
	if failed != 4 || !speed[6].Failed || speed[6].Node != "Suzhou, CN" {
		t.Fatalf("failed nodes: %d %+v", failed, speed)
	}

	tidy := tidyBench(cleanBench(string(raw)))
	for _, bad := range []string{`"status"`, "Rate limit", "TERM environment", "{", "}"} {
		if strings.Contains(tidy, bad) {
			t.Errorf("tidy output still has %q", bad)
		}
	}
	for _, want := range []string{" Location           : - / -", " Organization       : -", "Tokyo, JP", "Finished in"} {
		if !strings.Contains(tidy, want) {
			t.Errorf("tidy output lost %q:\n%s", want, tidy)
		}
	}
}
