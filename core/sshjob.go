package main

// Remote commands over short SSH connections.
//
// Some routes - from Iran to some foreign datacentres - carry an SSH
// connection for only a few seconds: past that the path stops passing it,
// mostly without closing it, while ping and new connections still go through.
// A long install step, a large upload or a connection reused a little later
// then hangs, or ends in EOF.
//
// So no command rides one long connection. It runs detached on the server, in
// a job directory, and the panel reaches it only with short connections that
// each must be done by a deadline: one hands the command over (a large stdin
// goes ahead in pieces), then as many as it takes read its output as it grows,
// until its exit status is there. Every step can be repeated, so a connection
// the path drops is simply dialled again. A command that ends at once is
// answered on the first connection.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	jobPiece   = 1 << 20   // output read per connection
	jobUpPiece = 256 << 10 // a payload sent over SSH, per connection
	jobInline  = 2560      // command and input this small go on the command line
	jobRetries = 6
	jobPollMax = 2 * time.Second
)

// jobLend, when set, lends a payload for a server to fetch from the panel for
// a short while; withdraw ends the loan. "" means it cannot.
var jobLend func(data []byte) (url string, withdraw func())

var (
	jobOpTimeout  = 15 * time.Second // one short connection, dial to last byte
	jobPieceLimit = 45 * time.Second // the same for a connection carrying a stdin piece
)

// sshDialFunc opens a fresh connection whose every read and write must be done
// by the deadline.
type sshDialFunc func(ctx context.Context, deadline time.Time) (*ssh.Client, error)

// jobExec runs commands on a server as detached jobs over short connections.
type jobExec struct{ dial sshDialFunc }

// jobExitError is a remote command that ended with a non-zero status; it reads
// like the SSH library's own.
type jobExitError struct{ status int }

func (e *jobExitError) Error() string { return "Process exited with status " + strconv.Itoa(e.status) }

// dialSSHBy opens an SSH connection that fails, instead of hanging, when the
// path stops carrying it before the deadline.
func dialSSHBy(ctx context.Context, addr string, cfg *ssh.ClientConfig, deadline time.Time) (*ssh.Client, error) {
	d := net.Dialer{Deadline: deadline}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(deadline)
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// transientSSH tells a dropped or stalled connection, worth dialling again,
// from an answer that will not change.
func transientSSH(err error) bool {
	var ee *ssh.ExitError
	if errors.As(err, &ee) || errors.Is(err, errHostKeyChanged) {
		return false
	}
	m := err.Error()
	return !strings.Contains(m, "unable to authenticate") && !strings.Contains(m, "no common algorithm") &&
		!strings.Contains(m, "connection refused")
}

// once runs one script on a fresh connection and returns what it printed.
func (e jobExec) once(ctx context.Context, script string, stdin []byte, limit time.Duration) ([]byte, error) {
	c, err := e.dial(ctx, time.Now().Add(limit))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	sess, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdin, sess.Stdout = bytes.NewReader(stdin), &out
	err = sess.Run(script)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out.Bytes(), err
}

// op is once, dialled again while the connection is what failed.
func (e jobExec) op(ctx context.Context, script string, stdin []byte, limit time.Duration) ([]byte, error) {
	var err error
	for try := 0; try < jobRetries; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(try) * 700 * time.Millisecond):
			}
		}
		var out []byte
		if out, err = e.once(ctx, script, stdin, limit); err == nil || ctx.Err() != nil || !transientSSH(err) {
			return out, err
		}
	}
	return nil, err
}

// jobFetchScript has the server fetch a payload the panel lent it, checked by
// its sha256 (the panel's certificate may be its own); it fails, so the
// payload is sent another way, when the server cannot reach the panel.
const jobFetchScript = `D=%[1]s; (umask 077; mkdir -p "$D") || exit 1; T="$D/.f.%[2]s"
{ command -v curl >/dev/null 2>&1 && curl -fsSk --max-time 40 -o "$T" %[3]s; } ||
  { command -v wget >/dev/null 2>&1 && wget -q --no-check-certificate -T 40 -O "$T" %[3]s; } || exit 1
echo "%[4]s  $T" | sha256sum -c --quiet >/dev/null 2>&1 || exit 1
mv -f "$T" "$D/%[2]s"`

// jobPieceScript stores one piece of a payload; it prints the size that
// arrived.
const jobPieceScript = `D=%[1]s; (umask 077; mkdir -p "$D") && cat > "$D/.p" && mv -f "$D/.p" "$D/%[2]s.p.%05[3]d" && wc -c < "$D/%[2]s.p.%05[3]d"`

// jobStartScript starts the command once, however often it is sent. The
// command and its input are in the job directory already, or come inline; the
// output and exit status go to files. Then it waits a moment, so a command
// that ends at once is answered right here. A job is a transient systemd unit
// where there is one, so ending the login session does not end it.
const jobStartScript = `D=%[1]s
find /tmp -maxdepth 1 -name '.midos-job-*.done' -mmin +10 -exec rm -rf {} + 2>/dev/null
find /tmp -maxdepth 1 -name '.midos-job-*' -mmin +1440 -exec rm -rf {} + 2>/dev/null
(umask 077; mkdir -p "$D") && cd "$D" || exit 1
if [ ! -e started ]; then
  for f in cmd in; do if ls $f.p.[0-9]* >/dev/null 2>&1; then cat $f.p.[0-9]* > $f && rm -f $f.p.[0-9]*; fi; done
  [ -e cmd ] || echo %[2]s | base64 -d > cmd || exit 1
  [ -e in ] || echo %[3]s | base64 -d > in || exit 1
  : > out; touch started
  W="cd $D || exit 1; "'echo $$ > pid; bash ./cmd < ./in > ./out 2>&1; echo $? > rc.t; mv -f rc.t rc'
  if [ "$(id -u)" = 0 ] && command -v systemd-run >/dev/null 2>&1 &&
    systemd-run --quiet --collect -p KillMode=process --unit=midos-job-%[4]s --setenv=PATH="$PATH" --setenv=HOME="$HOME" bash -c "$W" >/dev/null 2>&1; then :
  elif command -v setsid >/dev/null 2>&1; then setsid bash -c "$W" </dev/null >/dev/null 2>&1 &
  else nohup bash -c "$W" </dev/null >/dev/null 2>&1 &
  fi
fi
i=0; while [ ! -e rc ] && [ $i -lt 10 ]; do sleep 0.1; i=$((i+1)); done
`

// jobReadScript prints a header line, then the output from an offset on. Once
// the status is there and the rest of the output fits, the job is set aside;
// a read repeated after a dropped connection still finds it there.
const jobReadScript = `D=%[1]s; [ -d "$D" ] || D="$D.done"
[ -d "$D" ] || { printf '\n@@midos-job gone\n'; exit 0; }
R=$(cat "$D/rc" 2>/dev/null); S=$(wc -c < "$D/out")
printf '\n@@midos-job %%s %%s\n' "${R:--}" "$S"
tail -c +%[2]d "$D/out" | head -c %[3]d
if [ -n "$R" ] && [ $((S - %[4]d)) -le %[3]d ] && [ -d %[1]s ]; then mv -f %[1]s %[1]s.done; fi
`

const jobKillScript = `P=$(cat %[1]s/pid 2>/dev/null) && { kill -TERM -- -"$P" 2>/dev/null || kill -TERM "$P"; }; systemctl stop midos-job-%[2]s >/dev/null 2>&1; true`

func jobRead(dir string, off int64) string {
	return fmt.Sprintf(jobReadScript, dir, off+1, jobPiece, off)
}

// parseJobRead splits a read into the exit status (-1 while running), the
// output's size so far and the piece of output it carried.
func parseJobRead(b []byte) (status int, size int64, chunk []byte, err error) {
	const mark = "\n@@midos-job "
	i := bytes.Index(b, []byte(mark))
	if i < 0 {
		return 0, 0, nil, errors.New("unexpected reply from the server")
	}
	rest := b[i+len(mark):]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return 0, 0, nil, errors.New("unexpected reply from the server")
	}
	f := strings.Fields(string(rest[:nl]))
	if len(f) == 1 && f[0] == "gone" {
		return 0, 0, nil, errors.New("the command's job is gone from the server (was it restarted?)")
	}
	if len(f) != 2 {
		return 0, 0, nil, errors.New("unexpected reply from the server")
	}
	status = -1
	if f[0] != "-" {
		if status, err = strconv.Atoi(f[0]); err != nil {
			return 0, 0, nil, errors.New("unexpected reply from the server")
		}
	}
	size, _ = strconv.ParseInt(f[1], 10, 64)
	return status, size, rest[nl+1:], nil
}

func (e jobExec) Run(ctx context.Context, cmd string, stdin io.Reader, out io.Writer) error {
	var in []byte
	if stdin != nil {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		in = b
	}
	id := randomToken(8)
	dir := "/tmp/.midos-job-" + id

	// A command and input this small ride inline on the command line; a larger
	// one goes ahead into the job directory.
	inlineCmd, inlineIn := []byte(cmd), in
	if len(cmd)+len(in) > jobInline && len(in) > 0 {
		if err := e.place(ctx, dir, "in", in); err != nil {
			return err
		}
		inlineIn = nil
	}
	if len(cmd)+len(inlineIn) > jobInline {
		if err := e.place(ctx, dir, "cmd", []byte(cmd)); err != nil {
			return err
		}
		inlineCmd = nil
	}

	b64 := func(b []byte) string {
		if b == nil {
			return "''"
		}
		return "'" + base64.StdEncoding.EncodeToString(b) + "'"
	}
	script := fmt.Sprintf(jobStartScript, dir, b64(inlineCmd), b64(inlineIn), id) + jobRead(dir, 0)
	res, err := e.op(ctx, script, nil, jobOpTimeout)
	var off int64
	wait := 300 * time.Millisecond
	for {
		if err != nil {
			if ctx.Err() != nil {
				e.kill(dir, id)
				return ctx.Err()
			}
			return err
		}
		status, size, chunk, perr := parseJobRead(res)
		if perr != nil {
			return perr
		}
		if len(chunk) > 0 {
			if _, err := out.Write(chunk); err != nil {
				return err
			}
			off += int64(len(chunk))
			wait = 300 * time.Millisecond
		}
		if status >= 0 && off >= size {
			if status != 0 {
				return &jobExitError{status}
			}
			return nil
		}
		if len(chunk) < jobPiece {
			select {
			case <-ctx.Done():
				e.kill(dir, id)
				return ctx.Err()
			case <-time.After(wait):
			}
			wait = min(wait*3/2, jobPollMax)
		}
		res, err = e.op(ctx, jobRead(dir, off), nil, jobOpTimeout)
	}
}

// place puts a payload into a file of the job directory. The server fetches
// it from the panel when it can: on the routes that hold SSH back, data the
// panel sends stalls past a few kilobytes, while a download over HTTPS goes
// through. Otherwise it goes over SSH in pieces.
func (e jobExec) place(ctx context.Context, dir, name string, data []byte) error {
	sum := sha256.Sum256(data)
	if jobLend != nil {
		if url, withdraw := jobLend(data); url != "" {
			// the download is a job of its own, so a connection that stalls
			// while it runs is only dialled again
			err := e.Run(ctx, fmt.Sprintf(jobFetchScript, dir, name, shQuote(url), hex.EncodeToString(sum[:])), nil, io.Discard)
			withdraw()
			if err == nil || ctx.Err() != nil {
				return err
			}
		}
	}
	for n := 0; n == 0 || len(data) > 0; n++ {
		piece := data[:min(jobUpPiece, len(data))]
		data = data[len(piece):]
		got, err := e.op(ctx, fmt.Sprintf(jobPieceScript, dir, name, n), piece, jobPieceLimit)
		if err != nil {
			return fmt.Errorf("sending the input to the server: %w", err)
		}
		if strings.TrimSpace(string(got)) != strconv.Itoa(len(piece)) {
			return errors.New("sending the input to the server: a piece arrived short")
		}
	}
	return nil
}

// kill stops a job whose caller gave up on it.
func (e jobExec) kill(dir, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*jobOpTimeout)
	defer cancel()
	_, _ = e.op(ctx, fmt.Sprintf(jobKillScript, dir, id), nil, jobOpTimeout)
}

// targetExec reaches one of the operator's own servers by address and login.
// There is no stored host key to pin against on a first set-up, so the key is
// accepted as given.
func targetExec(tgt sshTarget) (hostExec, error) {
	auths, err := sshAuth(tgt.Password, tgt.Key)
	if err != nil {
		return nil, err
	}
	port := tgt.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(tgt.Host, strconv.Itoa(port))
	cfg := &ssh.ClientConfig{User: tgt.User, Auth: auths, HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	return jobExec{dial: func(ctx context.Context, deadline time.Time) (*ssh.Client, error) {
		c, err := dialSSHBy(ctx, addr, cfg, deadline)
		if err != nil {
			return nil, fmt.Errorf("connect %s: %w", addr, err)
		}
		return c, nil
	}}, nil
}
