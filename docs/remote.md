# The panel in a browser, over Tailscale

`lzr1-panel remote on` publishes the panel to your tailnet. Any browser on a device signed in to the same tailnet,
as one of the logins you allow, can open the URL, type the password and use the panel as in a terminal.

```text
lzr1-panel remote on [--allow EMAIL[,EMAIL...]] [--ttl 8h]   publish; prints the URL and the password, copies the URL
lzr1-panel remote password                                   copy the password
lzr1-panel remote url                                        copy the URL again
lzr1-panel remote status                                     on or off, the URL, who is allowed, when it expires,
                                                             the logins and wrong passwords so far
lzr1-panel remote off                                        unpublish and stop everything
```

| Variable | Default | What it sets |
|----------|---------|--------------|
| `LZR1_REMOTE_TTL` | `8h` | How long the remote stays on (`--ttl` wins over it). Hours, minutes or seconds: `8h`, `90m`, `1h30m`. |
| `LZR1_REMOTE_IDLE` | `30m` | A browser that sends no input for this long is logged out and its panel ends. |
| `LZR1_REMOTE_NOTIFY` | `1` | `0` turns off the remote's alerts. `LZR1_NOTIFY=0` (the sessions' alerts) does not, so a wrong-password burst is still seen. |
| `LZR1_REMOTE_PWLEN` | `16` | The password's length, 12 at least. |

Without `--allow`, `remote on` asks for the logins allowed in, prefilled with the last answer (the first time, this
machine's Tailscale login), and keeps the answer in `~/.config/lzr1-panel/remote.conf`.

## What it needs

- **Tailscale**, connected, with **HTTPS certificates** turned on for the tailnet (admin console → DNS → HTTPS
  Certificates). On macOS the app's CLI is used when `tailscale` is not on PATH. On Linux, `tailscale serve` needs
  root unless you ran `sudo tailscale set --operator=$USER` once.
- **ttyd** (`brew install ttyd`, `apt install ttyd`), **Go** to build the gateway (once; `./install.sh` does it),
  **jq** and **openssl**.

## How it works

```text
browser ──HTTPS (tailnet only)──▶ tailscale serve ──▶ lzr1-remote-gw ──▶ ttyd ──▶ lzr1-panel open (one per browser)
          https://<machine>.<tailnet>.ts.net/<uuid>/    Unix socket        Unix socket   tmux layout, splash off
                                                        (macOS: 127.0.0.1:<port>)
```

1. `remote on` checks Tailscale, makes a random UUID (the URL path) and a random password, and writes
   `~/.cache/lzr1-panel/remote/session` (mode 0600, in a 0700 folder) so the URL and password can be copied again.
2. **ttyd** listens on a Unix socket in that folder. Each browser that connects gets a fresh `lzr1-panel open` in the
   tmux layout (a Ghostty split cannot be shown in a browser), at most 4 at once. Every panel sees and drives the same
   tmux sessions as the one on your screen. A browser's layout ends when the browser goes away.
3. **lzr1-remote-gw** (`gateway/`, Go standard library only) listens on a Unix socket (mode 0600) in that folder, or,
   on macOS, on `127.0.0.1` on a free port (see below). It reads the password once on its standard input, checks every
   request and proxies the page and its WebSocket to ttyd.
4. **`tailscale serve`** publishes the gateway at `https://<machine>.<tailnet>.ts.net/<uuid>/` with a certificate for
   that name. It is reachable only from the tailnet: `tailscale funnel` (the public internet) is never used.
   `remote on` reads `tailscale serve status --json` before publishing and refuses if Funnel is on for
   `<machine>:443`, or if that status cannot be read; it checks again right after publishing, and the watcher checks
   every 10 seconds and turns remote off if Funnel appears (or the status cannot be read). `serve` runs in the
   foreground (no `--bg`), so the path lives only as long as that process: Tailscale drops it when the process ends,
   is killed, or the machine reboots. `serve` is started from the gateway's output and ended as soon as the gateway
   exits (`--ttl`, a crash, a kill), so the path never points at a stopped gateway, even if the watcher is gone.
5. The URL is copied to the clipboard. The password is copied only by `remote password`, so the two are not on the
   clipboard together.

On Linux `tailscaled` runs as root and reaches the gateway's 0600 socket (`serve ... unix:<socket>`); `serve` strips
its mount path and sends `Host: localhost` there, so the gateway puts `/<uuid>` back (`--stripped`) and checks `Origin`
against the published name (`--host`). On macOS the app's `serve` cannot reach a Unix socket (every request answers
502), so the gateway listens on loopback TCP and the target is `http://127.0.0.1:<port>/<uuid>`, which puts the prefix
back.

## Safety layers

Each request has to pass all of these, in order:

| Layer | What it stops |
|-------|---------------|
| Tailnet only (`serve`, never `funnel`) | Anyone outside your tailnet: there is no public address. |
| TLS (Tailscale's certificate for the `*.ts.net` name) and WireGuard underneath | Anyone on the path reading or changing traffic. |
| A random path, a UUID v4 (122 random bits) | Guessing the address from inside the tailnet; anything else answers 404. |
| The Tailscale identity (`Tailscale-User-Login`, set by `serve`) on the allowlist | Other people on the tailnet, and tagged devices (they carry no identity), on every request and WebSocket. |
| The password: 16 characters by default (`LZR1_REMOTE_PWLEN`, 12 at least) from 56 look-alike-free characters, about 93 bits, made by `openssl rand` | Someone using an allowed person's unlocked device without knowing it. The gateway keeps only its SHA-256 and compares in constant time; it is never in a command line or the environment. |
| A session cookie: 32 random bytes, `HttpOnly; Secure; SameSite=Strict; Path=/<uuid>/`, no `Expires` (ends with the browser, or earlier at the idle logout), bound to the login that signed in | Scripts reading it, sending it to another site, or another tailnet user replaying it. |
| A lockout: 5 wrong passwords from one login lock that login out for 15 minutes; attempts while locked are refused unchecked (429) | Guessing the password from one allowed device. |
| A kill switch: 20 wrong passwords in total (locked attempts included) turn the whole remote off and alert | A slow guess spread over several logins, or one that waits out each lockout. |
| An idle logout (`LZR1_REMOTE_IDLE`, 30 minutes by default): no key, click, paste or resize from the browser for that long ends the session and its panel; the next request gets the login form | A browser left open and walked away from. ttyd's keep-alive pings do not count as input, so a session that only prints (a long Claude run you are not touching) is logged out too. |
| `Origin` must be `https://<the machine's name>` on every POST and WebSocket upgrade | Another web page driving the terminal from your browser (cross-site WebSocket hijacking). |
| Headers on every response: `Cache-Control: no-store`, `X-Frame-Options: DENY`, `frame-ancestors 'none'`, a same-origin `Content-Security-Policy`, `Referrer-Policy: same-origin`, `nosniff` | Caching the terminal, framing it, leaking the URL in a Referer. |
| An expiry (`--ttl` or `LZR1_REMOTE_TTL`, 8 hours by default) | A remote forgotten on. |

What it does not protect against: anyone who can run programs as you on this computer (they can read the session
file, password included); on macOS, any local user or process on this Mac, because they can reach the gateway's
loopback port, send any `Tailscale-User-Login` and `Origin`, and so bypass the e-mail allowlist and the Origin check
(the password still protects it); and an allowed person who has the password: they get a full terminal as you, on
every machine your profiles reach. On Linux the gateway's socket is reachable only by you and root. Logins, failures,
lockouts, logouts and shutdowns go to the audit trail, `~/.cache/lzr1-panel/remote/audit.log` (below), never the
password or a cookie.

## Audit and alerts

The gateway appends one line per event to `~/.cache/lzr1-panel/remote/audit.log` (mode 0600; a symlink or anything
but a regular file is refused). It never holds the password, a cookie or a session token; a session is named by the
first 8 hex characters of the SHA-256 of its token. `remote on` keeps the file's last 2000 lines.

```text
TIME EVENT key=value...      TIME is RFC 3339 with the local offset; values never hold spaces
start     ttl=8h0m0s idle=30m0s
login     user=LOGIN sid=SID
failure   user=LOGIN fails=N total=T [locked=1]
lockout   user=LOGIN until=TIME
logout    user=LOGIN sid=SID
idle-out  user=LOGIN sid=SID
shutdown  reason=ttl|signal|killswitch
```

The watcher reads the new lines every second and raises an OS notification (titled "lzr1 remote", through
terminal-notifier, `osascript` or `notify-send`, as the sessions' alerts; none when there is no notifier) for:

| Event | Notification |
|-------|--------------|
| `login` | LOGIN logged in |
| `failure` | N wrong passwords (LOGIN, ...): at most one every 30 seconds, later ones added to the next |
| `lockout` | LOGIN locked out until HH:MM (5 wrong passwords) |
| `shutdown reason=killswitch` | turned off: 20 wrong passwords |
| `shutdown reason=ttl` | expired, turned off |
| the gateway ended without a `shutdown` line (a crash, a kill) | the gateway stopped; turned off |
| `tailscale serve` ended (Tailscale quit) | tailscale serve ended; turned off |
| Funnel appeared for the machine | Funnel appeared; turned off |

`logout` and `idle-out` are recorded, not notified, and a `remote off` you type is neither. `remote status` adds the
remote's logins and wrong passwords so far, and who is locked out until when; after the kill switch it says
"off: the kill switch turned it off at HH:MM". `~/.cache/lzr1-panel/remote/remote.log` keeps the other messages: start
errors, upstream errors and refused logins (a login not on the allowlist). Both files stay after `remote off`.

## Turning it off

`lzr1-panel remote off` ends the watcher and `serve`, stops the gateway, ttyd and every browser's layout, then checks
with `tailscale serve status` that the path is gone (removing it with `serve ... off` if not), and deletes the sockets
and the session file. It is safe to run twice. It signals a process only while its command line still holds this
remote's UUID (the watcher runs as `lzr1-panel remote watch <uuid>`, the gateway with `--base /<uuid>/`, ttyd with
`-b /<uuid>`, `serve` with `--set-path /<uuid>`), so a pid reused after a crash or a reboot is never touched. If
Tailscale cannot confirm the path is gone (the app is quit, `tailscaled` is down, the operator permission was removed),
everything else is still stopped, but `off` keeps the session file (the only record of the UUID), prints the manual
command and exits 1. Run `remote off` again once Tailscale is back.

When `--ttl` passes the gateway exits, `serve` ends with it (the path goes too), and the watcher turns the rest off
within a second or so. If Tailscale quits, the foreground `serve` ends and the watcher stops the gateway and ttyd the
same way; since Tailscale cannot confirm the path is gone, it keeps the session file and writes the manual command to
`remote.log`, and the next `remote status`, `on` or `off` finishes the cleanup. After a reboot nothing runs and nothing
is published; `remote status` deletes the stale session file.

If something is left over, by hand: `tailscale serve status`, then `tailscale serve --https=443 --set-path /<uuid>
off`; `remote status` shows the pids of the gateway, ttyd and `serve`.

## Copy, select and the right button

The gateway adds one small script, `gateway/client.js`, to the page ttyd serves (a `<script>` tag right after its
`<head>`; ttyd's own client is left as it is, so a ttyd upgrade keeps working). It is served at
`/<uuid>/lzr1-client.js` behind the same login as the rest, and does four things:

- **Copies reach the browser's clipboard.** Claude Code's own copy and a tmux drag-select both arrive as OSC 52; the
  script writes them with the browser's clipboard API (1 MiB at most). A browser that refuses a write without a click
  (Safari) shows "Copied text ready — click to copy" for 15 seconds instead. A program asking to *read* the clipboard
  (OSC 52 with `?`) gets nothing.
- **Option-drag selects** in the terminal itself on a Mac (Shift-drag elsewhere), even where the panel or tmux has the
  mouse; Cmd-C copies that selection.
- **The right button** over the terminal opens only the panel's own menu: the browser's menu is cancelled there.
- **A session that is gone** (logged out in another tab, idle logout, kill switch) sends the page to the login form as
  soon as its connection closes, instead of ttyd reconnecting forever.

A browser's layout does not hand copies to this computer's `pbcopy`, so a copy made in a browser does not overwrite
this Mac's clipboard, for sessions on other machines. A session on this Mac is the exception: its own tmux is shared
with the panel on this screen, and pipes every copy to `pbcopy` too (as it does for that panel), so a copy made in it
from a browser lands on both clipboards.

## Fidelity in a browser

Measured on 2026-10-29 through the real tailnet URL, with ttyd 1.7.7 (xterm.js in the page), tmux 3.8 and fzf 0.74.
Playwright 1.64 drove headless Chromium (build 1248, macOS) with real mouse events at xterm cell coordinates and real
key presses; results were read back from xterm.js's screen, `tmux capture-pane` of the test sessions and a stand-in
`pbcopy`. The sessions ran a stand-in for Claude that echoes each line, reports Ctrl-C and copies with OSC 52. Safari
and phones were out of the automation's reach: their cells say **needs a person** until someone measures them. "Also
in a terminal" marks a failure reproduced in the tmux layout in a plain terminal: a panel bug, not the browser's.

| Item | Chrome (headless Chromium, automated) | Safari | Phone |
|------|---------------------------------------|--------|-------|
| **Login** | pass: the URL shows the password form; the panel lists sessions 0.7 s after Enter. Needed one fix first: with `Referrer-Policy: no-referrer` Chrome sent `Origin: null` on the form's POST and every login was refused; it is `same-origin` now. | needs a person | needs a person |
| **Listing** sessions | pass: every session listed with its summary line; the first one shows in the viewer by itself. | needs a person | needs a person |
| **Selecting** with ↑ ↓ | pass: each arrow shows that session in the viewer. | needs a person | needs a person |
| **Typing** in a session | pass, once the keyboard is in the viewer (Enter on the row, or a click in the viewer): the session read every character. | needs a person | needs a person |
| **Left click** on a row | pass: the viewer shows that session; the keyboard stays in the panel. | needs a person | needs a person |
| **Right-click** menu | pass (2.2.1): the row's menu opens and its actions work; the page cancels the `contextmenu` event over the terminal, so the browser draws no menu of its own. | needs a person | needs a person (no right button: long-press) |
| **Double-click** a row | fail, also in a terminal: the keyboard reaches the viewer for about 0.2 s, then tmux's own `DoubleClick1Pane` binding (`select-pane -t =`) puts it back on the panel. Enter works. | needs a person | needs a person |
| **Footer** buttons | `[?]`, `[+opus]` and `[+sonnet]` work. The first footer click right after a row's menu was lost, also in a terminal; fixed in 2.2.2 and checked in a terminal's tmux layout (Task 2.2.4 measures it again in Chrome). | needs a person | needs a person |
| **New-session picker** | pass: `[+opus]` opens it, a typed name + Enter starts the session and shows it; `[+sonnet]` then a click on a repository starts it there (once in four runs that click did not start it). | needs a person | needs a person |
| **Ctrl-C** to Claude | pass: the agent got SIGINT. | needs a person | needs a person (no Ctrl key on the keyboard) |
| **Ctrl-b d** leaves a session | After a click switched the viewer to another session of the same machine, Ctrl-b d detached and the viewer attached again at once, also in a terminal. Fixed in 2.2.2 and checked in a terminal's tmux layout, after a click, an Enter and a rename (Task 2.2.4 measures it again in Chrome). | needs a person | needs a person |
| **Copy** (OSC 52, drag) | pass (2.2.1): Claude's OSC 52 copy, in a session on this Mac and on another machine (a stand-in reached through `ssh`), and a tmux drag-select both land on the browser's clipboard; the browser's layout no longer calls this Mac's `pbcopy` (a session on this Mac still does, from its own tmux: see "Copy, select and the right button"). Option-drag selects in xterm.js over the panel's mouse mode. | needs a person | needs a person |
| **Paste** | pass: Cmd-V pasted the browser's clipboard into the session. | needs a person | needs a person |
| **Resize** | pass: 1400×900 → 1000×650 px gives a 139×42 terminal and tmux follows; the panel shrinks with the window (56 → 38 columns) rather than keeping its width. | needs a person | needs a person |
| **Phone-width** layout | fail (Chromium at 390×844 px): 52 columns, narrower than the 56-column panel, so tmux squeezes the panel to 1 column and the list disappears. | needs a person | needs a person |
| **Reconnect** after the laptop sleeps | partial (simulated by closing the WebSocket; a real sleep needs a person): the old layout ends and ttyd reconnects by itself, but its new panel fails with "server exited unexpectedly / cannot start tmux" (the dying layout was the last on the `lzr1` server); a second Enter gives a fresh panel, with the earlier selection gone. | needs a person | needs a person |
| **Browser keys** (Cmd-W, Cmd-T, Ctrl-W) | needs a person: headless Chromium has no browser shortcuts. What reaches the page works: Ctrl-W arrives as the terminal's word erase. | needs a person | needs a person |

Copy and the right button were fixed after this measurement (Task 2.2.1, measured again the same way). Ctrl-b d and
the first footer click after a menu were fixed in Task 2.2.2 and checked in a terminal. Still for Phase 2 (Epic 2.2):
a narrower or single-pane layout under about 80 columns; double-click in the tmux layout; reconnect without the
`lzr1` server race; then the Safari and phone columns, measured by a person.
