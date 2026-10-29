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
	"strings"
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
// a POST or a WebSocket upgrade must carry exactly Origin: https://<Host>.
func sameOrigin(r *http.Request) bool {
	safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
	if safe && r.Header.Get("Upgrade") == "" {
		return true
	}
	o := r.Header.Values("Origin")
	return r.Host != "" && len(o) == 1 && o[0] == "https://"+r.Host
}

// newSession issues a session token bound to login.
func (s *server) newSession(login string) string {
	b := make([]byte, 32)
	rand.Read(b)
	t := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[t] = login
	s.mu.Unlock()
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

func (s *server) renderLogin(w http.ResponseWriter, code int, login, msg string) {
	var b bytes.Buffer
	if err := loginPage.Execute(&b, struct{ Login, Error string }{login, msg}); err != nil {
		http.Error(w, "login page error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	w.Write(b.Bytes())
}

// serveLogin: GET shows the form; POST checks the password and starts a session.
func (s *server) serveLogin(w http.ResponseWriter, r *http.Request, login string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.renderLogin(w, http.StatusOK, login, "")
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
	if subtle.ConstantTimeCompare(sum[:], s.pwHash[:]) != 1 {
		s.log.Printf("login failed: %s", login)
		s.renderLogin(w, http.StatusUnauthorized, login, "Wrong password.")
		return
	}
	s.log.Printf("login: %s", login)
	http.SetCookie(w, s.cookie(s.newSession(login), 0))
	http.Redirect(w, r, s.base, http.StatusSeeOther)
}

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
		s.log.Printf("logout: %s", login)
	}
	http.SetCookie(w, s.cookie("", -1))
	http.Redirect(w, r, s.base+"login", http.StatusSeeOther)
}

// wantsHTML reports whether r is a browser navigation, which is sent to the login form.
func wantsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" &&
		strings.Contains(r.Header.Get("Accept"), "text/html")
}
