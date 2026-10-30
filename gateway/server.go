package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"sync"
	"time"
)

const cookieName = "lzr1_remote"

// Applied to every response, the gateway's own and ttyd's. ttyd's page bootstraps with inline
// script and style; it loads nothing from another origin. Referrer-Policy is same-origin, not no-referrer: with
// no-referrer, browsers send "Origin: null" on the login form's POST and the Origin check refuses it.
var securityHeaders = map[string]string{
	"Cache-Control":           "no-store",
	"X-Frame-Options":         "DENY",
	"Referrer-Policy":         "same-origin",
	"X-Content-Type-Options":  "nosniff",
	"Content-Security-Policy": "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' wss:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
}

func setSecurityHeaders(h http.Header) {
	for k, v := range securityHeaders {
		h.Set(k, v)
	}
}

type server struct {
	base     string
	host     string
	stripped bool
	allow    []string
	pwHash   [32]byte
	log      *log.Logger
	proxy    *httputil.ReverseProxy

	audit *auditLog        // nil without --audit
	now   func() time.Time // tests move it

	// Lockout: lockAfter wrong passwords in a row from one login lock it for lockFor; killAfter wrong passwords in
	// total (attempts while locked included) fire the kill switch: kill is closed and the gateway exits 3.
	lockAfter, killAfter int
	lockFor              time.Duration
	kill                 chan struct{}
	idle                 time.Duration // a session with no input for longer is ended (idle.go)

	mu          sync.Mutex
	sessions    map[string]*liveSession // session token -> its login, last input and WebSockets
	fails       map[string]int          // wrong passwords in a row, per login
	lockedUntil map[string]time.Time
	total       int  // wrong passwords and locked attempts, never reset
	killed      bool // kill is closed
	closing     bool // closeAll ran: a WebSocket registered now is closed at once
}

// The lockout and kill switch limits decided for Epic 2.1; config fields left at zero get these.
const (
	defaultLockAfter = 5
	defaultLockFor   = 15 * time.Minute
	defaultKillAfter = 20
)

func newServer(c config, pwHash [32]byte, logger *log.Logger) *server {
	s := &server{base: c.base, host: c.host, stripped: c.stripped, allow: c.allow, pwHash: pwHash, log: logger,
		now: time.Now, lockAfter: c.lockAfter, lockFor: c.lockFor, killAfter: c.killAfter, kill: make(chan struct{}),
		idle: c.idle, sessions: map[string]*liveSession{}, fails: map[string]int{}, lockedUntil: map[string]time.Time{}}
	if s.lockAfter <= 0 {
		s.lockAfter = defaultLockAfter
	}
	if s.lockFor <= 0 {
		s.lockFor = defaultLockFor
	}
	if s.killAfter <= 0 {
		s.killAfter = defaultKillAfter
	}
	if s.idle <= 0 {
		s.idle = defaultIdle
	}
	upstream := c.upstream
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "ttyd"
			for k := range pr.Out.Header {
				if strings.HasPrefix(k, "Tailscale-") {
					pr.Out.Header.Del(k)
				}
			}
			pr.Out.Header.Del("Cookie")
			for _, ck := range pr.In.Cookies() {
				if ck.Name != cookieName {
					pr.Out.AddCookie(ck)
				}
			}
			if s.isPage(pr.In) {
				// ttyd's index gets lzr1-client.js (inject.go): ask for it plain and whole.
				pr.Out.Header.Del("Accept-Encoding")
				pr.Out.Header.Del("If-None-Match")
				pr.Out.Header.Del("If-Modified-Since")
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", upstream)
			},
			DisableCompression: true,
		},
		FlushInterval: -1,
		ModifyResponse: func(r *http.Response) error {
			setSecurityHeaders(r.Header)
			if s.isPage(r.Request) {
				return s.injectClient(r)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.Printf("upstream: %v", err)
			http.Error(w, "the terminal is not answering (ttyd still starting or stopped)", http.StatusBadGateway)
		},
		ErrorLog: logger,
	}
	return s
}

// underBase reports whether p, once cleaned, is the base itself or below it.
func (s *server) underBase(p string) bool {
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c == p && strings.HasPrefix(p, s.base)
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if s.stripped {
		// Only serve reaches the 0600 socket, and it strips the mount path: put it back. "/../x" becomes
		// "/UUID/../x", which underBase refuses.
		if r.URL.Path != "" && !strings.HasPrefix(r.URL.Path, "/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		r.URL.Path = strings.TrimSuffix(s.base, "/") + r.URL.Path
		if r.URL.Path == strings.TrimSuffix(s.base, "/") {
			r.URL.Path = s.base
		}
	}
	if r.URL.Path == strings.TrimSuffix(s.base, "/") {
		http.Redirect(w, r, s.base, http.StatusPermanentRedirect)
		return
	}
	if r.URL.RawPath != "" || !s.underBase(r.URL.Path) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	login, ok := s.identity(w, r)
	if !ok {
		return
	}
	if !s.sameOrigin(r) {
		s.log.Printf("refused %s %s for %s: Origin %q", r.Method, r.URL.Path, login, r.Header.Get("Origin"))
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case s.base + "login":
		s.serveLogin(w, r, login)
		return
	case s.base + "logout":
		s.serveLogout(w, r, login)
		return
	}
	tok, ok := s.session(r)
	if !ok {
		if wantsHTML(r) {
			http.Redirect(w, r, s.base+"login", http.StatusSeeOther)
			return
		}
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == s.base+clientPath {
		serveClient(w, r)
		return
	}
	if r.Header.Get("Upgrade") != "" {
		// The terminal's WebSocket: its input keeps the session alive, and it ends with the session.
		w = &upgradeWriter{ResponseWriter: w, s: s, tok: tok}
	}
	s.proxy.ServeHTTP(w, r)
}
