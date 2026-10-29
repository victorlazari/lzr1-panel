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
	base   string
	allow  []string
	pwHash [32]byte
	log    *log.Logger
	proxy  *httputil.ReverseProxy

	mu       sync.Mutex
	sessions map[string]string // session token -> the Tailscale login it was issued to
}

func newServer(c config, pwHash [32]byte, logger *log.Logger) *server {
	s := &server{base: c.base, allow: c.allow, pwHash: pwHash, log: logger, sessions: map[string]string{}}
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
	if !sameOrigin(r) {
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
	if !s.authenticated(r) {
		if wantsHTML(r) {
			http.Redirect(w, r, s.base+"login", http.StatusSeeOther)
			return
		}
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	s.proxy.ServeHTTP(w, r)
}
