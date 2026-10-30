package main

// Traffic accounting and optional per-tunnel monthly limits.
//
// Each relay reports bytes since it last started; a restart resets them. To get
// a usage figure that survives restarts, a sampler reads every tunnel's byte
// counter on a timer, adds the increase since the previous read to a stored
// per-tunnel total, and rolls the monthly bucket over on the first day of each
// month. When a tunnel has a monthly limit and passes it, the sampler stops the
// tunnel (managed ones only) and sends one alert; raising or clearing the limit
// arms it again.

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

const usageInterval = 30 * time.Second

type usageRec struct {
	Month      string `json:"month"` // YYYY-MM of the current bucket
	MonthBytes uint64 `json:"month_bytes"`
	TotalBytes uint64 `json:"total_bytes"`
	LimitBytes uint64 `json:"limit_bytes"` // 0 = no limit
	LimitHit   bool   `json:"limit_hit"`
}

type usageTracker struct {
	path string
	mu   sync.Mutex
	data map[string]*usageRec
	last map[string]uint64 // last raw counter seen, for deltas
}

func newUsageTracker(path string) *usageTracker {
	u := &usageTracker{path: path, data: map[string]*usageRec{}, last: map[string]uint64{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &u.data)
	}
	if u.data == nil {
		u.data = map[string]*usageRec{}
	}
	return u
}

func curMonth() string { return time.Now().Format("2006-01") }

// record folds a tunnel's current raw byte counter into its stored totals and
// returns the record and whether the monthly limit was just crossed.
func (u *usageTracker) record(id string, raw uint64) (usageRec, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	rec := u.data[id]
	if rec == nil {
		rec = &usageRec{Month: curMonth()}
		u.data[id] = rec
	}
	if rec.Month != curMonth() {
		rec.Month, rec.MonthBytes, rec.LimitHit = curMonth(), 0, false
	}
	prev, seen := u.last[id]
	u.last[id] = raw
	var delta uint64
	if !seen {
		delta = 0 // first sight only sets the baseline
	} else if raw >= prev {
		delta = raw - prev
	} else {
		delta = raw // the relay restarted; its counter went back to near zero
	}
	rec.MonthBytes += delta
	rec.TotalBytes += delta
	crossed := rec.LimitBytes > 0 && rec.MonthBytes >= rec.LimitBytes && !rec.LimitHit
	if crossed {
		rec.LimitHit = true
	}
	return *rec, crossed
}

func (u *usageTracker) get(id string) usageRec {
	u.mu.Lock()
	defer u.mu.Unlock()
	if rec := u.data[id]; rec != nil {
		return *rec
	}
	return usageRec{Month: curMonth()}
}

func (u *usageTracker) setLimit(id string, limit uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	rec := u.data[id]
	if rec == nil {
		rec = &usageRec{Month: curMonth()}
		u.data[id] = rec
	}
	rec.LimitBytes = limit
	// raising or clearing the limit re-arms the stop-and-alert
	if limit == 0 || rec.MonthBytes < limit {
		rec.LimitHit = false
	}
	u.saveLocked()
}

func (u *usageTracker) reset(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if rec := u.data[id]; rec != nil {
		rec.MonthBytes, rec.TotalBytes, rec.LimitHit = 0, 0, false
	}
	delete(u.last, id)
	u.saveLocked()
}

// forget drops a deleted tunnel's record so the file does not grow forever.
func (u *usageTracker) forget(keep map[string]bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for id := range u.data {
		if !keep[id] {
			delete(u.data, id)
			delete(u.last, id)
		}
	}
	u.saveLocked()
}

func (u *usageTracker) save() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.saveLocked()
}

func (u *usageTracker) saveLocked() {
	b, err := json.MarshalIndent(u.data, "", "  ")
	if err != nil {
		return
	}
	_ = writeFileAtomic(u.path, append(b, '\n'), 0o600)
}

func bytesFromStats(d *statsDoc) uint64 {
	var total uint64
	for _, p := range d.Ports {
		total += p.Up + p.Down
	}
	return total
}

// usageLoop samples every tunnel's byte counter and enforces limits.
func (s *panelServer) usageLoop() {
	for {
		time.Sleep(usageInterval)
		if s.usage == nil {
			continue
		}
		keep := map[string]bool{}
		for _, t := range s.findTunnels() {
			keep[t.ID] = true
			c, err := readTunnelConfig(t.ConfigPath)
			if err != nil || c.StatsListen == "" {
				continue
			}
			if s.unitStatus(t.Unit).Active != "active" {
				continue
			}
			d, err := fetchStats(c.StatsListen)
			if err != nil {
				continue
			}
			_, crossed := s.usage.record(t.ID, bytesFromStats(d))
			if crossed && t.Managed {
				if err := s.systemctl("stop", t.Unit); err == nil {
					s.audit(nil, "limit", "stopped "+t.ID+" over quota")
					s.notify("⛔ تانل «" + t.ID + "» به سقف ترافیک ماه رسید و متوقف شد")
				}
			}
		}
		s.usage.forget(keep)
	}
}

func (s *panelServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	type row struct {
		ID         string `json:"id"`
		Managed    bool   `json:"managed"`
		Active     bool   `json:"active"`
		Month      string `json:"month"`
		MonthBytes uint64 `json:"month_bytes"`
		TotalBytes uint64 `json:"total_bytes"`
		LimitBytes uint64 `json:"limit_bytes"`
		LimitHit   bool   `json:"limit_hit"`
	}
	var out []row
	for _, t := range s.findTunnels() {
		rec := usageRec{Month: curMonth()}
		if s.usage != nil {
			rec = s.usage.get(t.ID)
		}
		out = append(out, row{
			ID: t.ID, Managed: t.Managed, Active: s.unitStatus(t.Unit).Active == "active",
			Month: rec.Month, MonthBytes: rec.MonthBytes, TotalBytes: rec.TotalBytes,
			LimitBytes: rec.LimitBytes, LimitHit: rec.LimitHit,
		})
	}
	if out == nil {
		out = []row{}
	}
	jsonOK(w, out)
}

func (s *panelServer) handleUsageLimit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string `json:"id"`
		LimitBytes uint64 `json:"limit_bytes"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	if _, ok := s.tunnelByID(req.ID); !ok {
		jsonError(w, http.StatusNotFound, "no such tunnel")
		return
	}
	if s.usage == nil {
		jsonError(w, http.StatusInternalServerError, "usage tracking is off")
		return
	}
	s.usage.setLimit(req.ID, req.LimitBytes)
	s.audit(r, "limit", req.ID)
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *panelServer) handleUsageReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	if _, ok := s.tunnelByID(req.ID); !ok {
		jsonError(w, http.StatusNotFound, "no such tunnel")
		return
	}
	if s.usage == nil {
		jsonError(w, http.StatusInternalServerError, "usage tracking is off")
		return
	}
	s.usage.reset(req.ID)
	s.audit(r, "limit", "reset "+req.ID)
	jsonOK(w, map[string]bool{"ok": true})
}
