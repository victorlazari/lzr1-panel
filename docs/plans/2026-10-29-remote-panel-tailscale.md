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
| 1 | `lzr1-panel remote on` prints a tailnet URL and password; an allowlisted tailnet user opens it in a browser, logs in and drives a working panel; `remote off` removes every trace | 1.1, 1.2 | Detailed |
| 2 | Hardened: lockout and kill switch, expiry and idle logout, audit log, an OS notification on every login and failure; terminal-fidelity fixes from Phase 1's spike | 2.1, 2.2 | Epic-level |
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
**Status:** Pending

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

- [ ] Done

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
**Status:** Pending

#### Task 1.2.1: Find Tailscale, build the gateway, start and stop the pieces

- [ ] Done

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

- [ ] Done

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
**Scope:** `gateway/auth.go`, `gateway/audit.go`, `bin/lzr1-panel` (`remote` reads the audit log; notifications)
**Dependencies:** Phase 1
**Done when:** gateway tests cover each rule; a real wrong-password burst locks out and then triggers the kill
switch, with notifications on this computer.
**Status:** Pending

### Epic 2.2: Terminal fidelity fixes

**Goal:** every failure in Phase 1's fidelity table is fixed or documented with a workaround (likely candidates:
right-click reaching fzf instead of the browser menu via ttyd's client options, OSC 52 copy to the browser
clipboard, a narrower layout for phone widths, `t` new-tab action hidden when remote).
**Scope:** `bin/lzr1-panel` (`open`, `_panel` in remote mode), ttyd client options in `remote on`, `docs/remote.md`
**Dependencies:** Phase 1 (Task 1.2.2's table)
**Done when:** the fidelity table is all pass or documented workaround, re-measured on the same browsers.
**Status:** Pending

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
