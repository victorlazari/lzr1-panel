package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// Idle logout. A session is active while the browser sends input: any authenticated HTTP request, or a WebSocket
// data frame (keystrokes, mouse, resizes). ttyd pings the browser every few seconds and the browser answers with
// pongs, so bytes alone would never be idle: pings, pongs and closes do not count. A session idle for longer than
// --idle is deleted and its WebSockets (its terminals) are closed.

const defaultIdle = 30 * time.Minute

// liveSession is a logged-in browser: the login it was issued to, its last input, and its proxied WebSockets.
type liveSession struct {
	login string
	last  time.Time
	conns map[*activityConn]struct{}
}

// frameParser follows the RFC 6455 frame headers a browser sends, skipping payloads by count across reads.
// It never copies or changes the bytes; a malformed header stops it for good (the upstream decides what to do).
type frameParser struct {
	state   int    // 0: FIN/opcode byte, 1: mask bit and length, 2: extended length, 3: mask key, 4: payload
	op      byte   // the current frame's opcode
	ext     int    // extended length bytes still to read (2 or 8, counting down)
	length  uint64 // the payload length, then what is left of it
	mask    int    // mask key bytes still to read
	invalid bool
}

// feed parses b and returns how many data frames (opcode 0, 1 or 2) with a payload it saw begin.
func (p *frameParser) feed(b []byte) int {
	n := 0
	for len(b) > 0 && !p.invalid {
		switch p.state {
		case 0:
			p.op = b[0] & 0x0f
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
			p.length, p.mask = uint64(b[0]&0x7f), 4
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
			k := min(p.mask, len(b))
			p.mask -= k
			b = b[k:]
			if p.mask == 0 {
				if p.op <= 2 && p.length > 0 {
					n++
				}
				p.state = 4
				if p.length == 0 {
					p.state = 0
				}
			}
		case 4:
			k := uint64(len(b))
			if k > p.length {
				k = p.length
			}
			p.length -= k
			b = b[k:]
			if p.length == 0 {
				p.state = 0
			}
		}
	}
	return n
}

// activityConn is a hijacked browser connection: what the browser sends goes through a frameParser, and each data
// frame marks the session active. It is registered under its session so logout, idle-out and shutdown can close it.
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

// touch marks the session tok active now.
func (s *server) touch(tok string) {
	now := s.now()
	s.mu.Lock()
	if sess := s.sessions[tok]; sess != nil {
		sess.last = now
	}
	s.mu.Unlock()
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
	now := s.now()
	var conns []*activityConn
	s.mu.Lock()
	for tok, sess := range s.sessions {
		if now.Sub(sess.last) > s.idle {
			login := sess.login
			conns = append(conns, s.endSessionLocked(tok)...)
			s.audit.event("idle-out", "user", login, "sid", sid(tok))
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
