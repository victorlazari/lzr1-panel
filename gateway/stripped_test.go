package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// On Linux, tailscale serve proxies to the gateway's Unix socket: it strips the mount path (a unix: target cannot
// put it back) and sends Host: localhost. --stripped puts --base back; --host is the name the browser's Origin carries.
func TestStripped(t *testing.T) {
	dir := sockDir(t)
	upSock := filepath.Join(dir, "ttyd.sock")
	up := startUpstream(t, upSock)
	sock := filepath.Join(dir, "gw.sock")
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	c := config{listen: sock, upstream: upSock, base: testBase, allow: []string{"you@example.com"},
		host: "h.example.ts.net", stripped: true}
	srv := &http.Server{Handler: newServer(c, sha256.Sum256([]byte(testPassword)), log.New(io.Discard, "", 0))}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	client := &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// As serve sends them: the path without the base, Host localhost.
	sreq := func(method, p, origin, tok string, form url.Values) *http.Request {
		r := req(method, p, "you@example.com", origin, tok, form)
		r.Host = "localhost"
		return r
	}
	if resp, body := do(t, client, sreq("GET", "/login", "", "", nil)); resp.StatusCode != 200 || !strings.Contains(body, "password") {
		t.Fatalf("login form: %d", resp.StatusCode)
	}
	for _, o := range []string{"https://localhost", "https://gw", ""} {
		if resp, _ := do(t, client, sreq("POST", "/login", o, "", url.Values{"password": {testPassword}})); resp.StatusCode != 403 {
			t.Errorf("login with Origin %q: %d, want 403", o, resp.StatusCode)
		}
	}
	resp, _ := do(t, client, sreq("POST", "/login", "https://h.example.ts.net", "", url.Values{"password": {testPassword}}))
	ck := sessionCookie(resp)
	if resp.StatusCode != http.StatusSeeOther || ck == nil || resp.Header.Get("Location") != testBase {
		t.Fatalf("login: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, body := do(t, client, sreq("GET", "/token", "", ck.Value, nil)); resp.StatusCode != 200 || body != "terminal "+testBase+"token" {
		t.Errorf("proxied: %d %q", resp.StatusCode, body)
	}
	if resp, _ := do(t, client, sreq("GET", "/", "", ck.Value, nil)); resp.StatusCode != 200 {
		t.Errorf("base: %d", resp.StatusCode)
	}
	if n := len(up.hits()); n != 2 {
		t.Errorf("upstream saw %d requests, want 2", n)
	}
	for _, p := range []string{"/../token", "/./token", "//token"} {
		if resp, _ := do(t, client, sreq("GET", p, "", ck.Value, nil)); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestFlagsHostStripped(t *testing.T) {
	ok := []string{"--listen", "/d/gw.sock", "--upstream", "/d/ttyd.sock", "--base", testBase, "--allow", "a@b"}
	c, err := parseFlags(append(ok, "--host", "H.example.ts.net", "--stripped"), io.Discard)
	if err != nil || c.host != "h.example.ts.net" || !c.stripped {
		t.Fatalf("parsed %+v, %v", c, err)
	}
	for name, extra := range map[string][]string{
		"stripped without host": {"--stripped"},
		"host with a scheme":    {"--host", "https://h.example.ts.net"},
		"host with a path":      {"--host", "h/x"},
		"stripped over tcp":     {"--host", "h", "--stripped", "--listen", "127.0.0.1:0"},
	} {
		if _, err := parseFlags(append(append([]string(nil), ok...), extra...), io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("%s: err = %v, want usage", name, err)
		}
	}
}
