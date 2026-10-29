package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testBase = "/0b6f1a52-3c1d-4e8a-9f20-7d5e2c4b1a90/"

// sockDir returns a fresh 0700 directory short enough for Unix socket paths.
func sockDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeTTYD is an upstream on a Unix socket that records requests and echoes WebSocket bytes.
type fakeTTYD struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (f *fakeTTYD) hits() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.reqs...)
}

func (f *fakeTTYD) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		io.Copy(conn, rw) // echo
		return
	}
	w.Header().Set("X-Upstream", "ttyd")
	io.WriteString(w, "terminal "+r.URL.RequestURI())
}

func startUpstream(t *testing.T, sock string) *fakeTTYD {
	t.Helper()
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTTYD{}
	srv := &http.Server{Handler: f}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return f
}

// startGateway serves newServer on a fresh socket; returns the server, an HTTP client for it and its socket.
func startGateway(t *testing.T, dir, upstream string) (*server, *http.Client, string) {
	t.Helper()
	return startGatewayWith(t, dir, upstream, []string{"you@example.com"}, nil)
}

// startGatewayWith is startGateway with its own allowlist; tweak (if any) changes the server before it serves.
func startGatewayWith(t *testing.T, dir, upstream string, allow []string, tweak func(*server)) (*server, *http.Client, string) {
	t.Helper()
	sock := filepath.Join(dir, "gw.sock")
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	c := config{listen: sock, upstream: upstream, base: testBase, allow: allow}
	s := newServer(c, sha256.Sum256([]byte("correct horse battery")), log.New(io.Discard, "", 0))
	if tweak != nil {
		tweak(s)
	}
	srv := &http.Server{Handler: s}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	client := &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s, client, sock
}

func checkSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for k, v := range securityHeaders {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestProxyHTTP(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	s, client, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	tok := s.newSession("you@example.com")

	req, _ := http.NewRequest("GET", "http://gw"+testBase+"token?x=1", nil)
	req.Header.Set("Tailscale-User-Login", "you@example.com")
	req.Header.Set("Tailscale-User-Name", "You")
	req.Header.Set("Cookie", cookieName+"="+tok+"; other=keep")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "terminal "+testBase+"token?x=1" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Upstream") != "ttyd" {
		t.Error("upstream header lost")
	}
	checkSecurityHeaders(t, resp.Header)
	hits := up.hits()
	if len(hits) != 1 {
		t.Fatalf("upstream saw %d requests", len(hits))
	}
	h := hits[0].Header
	for k := range h {
		if strings.HasPrefix(k, "Tailscale-") {
			t.Errorf("%s reached upstream", k)
		}
	}
	if ck := h.Get("Cookie"); strings.Contains(ck, cookieName) || !strings.Contains(ck, "other=keep") {
		t.Errorf("upstream Cookie = %q", ck)
	}
}

func TestProxyWebSocket(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	s, _, sock := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	tok := s.newSession("you@example.com")

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "GET "+testBase+"ws HTTP/1.1\r\nHost: gw\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: tty\r\n"+
		"Tailscale-User-Login: you@example.com\r\nOrigin: https://gw\r\nCookie: "+cookieName+"="+tok+"\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade got %d", resp.StatusCode)
	}
	msg := []byte("\x81\x05hello")
	conn.Write(msg)
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(br, got); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("echo got %q, %v", got, err)
	}
	if len(up.hits()) != 1 {
		t.Fatalf("upstream saw %d requests", len(up.hits()))
	}
}

func TestProxyUnauthenticated(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	_, client, sock := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))

	cases := []struct {
		path, cookie string
		want         int
		emptyBody    bool
	}{
		{testBase, "", 401, false},
		{testBase + "ws", cookieName + "=bogus", 401, false},
		{testBase + "token", cookieName + "=", 401, false},
		{"/", "", 404, true},
		{"/other/", "", 404, true},
		{testBase + "../x", "", 404, true},
		{testBase + "%2e%2e/x", "", 404, true},
		{strings.TrimSuffix(testBase, "/"), "", 308, false},
	}
	for _, c := range cases {
		req, _ := http.NewRequest("GET", "http://gw/", nil)
		req.URL.Opaque = c.path // keep the path exactly as written
		req.Header.Set("Tailscale-User-Login", "you@example.com")
		if c.cookie != "" {
			req.Header.Set("Cookie", c.cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s: got %d, want %d", c.path, resp.StatusCode, c.want)
		}
		if c.emptyBody && len(body) != 0 {
			t.Errorf("%s: body %q, want empty", c.path, body)
		}
		checkSecurityHeaders(t, resp.Header)
	}

	// An unauthenticated WebSocket upgrade never reaches upstream either.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET "+testBase+"ws HTTP/1.1\r\nHost: gw\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+
		"Tailscale-User-Login: you@example.com\r\nOrigin: https://gw\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("unauthenticated upgrade: %v %v", resp, err)
	}
	if n := len(up.hits()); n != 0 {
		t.Fatalf("upstream saw %d unauthenticated requests", n)
	}
}

func TestProxyUpstreamDown(t *testing.T) {
	dir := sockDir(t)
	s, client, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	req, _ := http.NewRequest("GET", "http://gw"+testBase, nil)
	req.Header.Set("Tailscale-User-Login", "you@example.com")
	req.Header.Set("Cookie", cookieName+"="+s.newSession("you@example.com"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", resp.StatusCode)
	}
	checkSecurityHeaders(t, resp.Header)
}

func TestFlags(t *testing.T) {
	ok := []string{"--listen", "/d/gw.sock", "--upstream", "/d/ttyd.sock", "--base", testBase, "--allow", " You@Example.com, ,b@x.io "}
	c, err := parseFlags(ok, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.ttl != 8*time.Hour || strings.Join(c.allow, ",") != "you@example.com,b@x.io" || c.base != testBase {
		t.Fatalf("parsed %+v", c)
	}
	with := func(k, v string) []string {
		a := append([]string(nil), ok...)
		for i := range a {
			if a[i] == k {
				a[i+1] = v
			}
		}
		return a
	}
	bad := map[string][]string{
		"no flags":      {},
		"no listen":     ok[2:],
		"base no slash": with("--base", "abc"),
		"base no tail":  with("--base", "/abc"),
		"base no head":  with("--base", "abc/"),
		"base nested":   with("--base", "/a/b/"),
		"base root":     with("--base", "/"),
		"empty allow":   with("--allow", " , "),
		"zero ttl":      append(append([]string(nil), ok...), "--ttl", "0s"),
		"bad ttl":       append(append([]string(nil), ok...), "--ttl", "soon"),
		"extra arg":     append(append([]string(nil), ok...), "x"),
		"unknown flag":  append(append([]string(nil), ok...), "--funnel"),
	}
	for name, args := range bad {
		if _, err := parseFlags(args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("%s: err = %v, want usage", name, err)
		}
		if code := run(context.Background(), args, strings.NewReader("correct horse battery\n"), io.Discard, io.Discard); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestFlagsPassword(t *testing.T) {
	if _, err := readPassword(strings.NewReader("short\n")); err == nil {
		t.Error("an 11-or-fewer-character password was accepted")
	}
	if _, err := readPassword(strings.NewReader("")); err == nil {
		t.Error("an empty password was accepted")
	}
	sum, err := readPassword(strings.NewReader("correct horse battery\r\nignored\n"))
	if err != nil || sum != sha256.Sum256([]byte("correct horse battery")) {
		t.Errorf("got %x, %v", sum, err)
	}
	if sum, err := readPassword(strings.NewReader("exactly12chr")); err != nil || sum != sha256.Sum256([]byte("exactly12chr")) {
		t.Errorf("no trailing newline: %v", err)
	}
	dir := sockDir(t)
	args := []string{"--listen", filepath.Join(dir, "gw.sock"), "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase, "--allow", "you@example.com"}
	if code := run(context.Background(), args, strings.NewReader("short\n"), io.Discard, io.Discard); code != 1 {
		t.Errorf("short password: exit %d, want 1", code)
	}
}

func TestSocketDirRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o777} {
		dir := sockDir(t)
		os.Chmod(dir, mode)
		if l, err := listenUnix(filepath.Join(dir, "gw.sock")); err == nil {
			l.Close()
			t.Errorf("%04o: listened in an open directory", mode)
		}
		args := []string{"--listen", filepath.Join(dir, "gw.sock"), "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase, "--allow", "you@example.com"}
		if code := run(context.Background(), args, strings.NewReader("correct horse battery\n"), io.Discard, io.Discard); code != 1 {
			t.Errorf("%04o: exit %d, want 1", mode, code)
		}
	}
}

func TestSocketLiveRefused(t *testing.T) {
	dir := sockDir(t)
	sock := filepath.Join(dir, "gw.sock")
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(sock)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %04o, want 0600", fi.Mode().Perm())
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if l2, err := listenUnix(sock); err == nil {
		l2.Close()
		t.Fatal("a second gateway took a live socket")
	}
	if c, err := net.Dial("unix", sock); err != nil {
		t.Fatal("the live socket was removed")
	} else {
		c.Close()
	}
	// Stale: the file stays after its listener is gone.
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	l3, err := listenUnix(sock)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	l3.Close()
	// Not a socket: never removed.
	os.WriteFile(sock, []byte("x"), 0o600)
	if _, err := listenUnix(sock); err == nil {
		t.Fatal("a regular file was replaced")
	}
}

func TestSocketRun(t *testing.T) {
	dir := sockDir(t)
	startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	sock := filepath.Join(dir, "gw.sock")
	args := []string{"--listen", sock, "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase, "--allow", "you@example.com", "--log", filepath.Join(dir, "gw.log")}

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- run(ctx, args, strings.NewReader("correct horse battery\n"), pw, io.Discard) }()
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("stdout %q, %v", line, err)
	}
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v %v", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "gw.log")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("log file: %v %v", fi, err)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d on shutdown", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no shutdown")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}

	// --ttl ends it by itself.
	args = append(args, "--ttl", "300ms")
	start := time.Now()
	if code := run(context.Background(), args, strings.NewReader("correct horse battery\n"), io.Discard, io.Discard); code != 0 {
		t.Errorf("ttl exit %d", code)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 6*time.Second {
		t.Errorf("ttl took %v", d)
	}
}

func TestListenTCP(t *testing.T) {
	dir := sockDir(t)
	up := filepath.Join(dir, "ttyd.sock")
	ttyd := startUpstream(t, up)
	args := func(listen string) []string {
		return []string{"--listen", listen, "--upstream", up, "--base", testBase, "--allow", "you@example.com"}
	}
	for _, l := range []string{"0.0.0.0:0", "192.168.1.10:8080", "example.com:80", "localhost:0", "127.0.0.1", ":0"} {
		if code := run(context.Background(), args(l), strings.NewReader("correct horse battery\n"), io.Discard, io.Discard); code != 2 {
			t.Errorf("--listen %s: exit %d, want 2", l, code)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- run(ctx, args("127.0.0.1:0"), strings.NewReader("correct horse battery\n"), pw, io.Discard)
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	addr := strings.TrimPrefix(strings.TrimSuffix(line, "\n"), "ready 127.0.0.1:")
	if err != nil || addr == line || addr == "0" || addr == "" {
		t.Fatalf("stdout %q, %v", line, err)
	}
	addr = "127.0.0.1:" + addr
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, _ := http.NewRequest("GET", "http://"+addr+testBase, nil)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("Tailscale-User-Login", "you@example.com")
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != testBase+"login" {
		t.Errorf("no session over TCP: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(ttyd.hits()) != 0 {
		t.Error("an unauthenticated request reached the upstream")
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d on shutdown", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no shutdown")
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Error("still listening after shutdown")
	}
}
