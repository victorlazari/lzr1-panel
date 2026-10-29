package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// clientFrame is one masked frame as a browser sends it, with the 7-, 16- or 64-bit length its payload needs.
func clientFrame(op byte, payload []byte) []byte {
	b := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		b = append(b, 0x80|byte(n))
	case n < 1<<16:
		b = append(b, 0x80|126)
		b = binary.BigEndian.AppendUint16(b, uint16(n))
	default:
		b = append(b, 0x80|127)
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	mask := []byte{0x81, 0x82, 0x89, 0x8a} // bytes that look like frame headers, masked into the payload too
	b = append(b, mask...)
	for i, c := range payload {
		b = append(b, c^mask[i%4])
	}
	return b
}

// headerish is a payload full of bytes that would read as data-frame headers if the parser looked at them.
func headerish(n int) []byte {
	return bytes.Repeat([]byte{0x81, 0x01, 0x82, 0x00}, n/4+1)[:n]
}

func TestFrameParser(t *testing.T) {
	var stream []byte
	add := func(op byte, p []byte) { stream = append(stream, clientFrame(op, p)...) }
	add(9, []byte("ping"))         // ping: not input
	add(10, nil)                   // pong: not input
	add(1, []byte("a"))            // a keystroke: 1
	add(2, headerish(300))         // 16-bit length: 2
	add(1, nil)                    // an empty data frame: not input
	add(10, headerish(125))        // a long pong
	add(2, headerish(70000))       // 64-bit length: 3
	add(0, []byte("continuation")) // 4
	add(8, []byte{0x03, 0xe8})     // close: not input
	const want = 4

	count := func(chunks ...[]byte) int {
		var p frameParser
		n := 0
		for _, c := range chunks {
			n += p.feed(c)
		}
		return n
	}
	if n := count(stream); n != want {
		t.Fatalf("whole stream: %d data frames, want %d", n, want)
	}
	for i := 0; i <= len(stream); i++ {
		if n := count(stream[:i], stream[i:]); n != want {
			t.Fatalf("split at %d: %d data frames, want %d", i, n, want)
		}
	}
	var p frameParser
	n := 0
	for i := range stream {
		n += p.feed(stream[i : i+1])
	}
	if n != want {
		t.Fatalf("byte by byte: %d data frames, want %d", n, want)
	}

	// A malformed header stops the parser for good: nothing after it counts.
	for name, bad := range map[string][]byte{
		"unmasked":        {0x81, 0x01, 'a'},
		"reserved opcode": {0x83, 0x81, 1, 2, 3, 4, 'a'},
		"64-bit top bit":  {0x82, 0xff, 0x80, 0, 0, 0, 0, 0, 0, 0},
	} {
		var p frameParser
		if n := p.feed(append(append(append([]byte{}, clientFrame(1, []byte("x"))...), bad...), clientFrame(1, []byte("y"))...)); n != 1 {
			t.Errorf("%s: %d data frames, want 1 (the one before it)", name, n)
		}
		if n := p.feed(clientFrame(1, []byte("z"))); n != 0 {
			t.Errorf("%s: the parser counted again after it", name)
		}
	}
}

// pingTTYD is an upstream whose WebSockets get a ping every 50 ms, like ttyd -P; it reads and drops what the
// browser sends, and records when a WebSocket ends.
type pingTTYD struct {
	mu    sync.Mutex
	ended int
}

func (f *pingTTYD) endedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

func (f *pingTTYD) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		io.WriteString(w, "terminal")
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer func() {
		conn.Close()
		f.mu.Lock()
		f.ended++
		f.mu.Unlock()
	}()
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	rw.Flush()
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, rw); close(done) }()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			if _, err := conn.Write([]byte{0x89, 0x00}); err != nil {
				return
			}
		}
	}
}

// runGatewayWith is runGateway with its own upstream handler.
func runGatewayWith(t *testing.T, up http.Handler, extra ...string) (string, string, chan int, *http.Client) {
	t.Helper()
	dir := sockDir(t)
	l, err := net.Listen("unix", filepath.Join(dir, "ttyd.sock"))
	if err != nil {
		t.Fatal(err)
	}
	usrv := &http.Server{Handler: up}
	go usrv.Serve(l)
	t.Cleanup(func() { usrv.Close() })
	sock := filepath.Join(dir, "gw.sock")
	audit := filepath.Join(dir, "audit.log")
	args := append([]string{"--listen", sock, "--upstream", filepath.Join(dir, "ttyd.sock"), "--base", testBase,
		"--allow", "a@x", "--log", filepath.Join(dir, "gw.log"), "--audit", audit}, extra...)
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

// browserWS opens a WebSocket as a@x with tok and answers every ping with a pong; with every > 0 it also sends a
// keystroke that often. It returns the conn and a channel closed when the gateway ends the WebSocket.
func browserWS(t *testing.T, sock, tok string, every time.Duration) (net.Conn, chan struct{}) {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	io.WriteString(conn, "GET "+testBase+"ws HTTP/1.1\r\nHost: gw\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"+
		"Tailscale-User-Login: a@x\r\nOrigin: https://gw\r\nCookie: "+cookieName+"="+tok+"\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	var wmu sync.Mutex
	write := func(b []byte) {
		wmu.Lock()
		conn.Write(b)
		wmu.Unlock()
	}
	ended := make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		defer close(ended)
		hdr := make([]byte, 2)
		for {
			if _, err := io.ReadFull(br, hdr); err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, br, int64(hdr[1]&0x7f)); err != nil {
				return
			}
			if hdr[0]&0x0f == 9 {
				write(clientFrame(10, nil))
			}
		}
	}()
	if every > 0 {
		go func() {
			tick := time.NewTicker(every)
			defer tick.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ended:
					return
				case <-tick.C:
					write(clientFrame(2, []byte("0k")))
				}
			}
		}()
	}
	return conn, ended
}

func waitEnded(t *testing.T, ended chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-ended:
	case <-time.After(within):
		t.Fatalf("%s: the WebSocket is still open after %v", what, within)
	}
}

func readAudit(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIdleOutPings(t *testing.T) {
	up := &pingTTYD{}
	sock, audit, done, client := runGatewayWith(t, up, "--idle", "300ms")
	_, tok := login(t, client, "a@x", testPassword)
	if tok == "" {
		t.Fatal("login failed")
	}
	_, ended := browserWS(t, sock, tok, 0)
	waitEnded(t, ended, 3*time.Second, "pings only")
	if resp, _ := do(t, client, req("GET", testBase, "a@x", "", tok, nil)); resp.StatusCode != 401 {
		t.Errorf("the idle cookie got %d, want 401", resp.StatusCode)
	}
	text := readAudit(t, audit)
	if !strings.Contains(text, " start ttl=8h0m0s idle=300ms\n") || !strings.Contains(text, " idle-out user=a@x sid="+sid(tok)+"\n") {
		t.Errorf("audit:\n%s", text)
	}
	if strings.Contains(text, tok) {
		t.Errorf("audit holds the token:\n%s", text)
	}
	deadline := time.Now().Add(2 * time.Second)
	for up.endedCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if up.endedCount() == 0 {
		t.Error("the terminal's WebSocket still runs upstream")
	}
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	exitCode(t, done)
}

func TestIdleInputKeepsAlive(t *testing.T) {
	sock, audit, done, client := runGatewayWith(t, &pingTTYD{}, "--idle", "300ms")
	_, tok := login(t, client, "a@x", testPassword)
	_, ended := browserWS(t, sock, tok, 100*time.Millisecond)
	select {
	case <-ended:
		t.Fatal("a WebSocket with input every 100 ms was closed")
	case <-time.After(time.Second):
	}
	if strings.Contains(readAudit(t, audit), "idle-out") {
		t.Error("idle-out audited for an active session")
	}
	if resp, _ := do(t, client, req("GET", testBase, "a@x", "", tok, nil)); resp.StatusCode != 200 {
		t.Errorf("the active cookie got %d", resp.StatusCode)
	}
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	exitCode(t, done)
}

func TestIdleLogoutClosesTabs(t *testing.T) {
	sock, _, done, client := runGatewayWith(t, &pingTTYD{})
	_, tok := login(t, client, "a@x", testPassword)
	_, tab1 := browserWS(t, sock, tok, 0)
	_, tab2 := browserWS(t, sock, tok, 0)
	if resp, _ := do(t, client, req("POST", testBase+"logout", "a@x", "https://gw", tok, url.Values{})); resp.StatusCode != 303 {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	waitEnded(t, tab1, 2*time.Second, "logout, tab 1")
	waitEnded(t, tab2, 2*time.Second, "logout, tab 2")
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	exitCode(t, done)
}

func TestIdleShutdownClosesTerminals(t *testing.T) {
	sock, _, done, client := runGatewayWith(t, &pingTTYD{})
	_, tok := login(t, client, "a@x", testPassword)
	conn, ended := browserWS(t, sock, tok, 0)
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	if code := exitCode(t, done); code != 0 {
		t.Errorf("exit %d on SIGTERM", code)
	}
	waitEnded(t, ended, 2*time.Second, "SIGTERM")
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
		t.Errorf("the client read %v after run returned, want EOF", err)
	}
}

// A WebSocket registered after its session ended (deleted while the upgrade was in flight) is closed at once.
func TestIdleRegisterGoneSession(t *testing.T) {
	dir := sockDir(t)
	s, _, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	a, b := net.Pipe()
	defer b.Close()
	c := s.track("no-such-token", a)
	b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read %v, want EOF from a closed pipe", err)
	}
	c.Close() // twice is harmless
}

func TestIdleFlag(t *testing.T) {
	base := []string{"--listen", "/tmp/x/gw.sock", "--upstream", "/tmp/x/ttyd.sock", "--base", testBase, "--allow", "a@x"}
	c, err := parseFlags(base, io.Discard)
	if err != nil || c.idle != 30*time.Minute {
		t.Errorf("default idle %v, %v", c.idle, err)
	}
	c, err = parseFlags(append(base, "--idle", "1m"), io.Discard)
	if err != nil || c.idle != time.Minute {
		t.Errorf("--idle 1m: %v, %v", c.idle, err)
	}
	for _, v := range []string{"0", "-1m", "soon"} {
		if _, err := parseFlags(append(base, "--idle", v), io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("--idle %s accepted", v)
		}
	}
}
