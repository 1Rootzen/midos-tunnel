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
//
// Tunnels the network designer built run on the fleet's servers, so their
// traffic comes from the fleet samples instead: a kernel cable's from its
// peer interface on the Iran end, a relay's from its Iran end. A relay with
// this server at one end is a local tunnel and is counted under its own name.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const usageInterval = 30 * time.Second

// fleetUsagePrefix marks a cable's record ("net:<cable id>"); tunnel names
// cannot contain the colon.
const fleetUsagePrefix = "net:"

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
		if cables := usageCables(s.loadTopology()); len(cables) > 0 {
			for _, l := range cables {
				keep[fleetUsagePrefix+l.ID] = true
			}
			// the fleet sampler feeds these records, also with no page open
			s.fleetOnce.Do(func() { go s.fleetLoop() })
		}
		s.usage.forget(keep)
	}
}

// usageCables lists the applied cables whose traffic comes from the fleet.
func usageCables(t topology) []topoLink {
	var out []topoLink
	for _, l := range t.Links {
		if l.Tunnel == "" {
			continue
		}
		if l.Kind != "kernel" {
			a, _ := nodeByID(t, l.A)
			b, _ := nodeByID(t, l.B)
			if a.Kind == "self" || b.Kind == "self" {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

// cableEnds returns a cable's Iran and Kharej servers.
func cableEnds(t topology, l topoLink) (iran, kharej topoNode, ok bool) {
	a, okA := nodeByID(t, l.A)
	b, okB := nodeByID(t, l.B)
	if !okA || !okB {
		return topoNode{}, topoNode{}, false
	}
	if roleOf(t, a) == "kharej" {
		a, b = b, a
	}
	return a, b, true
}

// cableCounter reads a cable's raw byte counter from the fleet samples; ok is
// false when there is no fresh reading.
func (s *panelServer) cableCounter(t topology, l topoLink, stats map[string]nodeStats) (raw uint64, active, ok bool) {
	iran, kharej, found := cableEnds(t, l)
	if !found {
		return 0, false, false
	}
	st, have := stats[iran.ID]
	if !have || st.Error != "" {
		return 0, false, false
	}
	if l.Kind == "kernel" {
		host := kharej.IP
		if kharej.Kind == "self" {
			cfg := s.config()
			host = publicHost(&cfg)
		}
		v, seen := st.KernelPeers[host]
		return v, st.Kernel == "active", seen
	}
	for _, tn := range st.Tunnels {
		if tn.Name == l.Tunnel {
			up := tn.Active == "active"
			return tn.Bytes, up, up
		}
	}
	return 0, false, false
}

func (s *panelServer) fleetStatsCopy() map[string]nodeStats {
	s.fleetMu.Lock()
	defer s.fleetMu.Unlock()
	out := make(map[string]nodeStats, len(s.fleetStats))
	for k, v := range s.fleetStats {
		out[k] = v
	}
	return out
}

// recordFleetUsage folds the latest fleet samples into the cables' records.
func (s *panelServer) recordFleetUsage(t topology) {
	cables := usageCables(t)
	if s.usage == nil || len(cables) == 0 {
		return
	}
	stats := s.fleetStatsCopy()
	for _, l := range cables {
		if raw, _, ok := s.cableCounter(t, l, stats); ok {
			s.usage.record(fleetUsagePrefix+l.ID, raw)
		}
	}
	s.usage.save()
}

// usageKnown reports whether id is a tunnel or a counted cable.
func (s *panelServer) usageKnown(id string) bool {
	if strings.HasPrefix(id, fleetUsagePrefix) {
		for _, l := range usageCables(s.loadTopology()) {
			if fleetUsagePrefix+l.ID == id {
				return true
			}
		}
		return false
	}
	_, ok := s.tunnelByID(id)
	return ok
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
		// a cable between fleet servers: its two ends and kind, no limit
		Fleet bool   `json:"fleet,omitempty"`
		From  string `json:"from,omitempty"`
		To    string `json:"to,omitempty"`
		Kind  string `json:"kind,omitempty"`
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
	t := s.loadTopology()
	stats := s.fleetStatsCopy()
	for _, l := range usageCables(t) {
		iran, kharej, ok := cableEnds(t, l)
		if !ok {
			continue
		}
		id := fleetUsagePrefix + l.ID
		rec := usageRec{Month: curMonth()}
		if s.usage != nil {
			rec = s.usage.get(id)
		}
		_, active, _ := s.cableCounter(t, l, stats)
		out = append(out, row{
			ID: id, Managed: true, Active: active, Fleet: true,
			From: nodeName(iran), To: nodeName(kharej), Kind: l.Kind,
			Month: rec.Month, MonthBytes: rec.MonthBytes, TotalBytes: rec.TotalBytes,
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
	if !s.usageKnown(req.ID) {
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
