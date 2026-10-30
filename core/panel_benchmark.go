package main

// Path test: before an operator commits to a transport, measure what actually
// gets through to a server and how fast. It reuses the same probes the per-
// tunnel test uses - an ICMP ping for the raw route, and a timed TCP handshake
// for each port a TCP-framed transport (TLS, WebSocket, TCP, Stealth) would
// ride on. UDP-framed transports (KCP, QUIC) and ICMP have no handshake to time
// from one side, so they are reported by whether the route carries them at all.
//
// Nothing here changes the server; it only probes, so it is safe to run against
// a box that is not ours yet (the far end of a tunnel being planned).
//
// The test can also run from one of the fleet's servers (the Iran end of a
// tunnel the designer built): the same ping, and the same five timed TCP
// handshakes per port, by a short script over the panel's SSH connection.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type benchProbe struct {
	Port      int     `json:"port"`
	Transport string  `json:"transport,omitempty"`
	Kind      string  `json:"kind"` // tcp
	Reachable bool    `json:"reachable"`
	LossPct   float64 `json:"loss_pct"`
	AvgMs     float64 `json:"avg_ms"`
	MinMs     float64 `json:"min_ms"`
	MaxMs     float64 `json:"max_ms"`
	Error     string  `json:"error,omitempty"`
}

func (s *panelServer) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host  string `json:"host"`
		From  string `json:"from"` // a fleet server's id; "" or "self" is this server
		Ports []struct {
			Port      int    `json:"port"`
			Transport string `json:"transport"`
		} `json:"ports"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	host := hostOnly(req.Host)
	if host == "" {
		jsonError(w, http.StatusBadRequest, "enter a host or IP to test")
		return
	}
	if len(req.Ports) == 0 {
		jsonError(w, http.StatusBadRequest, "add at least one port to test")
		return
	}
	if len(req.Ports) > 24 {
		req.Ports = req.Ports[:24]
	}
	if req.From != "" && req.From != "self" {
		n, ok := nodeByID(s.loadTopology(), req.From)
		if !ok || n.Kind == "self" || n.State != "ready" {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("سرور «%s» هنوز آماده نیست؛ اول آن را اضافه و نصب کنید", firstNonEmpty(nodeName(n), req.From)))
			return
		}
		var ports []int
		for _, p := range req.Ports {
			ports = append(ports, p.Port)
		}
		icmp, probes, err := remoteBenchmark(fleetExecFor(s, n), host, ports)
		if err != nil {
			jsonError(w, http.StatusBadGateway, "تست از «"+nodeName(n)+"» اجرا نشد: "+err.Error())
			return
		}
		for i := range probes {
			probes[i].Transport = req.Ports[i].Transport
		}
		jsonOK(w, map[string]any{"host": host, "from": nodeName(n), "icmp": icmp, "probes": probes, "recommend": recommendProbe(probes)})
		return
	}

	// the ICMP route first, then every port in parallel
	icmp := s.icmpPing(host)
	probes := make([]benchProbe, len(req.Ports))
	var wg sync.WaitGroup
	for i, p := range req.Ports {
		if p.Port <= 0 || p.Port > 65535 {
			probes[i] = benchProbe{Port: p.Port, Transport: p.Transport, Kind: "tcp", Error: "port out of range"}
			continue
		}
		wg.Add(1)
		go func(i, port int, transport string) {
			defer wg.Done()
			res := tcpConnectTest(net.JoinHostPort(host, strconv.Itoa(port)))
			bp := benchProbe{
				Port: port, Transport: transport, Kind: "tcp",
				Reachable: res.Error == "" && res.Lost < 100,
				LossPct:   res.Lost, AvgMs: res.Avg, MinMs: res.Min, MaxMs: res.Max, Error: res.Error,
			}
			probes[i] = bp
		}(i, p.Port, p.Transport)
	}
	wg.Wait()

	jsonOK(w, map[string]any{
		"host":      host,
		"icmp":      icmp,
		"probes":    probes,
		"recommend": recommendProbe(probes),
	})
}

// remoteBenchScript pings the host, then times five TCP handshakes on every
// port at once. bash's own clock and /dev/tcp keep a forked process out of
// each timing. Output: the ping summary, then "tcp PORT MICROSECONDS" or
// "tcp PORT x" per attempt.
const remoteBenchScript = `export LC_ALL=C
H=%s
ping -n -q -c 10 -i 0.3 -W 2 "$H" 2>&1
probe() {
  for i in 1 2 3 4 5; do
    us=$(timeout 6 bash -c 'a=${EPOCHREALTIME/./}; exec 3<>"/dev/tcp/$0/$1" || exit 1; b=${EPOCHREALTIME/./}; echo $((b - a))' "$H" "$1" 2>/dev/null) && echo "tcp $1 $us" || echo "tcp $1 x"
  done
}
for p in %s; do probe "$p" & done
wait
`

func remoteBenchmark(ex hostExec, host string, ports []int) (pingResult, []benchProbe, error) {
	var list []string
	for _, p := range ports {
		if p > 0 && p <= 65535 {
			list = append(list, strconv.Itoa(p))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := runOut(ctx, ex, fmt.Sprintf(remoteBenchScript, shQuote(host), strings.Join(list, " ")))
	if err != nil && !strings.Contains(out, "tcp ") {
		return pingResult{}, nil, err
	}
	times := map[int][]float64{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "tcp" {
			continue
		}
		port, _ := strconv.Atoi(f[1])
		if us, err := strconv.ParseFloat(f[2], 64); err == nil {
			times[port] = append(times[port], us/1000)
		} else if _, ok := times[port]; !ok {
			times[port] = nil
		}
	}
	probes := make([]benchProbe, len(ports))
	for i, p := range ports {
		if p <= 0 || p > 65535 {
			probes[i] = benchProbe{Port: p, Kind: "tcp", Error: "port out of range"}
			continue
		}
		res := connectStats(pingResult{Target: net.JoinHostPort(host, strconv.Itoa(p)), Kind: "tcp", Sent: 5}, times[p])
		if len(times[p]) == 0 {
			res.Error = "no answer"
		}
		probes[i] = benchProbe{Port: p, Kind: "tcp", Reachable: res.Error == "" && res.Lost < 100,
			LossPct: res.Lost, AvgMs: res.Avg, MinMs: res.Min, MaxMs: res.Max, Error: res.Error}
	}
	return parsePing(host, []byte(out)), probes, nil
}

// recommendProbe names the port that answered with the lowest average latency,
// which is the one a TCP-framed transport should ride on.
func recommendProbe(ps []benchProbe) map[string]any {
	best := -1
	for i, p := range ps {
		if !p.Reachable {
			continue
		}
		if best < 0 || p.AvgMs < ps[best].AvgMs {
			best = i
		}
	}
	if best < 0 {
		return map[string]any{"ok": false}
	}
	// stable, lowest-first ordering for the client's ranked view
	order := make([]int, 0, len(ps))
	for i, p := range ps {
		if p.Reachable {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool { return ps[order[a]].AvgMs < ps[order[b]].AvgMs })
	return map[string]any{
		"ok":        true,
		"port":      ps[best].Port,
		"transport": ps[best].Transport,
		"avg_ms":    ps[best].AvgMs,
	}
}

// hostOnly strips a port, scheme or path a person may have pasted, leaving the
// bare host or IP.
func hostOnly(s string) string {
	s = trimScheme(s)
	if i := indexByteSet(s, "/"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

func trimScheme(s string) string {
	for _, p := range []string{"https://", "http://", "wss://", "ws://"} {
		if len(s) >= len(p) && s[:len(p)] == p {
			return s[len(p):]
		}
	}
	return s
}

func indexByteSet(s, set string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(set); j++ {
			if s[i] == set[j] {
				return i
			}
		}
	}
	return -1
}
