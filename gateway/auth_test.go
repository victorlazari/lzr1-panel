package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPassword = "correct horse battery"

// req builds a request to the gateway as Tailscale serve would send it: login is the
// Tailscale-User-Login header ("" for none), origin the Origin header ("" for none).
func req(method, path, login, origin, cookie string, form url.Values) *http.Request {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, _ := http.NewRequest(method, "http://gw"+path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if login != "" {
		r.Header.Set("Tailscale-User-Login", login)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookieName+"="+cookie)
	}
	return r
}

func do(t *testing.T, c *http.Client, r *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == cookieName {
			return ck
		}
	}
	return nil
}

// login posts the password as login and returns the session token ("" when none was set).
func login(t *testing.T, c *http.Client, who, pw string) (*http.Response, string) {
	t.Helper()
	resp, _ := do(t, c, req("POST", testBase+"login", who, "https://gw", "", url.Values{"password": {pw}}))
	if ck := sessionCookie(resp); ck != nil {
		return resp, ck.Value
	}
	return resp, ""
}

// upgrade sends a raw WebSocket upgrade and returns the status.
func upgrade(t *testing.T, sock, who, origin, tok string) int {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	h := "GET " + testBase + "ws HTTP/1.1\r\nHost: gw\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n"
	if who != "" {
		h += "Tailscale-User-Login: " + who + "\r\n"
	}
	if origin != "" {
		h += "Origin: " + origin + "\r\n"
	}
	if tok != "" {
		h += "Cookie: " + cookieName + "=" + tok + "\r\n"
	}
	io.WriteString(conn, h+"\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode
}

func TestAuthIdentity(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	s, client, sock := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	tok := s.newSession("you@example.com")

	for _, p := range []string{testBase, testBase + "login", testBase + "token"} {
		resp, body := do(t, client, req("GET", p, "", "", tok, nil))
		if resp.StatusCode != 403 || !strings.Contains(body, "no Tailscale identity") {
			t.Errorf("%s without identity: %d %q", p, resp.StatusCode, body)
		}
		checkSecurityHeaders(t, resp.Header)
		resp, body = do(t, client, req("GET", p, "eve@example.com", "", tok, nil))
		if resp.StatusCode != 403 || !strings.Contains(body, "signed in to Tailscale as eve@example.com, not allowed here") {
			t.Errorf("%s as eve: %d %q", p, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("403 Content-Type %q", ct)
		}
	}
	// Two identity headers: ambiguous, refused.
	r := req("GET", testBase, "you@example.com", "", tok, nil)
	r.Header.Add("Tailscale-User-Login", "eve@example.com")
	if resp, _ := do(t, client, r); resp.StatusCode != 403 {
		t.Errorf("two identities: %d", resp.StatusCode)
	}
	// Case and spaces do not matter.
	if resp, _ := do(t, client, req("GET", testBase, "  YOU@Example.COM ", "", tok, nil)); resp.StatusCode != 200 {
		t.Errorf("allowlisted login, other case: %d", resp.StatusCode)
	}
	// WebSocket upgrades check identity too.
	if code := upgrade(t, sock, "", "https://gw", tok); code != 403 {
		t.Errorf("upgrade without identity: %d", code)
	}
	if code := upgrade(t, sock, "eve@example.com", "https://gw", tok); code != 403 {
		t.Errorf("upgrade as eve: %d", code)
	}
	if n := len(up.hits()); n != 1 {
		t.Errorf("upstream saw %d requests, want 1", n)
	}
}

func TestAuthUnauthenticated(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	_, client, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))

	r := req("GET", testBase, "you@example.com", "", "", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	resp, _ := do(t, client, r)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != testBase+"login" {
		t.Errorf("browser: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := do(t, client, req("GET", testBase+"token", "you@example.com", "", "bogus", nil)); resp.StatusCode != 401 {
		t.Errorf("non-browser: %d", resp.StatusCode)
	}
	if n := len(up.hits()); n != 0 {
		t.Errorf("upstream saw %d requests", n)
	}
}

func TestLoginForm(t *testing.T) {
	dir := sockDir(t)
	_, client, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	resp, body := do(t, client, req("GET", testBase+"login", "you@example.com", "", "", nil))
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("form: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, want := range []string{`method="post"`, `name="password"`, `type="password"`, "you@example.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	for _, bad := range []string{"http://", "https://", "src="} {
		if strings.Contains(body, bad) {
			t.Errorf("form references external content: %s", bad)
		}
	}
	checkSecurityHeaders(t, resp.Header)
	// no-referrer makes Chrome send "Origin: null" on the form's POST, which the Origin check refuses: login never
	// works. same-origin keeps the Origin and still sends no Referer to another site.
	if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("Referrer-Policy = %q, want same-origin", got)
	}
	if strings.Contains(body, "no-referrer") {
		t.Errorf("form sets no-referrer, which makes its POST carry Origin: null")
	}
	if resp, _ := do(t, client, req("PUT", testBase+"login", "you@example.com", "https://gw", "", nil)); resp.StatusCode != 405 {
		t.Errorf("PUT login: %d", resp.StatusCode)
	}
}

func TestLoginPassword(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	_, client, _ := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))

	for _, pw := range []string{"", "wrong password!", testPassword + " ", strings.ToUpper(testPassword)} {
		resp, tok := login(t, client, "you@example.com", pw)
		if resp.StatusCode != 401 || tok != "" || len(resp.Header.Values("Set-Cookie")) != 0 {
			t.Errorf("password %q: %d, cookie %q", pw, resp.StatusCode, resp.Header.Values("Set-Cookie"))
		}
	}
	// The right password from someone not allowlisted still gets nothing.
	if resp, tok := login(t, client, "eve@example.com", testPassword); resp.StatusCode != 403 || tok != "" {
		t.Errorf("eve with the password: %d %q", resp.StatusCode, tok)
	}

	resp, tok := login(t, client, "you@example.com", testPassword)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != testBase {
		t.Fatalf("login: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(tok) != 64 {
		t.Fatalf("token %q, want 64 hex characters", tok)
	}
	raw := strings.Join(resp.Header.Values("Set-Cookie"), "\n")
	for _, attr := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=" + testBase} {
		if !strings.Contains(raw, attr) {
			t.Errorf("cookie %q lacks %s", raw, attr)
		}
	}
	for _, attr := range []string{"Expires", "Max-Age", "Domain"} {
		if strings.Contains(raw, attr) {
			t.Errorf("cookie %q has %s", raw, attr)
		}
	}
	checkSecurityHeaders(t, resp.Header)

	if resp, body := do(t, client, req("GET", testBase, "you@example.com", "", tok, nil)); resp.StatusCode != 200 || body != "terminal "+testBase {
		t.Errorf("with the cookie: %d %q", resp.StatusCode, body)
	}
	// Every login gets its own token.
	if _, tok2 := login(t, client, "you@example.com", testPassword); tok2 == "" || tok2 == tok {
		t.Errorf("second login token %q", tok2)
	}
	if n := len(up.hits()); n != 1 {
		t.Errorf("upstream saw %d requests, want 1", n)
	}
}

func TestLoginBinding(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	sock := filepath.Join(dir, "gw.sock")
	l, err := listenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	c := config{listen: sock, upstream: filepath.Join(dir, "ttyd.sock"), base: testBase, allow: []string{"you@example.com", "pat@example.com"}}
	srv := &http.Server{Handler: newServer(c, sha256.Sum256([]byte(testPassword)), log.New(io.Discard, "", 0))}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	client := &http.Client{
		Transport:     &http.Transport{Dial: func(_, _ string) (net.Conn, error) { return net.Dial("unix", sock) }},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	_, you := login(t, client, "you@example.com", testPassword)
	_, phone := login(t, client, "you@example.com", testPassword)
	_, pat := login(t, client, "pat@example.com", testPassword)
	if you == "" || phone == "" || pat == "" {
		t.Fatal("a login failed")
	}
	ok := func(who, tok string) int {
		resp, _ := do(t, client, req("GET", testBase, who, "", tok, nil))
		return resp.StatusCode
	}
	// Two allowed users at once, the same user on two devices: all valid.
	for _, p := range [][2]string{{"you@example.com", you}, {"you@example.com", phone}, {"pat@example.com", pat}} {
		if code := ok(p[0], p[1]); code != 200 {
			t.Errorf("%s: %d", p[0], code)
		}
	}
	// A cookie replayed by another (even allowlisted) tailnet user is refused.
	if code := ok("pat@example.com", you); code != 401 {
		t.Errorf("you's cookie as pat: %d", code)
	}
	if code := ok("you@example.com", pat); code != 401 {
		t.Errorf("pat's cookie as you: %d", code)
	}
	if code := upgrade(t, sock, "pat@example.com", "https://gw", you); code != 401 {
		t.Errorf("you's cookie upgraded as pat: %d", code)
	}
	if code := upgrade(t, sock, "you@example.com", "https://gw", you); code != 101 {
		t.Errorf("your own upgrade: %d", code)
	}
	if n := len(up.hits()); n != 4 {
		t.Errorf("upstream saw %d requests, want 4", n)
	}
}

func TestOrigin(t *testing.T) {
	dir := sockDir(t)
	up := startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	_, client, sock := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	_, tok := login(t, client, "you@example.com", testPassword)
	if tok == "" {
		t.Fatal("login failed")
	}
	foreign := []string{"", "null", "https://evil.example", "http://gw", "https://gw:444", "https://gw.evil", "https://GW/"}
	for _, o := range foreign {
		resp, _ := do(t, client, req("POST", testBase+"login", "you@example.com", o, "", url.Values{"password": {testPassword}}))
		if resp.StatusCode != 403 || sessionCookie(resp) != nil {
			t.Errorf("login with Origin %q: %d", o, resp.StatusCode)
		}
		if code := upgrade(t, sock, "you@example.com", o, tok); code != 403 {
			t.Errorf("upgrade with Origin %q: %d", o, code)
		}
		if resp, _ := do(t, client, req("POST", testBase+"token", "you@example.com", o, tok, url.Values{})); resp.StatusCode != 403 {
			t.Errorf("proxied POST with Origin %q: %d", o, resp.StatusCode)
		}
		if resp, _ := do(t, client, req("POST", testBase+"logout", "you@example.com", o, tok, url.Values{})); resp.StatusCode != 403 {
			t.Errorf("logout with Origin %q: %d", o, resp.StatusCode)
		}
	}
	if n := len(up.hits()); n != 0 {
		t.Fatalf("upstream saw %d requests with a foreign Origin", n)
	}
	if code := upgrade(t, sock, "you@example.com", "https://gw", tok); code != 101 {
		t.Errorf("same-origin upgrade: %d", code)
	}
	if resp, _ := do(t, client, req("GET", testBase, "you@example.com", "", tok, nil)); resp.StatusCode != 200 {
		t.Errorf("the session died from foreign logouts: %d", resp.StatusCode)
	}
}

func TestLoginLogout(t *testing.T) {
	dir := sockDir(t)
	startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	_, client, sock := startGateway(t, dir, filepath.Join(dir, "ttyd.sock"))
	_, tok := login(t, client, "you@example.com", testPassword)
	_, other := login(t, client, "you@example.com", testPassword)

	resp, _ := do(t, client, req("POST", testBase+"logout", "you@example.com", "https://gw", tok, url.Values{}))
	if resp.StatusCode != 303 || resp.Header.Get("Location") != testBase+"login" {
		t.Errorf("logout: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if ck := sessionCookie(resp); ck == nil || ck.Value != "" || ck.MaxAge >= 0 || ck.Path != testBase {
		t.Errorf("logout cookie %+v", ck)
	}
	if resp, _ := do(t, client, req("GET", testBase, "you@example.com", "", tok, nil)); resp.StatusCode != 401 {
		t.Errorf("after logout: %d", resp.StatusCode)
	}
	if code := upgrade(t, sock, "you@example.com", "https://gw", tok); code != 401 {
		t.Errorf("upgrade after logout: %d", code)
	}
	if resp, _ := do(t, client, req("GET", testBase, "you@example.com", "", other, nil)); resp.StatusCode != 200 {
		t.Errorf("the other device's session died: %d", resp.StatusCode)
	}
	if resp, _ := do(t, client, req("GET", testBase+"logout", "you@example.com", "", other, nil)); resp.StatusCode != 405 {
		t.Errorf("GET logout: %d", resp.StatusCode)
	}
}

// fakeClock is a server's now that tests move by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// syncBuf is an audit sink tests can read while the server writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// events returns the event names of an audit text, in order.
func events(text string) []string {
	var ev []string
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if f := strings.Fields(l); len(f) > 1 {
			ev = append(ev, f[1])
		}
	}
	return ev
}

// lockGateway starts a gateway for a@x and b@x with a fake clock and an audit buffer.
func lockGateway(t *testing.T) (*server, *http.Client, *fakeClock, *syncBuf) {
	t.Helper()
	dir := sockDir(t)
	startUpstream(t, filepath.Join(dir, "ttyd.sock"))
	clk := &fakeClock{t: time.Date(2026, 10, 29, 14, 0, 0, 0, time.Local)}
	buf := &syncBuf{}
	s, client, _ := startGatewayWith(t, dir, filepath.Join(dir, "ttyd.sock"), []string{"a@x", "b@x"}, func(s *server) {
		s.now = clk.now
		s.audit = &auditLog{w: buf, now: clk.now, log: s.log}
	})
	return s, client, clk, buf
}

func TestLockout(t *testing.T) {
	s, client, clk, buf := lockGateway(t)
	lockEnd := clk.now().Add(15 * time.Minute)
	until := lockEnd.Format("15:04")
	for i := 1; i <= 5; i++ {
		resp, tok := login(t, client, "a@x", "wrong password "+strconv.Itoa(i))
		want := 401
		if i == 5 {
			want = 429
		}
		if resp.StatusCode != want || tok != "" {
			t.Fatalf("wrong password %d: %d %q", i, resp.StatusCode, tok)
		}
	}
	// Locked: even the right password is refused, unchecked.
	resp, body := do(t, client, req("POST", testBase+"login", "a@x", "https://gw", "", url.Values{"password": {testPassword}}))
	if resp.StatusCode != 429 || sessionCookie(resp) != nil || !strings.Contains(body, "Too many wrong passwords: locked until "+until+".") {
		t.Fatalf("right password while locked: %d %q", resp.StatusCode, body)
	}
	if resp, body := do(t, client, req("GET", testBase+"login", "a@x", "", "", nil)); resp.StatusCode != 200 || !strings.Contains(body, "locked until "+until) {
		t.Errorf("form while locked: %d %q", resp.StatusCode, body)
	}
	// Another login is not locked.
	if resp, tok := login(t, client, "b@x", testPassword); resp.StatusCode != 303 || tok == "" {
		t.Fatalf("b@x: %d", resp.StatusCode)
	}
	clk.add(14 * time.Minute)
	if resp, _ := login(t, client, "a@x", testPassword); resp.StatusCode != 429 {
		t.Fatalf("a@x after 14 min: %d", resp.StatusCode)
	}
	clk.add(time.Minute)
	resp, tok := login(t, client, "a@x", testPassword)
	if resp.StatusCode != 303 || tok == "" {
		t.Fatalf("a@x after 15 min: %d", resp.StatusCode)
	}
	s.mu.Lock()
	fails, total := s.fails["a@x"], s.total
	s.mu.Unlock()
	if fails != 0 || total != 7 {
		t.Errorf("after the lock: fails %d total %d, want 0 7", fails, total)
	}
	// The counter is back to 0: four more wrong passwords do not lock.
	for i := 0; i < 4; i++ {
		if resp, _ := login(t, client, "a@x", "wrong again"); resp.StatusCode != 401 {
			t.Errorf("fresh wrong password %d: %d", i+1, resp.StatusCode)
		}
	}
	want := []string{"failure", "failure", "failure", "failure", "failure", "lockout", "failure", "login", "failure", "login",
		"failure", "failure", "failure", "failure"}
	if got := events(buf.String()); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("events\n got %v\nwant %v", got, want)
	}
	text := buf.String()
	for _, w := range []string{"failure user=a@x fails=5 total=5\n", "lockout user=a@x until=" + lockEnd.Format(time.RFC3339) + "\n",
		"failure user=a@x fails=0 total=6 locked=1\n", "login user=b@x sid=", "failure user=a@x fails=1 total=8\n"} {
		if !strings.Contains(text, w) {
			t.Errorf("audit lacks %q:\n%s", w, text)
		}
	}
	if strings.Contains(text, testPassword) || strings.Contains(text, tok) {
		t.Errorf("audit holds a secret:\n%s", text)
	}
}

func TestKillSwitch(t *testing.T) {
	s, client, _, buf := lockGateway(t)
	for i := 1; i <= 19; i++ {
		who := "a@x"
		if i%2 == 0 {
			who = "b@x"
		}
		resp, _ := login(t, client, who, "wrong password")
		if resp.StatusCode == 503 {
			t.Fatalf("attempt %d: 503 before 20", i)
		}
	}
	select {
	case <-s.kill:
		t.Fatal("kill switch before 20")
	default:
	}
	// The 20th attempt is the right password, but a@x is locked: it counts, and the switch fires.
	resp, body := do(t, client, req("POST", testBase+"login", "a@x", "https://gw", "", url.Values{"password": {testPassword}}))
	if resp.StatusCode != 503 || sessionCookie(resp) != nil || !strings.Contains(body, "too many wrong passwords: the remote panel was turned off") {
		t.Fatalf("20th attempt: %d %q", resp.StatusCode, body)
	}
	select {
	case <-s.kill:
	case <-time.After(time.Second):
		t.Fatal("kill not closed")
	}
	// Every later POST gets 503, the right password from an unlocked login included.
	for _, who := range []string{"b@x", "a@x"} {
		if resp, tok := login(t, client, who, testPassword); resp.StatusCode != 503 || tok != "" {
			t.Errorf("%s after the kill switch: %d", who, resp.StatusCode)
		}
	}
	ev := events(buf.String())
	if n := strings.Count(buf.String(), "shutdown reason=killswitch\n"); n != 1 || ev[len(ev)-1] != "shutdown" {
		t.Errorf("shutdown lines %d, last event %q", n, ev[len(ev)-1])
	}
	if n := strings.Count(buf.String(), " failure "); n != 20 {
		t.Errorf("%d failures audited, want 20", n)
	}
}

func TestKillSwitchConcurrent(t *testing.T) {
	s, client, _, buf := lockGateway(t)
	var wg sync.WaitGroup
	codes := make(chan int, 60)
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			who := []string{"a@x", "b@x"}[i%2]
			r := req("POST", testBase+"login", who, "https://gw", "", url.Values{"password": {"wrong password"}})
			resp, err := client.Do(r)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
			codes <- resp.StatusCode
		}(i)
	}
	wg.Wait()
	close(codes)
	n503 := 0
	for c := range codes {
		if c == 503 {
			n503++
		}
	}
	if n503 != 41 {
		t.Errorf("%d answers were 503, want 41 (the 20th and every later one)", n503)
	}
	<-s.kill
	if n := strings.Count(buf.String(), "shutdown reason=killswitch\n"); n != 1 {
		t.Errorf("%d shutdown lines", n)
	}
	if n := strings.Count(buf.String(), " failure "); n != 20 {
		t.Errorf("%d failures audited, want 20", n)
	}
}
