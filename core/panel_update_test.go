package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeRelease serves release files the way GitHub or a jsDelivr mirror does;
// files maps a URL path to its body. Anything else is a 404.
func fakeRelease(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(s.Close)
	return s
}

func shaLine(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:]) + "  " + updateAsset() + "\n"
}

// With GitHub out of reach the panel still learns the newest version and
// updates from a mirror; a mirror holding an older copy is passed over.
func TestUpdateFallsBackToMirrors(t *testing.T) {
	oldBase, oldMirrors := updateBase, updateMirrors
	t.Cleanup(func() { updateBase, updateMirrors = oldBase, oldMirrors })

	good := "\x7fELF new binary"
	stale := "\x7fELF old binary"
	dist := "/gh/" + updateRepo + "@dist-v9.9.9/"
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unreachable", http.StatusBadGateway)
	}))
	t.Cleanup(github.Close)
	staleMirror := fakeRelease(t, map[string]string{
		"/gh/" + updateRepo + "@main/core/main.go": "package main\n\nvar version = \"9.9.9\"\n",
		dist + updateAsset():                       stale,
		dist + updateAsset() + ".sha256":           shaLine(good), // the new checksum next to an old file
	})
	goodMirror := fakeRelease(t, map[string]string{
		dist + updateAsset():             good,
		dist + updateAsset() + ".sha256": shaLine(good),
	})
	updateBase = github.URL + "/releases/latest/download"
	updateMirrors = []string{staleMirror.URL, goodMirror.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ver, err := latestVersion(ctx)
	if err != nil || ver != "9.9.9" {
		t.Fatalf("latest version: %q %v", ver, err)
	}
	bin, src, err := fetchUpdate(ctx, ver)
	if err != nil || string(bin) != good || !strings.HasPrefix(src, goodMirror.URL) {
		t.Fatalf("update: %q from %q, %v", bin, src, err)
	}

	// nothing anywhere: an error, and no binary
	updateMirrors = []string{github.URL}
	if _, err := latestVersion(ctx); err == nil {
		t.Fatal("a version was reported with every source down")
	}
	if bin, _, err := fetchUpdate(ctx, "9.9.9"); err == nil || bin != nil {
		t.Fatal("an update was returned with every source down")
	}
}

// GitHub, when it answers, is used first.
func TestUpdatePrefersGitHub(t *testing.T) {
	oldBase, oldMirrors := updateBase, updateMirrors
	t.Cleanup(func() { updateBase, updateMirrors = oldBase, oldMirrors })
	good := "\x7fELF from github"
	gh := fakeRelease(t, map[string]string{
		"/latest/midos-tunnel.version":         "9.9.9\n",
		"/latest/" + updateAsset():             good,
		"/latest/" + updateAsset() + ".sha256": shaLine(good),
	})
	updateBase, updateMirrors = gh.URL+"/latest", []string{"http://127.0.0.1:1"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if v, err := latestVersion(ctx); err != nil || v != "9.9.9" {
		t.Fatalf("version: %q %v", v, err)
	}
	if bin, src, err := fetchUpdate(ctx, "9.9.9"); err != nil || string(bin) != good || src != updateBase {
		t.Fatalf("update: %q from %q, %v", bin, src, err)
	}
}
