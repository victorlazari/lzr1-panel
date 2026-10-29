package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// auditLog writes --audit: one line per event, "TIME EVENT key=value...", never a password, cookie or token.
// A nil *auditLog (no --audit) writes nothing.
type auditLog struct {
	mu     sync.Mutex
	w      io.Writer
	now    func() time.Time
	log    *log.Logger // a write error is reported here, once
	failed bool
}

// auditValue keeps a value to one field: anything outside printable ASCII, a space or "=" becomes "_".
func auditValue(v string) string {
	b := []byte(v)
	for i, c := range b {
		if c <= ' ' || c > '~' || c == '=' {
			b[i] = '_'
		}
	}
	return string(b)
}

// event writes "TIME name k=v ..." in one Write; kv alternates keys and values.
func (a *auditLog) event(name string, kv ...string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	b.WriteString(a.now().Format(time.RFC3339))
	b.WriteString(" " + name)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" " + kv[i] + "=" + auditValue(kv[i+1]))
	}
	b.WriteString("\n")
	if _, err := io.WriteString(a.w, b.String()); err != nil && !a.failed {
		a.failed = true
		if a.log != nil {
			a.log.Printf("audit: %v (still serving; later audit errors are not logged)", err)
		}
	}
}

// sid names a session in the audit log: the first 8 hex characters of SHA-256 of its token, never the token.
func sid(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

// openAudit opens path for appending, 0600 (an existing file is narrowed to 0600); a symlink or anything but a
// regular file is refused.
func openAudit(path string) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("audit: %s is not a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: %w", err)
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", path)
	}
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("audit: %w", err)
	}
	return f, nil
}
