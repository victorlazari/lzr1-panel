# The panel in a browser, over Tailscale

`lzr1-panel remote on` publishes the panel to your tailnet. Any browser on a device signed in to the same tailnet,
as one of the logins you allow, can open the URL, type the password and use the panel as in a terminal.

```text
lzr1-panel remote on [--allow EMAIL[,EMAIL...]] [--ttl 8h]   publish; prints the URL and the password, copies the URL
lzr1-panel remote password                                   copy the password
lzr1-panel remote url                                        copy the URL again
lzr1-panel remote status                                     on or off, the URL, who is allowed, when it expires
lzr1-panel remote off                                        unpublish and stop everything
```

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
          https://<machine>.<tailnet>.ts.net/<uuid>/    127.0.0.1:<port>   Unix socket   tmux layout, splash off
```

1. `remote on` checks Tailscale, makes a random UUID (the URL path) and a random password, and writes
   `~/.cache/lzr1-panel/remote/session` (mode 0600, in a 0700 folder) so the URL and password can be copied again.
2. **ttyd** listens on a Unix socket in that folder. Each browser that connects gets a fresh `lzr1-panel open` in the
   tmux layout (a Ghostty split cannot be shown in a browser), at most 4 at once. Every panel sees and drives the same
   tmux sessions as the one on your screen. A browser's layout ends when the browser goes away.
3. **lzr1-remote-gw** (`gateway/`, Go standard library only) listens on `127.0.0.1` on a free port, reads the
   password once on its standard input, checks every request and proxies the page and its WebSocket to ttyd.
4. **`tailscale serve`** publishes the gateway at `https://<machine>.<tailnet>.ts.net/<uuid>/` with a certificate for
   that name. It is reachable only from the tailnet: `tailscale funnel` (the public internet) is never used, and
   `remote on` turns itself off if Funnel is on for that address. It runs in the foreground (no `--bg`), so the
   path lives only as long as that process: Tailscale drops it when the process ends, is killed, or the machine
   reboots. A small watcher ends it as soon as the gateway exits, so the path never points at a freed port.
5. The URL is copied to the clipboard. The password is copied only by `remote password`, so the two are not on the
   clipboard together.

The gateway listens on loopback TCP, not a Unix socket, because `tailscale serve` in the macOS app cannot reach a Unix
socket (every request answers 502). `serve` strips its mount path, so the target is `http://127.0.0.1:<port>/<uuid>`,
which puts the prefix back.

## Safety layers

Each request has to pass all of these, in order:

| Layer | What it stops |
|-------|---------------|
| Tailnet only (`serve`, never `funnel`) | Anyone outside your tailnet: there is no public address. |
| TLS (Tailscale's certificate for the `*.ts.net` name) and WireGuard underneath | Anyone on the path reading or changing traffic. |
| A random path, a UUID v4 (122 random bits) | Guessing the address from inside the tailnet; anything else answers 404. |
| The Tailscale identity (`Tailscale-User-Login`, set by `serve`) on the allowlist | Other people on the tailnet, and tagged devices (they carry no identity), on every request and WebSocket. |
| The password: 16 characters by default (`LZR1_REMOTE_PWLEN`, 12 at least) from 56 look-alike-free characters, about 93 bits, made by `openssl rand` | Someone using an allowed person's unlocked device without knowing it. The gateway keeps only its SHA-256 and compares in constant time; it is never in a command line or the environment. |
| A session cookie: 32 random bytes, `HttpOnly; Secure; SameSite=Strict; Path=/<uuid>/`, no expiry (ends with the browser), bound to the login that signed in | Scripts reading it, sending it to another site, or another tailnet user replaying it. |
| `Origin` must be `https://<the machine's name>` on every POST and WebSocket upgrade | Another web page driving the terminal from your browser (cross-site WebSocket hijacking). |
| Headers on every response: `Cache-Control: no-store`, `X-Frame-Options: DENY`, `frame-ancestors 'none'`, a same-origin `Content-Security-Policy`, `Referrer-Policy: same-origin`, `nosniff` | Caching the terminal, framing it, leaking the URL in a Referer. |
| An expiry (`--ttl`, 8 hours by default) | A session forgotten on. |

What it does not protect against: anyone who can run programs as you on this computer (they could read the session
file, or connect to the gateway's loopback port and claim any Tailscale login, though they would still need the
password), and an allowed person who has the password: they get a full terminal as you, on every machine your profiles
reach. Logins, failures and logouts go to `~/.cache/lzr1-panel/remote/remote.log`, never the password or a cookie.

## Turning it off

`lzr1-panel remote off` removes the `serve` path, checks with `tailscale serve status` that it is gone, then stops
the gateway, ttyd and every browser's layout, and deletes the sockets and the session file. It is safe to run twice.
If Tailscale cannot confirm the path is gone (the app is quit, `tailscaled` is down, the operator permission was
removed), `off` keeps the session file (the only record of the UUID) and leaves the gateway holding its port, still
behind the allowlist and password, prints the manual command and exits 1. Run `remote off` again once Tailscale is
back.

When `--ttl` passes the gateway exits, and the watcher ends `serve` (the path goes with it) and turns the rest off
within a second or so. If Tailscale quits, the foreground `serve` ends and the watcher turns remote off the same way.
After a reboot nothing runs and nothing is published; `remote status` deletes the stale session file.

If something is left over, by hand: `tailscale serve status`, then `tailscale serve --https=443 --set-path /<uuid>
off`; `remote status` shows the pids of the gateway, ttyd and `serve`.

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
| **Right-click** menu | partial: the row's menu opens and its actions work, but the page does not cancel the `contextmenu` event, so a headed Chrome shows its own menu over it (confirm by a person). | needs a person | needs a person (no right button: long-press) |
| **Double-click** a row | fail, also in a terminal: the keyboard reaches the viewer for about 0.2 s, then tmux's own `DoubleClick1Pane` binding (`select-pane -t =`) puts it back on the panel. Enter works. | needs a person | needs a person |
| **Footer** buttons | partial, also in a terminal: `[?]`, `[+opus]` and `[+sonnet]` work; the first footer click right after a row's menu is lost (`[↻]` did nothing; the next click worked). | needs a person | needs a person |
| **New-session picker** | pass: `[+opus]` opens it, a typed name + Enter starts the session and shows it; `[+sonnet]` then a click on a repository starts it there (once in four runs that click did not start it). | needs a person | needs a person |
| **Ctrl-C** to Claude | pass: the agent got SIGINT. | needs a person | needs a person (no Ctrl key on the keyboard) |
| **Ctrl-b d** leaves a session | fail, also in a terminal: after a click switched the viewer to another session of the same machine, Ctrl-b d detaches and the viewer attaches again at once (its loop still holds the old target). Works when the viewer attached the session itself. | needs a person | needs a person |
| **Copy** (OSC 52, drag) | fail: nothing reaches the browser's clipboard. ttyd 1.7.7's xterm.js ignores OSC 52 (written straight into it: nothing), and both Claude's OSC 52 copy and a tmux drag-select go to `pbcopy` on the Mac running the panel instead, overwriting that Mac's clipboard. xterm.js's own selection (Shift- or Option-drag) selected nothing while the panel has the mouse. | needs a person | needs a person |
| **Paste** | pass: Cmd-V pasted the browser's clipboard into the session. | needs a person | needs a person |
| **Resize** | pass: 1400×900 → 1000×650 px gives a 139×42 terminal and tmux follows; the panel shrinks with the window (56 → 38 columns) rather than keeping its width. | needs a person | needs a person |
| **Phone-width** layout | fail (Chromium at 390×844 px): 52 columns, narrower than the 56-column panel, so tmux squeezes the panel to 1 column and the list disappears. | needs a person | needs a person |
| **Reconnect** after the laptop sleeps | partial (simulated by closing the WebSocket; a real sleep needs a person): the old layout ends and ttyd reconnects by itself, but its new panel fails with "server exited unexpectedly / cannot start tmux" (the dying layout was the last on the `lzr1` server); a second Enter gives a fresh panel, with the earlier selection gone. | needs a person | needs a person |
| **Browser keys** (Cmd-W, Cmd-T, Ctrl-W) | needs a person: headless Chromium has no browser shortcuts. What reaches the page works: Ctrl-W arrives as the terminal's word erase. | needs a person | needs a person |

For Phase 2 (Epic 2.2): copy to the browser (an xterm.js clipboard handler, and no `pbcopy` on the host in a remote
layout); a narrower or single-pane layout under about 80 columns; cancel the browser's context menu; double-click and
Ctrl-b d in the tmux layout; the first footer click after a menu; reconnect without the `lzr1` server race; then the
Safari and phone columns, measured by a person.
