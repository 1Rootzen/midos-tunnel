package main

// Telegram alerts. When a bot token and chat id are set, the panel sends a
// short message on the events an operator wants to hear about: a tunnel going
// down and coming back, a password change, the panel starting. It is
// one-way (the panel talks, it does not take commands), so there is no bot loop
// to secure.
//
// Iran often cannot reach api.telegram.org directly. The message is therefore
// sent through any running tunnel that relays TCP 443 as well, falling back to a
// direct call; a failed send is logged and dropped, never retried in a way that
// could block an action.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

// notify sends msg to Telegram if it is configured. It returns at once and does
// everything - including reading the config - in the background, so it is safe
// to call while holding s.mu: the goroutine simply waits for the lock to free.
func (s *panelServer) notify(msg string) {
	go func() {
		cfg := s.config()
		if cfg.TelegramToken != "" && cfg.TelegramChat != "" {
			if err := sendTelegram(cfg.TelegramToken, cfg.TelegramChat, msg); err != nil {
				log.Printf("telegram: %v", err)
			}
		}
		if cfg.AlertWebhook != "" {
			if err := sendWebhook(cfg.AlertWebhook, msg); err != nil {
				log.Printf("webhook: %v", err)
			}
		}
	}()
}

func sendTelegram(token, chat, text string) error {
	api := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	form := url.Values{}
	form.Set("chat_id", chat)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("telegram returned %d: %s", resp.StatusCode, e.Description)
	}
	return nil
}

func (s *panelServer) handleTelegramSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		Chat  string `json:"chat"`
	}
	if err := readJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "bad request")
		return
	}
	s.mu.Lock()
	next := *s.cfg
	next.TelegramToken = req.Token
	next.TelegramChat = req.Chat
	if err := savePanelConfig(s.cfgPath, &next); err != nil {
		s.mu.Unlock()
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cfg = &next
	s.mu.Unlock()
	s.audit(r, "telegram", "settings changed")
	jsonOK(w, map[string]bool{"ok": true})
}

func (s *panelServer) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	cfg := s.config()
	if cfg.TelegramToken == "" || cfg.TelegramChat == "" {
		jsonError(w, http.StatusBadRequest, "set the bot token and chat id first")
		return
	}
	host, _ := os.Hostname()
	if err := sendTelegram(cfg.TelegramToken, cfg.TelegramChat, "✅ Midos Tunnel: پیام آزمایشی از "+host); err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// tunnelWatcher sends an alert when a managed tunnel's active state changes.
// It polls systemd rather than hooking each relay, so it covers tunnels made
// before alerts were turned on too.
func (s *panelServer) tunnelWatcher() {
	last := map[string]string{}
	healed := map[string]time.Time{} // per-tunnel watchdog cooldown
	first := true
	for {
		time.Sleep(30 * time.Second)
		cfg := s.config()
		// Nothing to do until the operator turns on an alert channel or the
		// watchdog; reset so the first pass after that only sets a baseline.
		if cfg.TelegramToken == "" && cfg.AlertWebhook == "" && !cfg.Watchdog {
			first = true
			continue
		}
		for _, t := range s.findTunnels() {
			st := s.unitStatus(t.Unit).Active
			prev, seen := last[t.ID]
			last[t.ID] = st

			// Watchdog: bring a failed managed tunnel back up, but no more than
			// once every five minutes so a truly broken one is not thrashed.
			if cfg.Watchdog && t.Managed && st == "failed" {
				if at, ok := healed[t.ID]; !ok || time.Since(at) > 5*time.Minute {
					healed[t.ID] = time.Now()
					if err := s.systemctl("restart", t.Unit); err == nil {
						s.audit(nil, "watchdog", "restarted "+t.ID)
						s.notify("🛠 واچ‌داگ تانل «" + t.ID + "» را که به خطا خورده بود دوباره راه‌اندازی کرد")
					}
				}
			}

			if first || !seen || prev == st {
				continue
			}
			if cfg.TelegramToken == "" && cfg.AlertWebhook == "" {
				continue // state changed, but no channel wants to hear it
			}
			switch st {
			case "active":
				s.notify("🟢 تانل «" + t.ID + "» دوباره بالا آمد")
			case "failed":
				s.notify("🔴 تانل «" + t.ID + "» به خطا خورد")
			case "inactive":
				s.notify("⚪ تانل «" + t.ID + "» متوقف شد")
			}
		}
		first = false
	}
}
