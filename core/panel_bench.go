package main

// Server benchmark for the network designer. When a server joins a tunnel the
// panel runs bench.sh on it - over SSH for a remote server, locally for this
// one - and keeps the parsed result, so the server's card shows its CPU,
// memory, disk I/O and network speed. bench.sh takes several minutes, longer
// than any HTTP request should wait, so it runs as a background job: the page
// polls for the live output, and the finished result is stored next to the
// panel config so it survives a reload.
//
// The command is fixed here; the request only names which server to run it on.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// what the operator would type; curl is the fallback bench.sh documents
	// TERM is unset in a non-interactive run, which makes bench.sh print a
	// warning; its screen-clear codes are stripped by cleanBench anyway.
	// The panel's systemd unit has no HOME, and Ookla's speedtest aborts
	// without one ("basic_string::_M_construct null not valid"), failing every
	// node: it gets the user's home from the passwd file instead.
	benchCommand = `export TERM="${TERM:-xterm}"; [ -n "$HOME" ] || export HOME="$(getent passwd "$(id -u)" | cut -d: -f6)"; export HOME="${HOME:-/root}"; if command -v wget >/dev/null 2>&1; then wget -qO- bench.sh | bash; else curl -Lso- bench.sh | bash; fi`
	benchTimeout = 20 * time.Minute
	benchKeep    = 64 << 10 // output kept per run
)

type benchKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type benchSpeed struct {
	Node     string `json:"node"`
	Upload   string `json:"upload"`
	Download string `json:"download"`
	Latency  string `json:"latency"`
	Failed   bool   `json:"failed,omitempty"` // bench.sh printed "Test failed"
}

type benchResult struct {
	Host  string       `json:"host"`
	At    int64        `json:"at"`
	OK    bool         `json:"ok"`
	Error string       `json:"error,omitempty"`
	Info  []benchKV    `json:"info"`
	IO    []benchKV    `json:"io"`
	Speed []benchSpeed `json:"speed"`
	Raw   string       `json:"raw"`
}

// benchJob collects a running benchmark's output as it arrives.
type benchJob struct {
	mu      sync.Mutex
	buf     []byte
	started time.Time
}

func (j *benchJob) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.buf = append(j.buf, p...)
	if len(j.buf) > benchKeep {
		j.buf = j.buf[len(j.buf)-benchKeep:]
	}
	return len(p), nil
}

func (j *benchJob) output() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return string(j.buf)
}

var benchRunLocal = func(ctx context.Context, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "bash", "-c", benchCommand)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd.Run()
}

var benchRunRemote = func(ctx context.Context, tgt sshTarget, w io.Writer) error {
	ex, err := targetExec(tgt)
	if err != nil {
		return err
	}
	return ex.Run(ctx, benchCommand, nil, w)
}

// ------------------------------------------------------------------ parsing

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	speedRowRE = regexp.MustCompile(`^(.*?)\s+([\d.]+\s*[KMG]bps)\s+([\d.]+\s*[KMG]bps)\s+([\d.]+\s*ms)`)
	speedBadRE = regexp.MustCompile(`^(.*?)\s+Test failed`)
	jsonBlobRE = regexp.MustCompile(`(?s)\{\s*\n\s*"status".*?\n\}`)
	benchKeyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 /()\-.]*$`)
)

// cleanBench strips colours and carriage-return progress from bench.sh output.
func cleanBench(raw string) string {
	s := ansiRE.ReplaceAllString(raw, "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		// a progress line rewritten with \r keeps only its last state
		if k := strings.LastIndex(l, "\r"); k >= 0 {
			l = l[k+1:]
		}
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

// tidyBench is the cleaned output as shown in full: without the TERM warning
// and with the JSON error blob of a rate-limited geo-IP lookup put as "-".
func tidyBench(clean string) string {
	s := jsonBlobRE.ReplaceAllString(clean, "-")
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "TERM environment variable not set." {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// parseBench reads bench.sh's "Key : value" lines, its I/O runs and its
// speedtest table.
func parseBench(raw string) (info, ioRuns []benchKV, speed []benchSpeed) {
	inTable := false
	for _, line := range strings.Split(cleanBench(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "---") {
			inTable = false
			continue
		}
		if strings.HasPrefix(t, "Node Name") {
			inTable = true
			continue
		}
		if inTable {
			if m := speedRowRE.FindStringSubmatch(t); m != nil {
				speed = append(speed, benchSpeed{Node: strings.TrimSpace(m[1]), Upload: m[2], Download: m[3], Latency: m[4]})
			} else if m := speedBadRE.FindStringSubmatch(t); m != nil {
				speed = append(speed, benchSpeed{Node: strings.TrimSpace(m[1]), Failed: true})
			}
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		// a bench.sh key starts with a letter; a line of the JSON error blob
		// bench.sh prints when its geo-IP lookup is rate-limited ("status": 429)
		// starts with a quote, and the value that opens the blob is "{"
		if k == "" || v == "" || !benchKeyRE.MatchString(k) || strings.HasPrefix(v, "{") ||
			k == "Usage" || k == "Version" {
			continue
		}
		if strings.HasPrefix(k, "I/O Speed") {
			ioRuns = append(ioRuns, benchKV{k, v})
		} else {
			info = append(info, benchKV{k, v})
		}
	}
	return info, ioRuns, speed
}

// ------------------------------------------------------------------ storage

func (s *panelServer) benchPath() string {
	return filepath.Join(filepath.Dir(s.cfgPath), "bench.json")
}

func (s *panelServer) loadBench() map[string]benchResult {
	out := map[string]benchResult{}
	if b, err := os.ReadFile(s.benchPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *panelServer) saveBench(id string, r benchResult) {
	s.benchMu.Lock()
	defer s.benchMu.Unlock()
	all := s.loadBench()
	all[id] = r
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := writeFileAtomic(s.benchPath(), append(b, '\n'), 0o600); err != nil {
		log.Printf("bench: save: %v", err)
	}
}

// loadTopology reads the fleet. A panel that never saved one starts as a
// manager only; a saved one keeps the role it was given.
func (s *panelServer) loadTopology() topology {
	t := topology{SelfRole: "none"}
	if b, err := os.ReadFile(s.topologyPath()); err == nil {
		t.SelfRole = "iran" // layouts saved before "none" existed
		_ = json.Unmarshal(b, &t)
	}
	return t
}

// ------------------------------------------------------------------ handlers

func (s *panelServer) handleBenchStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID  string     `json:"id"`
		SSH *sshTarget `json:"ssh"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	var node *topoNode
	topo := s.loadTopology()
	for i := range topo.Nodes {
		if topo.Nodes[i].ID == req.ID {
			node = &topo.Nodes[i]
		}
	}
	if node == nil {
		jsonError(w, http.StatusNotFound, "no such server in the network map")
		return
	}

	run := func(ctx context.Context, w io.Writer) error { return benchRunLocal(ctx, w) }
	host := "local"
	if node.Kind != "self" && node.State == "ready" && req.SSH == nil {
		// a managed server: reached with the mother's key
		n := *node
		host = n.IP
		run = func(ctx context.Context, w io.Writer) error { return fleetExecFor(s, n).Run(ctx, benchCommand, nil, w) }
	} else if node.Kind != "self" {
		if req.SSH == nil {
			jsonError(w, http.StatusBadRequest, "an SSH login is needed to benchmark a remote server")
			return
		}
		tgt := *req.SSH
		// the run goes to the server on the card, whatever the request says
		tgt.Host = node.IP
		if tgt.Host == "" {
			jsonError(w, http.StatusBadRequest, "the server has no IP yet")
			return
		}
		if err := validateTarget(tgt); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		host = tgt.Host
		run = func(ctx context.Context, w io.Writer) error { return benchRunRemote(ctx, tgt, w) }
	}

	if err := s.startBench(req.ID, host, run); err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, "bench", "started on "+host)
	jsonOK(w, map[string]bool{"ok": true})
}

// startBench runs bench.sh in the background on one server of the fleet.
func (s *panelServer) startBench(id, host string, run func(ctx context.Context, w io.Writer) error) error {
	s.benchMu.Lock()
	if s.benches == nil {
		s.benches = map[string]*benchJob{}
	}
	if _, busy := s.benches[id]; busy {
		s.benchMu.Unlock()
		return errors.New("a benchmark is already running on this server")
	}
	job := &benchJob{started: time.Now()}
	s.benches[id] = job
	s.benchMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), benchTimeout)
		defer cancel()
		err := run(ctx, job)
		raw := cleanBench(job.output())
		info, ioRuns, speed := parseBench(raw)
		res := benchResult{Host: host, At: time.Now().Unix(), OK: err == nil && len(info) > 0,
			Info: info, IO: ioRuns, Speed: speed, Raw: tidyBench(raw)}
		if err != nil {
			res.Error = err.Error()
		} else if len(info) == 0 {
			res.Error = "bench.sh produced no result (no internet access to bench.sh?)"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			res.Error = "the benchmark did not finish in time"
		}
		s.saveBench(id, res)
		s.benchMu.Lock()
		delete(s.benches, id)
		s.benchMu.Unlock()
	}()
	return nil
}

func (s *panelServer) handleBenchList(w http.ResponseWriter, r *http.Request) {
	type live struct {
		Tail    string `json:"tail"`
		Started int64  `json:"started"`
	}
	running := map[string]live{}
	s.benchMu.Lock()
	for id, j := range s.benches {
		out := cleanBench(j.output())
		if len(out) > 4000 {
			out = out[len(out)-4000:]
		}
		running[id] = live{Tail: out, Started: j.started.Unix()}
	}
	results := s.loadBench()
	s.benchMu.Unlock()
	for id, r := range results {
		// results saved by older releases: failed nodes back in, blobs out
		if r.Raw != "" {
			_, _, r.Speed = parseBench(r.Raw)
			r.Raw = tidyBench(r.Raw)
			results[id] = r
		}
	}
	jsonOK(w, map[string]any{"running": running, "results": results})
}
