package main

// Alert fan-out beyond Telegram: a plain webhook and the watchdog toggle.
//
// The webhook is a single POST of {"text": "..."} JSON - the shape Slack- and
// Discord-style incoming hooks accept, and simple enough for a self-hosted
// receiver. Like the Telegram path it is one-way and best-effort: a failed send
// is logged and dropped, never retried in a way that could block an action.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func sendWebhook(rawURL, text string) error {
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return fmt.Errorf("webhook url must start with http:// or https://")
	}
	body, _ := json.Marshal(map[string]string{"text": text, "content": text})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (s *panelServer) handleAlertSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Webhook  string `json:"webhook"`
		Watchdog bool   `json:"watchdog"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	req.Webhook = strings.TrimSpace(req.Webhook)
	if req.Webhook != "" && !strings.HasPrefix(req.Webhook, "http://") && !strings.HasPrefix(req.Webhook, "https://") {
		jsonError(w, http.StatusBadRequest, "webhook url must start with http:// or https://")
		return
	}
	s.mu.Lock()
	next := *s.cfg
	next.AlertWebhook = req.Webhook
	next.Watchdog = req.Watchdog
	if err := savePanelConfig(s.cfgPath, &next); err != nil {
		s.mu.Unlock()
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg = &next
	s.mu.Unlock()
	s.audit(r, "alerts", fmt.Sprintf("webhook=%t watchdog=%t", req.Webhook != "", req.Watchdog))
	// a webhook change is worth a confirmation the operator can see land
	if req.Webhook != "" {
		s.notify("🔔 کانال هشدار وبهوک Midos Tunnel تنظیم شد")
	}
	jsonOK(w, map[string]bool{"ok": true})
}
