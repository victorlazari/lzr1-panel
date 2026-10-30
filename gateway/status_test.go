package main

import (
	"encoding/json"
	"testing"
	"time"
)

// The phone's Remote tab reads who is signed in, until when the remote runs and its idle limit, behind the same
// identity, Origin and session checks as everything else; nothing it returns changes the remote.
func TestStatus(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	path := testBase + statusPath
	deadline := time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC)
	s.mu.Lock()
	s.deadline = deadline
	s.mu.Unlock()

	for _, tok := range []string{"-", "bogus", s.newSession("other@example.com")} {
		if resp, _ := get(t, s, c, "GET", path, tok); resp.StatusCode != 401 {
			t.Errorf("GET with session %q: %d, want 401", tok, resp.StatusCode)
		}
	}
	if resp, _ := get(t, s, c, "POST", path, ""); resp.StatusCode != 405 {
		t.Errorf("POST: %d, want 405", resp.StatusCode)
	}

	tok := s.newSession("you@example.com")
	s.newSession("you@example.com") // the same login on a second device: listed once
	resp, body := get(t, s, c, "GET", path, tok)
	if resp.StatusCode != 200 {
		t.Fatalf("GET: %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
	checkSecurityHeaders(t, resp.Header)
	var got status
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if got.You != "you@example.com" {
		t.Errorf("you = %q", got.You)
	}
	if len(got.SignedIn) != 2 || got.SignedIn[0] != "other@example.com" || got.SignedIn[1] != "you@example.com" {
		t.Errorf("signedIn = %q, want other and you, once each", got.SignedIn)
	}
	if got.Expires != deadline.Unix() {
		t.Errorf("expires = %d, want %d", got.Expires, deadline.Unix())
	}
	if got.IdleSeconds != int64(s.idle/time.Second) {
		t.Errorf("idleSeconds = %d, want %d", got.IdleSeconds, int64(s.idle/time.Second))
	}
	if n := len(p.headers()); n != 0 {
		t.Errorf("ttyd saw %d requests for the status", n)
	}
}
