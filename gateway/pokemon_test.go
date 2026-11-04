package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testUUID = "0b6f1a52-3c1d-4e8a-9f20-7d5e2c4b1a90"

// stripState is a state directory like the panel's: a snapshot with three sessions, one panel (id 4242) of this remote
// and one (id 77) of another, and the art of Pokémon 25.
func stripState(t *testing.T, s *server) string {
	t.Helper()
	dir := t.TempDir()
	s.state = dir
	snap := `{"at":1,"sessions":[
	 {"key":"box/api","state":"work","idle":5,"done":false,"dex":25},
	 {"key":"box/web","state":"idle","idle":900,"done":true,"dex":143},
	 {"key":"box/bad\u0001","state":"work","idle":1,"done":false,"dex":25},
	 {"key":"nokey","state":"work","idle":1,"done":false,"dex":25},
	 {"key":"box/odd","state":"dancing","idle":1,"done":false,"dex":25},
	 {"key":"box/high","state":"work","idle":1,"done":false,"dex":152}]}`
	must(t, os.WriteFile(filepath.Join(dir, sessionsFile), []byte(snap), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "4242.remote"), []byte(testUUID), 0o600))
	must(t, os.WriteFile(filepath.Join(dir, "77.remote"), []byte("someone-elses-remote"), 0o600))
	art := filepath.Join(dir, artDir, "0025")
	must(t, os.MkdirAll(art, 0o700))
	must(t, os.WriteFile(filepath.Join(art, "walkR.gif"), []byte("GIF89a"), 0o600))
	must(t, os.WriteFile(filepath.Join(art, "meta.json"), []byte(`{"walkR":{"w":48}}`), 0o600))
	must(t, os.WriteFile(filepath.Join(art, "secret.txt"), []byte("no"), 0o600))
	must(t, os.Symlink("/etc/hosts", filepath.Join(art, "sleep.gif")))
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pick(t *testing.T, s *server, c *http.Client, tok, origin, body string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest("POST", "http://gw"+testBase+pickPath, bytes.NewBufferString(body))
	r.Header.Set("Tailscale-User-Login", "you@example.com")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Cookie", cookieName+"="+tok)
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestSessionsSnapshot(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	path := testBase + sessionsPath

	for _, tok := range []string{"-", "bogus"} {
		if resp, _ := get(t, s, c, "GET", path, tok); resp.StatusCode != 401 {
			t.Errorf("GET with session %q: %d, want 401", tok, resp.StatusCode)
		}
	}
	// Without a state directory: an empty list, not an error.
	resp, body := get(t, s, c, "GET", path, "")
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != `{"at":0,"sessions":[]}` {
		t.Fatalf("no state: %d %s", resp.StatusCode, body)
	}
	stripState(t, s)
	resp, body = get(t, s, c, "GET", path, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET: %d %s", resp.StatusCode, body)
	}
	checkSecurityHeaders(t, resp.Header)
	var got webSessions
	must(t, json.Unmarshal(body, &got))
	if got.At != 1 || len(got.Sessions) != 2 || got.Sessions[0].Key != "box/api" || got.Sessions[0].Dex != 25 ||
		got.Sessions[1].Key != "box/web" || !got.Sessions[1].Done || got.Sessions[1].Idle != 900 {
		t.Errorf("sessions = %+v: want the two well-formed ones only", got)
	}
	if resp, _ := get(t, s, c, "POST", path, ""); resp.StatusCode != 405 {
		t.Errorf("POST: %d, want 405", resp.StatusCode)
	}
	// A snapshot that is not JSON is no sessions.
	must(t, os.WriteFile(filepath.Join(s.state, sessionsFile), []byte("{nope"), 0o600))
	if _, body := get(t, s, c, "GET", path, ""); !strings.Contains(string(body), `"sessions":[]`) {
		t.Errorf("garbage snapshot: %s", body)
	}
}

func TestPick(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	dir := stripState(t, s)
	tok := s.newSession("you@example.com")
	follow := filepath.Join(dir, "4242.follow")
	const ok = `{"id":"4242","key":"box/web"}`

	if resp := pick(t, s, c, tok, "https://gw", ok); resp.StatusCode != 204 {
		t.Fatalf("pick: %d, want 204", resp.StatusCode)
	}
	if b, err := os.ReadFile(follow); err != nil || string(b) != "box/web" {
		t.Fatalf("follow = %q, %v", b, err)
	}
	if fi, _ := os.Stat(follow); fi.Mode().Perm() != 0o600 {
		t.Errorf("follow is %o, want 0600", fi.Mode().Perm())
	}
	os.Remove(follow)

	for _, tc := range []struct {
		name, tok, origin, body string
		want                    int
	}{
		{"no session", "-", "https://gw", ok, 401},
		{"no Origin", tok, "", ok, 403},
		{"other Origin", tok, "https://evil.example", ok, 403},
		{"a session the panel never listed", tok, "https://gw", `{"id":"4242","key":"box/zzz"}`, 404},
		{"a panel of another remote", tok, "https://gw", `{"id":"77","key":"box/web"}`, 404},
		{"a panel that does not exist", tok, "https://gw", `{"id":"1","key":"box/web"}`, 404},
		{"a path as the id", tok, "https://gw", `{"id":"../4242","key":"box/web"}`, 400},
		{"not JSON", tok, "https://gw", `id=4242`, 400},
		{"too big", tok, "https://gw", `{"id":"4242","key":"` + strings.Repeat("x", 5000) + `"}`, 400},
	} {
		if resp := pick(t, s, c, tc.tok, tc.origin, tc.body); resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
		if _, err := os.Stat(follow); err == nil {
			t.Errorf("%s: left a follow file", tc.name)
			os.Remove(follow)
		}
	}
	if resp, _ := get(t, s, c, "GET", testBase+pickPath, tok); resp.StatusCode != 405 {
		t.Errorf("GET: %d, want 405", resp.StatusCode)
	}
	s.state = ""
	if resp := pick(t, s, c, tok, "https://gw", ok); resp.StatusCode != 404 {
		t.Errorf("no state directory: %d, want 404", resp.StatusCode)
	}
}

func TestPokemonArt(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	base := testBase + pokemonPrefix

	if resp, _ := get(t, s, c, "GET", base+"0025/walkR.gif", ""); resp.StatusCode != 404 {
		t.Errorf("without a state directory: %d, want 404", resp.StatusCode)
	}
	stripState(t, s)
	if resp, _ := get(t, s, c, "GET", base+"0025/walkR.gif", "-"); resp.StatusCode != 401 {
		t.Errorf("without a session: %d, want 401", resp.StatusCode)
	}
	resp, body := get(t, s, c, "GET", base+"0025/walkR.gif", "")
	if resp.StatusCode != 200 || string(body) != "GIF89a" || resp.Header.Get("Content-Type") != "image/gif" {
		t.Fatalf("walkR: %d %q %q", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if resp.Header.Get("Content-Security-Policy") != securityHeaders["Content-Security-Policy"] {
		t.Errorf("missing security headers")
	}
	if resp, body := get(t, s, c, "GET", base+"0025/meta.json", ""); resp.StatusCode != 200 || !strings.Contains(string(body), "walkR") ||
		resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("meta.json: %d %s", resp.StatusCode, body)
	}
	for _, rest := range []string{
		"0025/secret.txt", // not on the list
		"0025/sleep.gif",  // a link out of the directory
		"0025/idleR.gif",  // not cut
		"0026/walkR.gif",  // no art for it
		"25/walkR.gif",    // not four digits
		"0000/walkR.gif",  // out of range
		"0152/walkR.gif",  // out of range
		"0025/",           // no file
		"0025/../0025/meta.json",
		"..%2F..%2Fetc/passwd",
		"0025/walkR.gif/extra",
	} {
		if resp, _ := get(t, s, c, "GET", base+rest, ""); resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", rest, resp.StatusCode)
		}
	}
	if resp, _ := get(t, s, c, "POST", base+"0025/walkR.gif", ""); resp.StatusCode != 405 {
		t.Errorf("POST: %d, want 405", resp.StatusCode)
	}
}
