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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Where updates come from, in order: the latest GitHub release (GitHub
// redirects to the files and the client follows), then jsDelivr, which serves
// each release's files from its dist-vX.Y.Z tag. From inside Iran GitHub is
// often slow or unreachable while one of these jsDelivr hosts still answers.
// Variables so tests can point them elsewhere.
var (
	updateBase    = "https://github.com/1Rootzen/midos-tunnel/releases/latest/download"
	updateMirrors = []string{"https://gcore.jsdelivr.net", "https://testingcf.jsdelivr.net", "https://cdn.jsdelivr.net", "https://fastly.jsdelivr.net"}
)

const updateRepo = "1Rootzen/midos-tunnel"

func mirrorBase(mirror, ref string) string { return mirror + "/gh/" + updateRepo + "@" + ref }

var (
	versionRE     = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	mainVersionRE = regexp.MustCompile(`(?m)^var version = "(\d+\.\d+\.\d+)"`)
)

// latestVersion reads the newest version from the GitHub release, or from the
// mirrors' copy of main.go when GitHub does not answer.
func latestVersion(ctx context.Context) (string, error) {
	get := func(url string) string {
		c, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		s, _ := httpGetString(c, url)
		return s
	}
	if v := strings.TrimSpace(get(updateBase + "/midos-tunnel.version")); versionRE.MatchString(v) {
		return v, nil
	}
	for _, m := range updateMirrors {
		if x := mainVersionRE.FindStringSubmatch(get(mirrorBase(m, "main") + "/core/main.go")); x != nil {
			return x[1], nil
		}
	}
	return "", errors.New("could not reach the update server")
}

// fetchUpdate downloads the binary for this machine from the first source
// that delivers it with a matching checksum - binary and checksum always from
// the same source, so a mirror holding an older copy cannot pass for the new
// one. ver ("" when unknown) names the dist tag on the mirrors.
func fetchUpdate(ctx context.Context, ver string) ([]byte, string, error) {
	sources := []string{updateBase}
	if versionRE.MatchString(ver) {
		for _, m := range updateMirrors {
			sources = append(sources, mirrorBase(m, "dist-v"+ver))
		}
	}
	var errs []string
	for _, src := range sources {
		bin, err := fetchVerified(ctx, src)
		if err == nil {
			return bin, src, nil
		}
		errs = append(errs, src+": "+err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return nil, "", errors.New(strings.Join(errs, "; "))
}

func fetchVerified(ctx context.Context, src string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	sum, err := httpGetString(c, src+"/"+updateAsset()+".sha256")
	if err != nil {
		return nil, fmt.Errorf("checksum: %w", err)
	}
	f := strings.Fields(strings.TrimSpace(sum))
	if len(f) == 0 {
		return nil, errors.New("empty checksum")
	}
	bin, err := httpGetBytes(c, src+"/"+updateAsset())
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != f[0] {
		return nil, errors.New("checksum mismatch")
	}
	if len(bin) < 4 || string(bin[:4]) != "\x7fELF" {
		return nil, errors.New("not a Linux binary")
	}
	return bin, nil
}

func updateAsset() string {
	if runtime.GOARCH == "arm64" {
		return "midos-tunnel-arm64"
	}
	return "midos-tunnel"
}

// handleUpdateCheck reports the running version and the latest published one.
func (s *panelServer) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	latest, err := latestVersion(ctx)
	resp := map[string]any{"current": version}
	if err != nil {
		resp["error"] = "could not reach the update server"
	} else {
		resp["latest"] = latest
	}
	jsonOK(w, resp)
}

// handleUpdateApply downloads, verifies and installs the new binary, then
// restarts the panel and the tunnels onto it.
func (s *panelServer) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ver, _ := latestVersion(ctx) // only names the mirrors' dist tag; GitHub needs none
	bin, src, err := fetchUpdate(ctx, ver)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "download failed - nothing was installed: "+err.Error())
		return
	}
	log.Printf("update: downloaded from %s, checksum verified", src)

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
