# lzr1-panel

Every Claude Code session you run, on every machine you own, in one clickable list.

```
┌ lzr1 ───────────────────────┬─ viewer ─────────────────────────────┐
│ 3 sessions · 1▶ 1⧗ 1·       │                                      │
│ oracle-server 2             │   the session you picked, live:      │
│   ▶ lerian-clients   2m ●   │   Claude Code running in tmux on     │
│   ⧗ midaz-fees      12m     │   that machine                       │
│ macbook-pro-lazari 1        │                                      │
│   · lzr1-panel      1h      │                                      │
│ [+opus] [+sonnet] [↻] [?]   │                                      │
└─────────────────────────────┴──────────────────────────────────────┘
```

- **Profiles** are machines: this computer, or servers over SSH. Each lists its tmux sessions with their state:
  `▶` working, `⧗` waiting on you, `·` idle, `●` a terminal is attached.
- **Click a session** (or use the arrow keys) and the viewer on the right shows it. `enter` moves the keyboard there.
- **New sessions** (`n` Opus, `s` Sonnet, or the footer buttons) ask for a name. A name matching a folder in the
  profile's repositories opens Claude there; the first-run "trust this folder?" question is answered for you.
- **Every session runs Remote Control**, so it is also on your phone and at claude.ai/code (`o` opens the link).
- **Sessions survive**: if Claude exits (two Ctrl-Cs, `/exit`, a crash), the session stays and Enter resumes the
  same conversation.
- Opens as a **Ghostty split** on macOS, or as a tmux layout anywhere else, with a 5-second splash (any key skips).

## Install

```sh
git clone https://github.com/victorlazari/lzr1-panel.git ~/Github/lazari/lzr1-panel
cd ~/Github/lazari/lzr1-panel
./install.sh
```

The installer works on macOS and Linux and is safe to run again. It installs what is missing (zsh, tmux, fzf 0.74+,
curl, openssl, git; Claude Code and, on macOS, Ghostty after asking), links `lzr1-panel` into `~/.local/bin`, adds
the Claude Code skill that creates profiles, adds a shell hook (`cl-<profile>-opus` / `-sonnet` helpers and,
optionally, the panel opening with Ghostty), and walks you through your first profile.
`./install.sh --uninstall` removes it all except your profiles.

Each SSH machine needs tmux and Claude Code installed (and signed in) there. `lzr1-panel profile test NAME` checks.

## Profiles

```sh
lzr1-panel profile add                       # guided
lzr1-panel profile add macbook-pro-lazari --local --repos ~/Github/lerian
lzr1-panel profile add my-server --ssh user@host --key ~/.ssh/my.key --repos '~/repos' --root '~'
lzr1-panel profile list | show NAME | test NAME | remove NAME
```

Or ask Claude (or any assistant): *"create a new lzr1-panel profile"*. The guide it follows is
[docs/profiles.md](docs/profiles.md): it asks for the name, the target and the repositories folder, then runs the
command above.

Profiles live in `~/.config/lzr1-panel/profiles/` and never in this repository: they hold hosts and key paths.

## Use

```sh
lzr1-panel open                               # the panel (Ghostty split on macOS, tmux layout elsewhere)
lzr1-panel start PROFILE opus|sonnet NAME     # a session without the panel
lzr1-panel list                               # sessions on every profile
lzr1-panel keys                               # every key
```

| Key | |
|---|---|
| click, `↑` `↓` | show that session in the viewer |
| `enter`, double-click | show it and move the keyboard to the viewer |
| `n` / `s`, `[+opus]` / `[+sonnet]` | new session on the highlighted profile |
| `t` | open it in a new Ghostty tab |
| `o` | open its Remote Control link |
| `x` | end it (asks first) |
| `r`, `[↻]` | refresh (it refreshes every 15s) |
| `?`, `q` | keys, quit |

In the viewer, **`Ctrl-b d` leaves a session**. Ctrl-C goes to Claude.

Settings: `LZR1_SPLASH=0` skips the splash, `LZR1_SPLASH_SECONDS=3` shortens it, `LZR1_PANEL=0` stops the panel
opening with Ghostty, `LZR1_CONFIG` moves the profiles folder.

## Layout

```
bin/lzr1-panel                  the command: panel, viewer, sessions, profiles
lib/splash.zsh                  the opening animation
install.sh                      installer for macOS and Linux
docs/profiles.md                how to create a profile (for people and AI assistants)
docs/splash-options.html        the ten splash animations considered (BIOS boot won)
skills/lzr1-panel-profiles/     the Claude Code skill that runs that guide
AGENTS.md                       pointers for AI assistants working here
```
