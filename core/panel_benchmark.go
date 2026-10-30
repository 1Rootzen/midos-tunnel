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

import (
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
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
