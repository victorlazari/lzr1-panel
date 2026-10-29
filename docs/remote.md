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
   `remote on` turns itself off if Funnel is on for that address.
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
| Headers on every response: `Cache-Control: no-store`, `X-Frame-Options: DENY`, `frame-ancestors 'none'`, a same-origin `Content-Security-Policy`, `Referrer-Policy: no-referrer`, `nosniff` | Caching the terminal, framing it, leaking the URL in a Referer. |
| An expiry (`--ttl`, 8 hours by default) | A session forgotten on. |

What it does not protect against: anyone who can run programs as you on this computer (they could read the session
file, or connect to the gateway's loopback port and claim any Tailscale login, though they would still need the
password), and an allowed person who has the password: they get a full terminal as you, on every machine your profiles
reach. Logins, failures and logouts go to `~/.cache/lzr1-panel/remote/remote.log`, never the password or a cookie.

## Turning it off

`lzr1-panel remote off` removes the `serve` path, stops the gateway, ttyd and every browser's layout, and deletes the
sockets and the session file. It is safe to run twice. When `--ttl` passes the gateway stops by itself; the next
`remote status` or `remote on` notices and cleans up the rest. After a reboot nothing is running and `remote status`
cleans the stale session file.

If something is left over, by hand: `tailscale serve status`, then `tailscale serve --https=443 --set-path /<uuid>
off`; `pkill -f lzr1-panel/remote/` stops the gateway and ttyd.
