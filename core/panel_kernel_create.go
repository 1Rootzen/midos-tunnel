package main

// Creating a kernel tunnel (ESP/MOI) from the panel. The panel never re-derives
// keys or slots itself - it drives the same Midos-tunnel.sh the menu uses,
// through its non-interactive "provision" entrypoint. For this server it runs
// the script locally; for the server at the other end it connects over SSH,
// copies the script and runs the same command there, so one click brings up
// both ends of a tunnel that agree because every key is derived from the shared
// passphrase and the ordered IP pair.
//
// Two topologies are supported, which cover the real ones:
//   - this server is the IRAN hub, with one or more KHAREJ peers; or
//   - this server is a KHAREJ end with a single IRAN peer.
// Either way it is "provision the local end, then provision each remote end
// with the opposite role", so the code treats both the same.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// the installer script the panel drives; a variable so tests can point elsewhere
var kernelScriptPathVar = "/usr/local/sbin/Midos-tunnel.sh"

type sshTarget struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Key      string `json:"key"` // optional PEM private key
}

type kernelPeerReq struct {
	SelfIP string    `json:"self_ip"` // the peer's own public IP
	Label  string    `json:"label"`
	Slot   string    `json:"slot"` // "" or "auto" lets the script pick
	SSH    sshTarget `json:"ssh"`
}

type kernelCreateReq struct {
	Role     string          `json:"role"` // this server: IRAN or KHAREJ
	SelfIP   string          `json:"self_ip"`
	Mode     string          `json:"mode"`
	PSK      string          `json:"psk"`
	MTU      string          `json:"mtu"`
	Watchdog bool            `json:"watchdog"`
	TCPPorts string          `json:"tcp_ports"`
	UDPPorts string          `json:"udp_ports"`
	Peers    []kernelPeerReq `json:"peers"`   // KHAREJ ends when this is IRAN
	IranIP   string          `json:"iran_ip"` // the IRAN end when this is KHAREJ
	Slot     string          `json:"slot"`    // this server's slot when it is KHAREJ
}

type envKV struct{ K, V string }

type remoteJob struct {
	label string
	tgt   sshTarget
	env   []envKV
}

// kv builds a non-empty environment entry, skipping empty values so the script
// falls back to its own defaults.
func kv(list []envKV, k, v string) []envKV {
	if v != "" {
		list = append(list, envKV{k, v})
	}
	return list
}

func commonKernelEnv(req kernelCreateReq) ([]envKV, error) {
	switch req.Mode {
	case "esp", "espudp", "gre", "fou", "ipsec":
	default:
		return nil, fmt.Errorf("mode must be esp, espudp, gre, fou or ipsec")
	}
	switch req.Mode {
	case "esp", "espudp", "ipsec":
		if len(req.PSK) < 12 {
			return nil, errors.New("a shared passphrase of at least 12 characters is required for this mode")
		}
	}
	var e []envKV
	e = kv(e, "MIDOS_MODE", req.Mode)
	e = kv(e, "MIDOS_PSK", req.PSK)
	e = kv(e, "MIDOS_MTU", req.MTU)
	e = kv(e, "MIDOS_TCP_PORTS", req.TCPPorts)
	e = kv(e, "MIDOS_UDP_PORTS", req.UDPPorts)
	if req.Watchdog {
		e = kv(e, "MIDOS_WATCHDOG", "1")
	} else {
		e = kv(e, "MIDOS_WATCHDOG", "0")
	}
	return e, nil
}

// buildKernelJobs turns a create request into the local environment and the
// per-peer remote jobs. It is pure so it can be tested without touching a shell
// or the network.
func buildKernelJobs(selfHost string, req kernelCreateReq) (local []envKV, remotes []remoteJob, err error) {
	base, err := commonKernelEnv(req)
	if err != nil {
		return nil, nil, err
	}
	role := strings.ToUpper(req.Role)
	selfIP := strings.TrimSpace(req.SelfIP)
	if selfIP == "" {
		selfIP = selfHost
	}
	if net.ParseIP(selfIP) == nil {
		return nil, nil, fmt.Errorf("this server's IP %q is not valid", selfIP)
	}

	switch role {
	case "IRAN":
		if len(req.Peers) == 0 {
			return nil, nil, errors.New("add at least one remote (kharej) server")
		}
		var specs []string
		for i, p := range req.Peers {
			ip := strings.TrimSpace(firstNonEmpty(p.SelfIP, p.SSH.Host))
			if net.ParseIP(ip) == nil {
				return nil, nil, fmt.Errorf("peer %d has no valid IP", i+1)
			}
			label := sanitizeLabel(p.Label, fmt.Sprintf("kharej%d", i+1))
			slot := strings.TrimSpace(p.Slot)
			if slot == "" || slot == "auto" {
				slot = strconv.Itoa(i + 1)
			}
			if !validSlot(slot) {
				return nil, nil, fmt.Errorf("peer %d has an invalid slot %q", i+1, slot)
			}
			specs = append(specs, ip+":"+label+":"+slot)

			rEnv := append([]envKV{}, base...)
			rEnv = kv(rEnv, "MIDOS_ROLE", "KHAREJ")
			rEnv = kv(rEnv, "MIDOS_SELF_IP", ip)
			rEnv = kv(rEnv, "MIDOS_IRAN_IP", selfIP)
			rEnv = kv(rEnv, "MIDOS_SLOT", slot)
			if err := validateTarget(p.SSH); err != nil {
				return nil, nil, fmt.Errorf("peer %q: %w", label, err)
			}
			remotes = append(remotes, remoteJob{label: label, tgt: p.SSH, env: rEnv})
		}
		local = append([]envKV{}, base...)
		local = kv(local, "MIDOS_ROLE", "IRAN")
		local = kv(local, "MIDOS_SELF_IP", selfIP)
		local = kv(local, "MIDOS_PEERS", strings.Join(specs, ","))

	case "KHAREJ":
		if len(req.Peers) != 1 {
			return nil, nil, errors.New("a kharej server pairs with exactly one iran server")
		}
		p := req.Peers[0]
		iranIP := strings.TrimSpace(firstNonEmpty(req.IranIP, p.SelfIP, p.SSH.Host))
		if net.ParseIP(iranIP) == nil {
			return nil, nil, errors.New("the iran server's IP is not valid")
		}
		slot := strings.TrimSpace(req.Slot)
		if slot == "" {
			slot = "auto"
		}
		if slot != "auto" && !validSlot(slot) {
			return nil, nil, fmt.Errorf("slot %q is invalid", slot)
		}
		if req.Mode == "ipsec" && slot == "auto" {
			return nil, nil, errors.New("ipsec needs an explicit slot number")
		}
		local = append([]envKV{}, base...)
		local = kv(local, "MIDOS_ROLE", "KHAREJ")
		local = kv(local, "MIDOS_SELF_IP", selfIP)
		local = kv(local, "MIDOS_IRAN_IP", iranIP)
		if slot != "auto" {
			local = kv(local, "MIDOS_SLOT", slot)
		}

		label := sanitizeLabel(p.Label, "kharej1")
		effSlot := slot
		if effSlot == "auto" {
			effSlot = "1"
		}
		rEnv := append([]envKV{}, base...)
		rEnv = kv(rEnv, "MIDOS_ROLE", "IRAN")
		rEnv = kv(rEnv, "MIDOS_SELF_IP", iranIP)
		rEnv = kv(rEnv, "MIDOS_PEERS", selfIP+":"+label+":"+effSlot)
		if err := validateTarget(p.SSH); err != nil {
			return nil, nil, fmt.Errorf("iran server: %w", err)
		}
		remotes = append(remotes, remoteJob{label: "iran", tgt: p.SSH, env: rEnv})

	default:
		return nil, nil, errors.New("role must be IRAN or KHAREJ")
	}
	return local, remotes, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sanitizeLabel(s, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
		if b.Len() >= 14 {
			break
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}

func validSlot(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 32
}

func validateTarget(t sshTarget) error {
	if strings.TrimSpace(t.Host) == "" {
		return errors.New("missing SSH host")
	}
	if strings.TrimSpace(t.User) == "" {
		return errors.New("missing SSH user")
	}
	if t.Password == "" && strings.TrimSpace(t.Key) == "" {
		return errors.New("give an SSH password or a private key")
	}
	return nil
}

// envArgs renders the environment as a shell prefix with every value quoted, so
// a passphrase with spaces or symbols survives the remote shell intact.
func envPrefix(env []envKV) string {
	var parts []string
	for _, e := range env {
		parts = append(parts, e.K+"="+shQuote(e.V))
	}
	return strings.Join(parts, " ")
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- effectful parts, replaced in tests ---

var provisionLocal = func(ctx context.Context, env []envKV) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bash", kernelScriptPathVar, "provision")
	cmd.Env = os.Environ()
	for _, e := range env {
		cmd.Env = append(cmd.Env, e.K+"="+e.V)
	}
	return cmd.CombinedOutput()
}

var provisionRemote = func(ctx context.Context, tgt sshTarget, script []byte, env []envKV) ([]byte, error) {
	return sshProvision(ctx, tgt, script, env)
}

// sshProvision copies the installer to the peer and runs its provision command
// with the given environment, returning the combined output of the run.
// sshAuth turns a password and/or a PEM private key into SSH auth methods.
func sshAuth(password, key string) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod
	if k := strings.TrimSpace(key); k != "" {
		signer, err := ssh.ParsePrivateKey([]byte(k))
		if err != nil {
			return nil, fmt.Errorf("private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if password != "" {
		auths = append(auths, ssh.Password(password))
		// many sshd setups only offer keyboard-interactive for passwords
		auths = append(auths, ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			out := make([]string, len(qs))
			for i := range out {
				out[i] = password
			}
			return out, nil
		}))
	}
	if len(auths) == 0 {
		return nil, errors.New("no SSH password or key")
	}
	return auths, nil
}

// sshDial opens an SSH connection to one of the operator's own servers.
func sshDial(tgt sshTarget) (*ssh.Client, error) {
	auths, err := sshAuth(tgt.Password, tgt.Key)
	if err != nil {
		return nil, err
	}
	port := tgt.Port
	if port == 0 {
		port = 22
	}
	// The operator is connecting to their own server; there is no stored host
	// key to pin against on a first set-up, so the key is accepted as given.
	cfg := &ssh.ClientConfig{
		User:            tgt.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	addr := net.JoinHostPort(tgt.Host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	return client, nil
}

// sshRun runs one command on a remote server and returns its combined output.
func sshRun(ctx context.Context, tgt sshTarget, cmd string) ([]byte, error) {
	client, err := sshDial(tgt)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		_ = sess.Close()
		return nil, ctx.Err()
	}
}

func sshProvision(ctx context.Context, tgt sshTarget, script []byte, env []envKV) ([]byte, error) {
	client, err := sshDial(tgt)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// 1) copy the installer over the session's stdin
	up, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	up.Stdin = bytes.NewReader(script)
	remotePath := "/usr/local/sbin/Midos-tunnel.sh"
	if out, err := up.CombinedOutput("cat > " + remotePath + " && chmod +x " + remotePath); err != nil {
		up.Close()
		return out, fmt.Errorf("copy installer: %w", err)
	}
	up.Close()

	// 2) run provision with the environment inline
	run, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer run.Close()
	cmd := envPrefix(env) + " bash " + remotePath + " provision"
	out, err := run.CombinedOutput(cmd)
	if err != nil {
		return out, fmt.Errorf("remote provision: %w", err)
	}
	return out, nil
}

func (s *panelServer) kernelScript() ([]byte, error) {
	return os.ReadFile(kernelScriptPathVar)
}

type stepResult struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func (s *panelServer) handleKernelCreate(w http.ResponseWriter, r *http.Request) {
	var req kernelCreateReq
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	cfg := s.config()
	local, remotes, err := buildKernelJobs(publicHost(&cfg), req)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, err := s.kernelScript()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "the installer script is not on this server: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	results := []stepResult{}
	out, err := provisionLocal(ctx, local)
	res := stepResult{Step: "این سرور (" + strings.ToUpper(req.Role) + ")", OK: err == nil, Output: string(out)}
	if err != nil {
		res.Error = err.Error()
	}
	results = append(results, res)
	if err != nil {
		s.audit(r, "kernel-create", "local provision failed")
		jsonError2(w, http.StatusBadGateway, "local provision failed", results)
		return
	}

	allOK := true
	for _, rm := range remotes {
		o, e := provisionRemote(ctx, rm.tgt, script, rm.env)
		rr := stepResult{Step: "سرور مقابل (" + rm.label + ")", OK: e == nil, Output: string(o)}
		if e != nil {
			rr.Error = e.Error()
			allOK = false
		}
		results = append(results, rr)
	}
	if allOK {
		s.notify("🧩 تانل کرنلی جدید از پنل ساخته شد (" + strconv.Itoa(len(remotes)) + " سرور مقابل)")
	}
	s.audit(r, "kernel-create", req.Mode+" role="+strings.ToUpper(req.Role)+" peers="+strconv.Itoa(len(remotes)))
	jsonOK(w, map[string]any{"ok": allOK, "results": results})
}

// handleKernelTeardown removes the kernel tunnel on this server (and optionally
// on named remote peers) without touching the tunnel relays or the panel.
func (s *panelServer) handleKernelTeardown(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(kernelConfigPathVar); err != nil {
		jsonError(w, http.StatusNotFound, "no kernel tunnel on this server")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := provisionLocalTeardown(ctx)
	res := stepResult{Step: "این سرور", OK: err == nil, Output: string(out)}
	if err != nil {
		res.Error = err.Error()
		jsonError2(w, http.StatusInternalServerError, "teardown failed", []stepResult{res})
		return
	}
	s.audit(r, "kernel-teardown", "removed")
	jsonOK(w, map[string]any{"ok": true, "results": []stepResult{res}})
}

var provisionLocalTeardown = func(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "bash", kernelScriptPathVar, "teardown")
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

// jsonError2 reports an error together with the step results gathered so far,
// so the UI can show how far the build got.
func jsonError2(w http.ResponseWriter, code int, msg string, results []stepResult) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg, "results": results})
}
