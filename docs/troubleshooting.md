# Troubleshooting

**Start here.** This checks everything the panel needs and says how to fix what is missing:

```sh
lzr1-panel doctor
```

Still stuck? Find your problem below. Each one is: what you see, why, and the fix.

- [Installing](#installing)
- [Opening the panel](#opening-the-panel)
- [Machines and sessions](#machines-and-sessions)
- [The viewer](#the-viewer)
- [Copying text](#copying-text)
- [Pull requests](#pull-requests)
- [Alerts](#alerts)
- [Mouse and menus](#mouse-and-menus)
- [Looks wrong](#looks-wrong)
- [Remote panel (in a browser)](#remote-panel-in-a-browser)
- [Starting over](#starting-over)

---

## Installing

### `lzr1-panel: command not found`

The command lives in `~/.local/bin`, which your current terminal may not have on its PATH yet.

**Fix:** open a new terminal, or run `source ~/.zshrc`. Still missing? Run `./install.sh` again: it adds the PATH
line to your shell.

### `Homebrew is needed on macOS`

The installer uses Homebrew to install tools on a Mac.

**Fix:** install it from [brew.sh](https://brew.sh), then run `./install.sh` again.

### `fzf is still older than 0.74`

The panel needs fzf 0.74 or newer, and some Linux distributions ship an old one.

**Fix:** run `./install.sh` again; on Linux it puts the latest fzf in `~/.local/bin`. Check with `fzf --version`,
and make sure `~/.local/bin` comes first on your PATH (`which fzf`).

---

## Opening the panel

### `cannot script Ghostty` or `cannot split the Ghostty pane`

On macOS the panel splits your Ghostty window, which needs permission to control Ghostty.

**Fix:** open **System Settings → Privacy & Security → Automation** and allow your terminal to control **Ghostty**.
Then run `lzr1-panel open` again.

### It opens as a tmux layout instead of a Ghostty split

The split only happens when you run it **in Ghostty, outside tmux**.

**Fix:** run `lzr1-panel open` in a plain Ghostty window. Inside tmux, or in another terminal, the tmux layout is
expected and works the same way.

### The panel opens every time I open Ghostty, and I don't want that

**Fix:** add `export LZR1_PANEL=0` to `~/.zshenv`, or run `./install.sh` again and answer no to that question.

### The opening animation is too long

**Fix:** press any key to skip it. `LZR1_SPLASH_SECONDS=2` shortens it, `LZR1_SPLASH=0` turns it off.

---

## Machines and sessions

### A machine is missing from the list

The panel leaves out a machine that does not answer, and tries it again once a minute.

**Fix:** run `lzr1-panel profile test NAME`. Then check:

- **Can you SSH to it?** Run `ssh user@host` yourself once. The panel never types passwords: the machine must
  accept your SSH key, and its host key must already be known (the first `ssh` asks you to accept it).
- **The right key?** `lzr1-panel profile show NAME` shows the key file it uses.
- **Is tmux there?** `profile test` says `tmux: missing` if not. Install it on that machine.

### `Claude Code not found on that machine`

Claude Code is not installed there, or not where the panel can find it.

**Fix:** install it on that machine (`curl -fsSL https://claude.ai/install.sh | bash`), run `claude` there once to
sign in, then add the profile again. Or point to it: `--claude /path/to/claude`.

### A new session does not start

**Fix:** start it from a terminal to see the error:

```sh
lzr1-panel start PROFILE opus NAME
```

The usual causes:

- **Claude is not signed in on that machine.** Run `claude` there once.
- **The name has `.`, `:`, `/` or `|`.** Session names cannot.
- **A session with that name already exists.** The panel shows the existing one instead.

### A new session is stuck on "Do you trust this folder?"

The panel answers this for you only when the session opens in one of your repositories.

**Fix:** answer it in the viewer (click the session, then Enter). It only asks once per folder.

### My Claude session is not in the panel

The panel only lists sessions that run **inside tmux**. A `claude` started straight in a terminal tab is invisible
to it.

**Fix:** start sessions with `cl-opus` / `cl-sonnet` / `cl-opencode` (installed by `./install.sh`), `lzr1-panel
run`, or the panel's `n` / `s` / `c`. To move a running conversation over, quit it (`/exit`), then in the same folder:

```sh
lzr1-panel run --model opus --continue
```

Your own `cl-*` aliases can do the same: make them call `lzr1-panel run --model MODEL --effort EFFORT -- "$@"`.

### The same session shows under two profiles

Both profiles point at the same machine, and at least one was made by an older version, which listed every session
on its machine (no `only=` in `lzr1-panel profile show NAME`).

**Fix:** edit the profile (press Enter at each question): an edited profile lists just the sessions inside its
repositories folder.

```sh
lzr1-panel profile edit NAME
```

### A session is missing from its profile

A profile with `only=` lists just the sessions whose folder is inside it. `lzr1-panel profile show NAME` shows it.

**Fix:** start the session inside that folder, or add a profile for the folder it runs in.

### A session opened in the wrong folder

A session opens in a repository only when its name matches a folder in the profile's `repos` exactly
(`lerian-clients` → `repos/lerian-clients`). Any other name opens in `root`.

**Fix:** use the folder's exact name, or click the repository in the new-session list.

### I closed Claude by accident

The session is still there.

**Fix:** press **Enter** in it to resume the same conversation. To really close it, type `end` and Enter, or press
`x` in the panel.

The same goes for opencode: its session waits on `opencode exited. Enter: resume this conversation`.

### opencode sessions do not start on a machine

`c` or `[+opencode]` shows nothing new, or `start` says `opencode exited right after start`.

**Fix:** run `lzr1-panel profile test NAME`. Its `opencode:` line shows the opencode the panel starts there, or
`not installed`:

- **Not installed.** Install it on that machine (`brew install sst/tap/opencode`, or see
  [opencode.ai](https://opencode.ai)), then run `lzr1-panel profile edit NAME` and press Enter through its
  questions: a profile with no `opencode=` line gets the opencode it finds there. Installed somewhere else: add an
  `opencode=/path/to/opencode` line to the profile. tmux starts it with a bare PATH, so the profile needs its full
  path.
- **Installed, but it exits.** Start it from a terminal and view the session: opencode's error shows above its
  resume prompt. Usually it has no provider yet: run `opencode auth login` on that machine.

```sh
lzr1-panel start PROFILE opencode NAME
```

### An opencode session shows the wrong state

The panel reads opencode's screen: `esc interrupt` in its bottom bar is working (`▶`), its question dialog
(`enter submit`) or `opencode exited` is waiting on you (`⧗`), anything else is idle (`·`). These markers follow
opencode **2.0.32**'s screen; a newer version may change them, and the row (and its alerts) then go wrong.
`Permission required` also counts as waiting, but is unverified: with auto-approve on, 2.0.32 never showed it.

**Fix:**

- Check `opencode --version` on that machine. If it is newer and the glyphs are wrong, look at its screen
  (`tmux capture-pane -p -t '=NAME:'`) and update the markers in `LIST_SCRIPT` (`bin/lzr1-panel`).
- An opencode started by hand inside tmux is read as Claude, so it mostly shows idle. Start opencode sessions with
  `c`, `cl-opencode` or `lzr1-panel run --opencode`.

---

## The viewer

### The viewer says "pick a session" and stays empty

**Fix:** click a session in the panel. If you left it with `Ctrl-b d`, click it again or press Enter on it.

### The viewer is stuck on "starting NAME ..."

The viewer waits like this only for a session the panel has just created, for up to 30 seconds, and a click on
another session ends the wait. A session that ended moves the viewer to the first one left by itself.

**Fix:** click another session. Still stuck? You are on an older version: quit the panel (`q`), update
(`git pull` in the lzr1-panel folder) and run `lzr1-panel open` again.

### I closed the viewer pane

**Fix:** click another session, or press Enter on one. The viewer comes back.

### My keyboard is stuck in the viewer

**Fix:** press **`Ctrl-b d`** to leave the session, or click the panel.

### Ctrl-C does nothing in Claude

With text selected, Ctrl-C copies it instead of reaching Claude.

**Fix:** click once in the viewer to clear the selection, then press Ctrl-C.

---

## Copying text

### How copying works in a session

Claude Code handles the mouse itself, so copying goes through Claude, in any terminal and from any machine:

1. **Drag** over the text: Claude highlights it.
2. Press **Ctrl-C**: it is on your clipboard.
3. **Cmd-V** (or Ctrl-Shift-V on Linux) pastes it, as usual.

**Cmd-C does not copy there.** Your terminal keeps Cmd-C for its own selection, which stays empty while Claude
has the mouse. Use Ctrl-C.

### I select, press Ctrl-C, and nothing is copied

Claude sends the copy to the terminal as an escape code (OSC 52). Some terminals, such as macOS Terminal, ignore
it, so the panel's tmux takes the copy and puts it on the clipboard itself. That is set up when the panel opens
and when the viewer attaches to a session.

**Fix:**

1. Quit the panel (`q`) and run `lzr1-panel open` again, then pick the session.
2. Select, Ctrl-C, then paste somewhere to check.
3. On Linux, the panel needs `wl-copy` (Wayland) or `xclip` (X11) installed, or a terminal that supports OSC 52.

### Paste gives me old text

The copy did not happen, so the clipboard still holds what was there before. See the answer above.

### I want to select with my terminal instead (and use Cmd-C)

Turn off mouse reporting while you select:

- **macOS Terminal**: View → Allow Mouse Reporting (**Cmd-R**) to turn it off; Cmd-R again to turn it back on.
- **Ghostty**: hold **Shift** while dragging.
- **iTerm2**: hold **Option** while dragging.

Then Cmd-C copies, as in any terminal. This also works in the panel, which uses the mouse for clicks.

### Ctrl-C does not copy a Shift-selection in Ghostty

**Fix:** run `./install.sh` again and say yes to *"Let Ctrl+C copy selected text in Ghostty"*. Then reload
Ghostty's config with **Cmd+Shift+,**. `lzr1-panel doctor` tells you whether it is set.

### Copying from an SSH machine does not work

The copy travels back through SSH to this computer, where the panel puts it on the clipboard. Nothing needs
installing on the server.

**Fix:** as in "I select, press Ctrl-C, and nothing is copied" above.

---

## Pull requests

### The heading says `ssh · 0` though you have open PRs

The panel could not use `gh`, so it fell back to your SSH key, which only finds public PRs. This happened when
`gh` signs in through a `GITHUB_TOKEN` set in your shell's startup files: Ghostty starts the panel's split without
them. The panel now passes `GITHUB_TOKEN` and `GH_TOKEN` on, and a short `gh` failure keeps the last list.

**Fix:** quit the panel (`q`) and run `lzr1-panel open` again. Still `ssh`? Run `lzr1-panel prs` to see the
error, or sign gh in for good with `gh auth login` (it then no longer needs the variable).

### The pull requests section is missing

**Fix:** run `lzr1-panel prs`. It fetches them right now and says what is wrong. Usually:

- **Not signed in to GitHub.** Run `gh auth login`.
- **No `gh`.** Run `./install.sh` again; it installs it.
- **You hid it.** Remove `LZR1_PRS=0` from your shell.

Without `gh`, the panel finds your account from your SSH key (`ssh -T git@github.com`), but then it lists **public
PRs only**, with no checks or reviews. `gh` is better.

### A pull request is missing, or its status is old

The list refreshes once a minute.

**Fix:** right-click the **pull requests** heading → *refresh the list now*. Still missing? It may be in an
organization that uses single sign-on: run `gh auth refresh` and authorize the organization when GitHub asks.

### It shows the wrong GitHub account

The panel uses the account `gh` is signed in to.

**Fix:** `gh auth status` shows it; `gh auth switch` changes it.

### Merge, re-run or "mark ready" fails

These use `gh` with your account. The error shows in the panel until you press a key.

**Fix:** check that you can do it on GitHub (permissions, branch protection), and that `gh auth status` is fine.

---

## Alerts

`lzr1-panel notify` plays the tone and shows a sample notification, and `lzr1-panel doctor` says what alerts use on
this computer (its **alerts** section).

### No sound

**Fix:** check, in order:

- **The setting.** `LZR1_SOUND=0` turns the tone off. A file path plays that file: make sure it exists and is
  readable.
- **The player.** macOS uses `afplay`, which comes with the system. Linux uses `paplay` (PulseAudio or PipeWire),
  then `canberra-gtk-play`, then the terminal bell; install `pulseaudio-utils` (Debian/Ubuntu) to get `paplay`.
- **The volume.** The system's output, not muted. On Linux the default tone is
  `/usr/share/sounds/freedesktop/stereo/complete.oga` (the `sound-theme-freedesktop` package).

### No notification on macOS

Without `terminal-notifier`, notifications come from **Script Editor** (through `osascript`).

**Fix:**

- Open **System Settings → Notifications** and allow **Script Editor** (or **terminal-notifier**, if you installed
  it). Then run `lzr1-panel notify`.
- A **Focus** mode (Do Not Disturb, Work, …) hides them: allow the app there, or turn Focus off.
- `brew install terminal-notifier` is optional: it groups notifications per session, so a new one replaces the
  session's last.
- `LZR1_NOTIFY=0` turns them off.

### No notification on Linux

**Fix:**

- **`notify-send`** must be installed: the `libnotify-bin` package on Debian/Ubuntu, `libnotify` elsewhere.
- A **notification daemon** must be running. Desktops (GNOME, KDE) have one; on a bare window manager, run `dunst`
  or `mako`.
- The panel needs **`$DISPLAY`** or **`$WAYLAND_DISPLAY`**: over SSH, or in a tmux started before the desktop,
  there is none, and notifications are skipped. Run the panel from a terminal on the desktop.
- `LZR1_NOTIFY=0` turns them off.

### An alert came late or not at all

The panel samples every session every 15 seconds, so an alert can come up to 15 seconds after the change.

- A reply that starts and ends **between two samples** (idle, then idle again) makes no "finished" alert: the
  panel never saw it working. A question still alerts, since it stays on the screen.
- The session you are looking at in the viewer gets no `✱`, but the tone and the notification still come.
- Alerts only happen while a panel is open (`lzr1-panel list` never alerts). The first refresh after opening it
  only learns the states, so a session already waiting does not alert again.

---

## Mouse and menus

### Right-click does nothing, or opens my terminal's own menu

Some terminals keep the right button for themselves.

**Fix:** press **`m`** on the row instead, or switch menus to the left button:

```sh
export LZR1_MENU_BUTTON=left   # in ~/.zshenv
```

### After a row's menu, the first click on the footer does nothing

The footer (`[+opus]`, `[↻]`, `[?]`, ...) acts when the mouse button comes up, and only after fzf saw that button go
down. A click opens the menu as the button goes down, and the menu takes the release, so fzf reads your next press as
a drag. In the tmux layout (which a browser also gets) the panel sends fzf a release when the menu closes, so this does
not happen. A Ghostty split cannot be sent one.

**Fix:** in a Ghostty split, click the footer button again. You can also open the menu with **`m`**.

While the panel reloads its list (just after a menu, after the window is resized or a phone turned, or on its
15-second refresh), fzf ignores clicks and keys. When your machines answer slowly, a click right then does nothing.
Click again.

### Clicks do nothing at all

**Fix:** make sure your terminal sends mouse events to programs (in most terminals this is on by default; in
macOS Terminal, check View → Allow Mouse Reporting).

---

## Looks wrong

### Boxes and symbols show as `?` or garbage

The panel needs a UTF-8 terminal.

**Fix:** add `export LANG=en_US.UTF-8` to `~/.zshenv` (on Linux, `C.UTF-8` works too), and use a font with
box-drawing characters (Ghostty's default does).

### The panel is too narrow or too wide

**Fix:** `export LZR1_PANEL_WIDTH=64` (the default is 56).

### No Remote Control link

The link appears once Claude prints it, a few seconds after the session starts. Remote Control needs Claude Code
signed in with a claude.ai account.

**Fix:** wait a moment and refresh (`r`). If it never appears, run `claude` on that machine and check its sign-in.

---

## Remote panel (in a browser)

The panel in a browser over Tailscale (`w`, `[web]`, `lzr1-panel remote`) is described in [remote.md](remote.md).
`lzr1-panel doctor` checks its pieces in its "remote" section. Each heading below is the exact text you see: in the
terminal, in `doctor`, in an alert, or on the page in the browser.

### `remote needs Tailscale (tailscale.com/download)`, or doctor's `no Tailscale`

Neither `tailscale` is on your PATH nor, on macOS, the app is in `/Applications/Tailscale.app`. The installer never
installs Tailscale: it is a system VPN app.

**Fix:** install it from [tailscale.com/download](https://tailscale.com/download), sign in, then `lzr1-panel remote on`
again.

### `Tailscale is not connected (…)`, or doctor's `Tailscale is not running`

The state in brackets is Tailscale's own (`Stopped`, `NeedsLogin`, ...). `doctor` says "not running" when Tailscale
does not answer at all, as when the macOS app is installed but not open.

**Fix:** open Tailscale and connect it (on Linux: `sudo tailscale up`); sign in if it asks.

### `turn on HTTPS in the Tailscale admin console`, or doctor's `HTTPS certificates are off`

The remote's URL is `https://<machine>.<tailnet>.ts.net/<uuid>/`, which needs Tailscale's HTTPS certificates for your
tailnet. They are off by default.

**Fix:** in the admin console, [DNS](https://login.tailscale.com/admin/dns) → **HTTPS Certificates** → enable (MagicDNS
must be on too). Then `lzr1-panel remote on` again.

### `Funnel is on for …`

Funnel puts what `tailscale serve` publishes on the public internet. `remote on` refuses to publish while Funnel is on
for this machine's port 443, and turns the remote off if Funnel appears while it is on (the alert
"Funnel appeared; turned off"). When it cannot read `tailscale serve status --json` it refuses too: "could not read
tailscale serve status --json, so cannot check Funnel is off: not publishing".

**Fix:** `tailscale funnel status` shows what is public; `tailscale funnel reset` turns Funnel off (it also clears this
machine's other `serve` settings). Then `lzr1-panel remote on`.

### `serve needs root on Linux`, or `tailscale serve failed: …`

On Linux `tailscaled` runs as root, and `tailscale serve` is refused to other users unless you made yourself its
operator. `doctor` says it as "is not Tailscale's operator: tailscale serve needs root". Other `serve failed` reasons
are Tailscale's own words, after the colon.

**Fix:** once, `sudo tailscale set --operator=$USER`, then `lzr1-panel remote on`.

### `remote needs ttyd`, or doctor's `no ttyd`

ttyd runs a panel for each browser. The installer offers it (it asks first).

**Fix:** `./install.sh` again and answer yes, or `brew install ttyd` (Linux: `apt install ttyd`, or a release from
github.com/tsl0922/ttyd).

### `remote needs Go to build its gateway`, or `could not build the gateway`

The login gateway (`gateway/`) is built from source with Go, once, by the installer or by the first `remote on`; it is
built again after an update changes its source. The installer says it as "could not build the remote gateway", with
Go's last line. A Go older than the one `gateway/go.mod` names (1.26) downloads the right one by itself, unless
`GOTOOLCHAIN=local` is set or there is no network: then the build fails saying so.

**Fix:** `brew install go` (Linux: your distribution's `golang` package, `golang-go` on Debian/Ubuntu). To see Go's
whole message: `lzr1-panel remote build`. With an old Go, unset `GOTOOLCHAIN` (or set it to `auto`) and build again.

### `remote needs jq`, or `remote needs openssl`

`remote` reads Tailscale's status with jq and makes the password with openssl.

**Fix:** `./install.sh` installs both.

### `the gateway did not start: …`

After the colon come the last lines of `~/.cache/lzr1-panel/remote/remote.log` and `ttyd.log`, which say why: ttyd
could not start, or the gateway refused its folder or its flags.

**Fix:** read those lines, then `lzr1-panel remote off` and `lzr1-panel remote on` again. If the gateway is the
problem, `lzr1-panel remote build` rebuilds it.

### `takes hours, minutes or seconds, like 8h, 90m or 1h30m`, or `LZR1_REMOTE_PWLEN must be 12 or more`

A setting `remote on` cannot use: `LZR1_REMOTE_TTL`, `LZR1_REMOTE_IDLE` or `--ttl` must be like `8h`, `90m` or
`1h30m`, and more than 0; the password is at least 12 characters. `doctor` names the setting with "remote on refuses
it".

**Fix:** correct it in `~/.zshenv` (or unset it for the default), then open a new terminal.

### `remote on: no login allowed in`, or `not a Tailscale login (like you@example.com): …`

The question "Tailscale logins allowed in" got no answer (Esc leaves the remote off on purpose), or an answer that is
not a login.

**Fix:** answer with the logins exactly as Tailscale shows them (the admin console's Users page, or `tailscale
status`), comma-separated: `you@example.com`, or `name@github` for a GitHub sign-in. Or skip the question:
`lzr1-panel remote on --allow you@example.com`.

### `remote is already on (lzr1-panel remote off turns it off)`

Only one remote at a time. The status above the message has its URL.

**Fix:** `lzr1-panel remote url` copies the URL again, `lzr1-panel remote password` the password. To start over:
`lzr1-panel remote off`, then `on` (a new URL and password).

### The browser says "signed in to Tailscale as …, not allowed here"

The page reached the gateway, but the device's Tailscale login (shown in the message) is not on the allowlist you gave
`remote on`.

**Fix:** sign that device in to Tailscale as an allowed login, or add the login: `lzr1-panel remote off`, then
`lzr1-panel remote on` and add it to the answer (it is prefilled with the last one), or
`--allow you@example.com,other@example.com`. The URL changes.

### The browser says "no Tailscale identity: open this from a device signed in to Tailscale as an allowed user"

`tailscale serve` names the person behind each request, but a tagged device (a server, a CI runner) has no person, so
it is always refused.

**Fix:** open the URL from a device signed in to Tailscale with your own login.

### The browser says "Too many wrong passwords: locked until …"

Five wrong passwords in a row from one login lock that login out for 15 minutes. Attempts while locked are refused
without being checked, and still count toward the kill switch below. You get an alert, "LOGIN locked out until HH:MM".

**Fix:** wait until the time shown. `lzr1-panel remote password` copies the right password on this computer. If it
was not you, `lzr1-panel remote rotate` (a new password; it also ends the lock) or `lzr1-panel remote off`.

### The browser says "too many wrong passwords: the remote panel was turned off"

The kill switch: 20 wrong passwords in total, from any logins, turned the whole remote off. The alert says "turned off:
20 wrong passwords", and `lzr1-panel remote status` says when.

**Fix:** see who tried in `~/.cache/lzr1-panel/remote/audit.log`. When you are sure it was you, `lzr1-panel remote on`
again: a new URL and password.

### Back at the login form, though I logged in

The session ended, and the page went back to the login form by itself:

- **Idle logout:** nothing typed, clicked, pasted or resized in that browser for `LZR1_REMOTE_IDLE` (30 minutes by
  default). Watching a long Claude run does not count as input.
- **A rotate** (`lzr1-panel remote rotate`, or the `w` menu on the computer) logged every browser out with a new
  password.

**Fix:** log in again (after a rotate, with the new password: `lzr1-panel remote password`). To be logged out less
often: `export LZR1_REMOTE_IDLE=2h` before `remote on`.

### A 502 after login: "the terminal is not answering (ttyd still starting or stopped)"

The gateway let you in, but ttyd, which runs the browser's panel, does not answer. Right after `remote on` it may still
be starting; otherwise it stopped, and `lzr1-panel remote status` says "ttyd: stopped (lzr1-panel remote off, then on)".

**Fix:** reload once. Still there: `lzr1-panel remote off`, then `lzr1-panel remote on`.

### The URL does not open at all

The remote is off (it expires after `LZR1_REMOTE_TTL`, 8 hours by default), the URL is an old one (every `remote on`
makes a new one), or the device is not on your tailnet. While the remote turns itself off you get an alert:
"expired, turned off", "the gateway stopped; turned off", "tailscale serve ended; turned off" (Tailscale was quit or
disconnected).

**Fix:** `lzr1-panel remote status` on the computer; turn it on again if it is off. On the other device, check that
Tailscale is connected.

### `could not unpublish …`, or `remote: off, but its path may still be published`

Everything was stopped, but Tailscale could not confirm the URL's path is gone (Tailscale quit, `tailscaled` down, the
operator permission removed), so the session file, the only record of that path, was kept. `doctor` says it as "a path
may still be published". `remote on` refuses until it is cleaned: "remote on: the last remote is still published".
Nothing answers on that path while the gateway is stopped.

**Fix:** start Tailscale, then `lzr1-panel remote off` again. By hand: `tailscale serve status --json` lists what is
published (the plain `tailscale serve status` can say "No serve config" while a path is published), and
`tailscale serve --https=443 --set-path /<uuid> off` removes it.

### `w` in a browser has no rotate or turn off ("not from a browser's panel: …")

By design: a browser's own panel offers only the two copies and who is connected, so a browser never turns off or
logs out the remote it is using.

**Fix:** on the computer, press `w` in its panel, or run `lzr1-panel remote rotate` or `lzr1-panel remote off`.

### `rotate failed: …; remote is off`, or `it expires in under a minute`

A rotate starts a new gateway; when it cannot start, the remote is turned off rather than left half on. In its last
minute, a remote is not rotated.

**Fix:** read the reason after "rotate failed:", then `lzr1-panel remote on`.

### `another lzr1-panel remote command is still running after 30 s (or … cannot be locked)`

`remote on`, `off` and `rotate`, and the check every other remote command makes first, run one at a time (a lock in
`~/.cache/lzr1-panel/remote/lock`), so a `status` or a second panel's `w` during a rotate waits for it instead of
taking the old, stopped gateway for a dead remote. This one waited 30 s, though a `remote on` or `rotate` holds the
lock for seconds (never during the allowlist prompt): another remote command is stuck, likely waiting on Tailscale.

**Fix:** finish or Ctrl-C the other command, then try again. The lock ends with the process that holds it; the file
itself can stay.

### A copy in the browser does not reach its clipboard ("Copied text ready — click to copy")

Safari lets a page write the clipboard only right after a click, so the copy waits in that note for 15 seconds.

**Fix:** click the note. Copies made in a browser's panel go to that browser's clipboard, not to this computer's (a
session on this computer is the exception: its copies land on both). More in
[remote.md, copy](remote.md#copy-select-and-the-right-button).

---

## Starting over

```sh
./install.sh --uninstall   # removes the command, shell hook, Claude skill and Ghostty key (keeps profiles)
./install.sh               # installs everything again
```

Your profiles stay in `~/.config/lzr1-panel/profiles/`. Remove one with `lzr1-panel profile remove NAME`.
