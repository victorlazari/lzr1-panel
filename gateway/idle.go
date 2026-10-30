package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Idle logout. A session is active while a person uses the browser: any authenticated HTTP request, or a ttyd
// message on a WebSocket that a person sent. ttyd's client starts each message with a command byte: INPUT ('0', keys,
// pastes, mouse) and RESIZE ('1') count; PAUSE ('2') and RESUME ('3'), which ttyd's client sends by itself for flow
// control as output arrives, and the first JSON message ('{') do not. Neither does an INPUT message made only of
// terminal reports, which xterm.js sends by itself when a program queries the terminal (see reportsOnly). ttyd pings
// the browser every few seconds and the browser answers with pongs: pings, pongs and closes do not count either. A
// session idle for longer than --idle, on the wall clock, is deleted and its WebSockets (its terminals) are closed.

const defaultIdle = 30 * time.Minute

// liveSession is a logged-in browser: the login it was issued to, its last input (a wall-clock time), and its proxied
// WebSockets.
type liveSession struct {
	login string
	last  time.Time
	conns map[*activityConn]struct{}
}

// maxReport is the most of an INPUT message kept to tell terminal reports from input; a longer one is input.
const maxReport = 256

// Kinds of message, by their first byte.
const (
	msgFirst   = iota // the first payload byte is still to come
	msgInput          // INPUT: counted at its end unless it holds only terminal reports
	msgCounted        // counted already (RESIZE, or INPUT past maxReport)
	msgIgnored        // anything else
)

// frameParser follows the RFC 6455 frames a browser sends, across reads, and tells which ttyd messages a person sent.
// It unmasks only what it looks at (the first byte of each message, and up to maxReport bytes of INPUT), never
// copies anything back or changes the stream; a malformed header stops it for good (the upstream decides what to do).
type frameParser struct {
	state   int    // 0: FIN/opcode byte, 1: mask bit and length, 2: extended length, 3: mask key, 4: payload
	fin     bool   // the current frame ends its message
	op      byte   // the current frame's opcode
	ext     int    // extended length bytes still to read (2 or 8, counting down)
	length  uint64 // the payload length, then what is left of it
	key     [4]byte
	mask    int    // mask key bytes read
	pos     uint64 // payload bytes read in this frame (the mask key index)
	msg     int    // the current data message's kind
	buf     []byte // an INPUT message's bytes after its command byte, up to maxReport
	invalid bool
}

// feed parses b and returns how many ttyd messages that a person sent it saw counted.
func (p *frameParser) feed(b []byte) int {
	n := 0
	for len(b) > 0 && !p.invalid {
		switch p.state {
		case 0:
			p.fin, p.op = b[0]&0x80 != 0, b[0]&0x0f
			if p.op > 2 && p.op < 8 || p.op > 10 {
				p.invalid = true
				return n
			}
			p.state = 1
			b = b[1:]
		case 1:
			if b[0]&0x80 == 0 { // a browser masks every frame
				p.invalid = true
				return n
			}
			p.length, p.mask = uint64(b[0]&0x7f), 0
			switch p.length {
			case 126:
				p.ext, p.length, p.state = 2, 0, 2
			case 127:
				p.ext, p.length, p.state = 8, 0, 2
			default:
				p.state = 3
			}
			b = b[1:]
		case 2:
			p.length = p.length<<8 | uint64(b[0])
			p.ext--
			b = b[1:]
			if p.ext == 0 {
				if p.length>>63 != 0 {
					p.invalid = true
					return n
				}
				p.state = 3
			}
		case 3:
			k := copy(p.key[p.mask:], b)
			p.mask += k
			b = b[k:]
			if p.mask == 4 {
				p.pos, p.state = 0, 4
				if p.op == 1 || p.op == 2 { // a new message (opcode 0 continues the current one)
					p.msg, p.buf = msgFirst, p.buf[:0]
				}
				if p.length == 0 {
					n += p.frameEnd()
				}
			}
		case 4:
			k := min(uint64(len(b)), p.length)
			if p.op <= 2 {
				n += p.data(b[:k])
			}
			p.length -= k
			b = b[k:]
			if p.length == 0 {
				n += p.frameEnd()
			}
		}
	}
	return n
}

// data takes masked payload bytes of a data frame and returns 1 when they make its message count.
func (p *frameParser) data(b []byte) int {
	n := 0
	for i := 0; i < len(b) && (p.msg == msgFirst || p.msg == msgInput); i++ {
		c := b[i] ^ p.key[(p.pos+uint64(i))%4]
		switch {
		case p.msg == msgInput && len(p.buf) < maxReport:
			p.buf = append(p.buf, c)
		case p.msg == msgInput:
			p.msg, n = msgCounted, 1
		case c == '0':
			p.msg = msgInput
		case c == '1':
			p.msg, n = msgCounted, 1
		default:
			p.msg = msgIgnored
		}
	}
	p.pos += uint64(len(b))
	return n
}

// frameEnd ends the current frame and returns 1 when it ends an INPUT message that is more than terminal reports.
func (p *frameParser) frameEnd() int {
	p.state = 0
	if p.op > 2 || !p.fin {
		return 0
	}
	input := p.msg == msgInput && !reportsOnly(p.buf)
	p.msg, p.buf = msgIgnored, p.buf[:0]
	if input {
		return 1
	}
	return 0
}

// reportsOnly reports whether b is nothing but terminal reports, the replies xterm.js makes by itself: device
// attributes (ESC [ ? … c, ESC [ > … c), cursor position (ESC [ row ; col R, ESC [ ? row ; col R), status (ESC [ 0 n),
// modes (ESC [ ? … $ y, ESC [ … $ y), focus (ESC [ I, ESC [ O), window reports (ESC [ … t), settings (ESC P 0|1 $ r … ESC
// \) and colour reports (ESC ] 1x ; rgb:…, ESC ] 4 ; n ; rgb:…, ended by BEL or ESC \). Keys, pastes and mouse events
// are none of these. An empty b is nothing a person sent either.
func reportsOnly(b []byte) bool {
	for len(b) > 0 {
		k := report(b)
		if k == 0 {
			return false
		}
		b = b[k:]
	}
	return true
}

// report returns the length of the terminal report b starts with, or 0.
func report(b []byte) int {
	if len(b) < 3 || b[0] != 0x1b {
		return 0
	}
	switch b[1] {
	case ']':
		return oscColour(b)
	case 'P':
		return dcsSetting(b)
	case '[':
	default:
		return 0
	}
	i := 2
	prefix := byte(0)
	if b[i] == '?' || b[i] == '>' {
		prefix = b[i]
		i++
	}
	start := i
	for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';') {
		i++
	}
	if i == len(b) {
		return 0
	}
	params, final := string(b[start:i]), b[i]
	if final == '$' { // DECRQM reply: [?]mode ; value $ y
		if i+1 < len(b) && b[i+1] == 'y' && prefix != '>' && pair(params) {
			return i + 2
		}
		return 0
	}
	ok := false
	switch {
	case prefix == '?' && final == 'R': // DECXCPR: ? row ; col R
		ok = pair(params)
	case prefix != 0:
		ok = final == 'c' // DA1 and DA2 replies
	case final == 'R': // CPR: row;col
		ok = pair(params)
	case final == 'n':
		ok = params == "0"
	case final == 'I' || final == 'O':
		ok = params == ""
	case final == 't':
		ok = params != ""
	}
	if !ok {
		return 0
	}
	return i + 1
}

// oscColour returns the length of the colour report b starts with, or 0: OSC 10-19 (ESC ] 1x ; rgb:h/h/h) or an OSC 4
// palette entry (ESC ] 4 ; n ; rgb:h/h/h), ended by BEL or ST.
func oscColour(b []byte) int {
	i := 2
	switch {
	case len(b) > 4 && b[2] == '1' && b[3] >= '0' && b[3] <= '9':
		i = 4
	case len(b) > 4 && b[2] == '4' && b[3] == ';':
		i = 4
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == 4 {
			return 0
		}
	default:
		return 0
	}
	if len(b) < i+5 || string(b[i:i+5]) != ";rgb:" {
		return 0
	}
	i += 5
	head := i
	for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] >= 'a' && b[i] <= 'f' || b[i] >= 'A' && b[i] <= 'F' || b[i] == '/') {
		i++
	}
	if i == head {
		return 0
	}
	return terminated(b, i)
}

// dcsSetting returns the length of the DECRQSS reply b starts with (ESC P 0|1 $ r … ST), or 0. Its body is printable
// ASCII: the setting's own parameters.
func dcsSetting(b []byte) int {
	if len(b) < 6 || (b[2] != '0' && b[2] != '1') || b[3] != '$' || b[4] != 'r' {
		return 0
	}
	i := 5
	for i < len(b) && b[i] >= 0x20 && b[i] < 0x7f {
		i++
	}
	if i < len(b) && b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
		return i + 2
	}
	return 0
}

// terminated returns the length of b up to and including the string terminator (BEL or ESC \) at i, or 0.
func terminated(b []byte, i int) int {
	switch {
	case i >= len(b):
		return 0
	case b[i] == 0x07:
		return i + 1
	case b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\':
		return i + 2
	}
	return 0
}

// pair reports whether s is two ;-separated runs of digits.
func pair(s string) bool {
	a, c, found := strings.Cut(s, ";")
	return found && digits(a) && digits(c)
}

// digits reports whether s is one or more ASCII digits.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// activityConn is a hijacked browser connection: what the browser sends goes through a frameParser, and each message
// a person sent marks the session active. It is registered under its session so logout, idle-out and shutdown can close it.
type activityConn struct {
	net.Conn
	s    *server
	tok  string
	p    frameParser // only Read touches it; the proxy reads from one goroutine
	once sync.Once
	cerr error
}

func (c *activityConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.p.feed(b[:n]) > 0 {
		c.s.touch(c.tok)
	}
	return n, err
}

// Close unregisters the conn and closes it, once.
func (c *activityConn) Close() error {
	c.once.Do(func() {
		c.s.untrack(c)
		c.cerr = c.Conn.Close()
	})
	return c.cerr
}

// CloseWrite keeps the proxy's half-close when ttyd ends first (it looks for this method on the conn).
func (c *activityConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

// upgradeWriter hands the proxy an activityConn when it hijacks an authenticated upgrade.
type upgradeWriter struct {
	http.ResponseWriter
	s   *server
	tok string
}

func (u *upgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(u.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	// The proxy copies from conn only; bytes buffered in brw never reach ttyd, so they need no parsing.
	return u.s.track(u.tok, conn), brw, nil
}

func (u *upgradeWriter) Unwrap() http.ResponseWriter { return u.ResponseWriter }

// track wraps conn and registers it under the session tok; when that session is gone (deleted while the upgrade was
// in flight) or the gateway is closing, the conn is closed at once.
func (s *server) track(tok string, conn net.Conn) *activityConn {
	c := &activityConn{Conn: conn, s: s, tok: tok}
	s.mu.Lock()
	sess := s.sessions[tok]
	live := sess != nil && !s.closing
	if live {
		sess.conns[c] = struct{}{}
	}
	s.mu.Unlock()
	if !live {
		c.Close()
	}
	return c
}

func (s *server) untrack(c *activityConn) {
	s.mu.Lock()
	if sess := s.sessions[c.tok]; sess != nil {
		delete(sess.conns, c)
	}
	s.mu.Unlock()
}

// wall is now on the wall clock. Go's monotonic clock stops while the machine sleeps; idle and --ttl count that time.
func (s *server) wall() time.Time { return s.now().Round(0) }

// touch marks the session tok active now; one already past its idle limit ends instead (see session).
func (s *server) touch(tok string) {
	now := s.wall()
	var conns []*activityConn
	s.mu.Lock()
	if sess := s.sessions[tok]; sess != nil {
		var idle bool
		if conns, idle = s.idleEndLocked(tok, sess, now); !idle {
			sess.last = now
		}
	}
	s.mu.Unlock()
	closeConns(conns)
}

// idleEndLocked ends and audits the session tok when it has had no input for longer than s.idle at now, returning its
// conns (to close once s.mu is released) and true; otherwise nil and false. s.mu must be held.
func (s *server) idleEndLocked(tok string, sess *liveSession, now time.Time) ([]*activityConn, bool) {
	if now.Sub(sess.last) <= s.idle {
		return nil, false
	}
	login := sess.login
	conns := s.endSessionLocked(tok)
	s.audit.event("idle-out", "user", login, "sid", sid(tok))
	return conns, true
}

// endSessionLocked deletes the session tok and returns its conns, to be closed once s.mu is released.
func (s *server) endSessionLocked(tok string) []*activityConn {
	sess := s.sessions[tok]
	if sess == nil {
		return nil
	}
	delete(s.sessions, tok)
	conns := make([]*activityConn, 0, len(sess.conns))
	for c := range sess.conns {
		conns = append(conns, c)
	}
	return conns
}

func closeConns(conns []*activityConn) {
	for _, c := range conns {
		c.Close()
	}
}

// idleOut ends every session with no input for longer than s.idle, and audits each.
func (s *server) idleOut() {
	now := s.wall()
	var conns []*activityConn
	s.mu.Lock()
	for tok, sess := range s.sessions {
		if c, idle := s.idleEndLocked(tok, sess, now); idle {
			conns = append(conns, c...)
		}
	}
	s.mu.Unlock()
	closeConns(conns)
}

// sweep runs idleOut every min(idle/4, 30s) until ctx ends.
func (s *server) sweep(ctx context.Context) {
	every := max(min(s.idle/4, 30*time.Second), time.Millisecond)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.idleOut()
		}
	}
}

// expire cancels ctx with errTTL once the wall clock passes ttl from now, checking at least every `every`: a timer
// alone runs on the monotonic clock and would leave out the time the machine slept.
func (s *server) expire(ctx context.Context, cancel context.CancelCauseFunc, ttl, every time.Duration) {
	deadline := s.wall().Add(ttl)
	for {
		left := deadline.Sub(s.wall())
		if left <= 0 {
			cancel(errTTL)
			return
		}
		t := time.NewTimer(min(left, every))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// closeAll closes every proxied WebSocket, and any registered later: the gateway is stopping.
func (s *server) closeAll() {
	var conns []*activityConn
	s.mu.Lock()
	s.closing = true
	for _, sess := range s.sessions {
		for c := range sess.conns {
			conns = append(conns, c)
		}
	}
	s.mu.Unlock()
	closeConns(conns)
}
