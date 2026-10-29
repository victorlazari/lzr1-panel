package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const ttydPage = `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>ttyd</title></head>` +
	`<body><script>window.term=1</script></body></html>`

// pageTTYD answers every request with its fields and records the requests' headers.
type pageTTYD struct {
	status int
	ctype  string
	body   []byte
	gzip   bool

	mu   sync.Mutex
	seen []http.Header
}

func (p *pageTTYD) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.seen = append(p.seen, r.Header.Clone())
	p.mu.Unlock()
	body := p.body
	if p.gzip {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		zw.Write(body)
		zw.Close()
		body = b.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Type", p.ctype)
	w.Header().Set("ETag", `"ttyd-1"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(p.status)
	w.Write(body)
}

func (p *pageTTYD) headers() []http.Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]http.Header(nil), p.seen...)
}

func servePage(t *testing.T, p *pageTTYD) (*server, *http.Client) {
	t.Helper()
	dir := sockDir(t)
	up := filepath.Join(dir, "ttyd.sock")
	l, err := net.Listen("unix", up)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: p}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	s, client, _ := startGateway(t, dir, up)
	return s, client
}

// get sends method to path with a session for you@example.com (when tok is "", a fresh one) and returns the answer.
func get(t *testing.T, s *server, c *http.Client, method, path, tok string) (*http.Response, []byte) {
	t.Helper()
	if tok == "" {
		tok = s.newSession("you@example.com")
	}
	r, _ := http.NewRequest(method, "http://gw"+path, nil)
	r.Header.Set("Tailscale-User-Login", "you@example.com")
	r.Header.Set("Origin", "https://gw")
	r.Header.Set("Accept", "*/*")                        // as a script load or fetch sends it
	r.Header.Set("Accept-Encoding", "gzip, deflate, br") // set by hand: the client hands back the raw body
	r.Header.Set("If-None-Match", `"ttyd-1"`)
	if tok != "-" {
		r.Header.Set("Cookie", cookieName+"="+tok)
	}
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, b
}

func checkInjected(t *testing.T, resp *http.Response, body []byte, want string) {
	t.Helper()
	if resp.StatusCode != 200 || string(body) != want {
		t.Fatalf("got %d %q, want %q", resp.StatusCode, body, want)
	}
	if n := strings.Count(string(body), clientTag); n != 1 {
		t.Errorf("tag inserted %d times", n)
	}
	if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) || resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length %q (%d), body %d bytes", cl, resp.ContentLength, len(body))
	}
	if resp.Header.Get("ETag") != "" || resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("ETag %q, Content-Encoding %q left", resp.Header.Get("ETag"), resp.Header.Get("Content-Encoding"))
	}
	checkSecurityHeaders(t, resp.Header)
}

func TestInjectPage(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	resp, body := get(t, s, c, "GET", testBase, "")
	want := strings.Replace(ttydPage, "<head>", "<head>"+clientTag, 1)
	checkInjected(t, resp, body, want)
	h := p.headers()[0]
	for _, k := range []string{"Accept-Encoding", "If-None-Match"} {
		if h.Get(k) != "" {
			t.Errorf("%s reached ttyd for the page: %q", k, h.Get(k))
		}
	}
}

func TestInjectGzip(t *testing.T) {
	// A ttyd that gzips although the gateway asked for it plain: decoded, then sent plain.
	p := &pageTTYD{status: 200, ctype: "text/html; charset=utf-8", body: []byte(ttydPage), gzip: true}
	s, c := servePage(t, p)
	resp, body := get(t, s, c, "GET", testBase, "")
	checkInjected(t, resp, body, strings.Replace(ttydPage, "<head>", "<head>"+clientTag, 1))
}

func TestInjectUntouched(t *testing.T) {
	cases := []struct {
		name, method, path string
		p                  *pageTTYD
	}{
		{"not HTML", "GET", testBase, &pageTTYD{status: 200, ctype: "application/json", body: []byte(`{"a":"<head>"}`)}},
		{"not 200", "GET", testBase, &pageTTYD{status: 404, ctype: "text/html", body: []byte(ttydPage)}},
		{"not the index", "GET", testBase + "other", &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}},
		{"HEAD", "HEAD", testBase, &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c := servePage(t, tc.p)
			resp, body := get(t, s, c, tc.method, tc.path, "")
			want := string(tc.p.body)
			if tc.method == "HEAD" {
				want = ""
			}
			if resp.StatusCode != tc.p.status || string(body) != want {
				t.Fatalf("got %d %q", resp.StatusCode, body)
			}
			if resp.Header.Get("ETag") == "" {
				t.Error("ETag removed from an answer left alone")
			}
			if tc.path != testBase {
				if ae := tc.p.headers()[0].Get("Accept-Encoding"); ae == "" {
					t.Error("Accept-Encoding dropped for a path other than the page")
				}
			}
		})
	}
}

func TestInjectTooBig(t *testing.T) {
	big := append([]byte("<html><head>"), bytes.Repeat([]byte("x"), maxPage)...)
	for _, gz := range []bool{false, true} {
		p := &pageTTYD{status: 200, ctype: "text/html", body: big, gzip: gz}
		s, c := servePage(t, p)
		resp, body := get(t, s, c, "GET", testBase, "")
		if resp.StatusCode != 200 || !bytes.Equal(body, big) {
			t.Fatalf("gzip=%v: got %d, %d bytes, want the page unchanged (%d bytes)", gz, resp.StatusCode, len(body), len(big))
		}
		if resp.Header.Get("Content-Encoding") != "" {
			t.Errorf("gzip=%v: Content-Encoding %q", gz, resp.Header.Get("Content-Encoding"))
		}
	}
}

func TestInsertTag(t *testing.T) {
	cases := map[string]string{
		"<html><head><title>x</title></head></html>":   "<html><head>" + clientTag + "<title>x</title></head></html>",
		`<HTML><HEAD lang="en"><title></title></HEAD>`: `<HTML><HEAD lang="en">` + clientTag + "<title></title></HEAD>",
		"<html><header>no head</header></html>":        clientTag + "<html><header>no head</header></html>",
		"<header></header><head></head>":               "<header></header><head>" + clientTag + "</head>",
		"<head><head>":                                 "<head>" + clientTag + "<head>",
		"plain text":                                   clientTag + "plain text",
		"":                                             clientTag,
		"<head":                                        clientTag + "<head",
	}
	for in, want := range cases {
		if got := string(insertTag([]byte(in))); got != want {
			t.Errorf("insertTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClientScript(t *testing.T) {
	p := &pageTTYD{status: 200, ctype: "text/html", body: []byte(ttydPage)}
	s, c := servePage(t, p)
	path := testBase + clientPath

	// Without a session: 401, never the login redirect (the page's script reads it), and never ttyd.
	for _, m := range []string{"GET", "HEAD"} {
		for _, tok := range []string{"-", "bogus"} {
			resp, _ := get(t, s, c, m, path, tok)
			if resp.StatusCode != 401 {
				t.Errorf("%s without a session (%s): %d, want 401", m, tok, resp.StatusCode)
			}
		}
	}
	// The session of another login does not do either.
	other := s.newSession("other@example.com")
	if resp, _ := get(t, s, c, "GET", path, other); resp.StatusCode != 401 {
		t.Errorf("another login's session: %d", resp.StatusCode)
	}

	resp, body := get(t, s, c, "GET", path, "")
	if resp.StatusCode != 200 || !bytes.Equal(body, clientJS) {
		t.Fatalf("GET: %d, %d bytes", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type %q", ct)
	}
	checkSecurityHeaders(t, resp.Header)
	resp, body = get(t, s, c, "HEAD", path, "")
	if resp.StatusCode != 200 || len(body) != 0 || resp.Header.Get("Content-Length") != strconv.Itoa(len(clientJS)) {
		t.Errorf("HEAD: %d, %d bytes, Content-Length %q", resp.StatusCode, len(body), resp.Header.Get("Content-Length"))
	}
	if resp, _ := get(t, s, c, "POST", path, ""); resp.StatusCode != 405 {
		t.Errorf("POST: %d, want 405", resp.StatusCode)
	}
	if n := len(p.headers()); n != 0 {
		t.Errorf("ttyd saw %d requests for the script", n)
	}
	for _, want := range []string{"registerOscHandler(52", "macOptionClickForcesSelection", "contextmenu", "'login'"} {
		if !bytes.Contains(clientJS, []byte(want)) {
			t.Errorf("client.js has no %q", want)
		}
	}
}
