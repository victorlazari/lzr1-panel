package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed login.html
var loginHTML string

var loginPage = template.Must(template.New("login").Parse(loginHTML))

// identity returns the caller's allowlisted Tailscale login, or answers 403 and returns false.
// tailscale serve sets Tailscale-User-Login for user-owned devices; tagged devices carry none.
func (s *server) identity(w http.ResponseWriter, r *http.Request) (string, bool) {
	vs := r.Header.Values("Tailscale-User-Login")
	login := ""
	if len(vs) == 1 {
		login = strings.ToLower(strings.TrimSpace(vs[0]))
	}
	switch {
	case len(vs) > 1:
		http.Error(w, "more than one Tailscale identity, not allowed here", http.StatusForbidden)
	case login == "":
		http.Error(w, "no Tailscale identity: open this from a device signed in to Tailscale as an allowed user", http.StatusForbidden)
	case !slices.Contains(s.allow, login):
		s.log.Printf("refused %s: not allowlisted", login)
		http.Error(w, "signed in to Tailscale as "+login+", not allowed here", http.StatusForbidden)
	default:
		return login, true
	}
	return "", false
}

// sameOrigin reports whether r may change state: safe methods without an upgrade always may;
// a POST or a WebSocket upgrade must carry exactly Origin: https://<--host>, or https://<Host> without --host.
func (s *server) sameOrigin(r *http.Request) bool {
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
	if safe && r.Header.Get("Upgrade") == "" {
		return true
	}
	o := r.Header.Values("Origin")
	want := r.Host
	if s.host != "" {
		want = s.host
	}
	return want != "" && len(o) == 1 && o[0] == "https://"+want
}

// newSession issues a session token bound to login.
func (s *server) newSession(login string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newSessionLocked(login)
}

// newSessionLocked is newSession with s.mu held.
func (s *server) newSessionLocked(login string) string {
	b := make([]byte, 32)
	rand.Read(b)
	t := hex.EncodeToString(b)
	s.sessions[t] = login
	return t
}

// session returns r's session token when it is live and bound to the request's Tailscale login.
func (s *server) session(r *http.Request) (string, bool) {
	ck, err := r.Cookie(cookieName)
	if err != nil || ck.Value == "" {
		return "", false
	}
	login := strings.ToLower(strings.TrimSpace(r.Header.Get("Tailscale-User-Login")))
	s.mu.Lock()
	defer s.mu.Unlock()
	bound, ok := s.sessions[ck.Value]
	return ck.Value, ok && login != "" && bound == login
}

// authenticated reports whether r carries a live session issued to the same Tailscale login.
func (s *server) authenticated(r *http.Request) bool {
	_, ok := s.session(r)
	return ok
}

func (s *server) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: value, Path: s.base, MaxAge: maxAge,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

// renderLogin shows the form with msg, or with the lockout message when locked is an "HH:MM".
func (s *server) renderLogin(w http.ResponseWriter, code int, login, msg, locked string) {
	var b bytes.Buffer
	if err := loginPage.Execute(&b, struct{ Login, Error, Locked string }{login, msg, locked}); err != nil {
		http.Error(w, "login page error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(b.Bytes())
}

// lockedAt returns when login's lock ends, if it is locked at now; an expired lock is deleted. Needs s.mu.
func (s *server) lockedAt(login string, now time.Time) (time.Time, bool) {
	until, ok := s.lockedUntil[login]
	if ok && !now.Before(until) {
		delete(s.lockedUntil, login)
		ok = false
	}
	return until, ok
}

// serveLogin: GET shows the form; POST checks the password and starts a session. A login locked out is refused
// without checking the password; every wrong or locked attempt counts toward the kill switch.
func (s *server) serveLogin(w http.ResponseWriter, r *http.Request, login string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.mu.Lock()
		until, locked := s.lockedAt(login, s.now())
		s.mu.Unlock()
		if locked {
			s.renderLogin(w, http.StatusOK, login, "", until.Local().Format("15:04"))
			return
		}
		s.renderLogin(w, http.StatusOK, login, "", "")
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("password")))
	now := s.now()
	s.mu.Lock()
	if s.killed {
		s.mu.Unlock()
		http.Error(w, killedMsg, http.StatusServiceUnavailable)
		return
	}
	until, locked := s.lockedAt(login, now)
	tok := ""
	switch {
	case locked:
		s.total++
		s.audit.event("failure", "user", login, "fails", strconv.Itoa(s.fails[login]), "total", strconv.Itoa(s.total), "locked", "1")
	case subtle.ConstantTimeCompare(sum[:], s.pwHash[:]) != 1:
		s.fails[login]++
		s.total++
		s.audit.event("failure", "user", login, "fails", strconv.Itoa(s.fails[login]), "total", strconv.Itoa(s.total))
		if s.fails[login] >= s.lockAfter {
			until, locked = now.Add(s.lockFor), true
			s.lockedUntil[login] = until
			delete(s.fails, login)
			s.audit.event("lockout", "user", login, "until", until.Format(time.RFC3339))
		}
	default:
		delete(s.fails, login) // the total never resets
		tok = s.newSessionLocked(login)
		s.audit.event("login", "user", login, "sid", sid(tok))
	}
	fired := tok == "" && s.total >= s.killAfter
	if fired {
		s.killed = true
		s.audit.event("shutdown", "reason", "killswitch")
		close(s.kill)
	}
	s.mu.Unlock()
	switch {
	case fired:
		http.Error(w, killedMsg, http.StatusServiceUnavailable)
	case locked:
		s.renderLogin(w, http.StatusTooManyRequests, login, "", until.Local().Format("15:04"))
	case tok == "":
		s.renderLogin(w, http.StatusUnauthorized, login, "Wrong password.", "")
	default:
		http.SetCookie(w, s.cookie(tok, 0))
		http.Redirect(w, r, s.base, http.StatusSeeOther)
	}
}

const killedMsg = "too many wrong passwords: the remote panel was turned off"

// serveLogout ends this request's session (only one bound to the caller) and clears the cookie.
func (s *server) serveLogout(w http.ResponseWriter, r *http.Request, login string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if tok, ok := s.session(r); ok {
		s.mu.Lock()
		delete(s.sessions, tok)
		s.mu.Unlock()
		s.audit.event("logout", "user", login, "sid", sid(tok))
	}
	http.SetCookie(w, s.cookie("", -1))
	http.Redirect(w, r, s.base+"login", http.StatusSeeOther)
}

// wantsHTML reports whether r is a browser navigation, which is sent to the login form.
func wantsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}
