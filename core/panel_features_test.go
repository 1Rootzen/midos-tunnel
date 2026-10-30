package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"203.0.113.9":            "203.0.113.9",
		"203.0.113.9:443":        "203.0.113.9",
		"https://example.com":    "example.com",
		"wss://example.com/path": "example.com",
		"example.com:8443/x":     "example.com",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecommendProbe(t *testing.T) {
	ps := []benchProbe{
		{Port: 443, Transport: "tls", Reachable: true, AvgMs: 80},
		{Port: 80, Transport: "tcp", Reachable: true, AvgMs: 40},
		{Port: 8443, Transport: "ws", Reachable: false},
	}
	r := recommendProbe(ps)
	if r["ok"] != true {
		t.Fatalf("expected a recommendation, got %v", r)
	}
	if r["port"] != 80 {
		t.Errorf("recommend port = %v, want 80 (lowest latency)", r["port"])
	}
	// none reachable
	none := recommendProbe([]benchProbe{{Port: 1, Reachable: false}})
	if none["ok"] != false {
		t.Errorf("expected ok=false when nothing is reachable, got %v", none)
	}
}

func TestUsageRecordDeltaAndRestart(t *testing.T) {
	u := newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	// first sight sets the baseline, no usage counted
	if rec, _ := u.record("a", 1000); rec.MonthBytes != 0 || rec.TotalBytes != 0 {
		t.Fatalf("baseline should count nothing, got %+v", rec)
	}
	// an increase of 500 is counted
	if rec, _ := u.record("a", 1500); rec.MonthBytes != 500 || rec.TotalBytes != 500 {
		t.Fatalf("after +500 want 500/500, got %+v", rec)
	}
	// the relay restarts (counter drops); the new value is taken as the delta
	if rec, _ := u.record("a", 200); rec.MonthBytes != 700 || rec.TotalBytes != 700 {
		t.Fatalf("after restart+200 want 700/700, got %+v", rec)
	}
}

func TestUsageMonthlyRollover(t *testing.T) {
	u := newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	u.record("a", 0)
	u.record("a", 5000) // 5000 this month
	// force the stored bucket into a past month
	u.mu.Lock()
	u.data["a"].Month = "2000-01"
	u.data["a"].LimitHit = true
	u.mu.Unlock()
	rec, _ := u.record("a", 6000) // +1000, but month rolled over first
	if rec.Month != curMonth() {
		t.Errorf("month not rolled over: %q", rec.Month)
	}
	if rec.MonthBytes != 1000 {
		t.Errorf("month bytes after rollover = %d, want 1000", rec.MonthBytes)
	}
	if rec.LimitHit {
		t.Errorf("limit-hit flag should reset on a new month")
	}
	if rec.TotalBytes != 6000 {
		t.Errorf("total should persist across months, got %d want 6000", rec.TotalBytes)
	}
}

func TestUsageLimitCrossing(t *testing.T) {
	u := newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	u.record("a", 0)
	u.setLimit("a", 1000)
	if _, crossed := u.record("a", 900); crossed {
		t.Errorf("should not cross at 900 under a 1000 limit")
	}
	_, crossed := u.record("a", 1100) // now 1100 >= 1000
	if !crossed {
		t.Errorf("expected the limit to be crossed at 1100")
	}
	// crossing fires only once
	if _, again := u.record("a", 1200); again {
		t.Errorf("limit crossing must fire only once")
	}
	// raising the limit re-arms it
	u.setLimit("a", 5000)
	if got := u.get("a"); got.LimitHit {
		t.Errorf("raising the limit should clear limit-hit")
	}
}

func TestUsageReset(t *testing.T) {
	u := newUsageTracker(filepath.Join(t.TempDir(), "usage.json"))
	u.record("a", 0)
	u.record("a", 9000)
	u.reset("a")
	if got := u.get("a"); got.MonthBytes != 0 || got.TotalBytes != 0 {
		t.Errorf("after reset want 0/0, got %+v", got)
	}
}

func TestSendWebhook(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		got <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := sendWebhook(srv.URL, "hello"); err != nil {
		t.Fatalf("sendWebhook: %v", err)
	}
	select {
	case body := <-got:
		if body == "" {
			t.Errorf("webhook body was empty")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not received")
	}
	if err := sendWebhook("ftp://nope", "x"); err == nil {
		t.Errorf("expected an error for a non-http url")
	}
}

func TestOrDash(t *testing.T) {
	if orDash("") != "—" {
		t.Errorf("orDash(empty) should be a dash")
	}
	if orDash("cubic") != "cubic" {
		t.Errorf("orDash passthrough failed")
	}
}
