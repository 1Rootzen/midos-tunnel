package main

// Read-only view and safe lifecycle control of the kernel tunnel (the GRE/MOI
// and ESP engine driven by Midos-tunnel.sh). The panel never rebuilds that
// tunnel itself - slot and key derivation stay in the installer script - it
// only reports what is there and starts, stops or restarts the services the
// way an operator at the shell would.

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// paths to the kernel tunnel's files; variables so tests can point elsewhere
var kernelConfigPathVar = "/etc/Midos-tunnel/config"
var kernelHealthPathVar = "/etc/Midos-tunnel/health"
var kernelBaseMarkerVar = "/etc/Midos-tunnel/kernel-base-ready"

// kernelBaseReady reports whether the installer prepared the kernel-tunnel
// prerequisites (modules, packages, sysctl, the installer at its path), so the
// panel can build an ESP/MOI tunnel without the operator touching the shell.
func kernelBaseReady() bool {
	if _, err := os.Stat(kernelBaseMarkerVar); err == nil {
		return true
	}
	_, err := os.Stat(kernelScriptPathVar)
	return err == nil
}

// the services that make up the kernel tunnel, in start order
var kernelUnits = []string{"Midos-tunnel.service", "Midos-tunnel-watchdog.service", "Midos-tunnel-rekey.timer"}

// shellConfig reads a KEY=VALUE file as Midos-tunnel.sh writes it: one
// assignment per line, values optionally wrapped in single quotes, # comments.
func shellConfig(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, sc.Err()
}

func kernelHealth() map[int]string {
	out := map[int]string{}
	f, err := os.Open(kernelHealthPathVar)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) == 2 {
			if i, err := strconv.Atoi(parts[0]); err == nil {
				out[i] = parts[1]
			}
		}
	}
	return out
}

type kernelPeer struct {
	Index   int     `json:"index"`
	Name    string  `json:"name"`
	IP      string  `json:"ip"`
	Iface   string  `json:"iface"`
	Local   string  `json:"local"`
	Remote  string  `json:"remote"`
	TCP     string  `json:"tcp"`
	UDP     string  `json:"udp"`
	Weight  string  `json:"weight"`
	Enabled bool    `json:"enabled"`
	Health  string  `json:"health"`   // up, down, or disabled
	IfaceUp bool    `json:"iface_up"` // the interface exists on the host
	RTTms   float64 `json:"rtt_ms"`
	Loss    float64 `json:"loss_pct"`
}

type kernelDoc struct {
	Installed  bool                 `json:"installed"`
	BaseReady  bool                 `json:"base_ready"`
	Role       string               `json:"role"`
	Mode       string               `json:"mode"`
	SelfIP     string               `json:"self_ip"`
	IranIP     string               `json:"iran_ip"`
	KharejIP   string               `json:"kharej_ip"`
	Balance    string               `json:"balance"`
	MTU        string               `json:"mtu"`
	Shape      string               `json:"shape_mbit"`
	Profile    string               `json:"profile"`
	KeepIP     bool                 `json:"keep_client_ip"`
	Installed_ string               `json:"install_date"`
	Units      map[string]unitState `json:"units"`
	Peers      []kernelPeer         `json:"peers"`
}

var modeLabel = map[string]string{
	"esp": "ESP (رمزدار)", "espudp": "ESP-in-UDP", "ipsec": "IPsec", "gre": "MOI", "fou": "FOU",
}

func (s *panelServer) handleKernel(w http.ResponseWriter, r *http.Request) {
	cfg, err := shellConfig(kernelConfigPathVar)
	if err != nil {
		jsonOK(w, kernelDoc{Installed: false, BaseReady: kernelBaseReady()})
		return
	}
	doc := kernelDoc{
		Installed: true,
		BaseReady: kernelBaseReady(),
		Role:      cfg["ROLE"], Mode: cfg["TUNNEL_MODE"], SelfIP: cfg["SELF_IP"],
		IranIP: cfg["IRAN_IP"], KharejIP: cfg["KHAREJ_IP"], Balance: cfg["BALANCE_MODE"],
		MTU: cfg["TUNNEL_MTU"], Shape: cfg["SHAPE_MBIT"], Profile: cfg["TUNING_PROFILE"],
		KeepIP: cfg["KEEP_CLIENT_IP"] == "1", Installed_: cfg["INSTALL_DATE"],
		Units: map[string]unitState{}, Peers: []kernelPeer{},
	}
	for _, u := range kernelUnits {
		doc.Units[u] = s.unitStatus(u)
	}
	if cfg["TUNNEL_MODE"] == "espudp" {
		doc.Units["Midos-tunnel-encap.service"] = s.unitStatus("Midos-tunnel-encap.service")
	}

	health := kernelHealth()
	n, _ := strconv.Atoi(cfg["PEER_COUNT"])
	for i := 1; i <= n; i++ {
		p := func(f string) string { return cfg["PEER_"+strconv.Itoa(i)+"_"+f] }
		peer := kernelPeer{
			Index: i, Name: p("NAME"), IP: p("IP"), Iface: p("IFACE"),
			Local: p("LOCAL"), Remote: p("REMOTE"), TCP: p("TCP"), UDP: p("UDP"),
			Weight: p("WEIGHT"), Enabled: p("ENABLED") == "1", Health: health[i],
		}
		if !peer.Enabled {
			peer.Health = "disabled"
		} else if peer.Health == "" {
			peer.Health = "unknown"
		}
		if peer.Iface != "" {
			if _, err := net.InterfaceByName(peer.Iface); err == nil {
				peer.IfaceUp = true
			}
		}
		doc.Peers = append(doc.Peers, peer)
	}
	jsonOK(w, doc)
}

func (s *panelServer) handleKernelAction(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(kernelConfigPathVar); err != nil {
		jsonError(w, http.StatusNotFound, "no kernel tunnel on this server")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	// act on the main service and the watchdog together; the rekey timer too
	units := []string{"Midos-tunnel.service", "Midos-tunnel-watchdog.service"}
	rekey := "Midos-tunnel-rekey.timer"
	var err error
	switch req.Action {
	case "start", "restart":
		err = s.systemctl(req.Action, units[0])
		if err == nil {
			_ = s.systemctl(req.Action, units[1])
			_ = s.systemctl("start", rekey)
		}
	case "stop":
		_ = s.systemctl("stop", units[1])
		err = s.systemctl("stop", units[0])
	case "enable":
		err = s.systemctl("enable", units[0], units[1])
		_ = s.systemctl("enable", rekey)
	case "disable":
		err = s.systemctl("disable", units[0], units[1])
		_ = s.systemctl("disable", rekey)
	default:
		jsonError(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleKernel(w, r)
}

func (s *panelServer) handleKernelLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := s.run(ctx, "journalctl", "-u", "Midos-tunnel.service", "-u", "Midos-tunnel-watchdog.service",
		"-n", "300", "--no-pager", "-o", "short-iso")
	if err != nil && len(out) == 0 {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"logs": string(out)})
}

// handleKernelPing pings each enabled peer across the tunnel (its private
// address) so the panel can show real latency, not just the watchdog's up/down.
func (s *panelServer) handleKernelPing(w http.ResponseWriter, r *http.Request) {
	cfg, err := shellConfig(kernelConfigPathVar)
	if err != nil {
		jsonError(w, http.StatusNotFound, "no kernel tunnel")
		return
	}
	n, _ := strconv.Atoi(cfg["PEER_COUNT"])
	type res struct {
		Index int     `json:"index"`
		RTTms float64 `json:"rtt_ms"`
		Loss  float64 `json:"loss_pct"`
	}
	out := []res{}
	for i := 1; i <= n; i++ {
		remote := cfg["PEER_"+strconv.Itoa(i)+"_REMOTE"]
		if remote == "" || cfg["PEER_"+strconv.Itoa(i)+"_ENABLED"] != "1" {
			continue
		}
		pr := s.icmpPing(remote)
		out = append(out, res{Index: i, RTTms: pr.Avg, Loss: pr.Lost})
	}
	jsonOK(w, map[string]any{"results": out})
}
