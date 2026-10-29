package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuditLine(t *testing.T) {
	at := time.Date(2026, 10, 29, 14, 5, 0, 0, time.FixedZone("BRT", -3*3600))
	var b bytes.Buffer
	a := &auditLog{w: &b, now: func() time.Time { return at }}
	a.event("failure", "user", "a b=c\nlogin x=1", "fails", "2")
	a.event("shutdown", "reason", "ttl")
	want := "2026-10-29T14:05:00-03:00 failure user=a_b_c_login_x_1 fails=2\n" +
		"2026-10-29T14:05:00-03:00 shutdown reason=ttl\n"
	if b.String() != want {
		t.Errorf("got\n%q\nwant\n%q", b.String(), want)
	}
	// A crafted value can never add a field or a line.
	b.Reset()
	a.event("login", "user", "\x00é\t\r\n=\x7f", "sid", "")
	if got := b.String(); strings.Count(got, "\n") != 1 || len(strings.Fields(got)) != 4 {
		t.Errorf("sanitized line %q", got)
	}
	var nilLog *auditLog
	nilLog.event("start", "ttl", "1h") // no --audit: a no-op
}

// A write error is logged once and does not stop anything.
func TestAuditWriteError(t *testing.T) {
	var logb syncBuf
	a := &auditLog{w: errWriter{}, now: time.Now, log: log.New(&logb, "", 0)}
	a.event("start", "ttl", "1h")
	a.event("start", "ttl", "1h")
	if n := strings.Count(logb.String(), "\n"); n != 1 {
		t.Errorf("logged %d times: %q", n, logb.String())
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestSid(t *testing.T) {
	tok := strings.Repeat("ab", 32)
	sum := sha256.Sum256([]byte(tok))
	if got := sid(tok); got != hex.EncodeToString(sum[:])[:8] {
		t.Errorf("sid %q", got)
	}
}

func TestAuditFile(t *testing.T) {
	dir := sockDir(t)
	p := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(p, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openAudit(p)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("new\n")
	f.Close()
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("existing 0644 file left %04o", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != "old\nnew\n" {
		t.Errorf("not appended: %q", b)
	}
	os.Symlink(p, filepath.Join(dir, "link.log"))
	os.Mkdir(filepath.Join(dir, "d"), 0o700)
	for _, bad := range []string{filepath.Join(dir, "link.log"), filepath.Join(dir, "d"), filepath.Join(dir, "missing", "audit.log")} {
		if f, err := openAudit(bad); err == nil {
			f.Close()
			t.Errorf("%s accepted", bad)
		}
	}
}

// runGateway starts run with args on a fresh socket and an audit file; it returns the socket, the exit channel and
// a client, once run printed ready.
func runGateway(t *testing.T, extra ...string) (string, string, chan int, *http.Client) {
	t.Helper()
	dir := sockDir(t)
	startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	sock := filepath.Join(dir, "gw.sock")
	audit := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(audit, []byte("earlier line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--listen", sock, "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase,
		"--allow", "a@x,b@x", "--log", filepath.Join(dir, "gw.log"), "--audit", audit}, extra...)
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), args, strings.NewReader(testPassword+"\n"), pw, io.Discard) }()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("stdout %q, %v", line, err)
	}
	go io.Copy(io.Discard, pr)
	client := &http.Client{
		Transport:     &http.Transport{Dial: func(_, _ string) (net.Conn, error) { return net.Dial("unix", sock) }},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return sock, audit, done, client
}

func exitCode(t *testing.T, done chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return")
	}
	return -1
}

func TestAuditRunKillSwitch(t *testing.T) {
	sock, audit, done, client := runGateway(t)
	_, tok := login(t, client, "a@x", testPassword)
	if tok == "" {
		t.Fatal("login failed")
	}
	if resp, _ := do(t, client, req("POST", testBase+"logout", "a@x", "https://gw", tok, url.Values{})); resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	codes := []int{}
	for i := 1; i <= 20; i++ {
		who := "a@x"
		if i > 5 {
			who = "b@x"
		}
		resp, _ := login(t, client, who, "wrong password")
		codes = append(codes, resp.StatusCode)
	}
	if codes[4] != 429 || codes[19] != 503 {
		t.Errorf("codes %v", codes)
	}
	if code := exitCode(t, done); code != 3 {
		t.Errorf("exit %d after the kill switch, want 3", code)
	}
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		t.Error("the socket still answers")
	}
	fi, err := os.Stat(audit)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("audit file %v %v", fi, err)
	}
	b, _ := os.ReadFile(audit)
	text := string(b)
	if !strings.HasPrefix(text, "earlier line\n") {
		t.Errorf("earlier content lost: %q", text)
	}
	text = strings.TrimPrefix(text, "earlier line\n")
	want := []string{"start", "login", "logout", "failure", "failure", "failure", "failure", "failure", "lockout"}
	for i := 0; i < 5; i++ {
		want = append(want, "failure")
	}
	want = append(want, "lockout")
	for i := 0; i < 10; i++ {
		want = append(want, "failure")
	}
	want = append(want, "shutdown")
	if got := events(text); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("events\n got %v\nwant %v", got, want)
	}
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if _, err := time.Parse(time.RFC3339, strings.Fields(l)[0]); err != nil {
			t.Errorf("line %q: %v", l, err)
		}
	}
	for _, w := range []string{" start ttl=8h0m0s\n", " login user=a@x sid=" + sid(tok) + "\n", " logout user=a@x sid=" + sid(tok) + "\n",
		" failure user=b@x fails=0 total=20 locked=1\n", " shutdown reason=killswitch\n"} {
		if !strings.Contains(text, w) {
			t.Errorf("audit lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, testPassword) || strings.Contains(text, tok) || strings.Contains(text, "wrong password") {
		t.Errorf("audit holds a secret:\n%s", text)
	}
}

func TestAuditRunSignal(t *testing.T) {
	_, audit, done, _ := runGateway(t)
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	if code := exitCode(t, done); code != 0 {
		t.Errorf("exit %d on SIGTERM", code)
	}
	if b, _ := os.ReadFile(audit); !strings.HasSuffix(string(b), " shutdown reason=signal\n") {
		t.Errorf("audit %q", b)
	}
}

func TestAuditRunTTL(t *testing.T) {
	_, audit, done, _ := runGateway(t, "--ttl", "200ms")
	if code := exitCode(t, done); code != 0 {
		t.Errorf("exit %d at the ttl", code)
	}
	b, _ := os.ReadFile(audit)
	if ev := events(strings.TrimPrefix(string(b), "earlier line\n")); strings.Join(ev, " ") != "start shutdown" ||
		!strings.Contains(string(b), " start ttl=200ms\n") || !strings.HasSuffix(string(b), " shutdown reason=ttl\n") {
		t.Errorf("audit %q", b)
	}
}

func TestAuditRunRefused(t *testing.T) {
	dir := sockDir(t)
	startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	real := filepath.Join(dir, "real.log")
	os.WriteFile(real, nil, 0o600)
	os.Symlink(real, filepath.Join(dir, "link.log"))
	for _, a := range []string{filepath.Join(dir, "link.log"), filepath.Join(dir, "missing", "audit.log")} {
		args := []string{"--listen", filepath.Join(dir, "gw.sock"), "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase,
			"--allow", "a@x", "--audit", a, "--ttl", "5s"}
		var stderr bytes.Buffer
		if code := run(context.Background(), args, strings.NewReader(testPassword+"\n"), io.Discard, &stderr); code != 1 || stderr.Len() == 0 {
			t.Errorf("--audit %s: exit %d %q", a, code, stderr.String())
		}
	}
}
