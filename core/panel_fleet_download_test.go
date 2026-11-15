package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// bashExec runs the download part of an install command with bash, with the
// binary path moved into a temp dir (the service setup that follows is cut).
type bashExec struct {
	bin string
	out strings.Builder
}

func (b *bashExec) Run(ctx context.Context, cmd string, _ io.Reader, out io.Writer) error {
	if i := strings.Index(cmd, "install -d -m 700"); i > 0 {
		cmd = cmd[:i]
	}
	cmd = strings.ReplaceAll(cmd, remoteBin, b.bin)
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Stdout, c.Stderr = io.MultiWriter(out, &b.out), io.MultiWriter(out, &b.out)
	return c.Run()
}

// A server of another architecture gets the published binary; with GitHub
// out of reach it comes from a mirror, and a mirror with an old copy is skipped.
func TestFleetBinaryFromMirror(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not available")
	}
	oldBase, oldMirrors := updateBase, updateMirrors
	t.Cleanup(func() { updateBase, updateMirrors = oldBase, oldMirrors })

	arch, asset := "aarch64", "midos-tunnel-arm64"
	if runtime.GOARCH == "arm64" {
		arch, asset = "x86_64", "midos-tunnel"
	}
	good, stale := "\x7fELF new build", "\x7fELF old build"
	dist := "/gh/" + updateRepo + "@dist-v" + version + "/"
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unreachable", http.StatusBadGateway)
	}))
	t.Cleanup(github.Close)
	sum := strings.Fields(shaLine(good))[0] + "  " + asset + "\n"
	staleMirror := fakeRelease(t, map[string]string{dist + asset: stale, dist + asset + ".sha256": sum})
	goodMirror := fakeRelease(t, map[string]string{dist + asset: good, dist + asset + ".sha256": sum})
	updateBase = github.URL + "/releases/latest/download"
	updateMirrors = []string{staleMirror.URL, goodMirror.URL}

	dir := t.TempDir()
	ex := &bashExec{bin: filepath.ToSlash(filepath.Join(dir, "midos-tunnel"))}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s := &panelServer{}
	if _, err := s.installBinary(ctx, ex, nodeFacts{Arch: arch}, io.Discard); err != nil {
		t.Fatalf("install: %v\n%s", err, ex.out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "midos-tunnel")); string(b) != good {
		t.Fatalf("installed %q, want the good mirror's binary\n%s", b, ex.out.String())
	}

	// no source has it: nothing is installed and the old binary stays
	_ = os.WriteFile(filepath.Join(dir, "midos-tunnel"), []byte("current"), 0o755)
	updateMirrors = []string{staleMirror.URL}
	ex.out.Reset()
	if _, err := s.installBinary(ctx, ex, nodeFacts{Arch: arch}, io.Discard); err == nil {
		t.Fatalf("install succeeded with no good source\n%s", ex.out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "midos-tunnel")); string(b) != "current" {
		t.Fatalf("binary was replaced: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "midos-tunnel.new")); err == nil {
		t.Fatal("a partial download was left behind")
	}
}
