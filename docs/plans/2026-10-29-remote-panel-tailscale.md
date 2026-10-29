# Remote Panel over Tailscale Implementation Plan

> **For implementers:** Use ring:dispatching-workflows (rolling-phase: elaborate
> the current phase against the real code, run it as a reviewed multi-agent
> workflow, then elaborate the next phase — repeat), or ring:running-dev-cycle
> for the full subagent-orchestrated workflow.
> This document is the living source of truth — task elaboration for later
> phases is written back into it during execution.

**Goal:** One key in the panel publishes the panel to your tailnet at
`https://<machine>.<tailnet>.ts.net/<uuid>/`, behind a random 16-character password and an e-mail allowlist, so any
browser on the same tailnet can use the panel exactly like the terminal.

**Architecture:** `lzr1-panel remote on` starts three local pieces, and `remote off` (or expiry) stops them:
`ttyd` runs a fresh `lzr1-panel open` (tmux layout) per browser on a private Unix socket; a small Go gateway,
`lzr1-remote-gw`, on a second private Unix socket, checks the caller's Tailscale identity against the allowlist and
the password, then proxies the page and its WebSocket to ttyd; `tailscale serve` (never `funnel`) publishes the
gateway at a random path of the machine's tailnet HTTPS name. No TCP port is opened on the machine.

**Tech Stack:** zsh (`bin/lzr1-panel`), Go 1.26 standard library only (`gateway/`), ttyd 1.7.7 (xterm.js in the
browser), Tailscale 1.104 (`serve`, MagicDNS HTTPS certificates, identity headers), tmux, fzf, openssl.

## Phase Overview

| Phase | Milestone | Epics | Status |
|-------|-----------|-------|--------|
| 1 | `lzr1-panel remote on` prints a tailnet URL and password; an allowlisted tailnet user opens it in a browser, logs in and drives a working panel; `remote off` removes every trace | 1.1, 1.2 | Complete |
| 2 | Hardened: lockout and kill switch, expiry and idle logout, audit log, an OS notification on every login and failure; terminal-fidelity fixes from Phase 1's spike | 2.1, 2.2 | Complete |
| 3 | In the panel: a key and footer button that enable it and copy the URL, a menu to copy the URL or password, see who is connected, rotate or turn it off; installer, doctor and docs | 3.1, 3.2 | Epic-level |

**Decisions already made with the owner (2026-10-29):**

- **Feasible as specified.** Tailscale 1.104 on this Mac has HTTPS certificates enabled
  (`victors-macbook-pro-1.heron-justitia.ts.net`) and `serve` can proxy to a Unix socket.
- **E-mail = allowlist.** Enabling asks for the e-mail(s) allowed in (prefilled with the last answer). The gateway
  admits a request only when Tailscale's `Tailscale-User-Login` header matches one of them. Nothing is e-mailed.
- **Auth = a small Go gateway** (standard library only), not ttyd's Basic Auth: a login page, lockout, logout,
  allowlist and an audit log, and the password never in a command line.
- **Never `tailscale funnel`.** The page is reachable only from the tailnet; the code refuses any funnel config.
- **Each browser gets its own panel** (a fresh `lzr1-panel open` in the tmux layout, splash off), not a mirror of
  the local window: the local panel may be a Ghostty split, which cannot be mirrored. Both see and drive the same
  tmux sessions.
- **Secrets:** UUID v4 path (122 random bits); password 16 characters (minimum 12, `LZR1_REMOTE_PWLEN`) from an
  unambiguous 56-character alphabet (about 93 bits), made by `openssl rand`. Enabling copies the URL; the password is
  copied by its own action, so the two are never on the clipboard together unless asked.

---

## Phase 1 — Walking skeleton: published, logged in, usable

### Epic 1.1: `lzr1-remote-gw`, the login gateway

**Goal:** a Go binary that serves a login page under `/<uuid>/`, admits only an allowlisted Tailscale user with the
right password, sets a hardened session cookie, and reverse-proxies HTTP and WebSocket traffic to ttyd's socket.
**Scope:** `gateway/` (new Go module), `.gitignore`
**Dependencies:** none
**Done when:** `cd gateway && go test ./... -count=1` passes the cases in Task 1.1.2 and `go vet ./...` is clean.
**Status:** Done

Contract with Epic 1.2 (both epics depend on it, so it is fixed here):

```text
lzr1-remote-gw --listen SOCKET --upstream SOCKET --base /UUID/ --allow EMAIL[,EMAIL...]
               [--ttl 8h] [--log FILE]
  stdin:   the password, one line, read once (never argv or env)
  stdout:  "ready" once both sockets are usable; nothing else
  exit:    0 on SIGTERM/SIGINT or when --ttl passes; 2 on bad flags; 1 on any other start failure (message on stderr)
  routes:  GET  /UUID/login   the login form           POST /UUID/login  password → cookie, 303 to /UUID/
           POST /UUID/logout  clears the session       /UUID/*          authenticated → proxied to --upstream
           anything else → 404, an empty body
```

#### Task 1.1.1: Module, flags, sockets and the authenticated proxy

- [x] Done

**Context:** The repo is zsh only (`bin/lzr1-panel`, `lib/splash.zsh`, `install.sh`); there is no Go yet. Go 1.26
is on this machine. ttyd will listen on a Unix socket (Epic 1.2), and `tailscale serve` will send requests to the
gateway's Unix socket, so the gateway has no TCP listener at all.

**Implementation vision:**
- New module `gateway/` (`go.mod`: `module github.com/victorlazari/lzr1-panel/gateway`, `go 1.26`), standard
  library only: no third-party packages, so there is no supply chain to review. `main.go` parses flags;
  `server.go` holds the handler; `auth.go` holds sessions and the password check (Task 1.1.2).
- Listen with `net.Listen("unix", path)` after removing a stale socket file; `os.Chmod(path, 0600)` right after.
  The socket directory is created 0700 by Epic 1.2; refuse to start if the directory is group- or world-accessible.
- Proxy with `httputil.ReverseProxy` whose `Transport` dials the upstream Unix socket (WebSocket upgrades pass
  through `ReverseProxy` since Go 1.12). Strip the incoming `Cookie` header for the gateway's own cookie before
  proxying, and drop `Tailscale-*` headers on the way upstream: ttyd needs neither.
- Every response carries `Cache-Control: no-store`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
  `X-Content-Type-Options: nosniff` and a `Content-Security-Policy` that allows the page's own origin only
  (`default-src 'self'; connect-src 'self' wss:`; ttyd's inline bootstrap needs `'unsafe-inline'` for scripts and
  styles, which is acceptable since the page serves no third-party content).
- Read the password from stdin up to the first newline; refuse an empty or shorter-than-12 password (exit 1). Keep
  only a SHA-256 hash in memory, and compare with `crypto/subtle.ConstantTimeCompare`. A slow hash is unnecessary
  for a random 93-bit secret and would only add latency.
- `--ttl`: a timer that shuts down gracefully (`http.Server.Shutdown`, 5 s) and exits 0. Print `ready` once listening.
- Edge cases: upstream socket missing → 502 with a short text after login (ttyd still starting or crashed);
  `--base` without leading and trailing `/` → exit 2; a second gateway on the same socket → the stale-file removal
  must not delete a live socket: try to dial it first, exit 1 if it answers.

**Files:**
- Create: `gateway/go.mod`, `gateway/main.go`, `gateway/server.go`
- Modify: `.gitignore` (the built binary `gateway/lzr1-remote-gw`)
- Test: `gateway/server_test.go`

**Verification:** `cd gateway && go vet ./... && go test ./... -run 'TestProxy|TestFlags|TestSocket' -count=1 -v`:
a fake upstream on a temp Unix socket receives an authenticated request and a WebSocket echo round-trips; an
unauthenticated request never reaches it; a world-readable socket directory is refused.

**Done when:** the binary builds with `go build -o lzr1-remote-gw .`, starts on Unix sockets only, and proxies
HTTP and WebSocket traffic for authenticated requests.

#### Task 1.1.2: Identity allowlist, password login, session cookie, Origin checks

- [x] Done

**Context:** `tailscale serve` adds `Tailscale-User-Login` (the user's e-mail) and `Tailscale-User-Name` to
requests from user-owned tailnet devices; requests from tagged devices carry no identity. Only `tailscale serve`
and processes of this user can reach the gateway's 0600 socket, so the header cannot be forged from the tailnet.

**Implementation vision:**
- **Identity first:** every request under `--base` needs `Tailscale-User-Login` equal (case-insensitively, trimmed)
  to one `--allow` entry; otherwise 403 with a one-line page naming the login seen ("signed in to Tailscale as
  X, not allowed here"), or "no Tailscale identity" when absent (tagged device, or not through serve). The same
  check runs on every proxied request and WebSocket upgrade, not only at login.
- **Login:** `GET /UUID/login` serves a minimal self-contained HTML form (no external assets).
  `POST /UUID/login` (form field `password`) checks the password; success → a session: 32 random bytes from
  `crypto/rand`, hex, kept in memory bound to the login e-mail, cookie `lzr1_remote` with `HttpOnly; Secure;
  SameSite=Strict; Path=/UUID/` and no `Expires` (a browser session cookie), then `303` to `/UUID/`. A session
  is valid only while the request's `Tailscale-User-Login` equals the e-mail it was bound to.
- **Origin:** POSTs and WebSocket upgrades must carry `Origin: https://<Host>`; anything else is 403. This blocks
  cross-site WebSocket hijacking even with `SameSite` ignored.
- **Logout:** `POST /UUID/logout` deletes the session. Phase 2 adds lockout, idle timeout and audit.
- Unauthenticated requests to `/UUID/` and below → `303` to `/UUID/login` for browsers (`Accept: text/html`), 401
  otherwise.
- Edge cases: two allowed users at once (each their own sessions); the same user on two devices (two sessions, both
  valid); a session cookie replayed from another tailnet user → refused by the identity binding.

**Files:**
- Create: `gateway/auth.go`, `gateway/login.html` (embedded with `//go:embed`)
- Test: `gateway/auth_test.go`

**Verification:** `cd gateway && go test ./... -run 'TestAuth|TestLogin|TestOrigin' -count=1 -v`: no identity → 403;
identity not allowlisted → 403; wrong password → 401 and no cookie; right password → cookie with every attribute
above and 303; the cookie with a different `Tailscale-User-Login` → refused; WebSocket upgrade with a foreign
`Origin` → 403; logout → the cookie stops working.

**Done when:** only an allowlisted Tailscale user with the password reaches the upstream, on every request.

### Epic 1.2: `lzr1-panel remote on | off | status`

**Goal:** the panel command builds and starts ttyd and the gateway on private sockets, publishes the gateway with
`tailscale serve` at a random path, prints the URL and password, copies the URL, and tears it all down cleanly.
**Scope:** `bin/lzr1-panel` (new `remote` command and helpers), `install.sh` (build step only), `docs/remote.md`
**Dependencies:** Epic 1.1 (its contract above)
**Done when:** from a phone or another computer on the tailnet, the URL plus password opens a panel that lists the
sessions and attaches a viewer; `remote off` leaves no serve config, socket, process or secret behind.
**Status:** Done

#### Task 1.2.1: Find Tailscale, build the gateway, start and stop the pieces

- [x] Done

**Context:** `bin/lzr1-panel`'s `case $1 in` dispatch holds every command; `clip` (`bin/lzr1-panel:475`) copies to
the clipboard; `openssl rand` is already used (`bin/lzr1-panel:1369`); `open)` (`bin/lzr1-panel:927-931`) chooses
the Ghostty split when `TERM_PROGRAM=ghostty` or `GHOSTTY_RESOURCES_DIR` is set, otherwise the tmux layout. On macOS
the Tailscale CLI is `/Applications/Tailscale.app/Contents/MacOS/Tailscale` when not on PATH; on Linux it is
`tailscale` and `serve` needs `sudo` unless `tailscale set --operator=$USER` was run.

**Implementation vision:**
- `_ts`: the Tailscale CLI (PATH, then the macOS app path), else `die "remote needs Tailscale (tailscale.com/download)"`.
  Preflight from `_ts status --json`: `BackendState` is `Running`, `CertDomains` is non-empty (else: "turn on HTTPS
  in the Tailscale admin console: DNS → HTTPS Certificates"), and the host name is `Self.DNSName` without the dot.
- Gateway binary: `$STATE/remote/lzr1-remote-gw`, built with `go build` from `${SELF:h:h}/gateway` when missing or
  older than any `gateway/*.go`; no Go → `die` naming `brew install go` / the distro package.
- `remote on [--allow EMAIL[,EMAIL]] [--ttl 8h]`: refuse when already on (print `status` instead). Ask for the
  allowlist with `vared`, prefilled from `$CONFIG/remote.conf` (`allow=` line; first time: the Tailscale login of
  this machine, `Self.UserID` → `User[...].LoginName`); store it back. Create `$STATE/remote/` 0700. Generate the UUID
  (`uuidgen`, else `/proc/sys/kernel/random/uuid`, lowercased) and the password (`openssl rand -base64 48 | tr -dc
  'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789' | cut -c1-$LZR1_REMOTE_PWLEN`, default 16, refuse <12,
  retry if short). Write `$STATE/remote/session` (0600: `uuid=`, `url=`, `password=`, `allow=`, `started=`,
  `expires=`, pids) so the URL and password can be copied again later.
- Start ttyd detached: `ttyd -i $STATE/remote/ttyd.sock -b /UUID -W -m 4 -t titleFixed=lzr1 env -u
  GHOSTTY_RESOURCES_DIR -u TERM_PROGRAM -u TMUX LZR1_SPLASH=0 $SELF open` (writable, at most 4 browsers, tmux
  layout forced by unsetting Ghostty's variables). Then the gateway with the password on stdin; wait for `ready` (5 s).
- Publish: `_ts serve --bg --yes --https=443 --set-path /UUID unix:$STATE/remote/gw.sock`. Refuse to continue (and
  roll back) if `_ts serve status --json` shows any `AllowFunnel` entry for this node's 443.
- Print `URL`, `password`, allowlist and expiry; `clip` the URL; say "copied the URL; lzr1-panel remote password
  copies the password".
- `remote off`: `_ts serve --https=443 --set-path /UUID off`, SIGTERM gateway and ttyd (pids from the session file,
  checked to still be ours via `ps -o comm=`), remove sockets and the session file. Idempotent.
- `remote status`: on/off, URL, allowlist, expiry, the processes alive. `remote url` / `remote password` copy one
  item. A gateway that exits by itself (TTL) is noticed by `status` and `on`, which then run `off`'s cleanup.
- Edge cases: Tailscale stopped or logged out → refuse with its state; `serve` refused for permissions (Linux) →
  print the operator fix and roll back; ttyd missing → `die` with `brew install ttyd` / `apt install ttyd`; a stale
  session file after a reboot → `status` reports off and cleans it.

**Files:**
- Modify: `bin/lzr1-panel` (usage block, new `remote)` command, `_ts`, `_remote_*` helpers)
- Modify: `install.sh` (offer ttyd; build the gateway when Go is present)
- Create: `docs/remote.md` (how it works, the safety layers, how to turn it off)

**Verification:** `zsh -n bin/lzr1-panel && bash -n install.sh`; with a scratch `LZR1_CONFIG`/`XDG_CACHE_HOME`:
`bin/lzr1-panel remote on --allow you@example.com --ttl 10m` prints a `https://…ts.net/<uuid>/` URL and a 16-char
password; `curl -s -o /dev/null -w '%{http_code}' https://<host>/<uuid>/` from this machine through the tailnet name
gives 303 (to login) or 403 (no identity for this device), never 200 without a cookie; `remote status` shows on;
`remote off` → `tailscale serve status` shows no `/<uuid>` and no `lzr1-remote-gw`/`ttyd` process remains.

**Done when:** on/off/status/url/password work and off always restores the starting state.

#### Task 1.2.2: Fidelity spike from a real browser

- [x] Done

**Context:** The browser terminal is xterm.js inside ttyd. The panel relies on fzf mouse clicks (left, right,
double, footer), tmux's mouse, OSC 52 copies, Ctrl-C/Ctrl-b, and a 56-column panel next to a viewer.

**Implementation vision:** with Task 1.2.1 running, open the URL from Chrome and Safari on a laptop and from a phone
on the tailnet; record a pass/fail table in `docs/remote.md` for: listing and selecting sessions, typing in a
session, left click, right-click menu (the browser's context menu may take it), double-click, footer buttons, the
new-session picker, Ctrl-C to Claude, Ctrl-b d, copy (OSC 52 → browser clipboard), paste, resize, a phone-width
layout, reconnect after the laptop sleeps, browser keys that never reach the page (Cmd-W, Cmd-T, Ctrl-W in some
browsers). This is a measured spike, not a fix: every failure becomes a Phase 2 item.

**Files:** Modify: `docs/remote.md` (the fidelity table)

**Verification:** the table exists with a result for each item on each browser.

**Done when:** Phase 2's fidelity epic can be elaborated from facts.

---

## Phase 2 — Hardening and fidelity

### Epic 2.1: Lockout, expiry, audit and alerts

**Goal:** brute force and forgotten sessions are handled: 5 wrong passwords from one Tailscale login lock it for
15 minutes; 20 wrong passwords in total shut the remote session down (kill switch) and alert; sessions log out after
30 idle minutes; the whole remote session ends at its TTL (default 8 h, `LZR1_REMOTE_TTL`); every login, failure,
lockout and shutdown is written to `$STATE/remote/audit.log` (0600) and raises an OS notification through the
existing `_notify` path.
**Scope:** `gateway/` (`audit.go`, `idle.go`, `auth.go`, `server.go`, `main.go`), `bin/lzr1-panel` (`remote`
reads the audit log; notifications), `docs/remote.md`
**Dependencies:** Phase 1
**Done when:** gateway tests cover each rule; a real wrong-password burst locks out and then triggers the kill
switch, with notifications on this computer.
**Status:** Done

Contract additions to Epic 1.1's (Tasks 2.1.1 and 2.1.2 build them; Task 2.1.3 uses them):

```text
lzr1-remote-gw ... [--audit FILE] [--idle 30m]
  exit:    3 when the kill switch fired (20 wrong passwords in total); 0, 1 and 2 as before
  --audit: append-only, 0600; one line per event, never the password, a cookie or a session token:
           TIME EVENT key=value...      TIME is RFC 3339 with the local offset; values never hold spaces
           start     ttl=8h0m0s idle=30m0s
           login     user=LOGIN sid=SID                 SID: first 8 hex of SHA-256(token), never the token
           failure   user=LOGIN fails=N total=T [locked=1]
           lockout   user=LOGIN until=TIME
           logout    user=LOGIN sid=SID
           idle-out  user=LOGIN sid=SID
           shutdown  reason=ttl|signal|killswitch
```

#### Task 2.1.1: Audit log, lockout and kill switch in the gateway

- [x] Done

**Context:** The password is checked in `serveLogin` (`gateway/auth.go:105-130`); a wrong one is only logged
(`auth.go:123`, `s.log` is `remote.log`) and re-renders the form with 401. There is no per-login or total counter.
Sessions are `map[string]string` token → login (`gateway/server.go:42-43`, `auth.go:59-80`). `run`
(`gateway/main.go:176-241`) ends on SIGTERM/SIGINT or `--ttl` (`main.go:215-218`) through `srv.Shutdown` and returns 0;
it has no way to be told to stop by the handler. Identity is checked before the password (`server.go:117-120`), so
only allowlisted logins ever reach `serveLogin`. The flags live in `parseFlags` (`main.go:39-93`), the config in
`config` (`main.go:27-34`).

**Implementation vision:**
- New `gateway/audit.go`: `type auditLog struct{ mu sync.Mutex; w io.Writer; now func() time.Time }` and
  `func (a *auditLog) event(name string, kv ...string)` writing `TIME NAME k=v ...\n` in one `Write`. A nil
  `*auditLog` is a no-op (no `--audit`). Values are passed through `strings.Fields`-safe sanitizing: any byte outside
  printable ASCII, a space or `=` becomes `_`, so a crafted login can never forge a second event or field.
  `sid(token)` = first 8 hex characters of SHA-256 of the token.
- `--audit FILE`: opened in `run` with `O_WRONLY|O_CREATE|O_APPEND`, 0600, then `f.Chmod(0600)` (an existing
  file with wider modes is narrowed); refused (exit 1) when it is a symlink (`os.Lstat` first) or not a regular file.
  A write error later is logged once to `s.log` and the gateway keeps serving (the audit file is a record, not a
  gate). `start` is written right before `ready` is printed.
- Lockout, in `server`: `fails map[string]int`, `lockedUntil map[string]time.Time`, `total int`, all under `s.mu`;
  `now func() time.Time` (tests set it) and the limits as config fields with the decided defaults: `lockAfter = 5`,
  `lockFor = 15*time.Minute`, `killAfter = 20` (no flags: tests build `config` directly).
- `serveLogin` POST order: (1) still locked (`now < lockedUntil[login]`) → `total++`, audit `failure ... locked=1`,
  429 with the form saying "Too many wrong passwords: locked until HH:MM." and the password is not checked;
  (2) wrong password → `fails[login]++`, `total++`, audit `failure`; when `fails` reaches `lockAfter`, set
  `lockedUntil = now + lockFor`, reset `fails` to 0, audit `lockout`, answer 429 with the locked message; else 401 as
  today; (3) right password → `fails[login] = 0` (the total never resets), session, audit `login`. In every case,
  when `total` reaches `killAfter`: audit `shutdown reason=killswitch`, answer 503 "too many wrong passwords: the remote
  panel was turned off", and close `s.kill` (a `chan struct{}` closed through a `sync.Once`).
- A lock expires by time only: the next POST after `lockedUntil` is checked normally (expired entries are deleted
  then). `GET login` while locked renders the locked message with 200 (no counting). Attempts while locked count toward
  the total on purpose: a brute force on one login cannot outlast the kill switch by waiting out lockouts one at a time.
- `run`: selects on `served`, `ctx.Done()` and `s.kill`. The reason goes to the audit log once: `ttl` when the
  `--ttl` timer fired (`context.Cause`/`DeadlineExceeded`), `signal` on SIGTERM/SIGINT, `killswitch` (already written by
  the handler). Kill switch → `srv.Close()` (not `Shutdown`: nothing should finish gracefully) and exit 3.
- `serveLogout`: audit `logout user=… sid=…` (replaces the `s.log` line; `s.log` keeps "refused …" lines for
  non-allowlisted logins and upstream errors only).
- Edge cases: two logins failing at once (separate `fails`, shared `total`); the right password during a lockout
  (refused, counted, still locked); the 20th attempt is the right password while locked (kill switch still fires,
  since it counted); concurrent POSTs racing past 20 (the `Once` closes `kill` once; every later POST gets 503);
  `--audit` path in a missing directory (exit 1 with the error); a login with a space or `=` cannot occur after
  identity but is sanitized anyway.

**Files:**
- Create: `gateway/audit.go`, `gateway/audit_test.go`
- Modify: `gateway/auth.go` (`serveLogin`, `serveLogout`, the locked message), `gateway/server.go` (`server`
  fields, `newServer`), `gateway/main.go` (`config`, `--audit`, `run`'s exit paths), `gateway/login.html` (an
  `{{if .Locked}}` variant of the error line, nothing else)
- Test: `gateway/audit_test.go`, `gateway/auth_test.go` (`TestLockout*`, `TestKillSwitch*`)

**Verification:** RED first: `scratchpad/remote/verify-2.1.1.zsh` runs the tests below against the unchanged code
(they do not compile or fail). Then `cd gateway && gofmt -l . && go vet ./... && go test ./... -count=1 -race`:
5 wrong passwords from `a@x` → the 5th answers 429 and the right password then gets 429 too; `b@x` still logs in;
`now` moved past 15 min → `a@x` logs in and its counter is back to 0; 20 failures across `a@x` and `b@x` (locked
attempts included) → `run` returns 3 and the socket stops answering; the audit file is 0600 (also when it existed as
0644), holds exactly the events in order, and `grep` finds neither the password nor any session token in it; a symlink
`--audit` is refused with exit 1; SIGTERM writes `shutdown reason=signal`, `--ttl 200ms` writes `reason=ttl`.

**Done when:** the gateway enforces 5→15 min per login and 20→exit 3 in total, and writes every event above to a 0600
audit file without secrets.

#### Task 2.1.2: Idle logout from real browser input

- [x] Done

**Context:** A session lives as long as the browser keeps its cookie (`auth.go:88-91`, no `Expires`); nothing tracks
activity. WebSockets pass through `httputil.ReverseProxy` (`server.go:49-82`), which hijacks the client connection;
`http.Server.Shutdown` does not close hijacked connections, so closing a session today leaves its terminal running.
ttyd pings the browser every 5 s (`ttyd -P`, default 5) and the browser answers with pong frames, so "bytes read from
the browser" is never idle; only data frames are user input (ttyd's client sends keystrokes, mouse and resizes as
binary/text frames). Builds on Task 2.1.1's session code and audit log, so it follows it.

**Implementation vision:**
- Sessions become `map[string]*session` with `login string`, `last time.Time` and `conns map[net.Conn]struct{}`.
  `session(r)` (`auth.go:70-80`) also refreshes `last` when it succeeds: any authenticated HTTP request is activity.
- New `gateway/idle.go`: for an `Upgrade` request that passed authentication, `ServeHTTP` hands the proxy a
  `ResponseWriter` wrapper whose `Hijack` calls the real one (through `http.NewResponseController`) and wraps the
  returned `net.Conn` in `activityConn`, registered under the session; `Close` unregisters it. `activityConn.Read`
  feeds the bytes to a small incremental RFC 6455 frame-header parser (client frames: FIN/opcode byte, masked length
  7/16/64-bit, 4-byte mask, payload skipped by count across reads). A frame with opcode 0, 1 or 2 and a non-empty
  payload marks the session active; ping, pong and close (8, 9, 10) do not. A malformed header stops parsing for
  that connection and counts nothing further (the upstream decides what to do with it). Before writing it, read
  `$(go env GOROOT)/src/net/http/httputil/reverseproxy.go` (`handleUpgradeResponse`): if bytes already buffered in
  the hijacked `bufio.ReadWriter` reach the backend, they go through the same parser.
- `--idle 30m` (`config.idle`, must be > 0, default 30 min). A sweeper goroutine started in `run` ticks every
  `min(idle/4, 30s)`: each session with `now - last > idle` is deleted, its conns closed, `idle-out user=… sid=…`
  audited. The cookie is not cleared (it no longer matches anything); the next request gets the login form or 401.
- Logout closes the session's conns too (its terminal ends with it). Shutdown (ttl, signal, kill switch) closes every
  registered conn after `srv.Shutdown`/`srv.Close`, so `run` never returns with a terminal still proxied.
- Edge cases: two tabs on one session (both conns closed together); a WebSocket whose session was deleted while the
  upgrade was in flight (closed right after registration when the session is gone); output-only sessions (a long
  Claude run printing for an hour with no key pressed) idle out by design, and the docs say so; clock moves are
  irrelevant (`time.Now` monotonic).

**Files:**
- Create: `gateway/idle.go`, `gateway/idle_test.go`
- Modify: `gateway/auth.go` (session struct, `session`, `serveLogout`), `gateway/server.go` (wrap upgrades),
  `gateway/main.go` (`--idle`, sweeper, closing conns on every exit path)
- Test: `gateway/idle_test.go`

**Verification:** RED first (`verify-2.1.2.zsh` runs the new tests on the 2.1.1 code). `cd gateway && gofmt -l . &&
go vet ./... && go test ./... -count=1 -race`: the frame parser on frames split at every byte boundary, 16- and 64-bit
lengths, masked payloads; through the real proxy with `--idle 300ms` and the fake ttyd sending a ping every 50 ms that
the test client answers with pongs → the session idles out, the WebSocket is closed, `idle-out` is audited, the
cookie then gets 401; the same with a data frame every 100 ms → still alive after 1 s; logout closes the WebSocket;
SIGTERM with an open WebSocket → `run` returns and the client sees EOF.

**Done when:** a browser that sends no input for `--idle` loses its session and its terminal, pings notwithstanding.

#### Task 2.1.3: `remote` uses them: TTL and idle settings, audit trail, alerts

- [x] Done

**Context:** `remote on` (`bin/lzr1-panel:1653-1776`) defaults `ttl=8h` (`:1655`) and starts the gateway at
`:1729-1733` with `--log $RDIR/remote.log`; it has no `--audit`/`--idle`. The watcher `_remote_watch`
(`:853-865`) polls the gateway, `serve` and Funnel every second, then runs `_remote_off`. OS notifications go through
`_notify_one TITLE SUBTITLE BODY GROUP` (`:529-545`: terminal-notifier, osascript or notify-send; silent without
one); `notify_alerts` (`:492-509`) gates it on `LZR1_NOTIFY`. `_remote_status` (`:880-898`) prints the session.
`_remote_off` (`:797-823`) stops the watcher first, so a `remote off` you typed yourself is never seen by it.

**Implementation vision:**
- `remote on`: `ttl=${LZR1_REMOTE_TTL:-8h}` and `idle=${LZR1_REMOTE_IDLE:-30m}`, both through the existing
  `([0-9]##[hms])##` check (the error names the variable when the value came from it); `--ttl` still wins. Pass
  `--audit $RDIR/audit.log --idle $idle`. Before starting the gateway: create `audit.log` 0600 if missing, keep only its
  last 2000 lines (`tail` into a `umask 077` temp file, `mv`), and store its size in bytes as `RS[auditpos]` so the
  watcher and `status` read only this remote's events. The usage line, `die` messages and the header comment name the
  two variables.
- `_remote_audit`: reads complete lines after a byte offset (`tail -c +$((pos + 1))`, cut at the last newline, the
  offset advanced by the bytes consumed) into `reply`. Each line is split with `${(s: :)}`; only the fixed event names
  are acted on, values are stripped of control characters and passed to `_notify_one` as arguments only (never in a
  script), like `notify_alerts`.
- `_remote_watch`: every tick, after the existing checks, `_remote_audit` from its offset and `_remote_alert` each
  event; when the loop ends (the gateway or `serve` gone), one final read before `_remote_off`, so `lockout` and
  `shutdown` written just before the exit are not lost. Notifications (title `lzr1 remote`, group `lzr1-remote`):
  `login` → "LOGIN logged in"; failures of one tick → one "N wrong passwords (LOGIN, …)", at most one per 30 s, the
  rest added to the next; `lockout` → "LOGIN locked out until HH:MM (5 wrong passwords)"; `shutdown
  reason=killswitch` → "turned off: 20 wrong passwords"; `reason=ttl` → "expired, turned off"; gateway gone without a
  `shutdown` line → "the gateway stopped; turned off"; the Funnel case (`:859-862`) → "Funnel appeared; turned off".
  `logout` and `idle-out` are recorded, not notified. They follow `LZR1_REMOTE_NOTIFY` (default 1), not
  `LZR1_NOTIFY`: turning off session alerts must not silence a brute-force alert.
- `_remote_status`: two more lines from the audit since `auditpos`: "logins: N (last LOGIN at HH:MM)" and, when any,
  "failures: N; locked: LOGIN until HH:MM". After the kill switch the remote is off, and `status` says why ("off: the
  kill switch turned it off at HH:MM", from the last `shutdown` line in the file).
- `remote.log` keeps start errors, upstream errors and refused logins; the audit trail is `audit.log`. Both stay after
  `off`.
- `docs/remote.md`: the usage block and environment (`LZR1_REMOTE_TTL`, `LZR1_REMOTE_IDLE`, `LZR1_REMOTE_NOTIFY`);
  rows in "Safety layers" for the lockout, the kill switch and the idle logout; an "Audit and alerts" section with the
  line format of the contract above; the sentence at `docs/remote.md:78` points to `audit.log`.
- Edge cases: the audit file trimmed under a running watcher cannot happen (trim only in `on`, which refuses while on);
  an audit line without a final newline is left for the next tick; a missing audit file (deleted by hand) → no
  events, no error; no notifier on the machine → silent (as `_notify_one` already is); a killed watcher → the keeper
  still unpublishes, and the next `status` reports the kill switch from the file.

**Files:**
- Modify: `bin/lzr1-panel` (header and usage, `remote on`, `_remote_watch`, new `_remote_audit` and `_remote_alert`,
  `_remote_status`), `docs/remote.md`

**Verification:** `zsh -n bin/lzr1-panel lib/splash.zsh`. `scratchpad/remote/verify-2.1.3.zsh`, RED on the 2.1.2
code, then GREEN: in an isolated `HOME`, `TMUX_TMPDIR`, `LZR1_CONFIG`, `XDG_CACHE_HOME` with `LZR1_PRS=0` and a stub
`terminal-notifier` first on `PATH` that appends its arguments to a file, `tmux ls` recorded before, a `trap` running
`remote off` and checking `tailscale serve status` on any exit: `LZR1_REMOTE_TTL=15m bin/lzr1-panel remote on --allow
lazari@lerian.studio`; `status` shows the expiry 15 min ahead; `curl` POSTs to `https://<host>/<uuid>/login` with
`Origin: https://<host>`: the right password once (303), then 5 wrong (the 5th 429), then 14 more → the 20th answers
503; within 3 s `tailscale serve status` says "No serve config", `remote status` says the kill switch turned it off,
the stub recorded a login, at least one wrong-password, a lockout and a kill-switch notification, `audit.log` is 0600
and holds no password. Then the idle rule with a real browser: `LZR1_REMOTE_IDLE=1m remote on` again, headless Chromium
logs in (the spike's `lib122.mjs` pattern), the page sits idle 80 s → `idle-out` in `audit.log`, the browser's `lzr1-*`
session gone from the isolated `lzr1` server, a reload shows the login form; then `remote off`. Both runs end with "No
serve config", and `tmux ls` on the real server unchanged.

**Done when:** a real wrong-password burst locks out, then triggers the kill switch, with notifications on this
computer; an idle browser is logged out; `LZR1_REMOTE_TTL` sets the expiry.

### Epic 2.2: Terminal fidelity fixes

**Goal:** every failure in Phase 1's fidelity table is fixed or documented with a workaround (likely candidates:
right-click reaching fzf instead of the browser menu via ttyd's client options, OSC 52 copy to the browser
clipboard, a narrower layout for phone widths, `t` new-tab action hidden when remote).
**Scope:** `gateway/` (a client script injected into ttyd's page), `bin/lzr1-panel` (`open`, `_viewer`, `_select`,
`_ticker`, `_focus`, `_menu`, `_new`, `_panel`, `keys`, new `_fit`/`_back`), `docs/remote.md`, `README.md`
**Dependencies:** Phase 1 (Task 1.2.2's table); after Epic 2.1, since Task 2.2.1 changes the same gateway files
**Done when:** the fidelity table is all pass or documented workaround, re-measured on the same browsers.
**Status:** Done

Which task fixes which row of the fidelity table (`docs/remote.md:109-127`): **Copy** and **Right-click** → 2.2.1;
**Ctrl-b d** and **Footer** → 2.2.2; **Double-click** and **Reconnect** → 2.2.3; **Phone-width** and the re-measured
table → 2.2.4. Safari, phone and browser-key cells stay "needs a person" unless one is available.

#### Task 2.2.1: A small client script in ttyd's page: copy to the browser, no browser menu on the terminal

- [x] Done

**Context:** ttyd 1.7.7's page bundles xterm.js and exposes it as `window.term` (checked: the index holds
`window.term=t`, one `</head>`, one `</body>`, and registers OSC handlers of its own, but none for 52). It serves the
page uncompressed (730 KB, `content-type: text/html`, no `content-encoding`) when the request has no
`Accept-Encoding: gzip`, gzipped otherwise. The gateway proxies it (`gateway/server.go:49-82`; `ModifyResponse`
`:73-76` only sets headers). Copies inside reach the `lzr1` tmux server as OSC 52 and are forwarded to its client
(`set-clipboard on` and `terminal-features *:clipboard`, `bin/lzr1-panel:1141-1143`), i.e. ttyd's pty, where xterm.js
drops them; in a browser's layout `open` also installs a `pane-set-clipboard` hook piping every copy to `pbcopy` on
this Mac (`:1137-1139`, `:1144`). The page does not cancel `contextmenu`, so Chrome draws its own menu over the panel's.
xterm.js forces its own selection over an app's mouse mode only with Option on macOS when
`macOptionClickForcesSelection` is set (default off), Shift elsewhere.

**Implementation vision:**
- **Gateway injection, not `ttyd -I`.** `-I` would serve a copy of ttyd's generated 730 KB index kept in this repo,
  which must match the ttyd binary's client protocol: a `brew upgrade ttyd` would silently break it. The gateway
  already sits in front of the page; adding one `<script>` tag to ttyd's own index keeps ttyd's client and server in
  step, and the script stays a small file under review in `gateway/`.
- `gateway/client.js` (embedded with `//go:embed`), served at `GET /UUID/lzr1-client.js` (same identity, Origin and
  session checks as any proxied path, `Content-Type: text/javascript`). For `GET /UUID/` exactly, the proxy request
  drops `Accept-Encoding`, and `ModifyResponse` (status 200, `text/html`) reads the body (refusing more than 4 MiB:
  passed through unchanged, logged), inserts `<script src="lzr1-client.js"></script>` right after the first `<head>`
  (or at the very start when there is none), fixes `Content-Length`, removes `ETag`. A gzipped answer (a future
  ttyd ignoring the missing header) is decoded with `compress/gzip` first and sent plain. The CSP already allows
  same-origin scripts.
- `client.js` runs before ttyd's bundle: (1) wraps `window.WebSocket` to see ttyd's socket; on its `close`, a
  `HEAD lzr1-client.js` that answers 401 means the session is gone (logout, idle-out, kill switch) → `location =
  'login'`; otherwise ttyd's own reconnect proceeds. (2) Polls for `window.term` (50 ms, 10 s) and then:
  `term.parser.registerOscHandler(52, …)`: the payload `PC;BASE64` with any `Pc`; `?` (a read request) is ignored, so
  no program can read the browser's clipboard; base64 is decoded to UTF-8 with `TextDecoder`; more than 1 MiB is
  dropped; `navigator.clipboard.writeText` first; when the browser refuses (Safari wants a user gesture), a small
  fixed toast "Copied text ready — click to copy" whose click calls `writeText` and which disappears after 15 s or the
  next copy; returns `true`. `term.options.macOptionClickForcesSelection = true` (Option-drag selects in xterm.js on a
  Mac; Cmd-C copies it). (3) `document.addEventListener('contextmenu', e => { if (e.target.closest?.('.xterm'))
  e.preventDefault() }, true)`: the right button still reaches xterm.js and so fzf.
- `open`: in a browser's layout (`LZR1_REMOTE` set) no `clip` hook on the `lzr1` server: copies reach the browser
  through OSC 52, not this Mac's `pbcopy`. The session's own tmux keeps `CLIP`'s hook (`:102-112`): it is shared with
  the panel on this screen, so a copy in a browser's viewer of a session on this Mac still also reaches this Mac's
  clipboard; `docs/remote.md` says so (the fidelity row's "overwriting that Mac's clipboard" becomes a documented
  limit for local sessions, gone for sessions on other machines).
- Edge cases: an OSC 52 split across reads (xterm.js's parser handles it); empty payload (clears nothing, ignored);
  invalid base64 (ignored); a login page or error page (not ttyd's index) is never modified; `HEAD` of the script
  unauthenticated → 401, not a redirect; the script must not throw when `window.term` never appears (the 502 page).

**Files:**
- Create: `gateway/client.js`, `gateway/inject.go`, `gateway/inject_test.go`
- Modify: `gateway/server.go` (route, `Rewrite` dropping `Accept-Encoding` for the page, `ModifyResponse`),
  `bin/lzr1-panel` (`open`: no `clip` hook when `LZR1_REMOTE` is set), `docs/remote.md` (how copy works remotely)

**Verification:** RED first: `verify-2.2.1.js` (Playwright, the spike's isolated `remote on` through the tailnet URL,
allowlist only lazari@lerian.studio, `--ttl 15m`, `remote off` in `finally`) fails on the current code. Then
`cd gateway && gofmt -l . && go vet ./... && go test ./... -count=1 -race` (the tag inserted once after `<head>`;
`Content-Length` right; a gzipped upstream page decoded; a non-HTML or non-200 answer untouched; the script route
needs a session); and the browser run: the stand-in Claude's `copy` → `navigator.clipboard.readText()` gives
`osc52-from-<name>`; a tmux drag-select in the viewer → the clipboard holds the dragged text; the stand-in `pbcopy`
of the `lzr1` layer is not called for a session on a second (stand-in remote) profile; a right-click on the panel →
`window.__ctx` shows `defaultPrevented` true and the row's menu opens; Alt-drag over the viewer → `term.getSelection()`
non-empty; logging out from another tab → the page goes to `login` within 2 s. Ends with "No serve config" and `tmux
ls` unchanged.

**Done when:** copies made in the panel or a session land on the browser's clipboard, and the right button opens only
the row's menu.

#### Task 2.2.2: Ctrl-b d after an in-place switch; the first footer click after a menu

- [x] Done

**Context:** Both fail in a plain terminal too. **Ctrl-b d:** `_viewer` (`bin/lzr1-panel:1156-1211`) attaches
`$target`; `_select` (`:1213-1246`), for another session on the same machine, switches the attached client in place
(`:1237-1241`) and writes the new key to `$tfile`. When the attach then ends (Ctrl-b d), the viewer sees
`$tfile != $target` and takes it as "the panel moved on" (`:1196`), so it attaches `$tfile`'s session again at once.
**Footer:** reproduced with `scratchpad/remote/p2plan/footer-probe3.zsh` (isolated tmux, mouse bytes typed in): after a
row's menu, the first click on any footer button is lost, whether the menu was opened by right-click or `m`, closed by
a click or Esc, and whether the click comes 0.5 s or 8 s later or after a row click; a key (`?`) right after the menu
works, and the second footer click works. The menu is `execute($S _menu …)+reload-sync(…)` (`:1555`), a child `fzf`
(`:1453-1456`); `_new`'s pickers (`:1296`, `:1305`) are children the same way. The footer handler is a `transform`
on `$FZF_CLICK_FOOTER_WORD` (`:1580-1586`).

**Implementation vision:**
- **Ctrl-b d:** the viewer writes `$tfile.on` (the key its client is on) before each attach; `_select`'s in-place
  branch writes it too, only when `switch-client` succeeded (the remote script prints `switched`; `true` today hides a
  failure); `_menu`'s rename updates it with `$tfile`. After the attach returns, the viewer reads `.on`: when it
  equals `$tfile`, the client was on the current pick, so this was a detach (or the session ended) of that session:
  `target` becomes it and the existing gone/detached path runs (`:1197-1209`). Only when `.on` differs from `$tfile`
  did the panel move on (a cross-machine pick, `viewer_detach`). The `EXIT` trap removes `.on`.
- **Footer:** root cause first, then fix where the cause is. The probe narrows it in this order, each a few minutes:
  (1) log `$FZF_CLICK_FOOTER_WORD` from the transform: if the first click arrives with an empty or stale word, the
  transform falls through `case`: the fix is a default branch that maps by column (`$FZF_CLICK_FOOTER_COLUMN`, or
  the word nearest to it), kept in `_panel`; (2) if the transform is not run at all, drain `/dev/tty` for 100 ms
  after the child `fzf` in `_menu` exits, logging what it reads: if stray terminal replies left by the child (cursor
  position, mode reports) show up and the click then works, keep the drain in `_menu` and in both `_new` pickers; (3) if neither,
  it is fzf's (a bare `fzf --footer` with `execute(fzf …)` reproduces it): file it upstream at junegunn/fzf with the
  reduced script, and the workaround is documented: the footer row of the fidelity table and `keys` say "after a
  menu, the first footer click only wakes the footer: click again". The task records which of the three it was under
  Deviations.
- Edge cases: Ctrl-b d in a session the viewer attached itself (works today, must keep working); a switch in place to
  a session that ended meanwhile (`switch-client` fails: `.on` keeps the old key, the viewer reattaches `$tfile`, as
  today); a detach right after a cross-machine pick (`.on` is the old machine's: the panel moved on, attach the new one).

**Files:**
- Modify: `bin/lzr1-panel` (`_viewer`, `_select`, `_menu`, `_new`, `_panel` only if the cause is (1), `keys` only if
  (3)); `docs/troubleshooting.md` (the footer, if (3))

**Verification:** `zsh -n bin/lzr1-panel lib/splash.zsh`. `verify-2.2.2.zsh` (an isolated `HOME`, `TMUX_TMPDIR`,
config and cache as `scratchpad/remote/p2plan/env.zsh`; sessions it made killed by exact name; `tmux ls` on the real
server compared before and after), RED on the current code then GREEN: two sessions on one profile; click the first
(viewer attaches), click the second (switched in place), `Ctrl-b d` typed into the viewer pane → after 2 s the viewer
pane shows "← pick a session" and no client is attached to either session; the same after an Enter-attach; then
`footer-probe3.zsh 1 menu` → "[?] works" (or, in case (3), the reduced upstream script reproduces it in bare fzf).

**Done when:** Ctrl-b d always leaves the session, and a footer click right after a menu works (or is documented as
fzf's with its workaround).

#### Task 2.2.3: Double-click focuses the viewer; reconnect gets a working panel on the same session

- [x] Done

**Context:** Both in the tmux layout `open` makes (`bin/lzr1-panel:1114-1153`), on the `lzr1` server started with
`-f /dev/null`, so tmux's default mouse bindings apply. **Double-click:** fzf's `double-click` runs `_go` (`:1569`),
which focuses the viewer (`_focus`, `:1344-1349`); then tmux's default `DoubleClick1Pane` (`select-pane -t = ; …`),
which fires after the double-click delay, puts the keyboard back on the panel. `local-dblclick.zsh` (spike) shows it
in a plain terminal. **Reconnect:** when a browser's WebSocket closes, its layout ends (`destroy-unattached`,
`:1151-1152`); if it was the last session, the `lzr1` server exits while ttyd's reconnect already runs a new `open`,
whose `new-session` (`:1140`) fails with "server exited unexpectedly" → `die "cannot start tmux"`. The new panel also
starts on the first session (`_ticker`, `:1248-1266`), not the one the browser was showing.

**Implementation vision:**
- `open`, with the other `$T set …` (it runs for every layout, idempotent): rebind `DoubleClick1Pane` and
  `TripleClick1Pane` on the `lzr1` server to tmux's defaults minus `select-pane`: `if -F
  '#{||:#{pane_in_mode},#{mouse_any_flag}}' { send -M } { copy-mode -H ; send -X select-word ; run -d0.3 ; send -X
  copy-pipe-and-cancel }` (and `select-line` for the triple). A single click still selects the pane
  (`MouseDown1Pane` untouched), so clicking the viewer still moves the keyboard there.
- `open`: `new-session` is retried up to 20 times, 0.25 s apart, when it fails, then checked with `has-session`
  after the `set` commands (a session made on a server that died in between is made again, at most 3 rounds); only
  then `die`. No `exit-empty off`: the server still exits when nothing uses it.
- Last pick per browser remote: `open` writes `$STATE/$id.remote` (the UUID) in a browser's layout; `_select` writes
  the key it shows to `$RDIR/$uuid.last` when that file exists (`$RDIR` is 0700); `_ticker`'s first pick prefers that
  key when the panel lists it, else the first row as today, and moves the highlight there with `_pos`. `_remote_off`
  removes `$RDIR/*.last`. Shared by the browsers of one remote (at most 4): the last one wins, which is what a
  reconnecting browser wants.
- Edge cases: a local panel opened while a browser's layout is dying (same retry); `new-session` failing for a real
  reason (no tmux, a bad socket dir) still dies after ~5 s with tmux's own message; a `.last` key whose session ended
  (falls back to the first row); `_select` in a local layout never writes `.last`.

**Files:**
- Modify: `bin/lzr1-panel` (`open`, `_select`, `_ticker`, `_remote_off`)

**Verification:** `zsh -n bin/lzr1-panel lib/splash.zsh`. `verify-2.2.3.zsh` (isolated as in 2.2.2), RED then GREEN:
the double-click of `local-dblclick.zsh` → the active pane is the viewer for the whole 2.5 s sampled after it; a
triple-click on the viewer does not move the keyboard; the reconnect: an isolated `open` with `LZR1_REMOTE=<uuid>`
attached from a throwaway outer session, a second session picked; kill the outer session (the browser leaving) and at
once start a new `open` with the same `LZR1_REMOTE` → it starts (no "cannot start tmux") and its viewer shows the second
session. Then `verify-2.2.3.js` through the tailnet (as in 2.2.1): close the page's WebSocket → ttyd reconnects
without a second Enter and the viewer shows the session picked before.

**Done when:** a double-click leaves the keyboard in the viewer, and a reconnecting browser gets a working panel on
the session it was showing.

#### Task 2.2.4: A narrow layout for phones; the fidelity table measured again

- [x] Done

**Context:** The tmux layout is a `PANEL_W`-column panel (`bin/lzr1-panel:36`, default 56) split left of the viewer
(`:1149`); tmux squeezes it when the window is narrower, and at 390 px (52 columns) the panel is 1 column wide and the
list disappears. `list_rows` sizes rows to `$FZF_COLUMNS` (minimum 40, `:595`). `_focus` (`:1344-1349`) selects the
viewer pane; the viewer's idle screen (`:1166-1170`) says to pick in the panel. The `lzr1` server's options are set by
every `open` (`:1141-1143`); hooks and status are per session. Tasks 2.2.1-2.2.3 changed `open`, `_viewer` and
`_select` before this one.

**Implementation vision:**
- Narrow when the window is under `LZR1_NARROW` columns (default `PANEL_W + 30` = 86), in every tmux layout (a narrow
  plain terminal had the same broken list; Ghostty splits are untouched). `open` sets on its session the hooks
  `client-attached` and `client-resized` → `run-shell -b "$SELF _fit $id"`.
- `_fit ID`: reads `#{window_width}` of `lzr1-ID`. Narrow: touch `$STATE/$id.narrow`; when the window is not zoomed,
  zoom the panel (`select-pane` it, `resize-pane -Z`); `status on` for that session with `status-left` " ◀ panel ·
  tap here" (`status-right ''`). Wide: remove `.narrow`, unzoom, `status off`, `resize-pane -t <panel> -x $PANEL_W`.
  Idempotent; a resize while the viewer is zoomed keeps the viewer zoomed.
- On the `lzr1` server, `bind -n MouseDown1Status run-shell "$SELF _back '#{session_name}'"`: `_back` takes the ID from
  `lzr1-ID`, selects the panel pane and zooms it (only when `.narrow`; else nothing). `_focus` in narrow mode selects
  the viewer and zooms it. The viewer, in narrow mode, calls the same as `_back` when it goes idle (Ctrl-b d, the
  session ended, nothing picked).
- `_panel`: `--bind "resize:reload-sync($S _list $id)"` so rows are re-cut to the new width; `left-click` in narrow
  mode acts as Enter (`transform: [ -e $STATE/$id.narrow ] && echo "execute-silent($S _go $id {1})"`), since the viewer
  is hidden and a tap that only moves the highlight would show nothing; wide, unchanged.
- `docs/remote.md`: re-run the spike's Chromium script (`spike-1.2.2.mjs`, updated for the fixes) on the same items and
  rewrite the table with the new results (Safari and phone columns stay "needs a person" unless one is measured);
  replace the "For Phase 2" paragraph with what is left, if anything. `keys` gains a line on the narrow layout.
- Edge cases: rotating a phone (narrow ↔ wide back and forth, zoom state follows); `LZR1_NARROW=0` turns it off;
  `LZR1_PANEL_WIDTH` changed (the default threshold follows it); several browsers at different widths (hooks and
  status are per session: each layout decides alone); the status line takes a row (`list_rows` already re-cuts on
  `resize`); a tap on the status line of a wide layout does nothing.

**Files:**
- Modify: `bin/lzr1-panel` (`open`, new `_fit` and `_back`, `_focus`, `_viewer`, `_panel`, `keys`), `docs/remote.md`
  (fidelity table, narrow layout), `README.md` (the `LZR1_NARROW` variable, next to `LZR1_PANEL_WIDTH`)

**Verification:** `zsh -n bin/lzr1-panel lib/splash.zsh`. `verify-2.2.4.zsh` (isolated as in 2.2.2), RED then GREEN:
an outer session 52×30 running `open` → the panel pane is zoomed and lists every session with its name readable; a
tap (mouse bytes) on a row → the viewer zoomed with that session; a tap on the status line → the panel back;
`resize-window -x 180` → unzoomed, panel 56 wide, status off. `verify-2.2.4.js`: Chromium at 390×844 through the
tailnet URL (as in 2.2.1) → the list is readable, a tap shows the session, the status line brings the panel back;
then the full spike re-run, whose results are the new table. Ends with "No serve config" and `tmux ls` unchanged.

**Done when:** the fidelity table is all pass or documented workaround in Chromium, re-measured, and a phone-width
browser gets a usable panel.

---

## Phase 3 — In the panel, installed and documented

### Epic 3.1: Panel key, footer button and remote menu

**Goal:** `w` and a `[web]` footer button turn remote on (asking the allowlist in a small popup) and copy the URL;
when on, the same key opens a menu: copy URL, copy password, who is connected (from the audit log), rotate the
password (restarts the gateway, ends sessions), turn off; the summary line shows `⇄ web` while it is on.
**Scope:** `bin/lzr1-panel` (`_panel` binds and footer, `_menu`-style picker, `keys`, `list_rows` summary)
**Dependencies:** Phases 1-2
**Done when:** every action works from the key, the footer and the menu, in both Ghostty and tmux layouts.
**Status:** Pending

### Epic 3.2: Installer, doctor and docs

**Goal:** `install.sh` offers ttyd and Go and builds the gateway; `doctor` gets a "remote" section (Tailscale
found and running, HTTPS certificates on, ttyd, gateway built, Linux operator permission, remote on/off); README,
troubleshooting and `docs/remote.md` cover setup, use, safety and turning it off.
**Scope:** `install.sh`, `bin/lzr1-panel` (`doctor`), `README.md`, `docs/troubleshooting.md`, `docs/remote.md`
**Dependencies:** Epic 3.1
**Done when:** a fresh `./install.sh` on macOS and Linux leaves `lzr1-panel remote on` working, and `doctor` reports
each missing piece with its fix.
**Status:** Pending

## Deviations

- Task 1.1.1: no `login`/`logout` routes yet (Task 1.1.2); until then every path under `--base` needs a session or
  gets 401. A minimal session store (`newSession`, `authenticated`) lives in `server.go` for 1.1.2 to extend.
- Task 1.1.1: `ready` is printed only once ttyd's socket answers (the contract's "both sockets usable"); SIGTERM or
  `--ttl` before that exits 0 without `ready`. `/UUID` redirects 308 to `/UUID/`; an unclean or percent-encoded path
  is 404. The CSP also sets `frame-ancestors 'none'; base-uri 'none'; form-action 'self'`.
- Task 1.1.1 live finding for Task 1.2.1: on this Mac (Tailscale.app 1.104) `tailscale serve --set-path /UUID
  unix:SOCK` was accepted but every request answered 502, for sockets in `$TMPDIR`, `/private/tmp` and `~/.cache`,
  with 0700/0600 and 0755/0666 modes alike. The macOS app apparently cannot reach a Unix-socket upstream; Task 1.2.1
  must confirm and probably give the gateway a loopback TCP listener (`127.0.0.1:0`, still behind the login).
  Whether `serve` strips the `/UUID` mount path before proxying is still unverified.
- Task 1.1.2: the 1.1.1 proxy tests now send `Tailscale-User-Login` (and `Origin` on upgrades), since identity is
  checked before the session; without it they get 403. A request with two `Tailscale-User-Login` headers is 403.
  Origin is required on every non-GET/HEAD/OPTIONS method and every `Upgrade`, not only POST and WebSocket, and must
  equal `https://` + the request's `Host` exactly: Task 1.2.1 must confirm `tailscale serve` passes the browser's
  Host through (if it rewrites it, compare against the published `*.ts.net` name instead). Wrong password re-renders
  the form with 401; other methods on `login`/`logout` get 405; `logout` only deletes a session bound to the caller.
- Task 1.2.1: confirmed live that the macOS app's `serve` cannot use a Unix-socket upstream, so the gateway's
  `--listen` also takes a loopback `IP:port` (non-loopback → exit 2); with `127.0.0.1:0` it prints `ready
  127.0.0.1:PORT` instead of `ready` (`TestListenTCP`). ttyd stays on its Unix socket. Live facts: `serve` strips its
  mount path, so the target is `http://127.0.0.1:PORT/UUID`; it passes the browser's `Host` (the `*.ts.net` name),
  `X-Forwarded-Host/Proto` and `Tailscale-User-Login`, so the Origin check works as built. Cost: a local process can
  reach the port and claim any login (it still needs the password); `docs/remote.md` says so.
- Task 1.2.1: Tailscale's status and serve JSON are read with `jq` (the installer already installs it). Added
  `remote build` (used by `install.sh`). `open` no longer deletes `$STATE/remote` in its day-old cleanup, and a
  browser's layout (`LZR1_REMOTE`) sets `destroy-unattached` and `@lzr1_remote UUID` at attach time (set on a detached
  session, tmux destroys it at once) so it ends with the browser and `remote off` finds it. `remote on` while on prints
  the status and exits 1. The session file also holds `host`, `port`, `gwpid`, `ttydpid`; `remote.log` and `ttyd.log`
  (0600, no secrets) stay after `off`. The live check (only lazari@lerian.studio allowed, `--ttl 10m`) did not open a
  WebSocket, since that starts `lzr1-*` sessions on the real `lzr1` tmux server: a browser's panel and its cleanup
  are first exercised by Task 1.2.2.
- Task 1.2.2: measured with headless Chromium only (Playwright, real mouse events at cell coordinates); Safari and phone
  cells say "needs a person", and the laptop sleep was simulated by closing the WebSocket. It ran in an isolated HOME,
  TMUX_TMPDIR, config and cache, with a stand-in `claude` and `pbcopy`. Not docs-only: login never worked in Chrome,
  which sends `Origin: null` on the form's POST under `Referrer-Policy: no-referrer`, so the gateway (and `login.html`)
  now send `same-origin` (`TestLoginForm` checks it). Double-click, Ctrl-b d after a switch in place and the first
  footer click after a menu fail in a plain terminal's tmux layout too: panel bugs, listed for Epic 2.2.
- Review fixes (Epic 1.2): `serve` runs in the foreground (no `--bg`), not as Task 1.2.1 says: Tailscale keeps a
  foreground path only while that process lives (checked live: `kill` and `kill -9` both leave "No serve config"; a
  reboot was not tested, by the same rule nothing should survive it). A watcher subshell (`watchpid`, HUP ignored like
  `serve`) ends `serve` and runs `off`'s cleanup as soon as the gateway exits (`--ttl`, a crash) or `serve` does, so
  expiry no longer waits for `status` or `on`. `_remote_off` confirms with `serve status --json` that `/UUID` is in no
  handler (else tries `serve ... off`); if Tailscale cannot confirm it keeps the session file and the gateway, prints
  the manual command and returns 1, so `remote off` exits 1, `status` says the path may still be published and `on`
  refuses. The Funnel check now looks inside foreground configs too. Verified by `verify-review-fixes.zsh` (a stub
  failing `tailscale`, then live with `--ttl 20s`).
- Phase 1 open findings: processes are matched by their full command line holding the UUID (`ps -o args=`), never by
  name, before every signal, KILL included: the watcher is its own process, `lzr1-panel remote watch UUID` (an
  internal subcommand), the gateway has `--base /UUID/`, ttyd `-b /UUID`, `serve` `--set-path /UUID`. `serve` is no
  longer started by `remote on` itself: a keeper reading the gateway's stdout starts it on `ready` and ends it when
  that stdout closes (the gateway exited), so the path dies with the gateway even when the watcher was killed (TERM and
  KILL checked live). The gateway's pid comes from `gw.pid`, written by the subshell that execs it. Funnel is checked
  before anything is published, again after, and by the watcher every 10 s; a status that cannot be read counts as
  Funnel on. `_remote_off` now always stops the watcher, `serve`, the gateway, ttyd and the browsers' layouts, then
  unpublishes; when Tailscale cannot confirm, only the session file is kept (the plan had it keep the gateway too).
- Phase 1 open findings: on Linux the gateway is back on its 0600 Unix socket (`serve ... unix:SOCK`); the 127.0.0.1
  listener is macOS only. Tailscale's source (`ipn/ipnlocal/serve.go`) shows that for a `unix:` target `serve` strips
  the mount path and sends `Host: localhost`, so the gateway gained `--stripped` (put `--base` back) and `--host`
  (the published name `Origin` must match; now passed on macOS too). Not run on a Linux machine: checked with a
  stand-in `tailscale` and curl over the socket. Over a Unix socket `serve` forwards both `/UUID` and `/UUID/` as `/`,
  so a URL typed without the final slash gets the page with broken relative links; the URL given always has it. The
  allowlist accepts any single-`@` login without spaces (`user@github`, `name@passkey`). `--allow`/`--ttl` without a
  value die with the usage.
- Phase 2 elaboration (decisions beyond the epic text): attempts made while a login is locked are refused unchecked but
  count toward the kill switch's 20, so waiting out lockouts cannot outlast it; the gateway exits 3 on the kill switch;
  the audit trail is `audit.log` (the events of Epic 2.1, a fixed line format) beside `remote.log`, which keeps
  errors and refused logins; idle means no data frame from the browser (ttyd's pings and their pongs do not count),
  so a session that only prints idles out; idle is `LZR1_REMOTE_IDLE` (default 30m); remote alerts follow
  `LZR1_REMOTE_NOTIFY`, not `LZR1_NOTIFY`, and a `remote off` you typed is not notified. Epic 2.2 reaches into
  `gateway/`: the copy and right-click fixes are a script the gateway adds to ttyd's own page, not ttyd client options
  or `ttyd -I` (Task 2.2.1 says why); the `t` key was already a viewer switch in the tmux layout, so it stays. The
  narrow layout applies to every tmux layout, not only a browser's. The footer click after a menu was reproduced
  without a browser and is not explained yet: Task 2.2.2 starts with its root cause.
- Task 2.1.1: the `start` line is `start ttl=…` only; Task 2.1.2 adds `idle=` with `--idle`. The gateway no longer
  writes logins, failures and logouts to `remote.log` (they are audit events), and `remote on` passes no `--audit`
  until Task 2.1.3, so in between a remote session records no logins; `docs/remote.md:78` is corrected by 2.1.3 as
  planned. A `failure` while locked carries `fails=0` (the counter was reset by the lockout). The limits are `config`
  fields (`lockAfter`, `lockFor`, `killAfter`) that default to 5, 15 min and 20 when left at zero. A kill switch firing
  before ttyd answers (the handler serves before `ready`) also exits 3, without `start`. `openAudit` also opens with
  `O_NOFOLLOW` and checks the opened file is regular, so a symlink swapped in after the `Lstat` is refused too.
- Task 2.1.2: `--idle` defaults to 30m (a `config.idle` of zero, as tests build it, means the same), and `start` is now
  `start ttl=… idle=…`. Reading `handleUpgradeResponse` showed the proxy copies from the hijacked `net.Conn` only:
  bytes buffered in the `bufio.ReadWriter` never reach ttyd, so only the conn is parsed. `activityConn` forwards
  `CloseWrite`, which the proxy looks for to half-close when ttyd ends first. The parser treats an unmasked frame, a
  reserved opcode (3-7, 11-15) or a 64-bit length with its top bit set as malformed, and ignores the RSV bits
  (permessage-deflate sets RSV1); a data frame counts once its header and mask key are read, and an empty one is not
  input. `authenticated` is gone: `ServeHTTP` uses `session`, since an upgrade needs the token. Test hooks:
  `server.track(tok, conn)`, `idleOut`, `closeAll`, and `runGatewayWith(t, upstream, args...)` in `idle_test.go`. No
  live or browser run: Task 2.1.3 does it; `docs/remote.md` (the cookie row says "no expiry") is also its to update.
- Task 2.1.3: alerts use a terminal-notifier group per kind (`lzr1-remote-login`, `-failure`, `-lockout`,
  `-killswitch`, `-ttl`, `-stopped`, `-funnel`), not one `lzr1-remote`: terminal-notifier keeps one notification per
  group, so a later alert would have replaced a login or a lockout. The subtitle is the machine's short name. Wrong
  passwords still waiting for their 30 s window are sent on the watcher's last read, so a burst's tail is reported when
  the remote ends. Beyond the plan's list: `serve` ending while the gateway still runs alerts "tailscale serve ended;
  turned off", and a `shutdown reason=signal` the watcher sees (something other than `remote off` stopped the gateway)
  alerts as "the gateway stopped; turned off". `remote on` checks `LZR1_REMOTE_TTL`/`LZR1_REMOTE_IDLE` (shared `_dur`)
  before Tailscale, prints the idle limit, and keeps `idle` and `auditpos` in the session file; `status` adds an
  `idle:` line and says "logins: none yet" before the first. There is no `--idle` flag (the variable only, as planned).
  Verification: the kill switch counts failures only, so the burst is the right password (303), 5 wrong (5th 429), the
  right password while locked (429, counted), then 14 wrong: the 20th failure answered 503 and the route was gone at
  once. Notifications were checked through a stub `terminal-notifier` first on PATH (as the plan says), with
  `LZR1_NOTIFY=0` to show remote alerts ignore it; the real notifier path is `_notify_one`, unchanged. The RED run
  passed `--ttl 15m` explicitly, since the 2.1.2 code ignores `LZR1_REMOTE_TTL` and would have published for 8 h.
- Task 2.2.1: `client.js` hooks ttyd's terminal through a `window.term` setter defined before ttyd's bundle runs, not by
  polling (it falls back to polling 50 ms for 10 s only if something defined `term` first): each terminal ttyd sets is
  hooked once, with no time limit. The `open` hook is now the layout session's own (`set-hook -t lzr1-ID`), not the
  `lzr1` server's (`-g`): a terminal's tmux layout and a browser's share that server, so a global hook from a local
  layout would still have piped the browsers' copies to `pbcopy`. A `-g` hook set by an older version on an `lzr1`
  server still running is not removed; it goes when that server exits. `remote` rebuilds the gateway when a `.js` in
  `gateway/` changed too. The page request also drops `If-None-Match`/`If-Modified-Since`, so ttyd always answers
  200 with the whole page. A page over 4 MiB passes through unchanged; a gzipped one over it is passed through decoded.
  `lzr1-client.js` answers GET and HEAD (405 otherwise); unauthenticated it is 401 (a browser navigation to it still
  gets the login redirect, as any path). The verify's "second (stand-in remote) profile" is a `type=ssh` profile with
  a stand-in `ssh` first on PATH that runs its scripts here with their own `TMUX_TMPDIR` and their own logging
  `pbcopy`. In the fidelity table only the Copy and Right-click Chrome cells changed (Task 2.2.4 re-measures the rest).
- Task 2.2.2: the footer was none of the three planned cases. fzf (0.74.2, the same on master) runs a footer click
  on the release, and only when `wasDown` is set and the press was at the same spot (`terminal.go`: `clicked :=
  wasDown && !me.Down && (mx == pmx && my == pmy)`). A row's right-click (or a left-click with
  `LZR1_MENU_BUTTON=left`) fires on the press, and the menu's `fzf` takes the release, so the panel reads the next
  press as a drag. The transform never runs, and a drain finds nothing. Bare `fzf` shows it with a right-click
  `execute` closed by Esc. The elaboration's bare probe was a false match: its click to close the menu missed the
  child's items, so the "lost" click was the child's. A second cause was found with an fzf built to log mouse events:
  `--track` with `reload-sync` drops every action until the reload ends, so a release sent inside the menu's chain
  was dropped. The fix applies to the tmux layout only (a browser's too): the menu bind is
  `execute(_menu)+execute-silent(_unclick ID PANE)`. `_unclick` types a button release into the panel's pane
  (`send-keys -l`, `\e[<0;1;1m`, a no-op at the header row), then POSTs `reload-sync(_list)` through fzf's
  `--listen` after 0.2 s, once fzf has read the release. A Ghostty split cannot be typed into, so it keeps the old
  chain, and `docs/troubleshooting.md` says to click again there. Clicks during any reload are still dropped (fzf's
  behaviour); troubleshooting mentions it. `keys` is unchanged. Nothing was filed upstream: an issue on
  junegunn/fzf is a public post for the owner to decide on. The reduced repro is
  `scratchpad/remote/v222/fzf-upstream-repro.sh` (2 of 3 footer clicks logged). Ctrl-b d follows the plan
  (`$tfile.on`, `switched` from the in-place switch, rename updates it). The verify also covers a rename after an
  in-place switch and a menu opened with `m`. The fidelity rows say "fixed, checked in a terminal's tmux layout".
  Chrome is measured again in Task 2.2.4.
- Task 2.2.3: besides `_select`, the browser's pick reaches `.last` from two more places, through one helper
  (`remote_last ID KEY`, a no-op without `$STATE/ID.remote`): the viewer moving on by itself when its session ended,
  and a rename from the row's menu. `remote on` also removes stale `*.last` files. A `.last` key the panel does not
  list falls back to the first row. `open`'s `set` and `bind` commands are now silent (2>/dev/null), since
  `has-session` decides whether to try again. When tmux cannot start, the error is tmux's message followed by
  "cannot start tmux", after about 5 s. Neither the isolated reconnect (3 rounds) nor the RED browser run hit the
  "server exited unexpectedly" race; there the new panel started but showed the first session. The retry was
  therefore checked with a stand-in `tmux` in the isolated HOME's `~/.local/bin`, which `open` puts first on PATH.
  The stand-in fails the layout's `new-session` 3 times, or removes the new session once, and the layout starts in
  both cases. The double-click check allows up to 0.6 s before the keyboard reaches the viewer, because `_go` takes
  about 0.2 s (`_select`'s debounce). After that it must stay there: RED went back to the panel after 0.3 s, and
  GREEN stayed for the 2.5 s, in a terminal and in Chrome. The triple-click check is a regression check on the
  viewer, which kept the keyboard before the change too. A triple-click on a row still ends on the panel, since its
  third press is a plain `MouseDown1Pane`. The live run allowed only lazari@lerian.studio with `--ttl 15m`, and it
  ended with "No serve config".
- Task 2.2.4: `_fit` runs one at a time (a `$STATE/ID.fit` lock folder), each reading the width when its turn comes:
  `client-attached` and `client-resized` fire together, and the attach's run, at the session's creation width, undid
  the zoom of the resize's. `open` writes the panel's pane id to `.me` itself (`split-window -P`), so `_fit` does not
  wait on `_panel`. The hooks and the status binds carry `LZR1_CONFIG`, `XDG_CACHE_HOME`, `LZR1_PANEL_WIDTH` and
  `LZR1_NARROW` in their `run-shell` command, since the `lzr1` server's environment is whichever `open` started it.
  The text is `status-left`, so the tap is bound on `MouseDown1Status`, `-StatusLeft`, `-StatusRight` and
  `-StatusDefault` (the plan named only the first); the bar is yellow and hides the window list. `LZR1_NARROW=0` sets
  no hooks at all, and a value that is not a number falls back to the default. Wide, `_fit` puts the panel back at
  `PANEL_W` on every resize (the Resize row now says the panel keeps 56 columns), and never unzooms a zoom it did not
  make. Beyond the plan: the viewer also goes back to the panel when its session ended; a tap on a machine's line or a
  PR row does nothing in narrow mode (a PR would have opened in this Mac's browser); with `LZR1_MENU_BUTTON=left` the
  left button keeps opening the menu, so there is no narrow tap. The `resize` bind is a transform that skips the
  reload while the panel is hidden behind the zoomed viewer (1 column). `keys` gains one line of at most 56 columns,
  and "Enter resumes the conversation" became "Enter resumes it": the `[?]` text fills a 40-row, 56-column panel
  exactly, and one more row pushed its title off (2.2.2's footer check caught it). Chromium at 390 px has 46 columns
  here, not the 52 of Task 1.2.2. The table was measured with a new `verify-2.2.4.js` (the spike's items, updated for
  the fixes, plus Option-drag) instead of re-running `spike-1.2.2.mjs`, whose environment and teardown (a tmux
  `kill-server`) are not allowed any more. Copy was measured on a session on this Mac only, not again through 2.2.1's
  `ssh` stand-in. Every Chrome cell passes; Safari, phone and the browser's own shortcuts stay "needs a person". The
  live runs allowed only lazari@lerian.studio with `--ttl 15m`, and each ended with "No serve config".
- Phase 2 review findings: `open` now removes `$STATE/$id.remote` with the other per-ID files, so a local panel that
  reuses a browser layout's PID no longer writes that remote's `.last` or opens on its session. The viewer's EXIT trap
  does not remove it, as the finding also suggested: `_respawn` can reopen a closed viewer in the same browser's
  layout, whose picks must still reach `.last`, and every layout passes through `open` first. `docs/remote.md` now
  states both copy directions (copies made on this Mac's screen, and any session output with OSC 52, reach a connected
  browser's clipboard) in the copy section and in "What it does not protect against". The optional focus or
  recent-input gate in `client.js` was not added (documented instead). Verified by `verify-p2-findings.zsh` (a stub
  `tmux`; no live publish).
- Phase 2 open findings (idle and clock): the frame parser now unmasks what it needs and reads ttyd's command byte:
  INPUT ('0') and RESIZE ('1') messages count, PAUSE ('2'), RESUME ('3') and the JSON auth message ('{') never do, so
  flow control sent while output streams no longer keeps a session alive. An INPUT message made only of terminal reports
  (DA `ESC[?…c`/`ESC[>…c`, CPR `ESC[r;cR`, `ESC[0n`, focus `ESC[I`/`ESC[O`, XTWINOPS `ESC[…t`, OSC 10-19 `rgb:`
  colour replies ended by BEL or ST) does not count; any other byte does, and so does an INPUT message longer than 256
  bytes (`maxReport`: replies are short, a paste is input). A fragmented message is judged by its first byte and its
  whole INPUT payload, with control frames between fragments ignored. Task 2.1.2's "clock moves are irrelevant
  (`time.Now` monotonic)" was wrong for sleep: the monotonic clock stops while the Mac sleeps. Session times are now
  wall-clock (`server.wall`, `now().Round(0)`), and `--ttl` is no longer a `context.WithTimeoutCause` timer but a
  wall-clock deadline that `server.expire` re-checks at least every 30 s, so a Mac that slept past the expiry stops
  within 30 s of waking. Login lockouts still use `now()` (a lock that outlasts a sleep is harmless). The RED for the
  clock is structural (a stored time carried `m=`; `expire` did not exist), since a test cannot suspend the machine.
  No live browser measurement of the 100 KB RESUME cadence was made in this fix.
