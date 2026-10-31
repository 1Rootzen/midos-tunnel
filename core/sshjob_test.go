package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// stallConn passes traffic until its time is up, then carries nothing more
// and never closes - what some routes do to an SSH connection.
type stallConn struct {
	net.Conn
	until time.Time
	once  sync.Once
	dead  chan struct{}
}

func (c *stallConn) stalled() bool {
	if time.Now().Before(c.until) {
		return false
	}
	c.once.Do(func() { close(c.dead) })
	return true
}

func (c *stallConn) Read(p []byte) (int, error) {
	if c.stalled() {
		<-c.dead
		select {} // the server side just waits; the client's deadline ends it
	}
	_ = c.Conn.SetReadDeadline(c.until)
	n, err := c.Conn.Read(p)
	if err != nil && c.stalled() {
		select {}
	}
	return n, err
}

func (c *stallConn) Write(p []byte) (int, error) {
	if c.stalled() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

// bashSSHServer serves SSH on loopback and runs every command with the local
// bash. With life > 0 each connection stalls that long after it is accepted.
func bashSSHServer(t *testing.T, life time.Duration) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil }}
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	sg, _ := ssh.NewSignerFromKey(hk)
	cfg.AddHostKey(sg)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			var c net.Conn = raw
			if life > 0 {
				c = &stallConn{Conn: raw, until: time.Now().Add(life), dead: make(chan struct{})}
			}
			go serveBash(c, cfg, bash)
		}
	}()
	return ln.Addr().String()
}

func serveBash(c net.Conn, cfg *ssh.ServerConfig, bash string) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range creqs {
				if r.Type != "exec" {
					_ = r.Reply(false, nil)
					continue
				}
				var p struct{ Cmd string }
				_ = ssh.Unmarshal(r.Payload, &p)
				_ = r.Reply(true, nil)
				cmd := exec.Command(bash, "-c", p.Cmd)
				cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
				code := 0
				if err := cmd.Run(); err != nil {
					var ee *exec.ExitError
					code = 255
					if errors.As(err, &ee) {
						code = ee.ExitCode()
					}
				}
				st := make([]byte, 4)
				binary.BigEndian.PutUint32(st, uint32(code))
				_, _ = ch.SendRequest("exit-status", false, st)
				ch.Close()
				return
			}
		}()
	}
}

func testJobExec(t *testing.T, addr string) jobExec {
	_, ck, _ := ed25519.GenerateKey(rand.Reader)
	sg, _ := ssh.NewSignerFromKey(ck)
	cfg := &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.PublicKeys(sg)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	return jobExec{dial: func(ctx context.Context, deadline time.Time) (*ssh.Client, error) {
		return dialSSHBy(ctx, addr, cfg, deadline)
	}}
}

func runJob(t *testing.T, ex jobExec, cmd string, stdin []byte) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var in io.Reader
	if stdin != nil {
		in = bytes.NewReader(stdin)
	}
	var out bytes.Buffer
	err := ex.Run(ctx, cmd, in, &out)
	return out.String(), err
}

func TestJobExecRuns(t *testing.T) {
	ex := testJobExec(t, bashSSHServer(t, 0))

	if out, err := runJob(t, ex, "echo hello; echo oops >&2", nil); err != nil || out != "hello\noops\n" {
		t.Fatalf("quick command: %q %v", out, err)
	}
	out, err := runJob(t, ex, "echo partial; exit 3", nil)
	var je *jobExitError
	if !errors.As(err, &je) || je.status != 3 || out != "partial\n" {
		t.Fatalf("exit status: %q %v", out, err)
	}
	if out, err := runJob(t, ex, "wc -c", []byte("12345")); err != nil || strings.TrimSpace(out) != "5" {
		t.Fatalf("small stdin: %q %v", out, err)
	}
	// stdin larger than a piece arrives whole and in order
	big := make([]byte, 2*jobPiece+12345)
	_, _ = rand.Read(big)
	sum := sha256.Sum256(big)
	if out, err := runJob(t, ex, "sha256sum | cut -d' ' -f1", big); err != nil || strings.TrimSpace(out) != hex.EncodeToString(sum[:]) {
		t.Fatalf("large stdin: %q %v", out, err)
	}
	// a command that outlives the first connection is followed to its end
	if out, err := runJob(t, ex, "for i in 1 2 3 4; do echo line$i; sleep 0.5; done", nil); err != nil || out != "line1\nline2\nline3\nline4\n" {
		t.Fatalf("long command: %q %v", out, err)
	}
	// output larger than one read comes in full
	if out, err := runJob(t, ex, "head -c 2500000 /dev/zero | tr '\\0' x", nil); err != nil || len(out) != 2500000 {
		t.Fatalf("large output: %d bytes, %v", len(out), err)
	}
}

// On a route that stalls every connection soon after it opens, a command
// longer than any one connection still runs once and is followed to its end.
func TestJobExecSurvivesStallingRoute(t *testing.T) {
	old := jobOpTimeout
	jobOpTimeout = 2 * time.Second
	t.Cleanup(func() { jobOpTimeout = old })
	ex := testJobExec(t, bashSSHServer(t, 1500*time.Millisecond))

	out, err := runJob(t, ex, "echo start; sleep 4; echo end", nil)
	if err != nil || out != "start\nend\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestParseJobRead(t *testing.T) {
	st, size, chunk, err := parseJobRead([]byte("motd line\n\n@@midos-job 0 5\nhello"))
	if err != nil || st != 0 || size != 5 || string(chunk) != "hello" {
		t.Fatalf("done: %d %d %q %v", st, size, chunk, err)
	}
	if st, _, _, err := parseJobRead([]byte("\n@@midos-job - 0\n")); err != nil || st != -1 {
		t.Fatalf("running: %d %v", st, err)
	}
	if _, _, _, err := parseJobRead([]byte("\n@@midos-job gone\n")); err == nil {
		t.Fatal("a vanished job must be an error")
	}
	if _, _, _, err := parseJobRead([]byte("garbage")); err == nil {
		t.Fatal("garbage must be an error")
	}
}

// A payload too large for the command line is fetched by the server from the
// panel over HTTPS; when the server cannot fetch it, it goes over SSH instead.
func TestJobExecLendsPayloads(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl")
	}
	var mu sync.Mutex
	lent := map[string][]byte{}
	var fetched atomic.Int32
	hs := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		b, ok := lent[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		fetched.Add(1)
		_, _ = w.Write(b)
	}))
	t.Cleanup(hs.Close)
	broken := false
	jobLend = func(data []byte) (string, func()) {
		if broken {
			return "https://127.0.0.1:1/nothing", func() {}
		}
		path := "/blob/" + randomToken(8)
		mu.Lock()
		lent[path] = data
		mu.Unlock()
		return hs.URL + path, func() { mu.Lock(); delete(lent, path); mu.Unlock() }
	}
	t.Cleanup(func() { jobLend = nil })

	// a route that stalls every connection after a second and a half
	old := jobOpTimeout
	jobOpTimeout = 2 * time.Second
	t.Cleanup(func() { jobOpTimeout = old })
	ex := testJobExec(t, bashSSHServer(t, 1500*time.Millisecond))

	big := make([]byte, 3<<20)
	_, _ = rand.Read(big)
	sum := sha256.Sum256(big)
	want := hex.EncodeToString(sum[:])
	long := "# " + strings.Repeat("x", 4000) + "\nsha256sum | cut -d' ' -f1"
	if out, err := runJob(t, ex, long, big); err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("lent payload: %q %v", out, err)
	}
	if fetched.Load() != 2 {
		t.Fatalf("command and input should both be fetched, got %d fetches", fetched.Load())
	}
	mu.Lock()
	left := len(lent)
	mu.Unlock()
	if left != 0 {
		t.Fatalf("%d loans not withdrawn", left)
	}

	broken = true
	small := big[:600<<10]
	sum = sha256.Sum256(small)
	if out, err := runJob(t, ex, "sha256sum | cut -d' ' -f1", small); err != nil || strings.TrimSpace(out) != hex.EncodeToString(sum[:]) {
		t.Fatalf("payload sent in pieces: %q %v", out, err)
	}
}
