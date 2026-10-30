// lzr1-remote-gw: the login gateway in front of ttyd for `lzr1-panel remote`.
// It listens on a loopback TCP address or a private Unix socket (published by `tailscale serve`), checks the
// caller, and proxies HTTP and WebSocket traffic to ttyd's Unix socket. No third-party code.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const minPasswordLen = 12

type config struct {
	listen, upstream, base string
	host                   string // the published name: Origin must be https://host (default: the request's Host)
	stripped               bool   // serve strips --base before proxying (its unix: upstream): put it back
	allow                  []string
	ttl                    time.Duration
	logPath                string
	auditPath              string        // --audit: the audit trail (none when empty)
	lockAfter, killAfter   int           // 0: the defaults (5 and 20); tests set them
	lockFor                time.Duration // 0: the default (15 min)
	idle                   time.Duration // --idle; 0 (tests): the default (30 min)
}

// errUsage marks a bad command line: exit 2.
var errUsage = errors.New("usage")

func parseFlags(args []string, stderr io.Writer) (config, error) {
	var c config
	var allow string
	fs := flag.NewFlagSet("lzr1-remote-gw", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.listen, "listen", "", "Unix socket, or loopback host:port (port 0 picks one), to serve on (required)")
	fs.StringVar(&c.upstream, "upstream", "", "ttyd's Unix socket (required)")
	fs.StringVar(&c.base, "base", "", "path prefix, /UUID/ (required)")
	fs.StringVar(&c.host, "host", "", "the published name, like m.tailnet.ts.net: Origin must be https://<it> (default: the Host header)")
	fs.BoolVar(&c.stripped, "stripped", false, "requests arrive without --base (tailscale serve to a Unix socket strips it); needs --host")
	fs.StringVar(&allow, "allow", "", "allowed Tailscale logins, comma-separated (required)")
	fs.DurationVar(&c.ttl, "ttl", 8*time.Hour, "exit after this long")
	fs.StringVar(&c.logPath, "log", "", "append the log to this file (default stderr)")
	fs.DurationVar(&c.idle, "idle", defaultIdle, "log a browser out, closing its terminal, after this long without input")
	fs.StringVar(&c.auditPath, "audit", "", "append the audit trail (logins, failures, lockouts, shutdown) to this file, 0600")
	if err := fs.Parse(args); err != nil {
		return c, errUsage
	}
	bad := func(f string, a ...any) (config, error) {
		fmt.Fprintf(stderr, "lzr1-remote-gw: "+f+"\n", a...)
		return c, errUsage
	}
	if fs.NArg() > 0 {
		return bad("unexpected argument %q", fs.Arg(0))
	}
	if c.listen == "" || c.upstream == "" || c.base == "" || allow == "" {
		return bad("--listen, --upstream, --base and --allow are required")
	}
	if !strings.Contains(c.listen, "/") {
		host, _, err := net.SplitHostPort(c.listen)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			return bad("--listen must be a Unix socket path or a loopback IP:port, got %q", c.listen)
		}
	}
	if len(c.base) < 3 || !strings.HasPrefix(c.base, "/") || !strings.HasSuffix(c.base, "/") || strings.Contains(c.base[1:len(c.base)-1], "/") {
		return bad("--base must look like /UUID/, got %q", c.base)
	}
	c.host = strings.ToLower(c.host)
	if strings.ContainsAny(c.host, "/:@ ") {
		return bad("--host must be a bare name, like m.tailnet.ts.net, got %q", c.host)
	}
	if c.stripped && (c.host == "" || !strings.Contains(c.listen, "/")) {
		return bad("--stripped needs --host and a Unix socket --listen")
	}
	for _, a := range strings.Split(allow, ",") {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			c.allow = append(c.allow, a)
		}
	}
	if len(c.allow) == 0 {
		return bad("--allow has no login")
	}
	if c.ttl <= 0 {
		return bad("--ttl must be positive")
	}
	if c.idle <= 0 {
		return bad("--idle must be positive")
	}
	return c, nil
}

// readPassword reads one line from r and returns its SHA-256; the password itself is not kept.
func readPassword(r io.Reader) ([32]byte, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return [32]byte{}, fmt.Errorf("reading the password: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < minPasswordLen {
		return [32]byte{}, fmt.Errorf("the password on stdin must have at least %d characters", minPasswordLen)
	}
	return sha256.Sum256([]byte(pw)), nil
}

// checkSocketDir refuses a socket directory that anyone but its owner can enter or list.
func checkSocketDir(sock string) error {
	dir := filepath.Dir(sock)
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is %04o; it must be 0700", dir, fi.Mode().Perm())
	}
	return nil
}

// listenUnix listens on path, 0600, after removing a stale socket file; a live one is left alone.
func listenUnix(path string) (net.Listener, error) {
	if err := checkSocketDir(path); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type() != os.ModeSocket {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("%s is in use by another process", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// listen opens --listen: a loopback TCP address (parseFlags checked it is one), else a Unix socket.
// tailscale serve in the macOS app cannot reach a Unix socket, so lzr1-panel uses 127.0.0.1:0 there.
func listen(addr string) (net.Listener, error) {
	if strings.Contains(addr, "/") {
		return listenUnix(addr)
	}
	return net.Listen("tcp", addr)
}

// waitUpstream returns true once ttyd's socket answers, false if ctx ends first.
func waitUpstream(ctx context.Context, path string) bool {
	for {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c, err := parseFlags(args, stderr)
	if err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "lzr1-remote-gw: %v\n", err)
		return 1
	}
	logw := stderr
	if c.logPath != "" {
		f, err := os.OpenFile(c.logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		logw = f
	}
	logger := log.New(logw, "lzr1-remote-gw: ", log.LstdFlags)
	var audit *auditLog
	if c.auditPath != "" {
		f, err := openAudit(c.auditPath)
		if err != nil {
			return fail(err)
		}
		defer f.Close()
		audit = &auditLog{w: f, now: time.Now, log: logger}
	}
	sum, err := readPassword(stdin)
	if err != nil {
		return fail(err)
	}
	if err := checkSocketDir(c.upstream); err != nil {
		return fail(fmt.Errorf("upstream: %w", err))
	}
	l, err := listen(c.listen)
	if err != nil {
		return fail(err)
	}
	tcp := l.Addr().Network() == "tcp"
	if !tcp {
		defer os.Remove(c.listen)
	}
	gw := newServer(c, sum, logger)
	gw.audit = audit
	// Every exit path, after the server stopped: Shutdown and Close leave hijacked WebSockets open.
	defer gw.closeAll()
	srv := &http.Server{
		Handler:           gw,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          logger,
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	// --ttl on the wall clock, checked every 30 s (and at the deadline): a machine that slept past it stops on waking.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go gw.expire(ctx, cancel, c.ttl, 30*time.Second)
	// The kill switch ends the wait for ttyd too: the handler serves (and counts) before ready.
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	go func() {
		select {
		case <-gw.kill:
			wcancel()
		case <-wctx.Done():
		}
	}()
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	go gw.sweep(sctx)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	up := waitUpstream(wctx, c.upstream)
	if killed(gw) {
		srv.Close()
		return 3
	}
	if !up {
		srv.Close()
		return 0
	}
	audit.event("start", "ttl", c.ttl.String(), "idle", gw.idle.String())
	if tcp {
		fmt.Fprintln(stdout, "ready", l.Addr()) // the port, when --listen asked for 0
	} else {
		fmt.Fprintln(stdout, "ready")
	}
	select {
	case err := <-served:
		return fail(err)
	case <-gw.kill:
		// The handler wrote "shutdown reason=killswitch". Nothing should finish gracefully.
		srv.Close()
		return 3
	case <-ctx.Done():
		if killed(gw) {
			srv.Close()
			return 3
		}
	}
	reason := "signal"
	if errors.Is(context.Cause(ctx), errTTL) {
		reason = "ttl"
	}
	audit.event("shutdown", "reason", reason)
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	if err := srv.Shutdown(dctx); err != nil {
		srv.Close()
	}
	return 0
}

// errTTL is the cause of run's context ending when --ttl passes.
var errTTL = errors.New("ttl passed")

// killed reports whether the kill switch fired.
func killed(s *server) bool {
	select {
	case <-s.kill:
		return true
	default:
		return false
	}
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
