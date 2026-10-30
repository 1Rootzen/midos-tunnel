package main

// Updating the helper binary from the panel. The new binary is fetched from the
// project's latest GitHub release, its published SHA-256 is checked before anything is replaced,
// and the old binary is kept so a bad update can be rolled back by hand. The
// running tunnels and the panel are restarted onto the new binary only after the
// checksum matches.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// the latest release's assets; GitHub redirects these to the files, and the
// http client follows the redirect
const updateBase = "https://github.com/1Rootzen/midos-tunnel/releases/latest/download"

func updateAsset() string {
	if runtime.GOARCH == "arm64" {
		return "midos-tunnel-arm64"
	}
	return "midos-tunnel"
}

// handleUpdateCheck reports the running version and the latest published one.
func (s *panelServer) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	latest, err := httpGetString(ctx, updateBase+"/midos-tunnel.version")
	resp := map[string]any{"current": version}
	if err != nil {
		resp["error"] = "could not reach the update server"
	} else {
		resp["latest"] = strings.TrimSpace(latest)
	}
	jsonOK(w, resp)
}

// handleUpdateApply downloads, verifies and installs the new binary, then
// restarts the panel and the tunnels onto it.
func (s *panelServer) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	want, err := httpGetString(ctx, updateBase+"/"+updateAsset()+".sha256")
	if err != nil {
		jsonError(w, http.StatusBadGateway, "could not fetch the checksum: "+err.Error())
		return
	}
	want = strings.Fields(strings.TrimSpace(want))[0]

	bin, err := httpGetBytes(ctx, updateBase+"/"+updateAsset())
	if err != nil {
		jsonError(w, http.StatusBadGateway, "download failed: "+err.Error())
		return
	}
	sum := sha256.Sum256(bin)
	if hex.EncodeToString(sum[:]) != want {
		jsonError(w, http.StatusBadGateway, "checksum mismatch - the download was not installed")
		return
	}
	if len(bin) < 4 || string(bin[:4]) != "\x7fELF" {
		jsonError(w, http.StatusBadGateway, "the download is not a Linux binary")
		return
	}

	self := installedBinary
	// keep the current binary for a manual rollback
	if cur, err := os.ReadFile(self); err == nil {
		_ = os.WriteFile(self+".prev", cur, 0o755)
	}
	tmp := self + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmp, self); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "update", "installed, checksum verified")
	s.notify("⬆️ Midos Tunnel به‌روزرسانی شد؛ در حال ری‌استارت سرویس‌ها")

	// restart the tunnels onto the new binary, then the panel last (restarting
	// the panel ends this request)
	go func() {
		for _, t := range s.findTunnels() {
			_ = s.systemctl("restart", t.Unit)
		}
		time.Sleep(time.Second)
		_ = s.systemctl("restart", "midos-tunnel-panel.service")
	}()
	jsonOK(w, map[string]string{"note": "به‌روزرسانی نصب شد. سرویس‌ها و پنل در حال ری‌استارت‌اند؛ چند لحظه بعد صفحه را تازه کنید."})
}

func (s *panelServer) rollbackAvailable() bool {
	_, err := os.Stat(installedBinary + ".prev")
	return err == nil
}

func (s *panelServer) handleRollback(w http.ResponseWriter, r *http.Request) {
	prev := installedBinary + ".prev"
	data, err := os.ReadFile(prev)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "no previous binary to roll back to")
		return
	}
	if err := os.WriteFile(installedBinary+".new", data, 0o755); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(installedBinary+".new", installedBinary); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.Remove(prev)
	s.audit(r, "rollback", "restored the previous binary")
	go func() {
		for _, t := range s.findTunnels() {
			_ = s.systemctl("restart", t.Unit)
		}
		time.Sleep(time.Second)
		_ = s.systemctl("restart", "midos-tunnel-panel.service")
	}()
	jsonOK(w, map[string]string{"note": "به نسخه‌ی قبلی برگشت؛ سرویس‌ها در حال ری‌استارت‌اند."})
}

// httpGet helpers with a browser-ish user agent and a bounded body.
func httpGetBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "midos-tunnel/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func httpGetString(ctx context.Context, url string) (string, error) {
	b, err := httpGetBytes(ctx, url)
	return string(b), err
}
