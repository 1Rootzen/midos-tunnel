package main

// Host statistics for the dashboard, read straight from /proc every two
// seconds. Rates need two samples, so a sampler keeps the previous one and a
// short history for the charts.

import (
	"bufio"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sysInterval = 2 * time.Second
	sysHistory  = 90 // three minutes at two-second steps
)

type sysPoint struct {
	T   int64   `json:"t"`
	CPU float64 `json:"cpu"`
	Rx  float64 `json:"rx"` // bytes per second
	Tx  float64 `json:"tx"`
}

type sysSampler struct {
	mu      sync.Mutex
	hist    []sysPoint
	iface   string
	prevCPU [2]uint64 // busy, total
	prevNet [2]uint64 // rx, tx
	prevAt  time.Time
}

func newSysSampler() *sysSampler { return &sysSampler{iface: defaultIface()} }

func (s *sysSampler) loop() {
	for {
		s.sample()
		time.Sleep(sysInterval)
	}
}

func (s *sysSampler) sample() {
	busy, total := readCPU()
	rx, tx := readNet(s.iface)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prevAt.IsZero() {
		dt := now.Sub(s.prevAt).Seconds()
		p := sysPoint{T: now.Unix()}
		if dTotal := total - s.prevCPU[1]; dTotal > 0 && total >= s.prevCPU[1] {
			p.CPU = 100 * float64(busy-s.prevCPU[0]) / float64(dTotal)
		}
		if dt > 0 && rx >= s.prevNet[0] && tx >= s.prevNet[1] {
			p.Rx = float64(rx-s.prevNet[0]) / dt
			p.Tx = float64(tx-s.prevNet[1]) / dt
		}
		s.hist = append(s.hist, p)
		if len(s.hist) > sysHistory {
			s.hist = s.hist[len(s.hist)-sysHistory:]
		}
	}
	s.prevCPU, s.prevNet, s.prevAt = [2]uint64{busy, total}, [2]uint64{rx, tx}, now
}

func (s *sysSampler) history() []sysPoint {
	if s == nil {
		return []sysPoint{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sysPoint{}, s.hist...)
}

func (s *panelServer) handleSystem(w http.ResponseWriter, r *http.Request) {
	memTotal, memAvail := readMem()
	diskTotal, diskFree := diskUsage("/")
	load := readFirstLine("/proc/loadavg")
	if f := strings.Fields(load); len(f) >= 3 {
		load = strings.Join(f[:3], " ")
	}
	up := 0.0
	if f := strings.Fields(readFirstLine("/proc/uptime")); len(f) > 0 {
		up, _ = strconv.ParseFloat(f[0], 64)
	}
	iface := ""
	if s.sys != nil {
		iface = s.sys.iface
	}
	host, _ := os.Hostname()
	jsonOK(w, map[string]any{
		"hostname": host, "kernel": readFirstLine("/proc/sys/kernel/osrelease"), "cpus": runtime.NumCPU(),
		"mem_total": memTotal, "mem_used": memTotal - memAvail,
		"disk_total": diskTotal, "disk_used": diskTotal - diskFree,
		"load": load, "uptime_s": int64(up), "iface": iface,
		"history": s.sys.history(),
	})
}

func readFirstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

// readCPU returns busy and total jiffies across all CPUs.
func readCPU() (busy, total uint64) {
	f := strings.Fields(readFirstLine("/proc/stat"))
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0
	}
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i != 3 && i != 4 { // idle and iowait are not busy
			busy += n
		}
	}
	return busy, total
}

func readMem() (total, avail uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	return total, avail
}

func readNet(iface string) (rx, tx uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i < 0 || strings.TrimSpace(line[:i]) != iface {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) >= 9 {
			rx, _ = strconv.ParseUint(fields[0], 10, 64)
			tx, _ = strconv.ParseUint(fields[8], 10, 64)
		}
	}
	return rx, tx
}

// defaultIface is the interface of the default route, from /proc/net/route.
func defaultIface() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 2 && fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}
