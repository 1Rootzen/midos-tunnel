package main

// A small audit trail.
//
// The audit log is an append-only JSON-lines file next to the config, capped at
// a few hundred recent entries, recording who did what from where: logins and
// failures, tunnel changes, setting changes. It is for the operator to review,
// not a security boundary.

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const auditMax = 500

type auditEntry struct {
	Time   int64  `json:"t"`
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
	IP     string `json:"ip,omitempty"`
}

type auditLog struct {
	mu   sync.Mutex
	path string
}

func (s *panelServer) auditPath() string {
	return filepath.Join(filepath.Dir(s.cfgPath), "audit.log")
}

// record appends one event. A failure to write is logged and swallowed: losing
// an audit line must never break the action that caused it.
func (s *panelServer) audit(r *http.Request, event, detail string) {
	e := auditEntry{Time: time.Now().Unix(), Event: event, Detail: detail}
	if r != nil {
		e.IP = clientIP(r)
	}
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	line, _ := json.Marshal(e)
	f, err := os.OpenFile(s.auditPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
	s.trimAudit()
}

// trimAudit keeps only the most recent auditMax lines. Called under auditMu.
func (s *panelServer) trimAudit() {
	data, err := os.ReadFile(s.auditPath())
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= auditMax {
		return
	}
	keep := lines[len(lines)-auditMax:]
	_ = writeFileAtomic(s.auditPath(), []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}

func (s *panelServer) handleAudit(w http.ResponseWriter, r *http.Request) {
	s.auditMu.Lock()
	data, _ := os.ReadFile(s.auditPath())
	s.auditMu.Unlock()
	var out []auditEntry
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 64*1024), 256*1024)
	for sc.Scan() {
		var e auditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if out == nil {
		out = []auditEntry{}
	}
	jsonOK(w, out)
}

// ---------------------------------------------------------------------------
// custom TLS certificate for the panel
// ---------------------------------------------------------------------------

func (s *panelServer) handleUploadCert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	if _, err := tls.X509KeyPair([]byte(req.Cert), []byte(req.Key)); err != nil {
		jsonError(w, http.StatusBadRequest, "the certificate and key do not match or are not valid PEM")
		return
	}
	cfg := s.config()
	if err := writeFileAtomic(cfg.CertPEM, []byte(req.Cert), 0o644); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := writeFileAtomic(cfg.KeyPEM, []byte(req.Key), 0o600); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "panel-cert", "replaced")
	jsonOK(w, map[string]string{"note": "the certificate is saved; restart the panel to use it: systemctl restart midos-tunnel-panel"})
}
