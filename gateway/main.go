// lzr1-remote-gw: the login gateway in front of ttyd for `lzr1-panel remote`.
// It listens on a private Unix socket (published by `tailscale serve`), checks the caller,
// and proxies HTTP and WebSocket traffic to ttyd's Unix socket. No TCP listener, no third-party code.
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
	allow                  []string
	ttl                    time.Duration
	logPath                string
}

// errUsage marks a bad command line: exit 2.
var errUsage = errors.New("usage")

func parseFlags(args []string, stderr io.Writer) (config, error) {
	var c config
	var allow string
	fs := flag.NewFlagSet("lzr1-remote-gw", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.listen, "listen", "", "Unix socket to serve on (required)")
	fs.StringVar(&c.upstream, "upstream", "", "ttyd's Unix socket (required)")
	fs.StringVar(&c.base, "base", "", "path prefix, /UUID/ (required)")
	fs.StringVar(&allow, "allow", "", "allowed Tailscale logins, comma-separated (required)")
	fs.DurationVar(&c.ttl, "ttl", 8*time.Hour, "exit after this long")
	fs.StringVar(&c.logPath, "log", "", "append the log to this file (default stderr)")
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
	if len(c.base) < 3 || !strings.HasPrefix(c.base, "/") || !strings.HasSuffix(c.base, "/") || strings.Contains(c.base[1:len(c.base)-1], "/") {
		return bad("--base must look like /UUID/, got %q", c.base)
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
	sum, err := readPassword(stdin)
	if err != nil {
		return fail(err)
	}
	if err := checkSocketDir(c.upstream); err != nil {
		return fail(fmt.Errorf("upstream: %w", err))
	}
	l, err := listenUnix(c.listen)
	if err != nil {
		return fail(err)
	}
	defer os.Remove(c.listen)
	srv := &http.Server{
		Handler:           newServer(c, sum, logger),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          logger,
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, c.ttl)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	if !waitUpstream(ctx, c.upstream) {
		srv.Close()
		return 0
	}
	fmt.Fprintln(stdout, "ready")
	select {
	case err := <-served:
		return fail(err)
	case <-ctx.Done():
	}
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	if err := srv.Shutdown(sctx); err != nil {
		srv.Close()
	}
	return 0
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
