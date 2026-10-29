# Session Alerts and opencode Sessions Implementation Plan

> **For implementers:** Use ring:dispatching-workflows (rolling-phase: elaborate
> the current phase against the real code, run it as a reviewed multi-agent
> workflow, then elaborate the next phase — repeat), or ring:running-dev-cycle
> for the full subagent-orchestrated workflow.
> This document is the living source of truth — task elaboration for later
> phases is written back into it during execution.

**Goal:** The panel tells you when a session needs you (a mark on its row, a tone, an OS notification on macOS and
Linux), and it can start opencode sessions in tmux, with auto-approve on, next to the Claude ones.

**Architecture:** Everything stays in `bin/lzr1-panel` (zsh), with the scripts sent to a profile's machine kept POSIX
`sh`. Alerts are worked out on the computer running the panel, from the records `LIST_SCRIPT` already returns on
every refresh: the panel keeps each session's last state in `$STATE`, and a change into "needs you" makes an alert.
opencode sessions reuse the `start` / `run` / `_new` path with a second agent, marked on the tmux session so the
list knows how to read its screen.

**Tech Stack:** zsh, POSIX sh, tmux, fzf 0.74+, `afplay` / `osascript` / optional `terminal-notifier` (macOS),
`notify-send` / `paplay` / `canberra-gtk-play` (Linux), opencode v2.

## Phase Overview

| Phase | Milestone | Epics | Status |
|-------|-----------|-------|--------|
| 1 | A session that finishes its reply or asks a question gets a `✱` on its row, plays a tone and shows an OS notification | 1.1, 1.2 | Detailed |
| 2 | New opencode sessions (`--auto`) from the panel, the command line and the shell shortcuts, listed and alerted like Claude's | 2.1, 2.2 | Epic-level |

**Decisions already made with the owner (2026-10-29):**

- "Needs you" means **both**: a session goes from working (`▶`) to idle (`·`, Claude finished its reply), or it
  shows a question or permission prompt (`⧗`). "Claude exited" is not an alert (it already shows on the row).
- opencode v2.0.32 has no `--yolo`; its auto-approve flag is **`--auto`** ("auto-approve permissions that are not
  explicitly denied"). New opencode sessions always use `--auto`; no setting for another flag.

**Known limit (accepted):** the panel samples every 15 s, so an alert lags up to 15 s, and a reply that starts and
ends between two samples (idle → idle) makes no "finished" alert. A question still alerts, since it stays on screen.

---

## Phase 1 — Alerts

### Epic 1.1: Detect "needs you" and mark the row

**Goal:** each refresh compares every session's state with the last one seen; a change into "needs you" marks the
session unread (`✱` on its row, a count in the summary line) until you look at it in the viewer.
**Scope:** `bin/lzr1-panel` (`list_rows`, `_list`, `_select`, `keys`), `README.md`
**Dependencies:** none
**Done when:** with a test profile and a fake tmux session, the second `_list` after a working → idle or idle →
question change prints the row with `✱` and the summary with `1✱`; selecting the row clears it; `lzr1-panel list`
never marks or alerts.
**Status:** Pending

#### Task 1.1.1: Track state changes per session in `list_rows`

- [x] Done

**Context:** `list_rows` (`bin/lzr1-panel:367-422`) gathers each profile's records (fields at
`bin/lzr1-panel:103-104`: name, state `work|wait|idle`, …, `waitfor`) and renders them. It is called by
`lzr1-panel list` (`:568-570`) and by `_list ID` (`:572-574`), which the panel's refreshes, the 15 s ticker
(`:740`) and `_pos` (`:748`) all go through. `waitfor` is `"Claude exited · Enter resumes"` for an exited Claude
(`:132`). Old state is removed by `open` (`rm -rf $STATE/*(Nmd+1)`, `:594`).

**Implementation vision:**
- `list_rows` takes an optional panel ID: `list_rows [ID]`. Only `_list` passes it (`list_rows $2`); without an ID
  (`lzr1-panel list`) nothing is tracked, marked or alerted.
- Per session, keep the last seen state in `$STATE/seen/<profile>/<session>`: one line, `state<US>waitfor`
  (`<US>` = `\037`, as in the records). Shared by every open panel, so two panels never alert twice for one change.
- Compare and write under one lock for the whole refresh: `mkdir $STATE/seen.lock` (remove it first when older than
  10 s: a killed refresh must not stop alerts for good), released after the loop. If the lock is not taken in 2 s,
  skip tracking for this refresh (rows still render; the next refresh catches up).
- Alert rule, with `prev` from the file and `cur` from the record:
  - no `prev` file → record only (a new session, or the first sight of it);
  - `cur=wait`, `waitfor` is not the exited text, and (`prev` state ≠ `wait` or `prev` waitfor ≠ `cur` waitfor) →
    alert kind `question`;
  - `prev=work` and `cur=idle` → alert kind `done`;
  - anything else → record only.
- A panel's first `_list` only records: when `$STATE/$id.seeded` is missing, run the comparison without alerting,
  then create it. This keeps a panel reopened hours later from firing one alert per session that finished meanwhile.
- An alert does two things: create `$STATE/unread/<profile>/<session>` holding the kind (Task 1.1.2 renders it),
  and append `kind<US>profile<US>session<US>text` to a local array `alerts` (Epic 1.2 delivers it). `text` is the
  `waitfor` for `question`, the session's `title` (its task) for `done`, empty when there is none.
- Session and profile names never contain `/` (refused at `:449`, `:789`, `:976`; profile names at `:220`), so
  they are safe as path parts.
- Edge cases: a profile that does not answer this refresh is skipped (`:391`) and its seen files stay untouched; a
  session that ended leaves its files behind, harmless and swept by `open` after a day; a renamed session counts as
  new (no `prev`, record only).

**Files:**
- Modify: `bin/lzr1-panel:364-422` (`list_rows`)
- Modify: `bin/lzr1-panel:572-574` (`_list` passes its ID)

**Verification:** `zsh -n bin/lzr1-panel`. Then, with a scratch config and cache (never your real ones):
```sh
export LZR1_CONFIG=$(mktemp -d) XDG_CACHE_HOME=$(mktemp -d) LZR1_PRS=0 LZR1_NOTIFY=0 LZR1_SOUND=0
bin/lzr1-panel profile add t --local --repos /tmp --claude /bin/sh
tmux new-session -d -s alert-t -c /tmp 'echo "esc to interrupt"; sleep 600'
bin/lzr1-panel _list 1 >/dev/null; bin/lzr1-panel _list 1 >/dev/null          # seeds, then records
tmux respawn-pane -k -t =alert-t: 'clear; echo "Do you want to proceed?"; sleep 600'
bin/lzr1-panel _list 1 | tr '\0' '\n' | grep -c '✱'                           # 1 (after Task 1.1.2)
ls $XDG_CACHE_HOME/lzr1-panel/unread/t/                                        # alert-t
tmux kill-session -t =alert-t
```
`LZR1_NOTIFY` / `LZR1_SOUND` are ignored until Epic 1.2 exists.

**Done when:** the rule above fires exactly once per change, never on a panel's first refresh, never from
`lzr1-panel list`, and never twice when two panels refresh together.

#### Task 1.1.2: Show the unread mark and clear it when you look

- [x] Done

**Context:** a session's first line is built at `bin/lzr1-panel:408`: state glyph, name padded to
`W - 6 - ${#right}`, model and age, `●` when attached. The summary heading is `:419`
(`"$n sessions · $w▶ $q⧗ $i·"`). The viewer's target is written by `_select` at `:694` and `:709`
(`$STATE/$id.target`); the currently shown key is `$(<$STATE/$id.target)`.

**Implementation vision:**
- A session with an unread file gets a bold yellow `✱` and a space right after its state glyph; the name's padding
  shrinks by 2 so the right side stays aligned. Line 2 is unchanged (it already shows the question, or the task).
- The summary adds ` · N✱` only when N > 0 (count of rows rendered with the mark).
- Cleared when you look: `_select` removes `$STATE/unread/$KP/$KS` wherever it writes `$tfile` with the key
  (`:694`, `:709`). `list_rows` with an ID also removes it, without rendering the mark, when the key equals the
  panel's current target: the session on screen never shows `✱`. The tone and notification still fire for it
  (Epic 1.2): you may be in another app.
- Width: `✱` (U+2731) is one column in Ghostty, macOS Terminal and the usual Linux fonts, like `⧗` already used.
- `keys` (`:1083`) and the README glyph lines (session glyphs in "What you get", the ASCII sample) learn
  `✱ finished or asking since you last looked`.

**Files:**
- Modify: `bin/lzr1-panel:398-419` (row and summary), `:688-710` (`_select`), `:1083` (`keys`)
- Modify: `README.md` ("What you get" table, the panel sample)

**Verification:** `zsh -n bin/lzr1-panel`; the Task 1.1.1 script shows one `✱` row and `1✱` in the first item;
then `bin/lzr1-panel _select 1 t/alert-t force` (in the same scratch env) and `_list 1` again shows no `✱`.

**Done when:** the mark appears after an alert, survives refreshes, and disappears once the session is selected;
row alignment matches unmarked rows at the default 56 columns.

### Epic 1.2: Tone and OS notification

**Goal:** each refresh that produced alerts plays one tone and shows OS notifications on the computer running the
panel, with settings to turn either off, a test command, and doctor checks.
**Scope:** `bin/lzr1-panel` (new `notify_alerts`, `notify` command, `genv`, `doctor`), `README.md`,
`docs/troubleshooting.md`
**Dependencies:** Epic 1.1 (the `alerts` array and its record format)
**Done when:** `lzr1-panel notify` plays the tone and shows a notification on macOS (with and without
`terminal-notifier`) and on Linux with `notify-send`; a real alert does the same; `LZR1_SOUND=0` / `LZR1_NOTIFY=0`
silence each; the refresh never waits on either.
**Status:** Pending

#### Task 1.2.1: `notify_alerts` — the tone and the notifications

- [x] Done

**Context:** alerts are collected in `list_rows` (Task 1.1.1). `browse` and `clip` (`bin/lzr1-panel:351-359`) show
the house pattern for macOS / Wayland / X11 branches. `ghostty()` (`:336`) already sends AppleScript on stdin.
`text` comes from the session's screen, so it is untrusted: it must never be pasted into AppleScript or a shell
command line.

**Implementation vision:**
- `notify_alerts` takes the alert records as arguments and runs in the background from `list_rows` (`… &!`) after
  the loop, so a slow `osascript` never delays the list.
- **Tone** (`LZR1_SOUND`, default `1`; `0` off; any other value is a sound file path): one per refresh, however many
  alerts. macOS: `afplay` with the file, default `/System/Library/Sounds/Glass.aiff`. Linux: `paplay` with the file,
  default `/usr/share/sounds/freedesktop/stereo/complete.oga`; else `canberra-gtk-play -i complete`; else a terminal
  bell to `/dev/tty`. Missing player or file → silent, no error.
- **Notification** (`LZR1_NOTIFY`, default `1`; `0` off): one per alert, up to 3; more than 3 → the first 2 and one
  "N more sessions need you". Title: `<session> finished` (`done`) or `<session> asks` (`question`); subtitle /
  app: the profile; body: `text`, with control characters stripped and cut to 120 characters (`_cut`), else
  `your turn`.
  - macOS with `terminal-notifier`: `-title`, `-subtitle`, `-message`, `-group lzr1-<profile>-<session>` (a newer
    alert replaces the session's older one), no `-sound` (the tone is ours).
  - macOS without it: `osascript` reading `on run argv … display notification (item 3 of argv) with title (item 1
    of argv) subtitle (item 2 of argv) … end run` from stdin, the three strings passed as argv. No sound name.
  - Linux with `notify-send` and `$DISPLAY` or `$WAYLAND_DISPLAY`: `notify-send -a lzr1-panel TITLE BODY`.
  - Anything else: no notification (the tone and the row mark still work).
- `genv` (`:343`) passes `LZR1_NOTIFY` and `LZR1_SOUND` into Ghostty splits, like the other `LZR1_*` settings.
- New command `notify` (`lzr1-panel notify`): calls `notify_alerts` in the foreground with one sample `question`
  alert (`lzr1-panel` / `test` / `this is how a session asks for you`), for checking setup. Add it to the usage
  block (`:5-16`, printed by `:1095` from lines 2-18: keep the block within those lines or update the `sed` range).
- Edge cases: `osascript` refused by macOS notification permissions → it fails silently in the background, and
  doctor/troubleshooting cover it; several panels open → the shared seen state (Task 1.1.1) means only one alerts.

**Files:**
- Modify: `bin/lzr1-panel` (new `notify_alerts` near `clip`, `:351-359`; call site in `list_rows`; `genv` `:343`;
  `notify)` command in the `case`; usage block `:5-16`)

**Verification:** `zsh -n bin/lzr1-panel`; `bin/lzr1-panel notify` → tone plus a notification titled "test asks";
`LZR1_SOUND=0 bin/lzr1-panel notify` → notification only; `LZR1_NOTIFY=0 bin/lzr1-panel notify` → tone only.
Rerun the Task 1.1.1 script without `LZR1_NOTIFY=0 LZR1_SOUND=0`: the question change gives one tone and one
notification; a session screen containing `"; do shell script "say hi` in its question shows that text literally.

**Done when:** tone and notifications work on macOS (both paths) and Linux, never block `_list`, and untrusted
screen text cannot run anything.

#### Task 1.2.2: Doctor, settings and troubleshooting

- [x] Done

**Context:** `doctor` (`bin/lzr1-panel:488-527`) prints sections with `_ok` / `_no` / `_hint`; optional things use
`print "  · …"` (e.g. Ghostty, `:507-508`) and do not set `bad`. README's Settings table and troubleshooting's
sections are where users look.

**Implementation vision:**
- Doctor gets an `alerts` section after `github`, informational only (never sets `bad`): shows `LZR1_SOUND=0` /
  `LZR1_NOTIFY=0` when set; macOS: `✓ tone: afplay`, then `✓ notifications: terminal-notifier` or
  `· notifications: osascript` with the hint `brew install terminal-notifier (groups them per session)`; Linux:
  `✓ notifications: notify-send` or `· no notify-send` with the hint `install libnotify (libnotify-bin on
  Debian/Ubuntu)`, and the tone player found (`paplay`, `canberra-gtk-play`, else "terminal bell"). Ends with the
  hint `lzr1-panel notify sends a test`.
- README: a "What you get" row for alerts; Settings rows for `LZR1_SOUND` and `LZR1_NOTIFY`; `lzr1-panel notify` in
  "From the command line".
- Troubleshooting, new section "Alerts": "No sound" (setting, player, file), "No notification on macOS" (System
  Settings → Notifications: allow Script Editor, or terminal-notifier; Focus modes), "No notification on Linux"
  (notify-send, a running notification daemon, `$DISPLAY`), "An alert came late or not at all" (the 15 s sample and
  the idle → idle case from this plan's Known limit), and add it to the section list at the top.

**Files:**
- Modify: `bin/lzr1-panel:488-527` (`doctor`)
- Modify: `README.md`, `docs/troubleshooting.md`

**Verification:** `zsh -n bin/lzr1-panel lib/splash.zsh && bash -n install.sh`; `bin/lzr1-panel doctor` shows the
alerts section and its exit status is unchanged by it.

**Done when:** doctor reports what alerts will use on this computer, and the docs cover every setting and the
common failures.

---

## Phase 2 — opencode sessions

### Epic 2.1: Start, run and list opencode sessions

**Goal:** `lzr1-panel start PROFILE opencode NAME` and `lzr1-panel run --opencode` start `opencode --auto` in a new
tmux session (repository folder rules as today), the session survives opencode exiting (Enter resumes with
`--continue`), and the list shows it with `opencode` as its model and the right working / waiting / idle state, so
Phase 1's alerts work for it.
**Scope:** `bin/lzr1-panel` (`load`, `profile_add`, `profile_test`, `start`, `run`, `LIST_SCRIPT`)
**Dependencies:** Phase 1 (alerts must apply to opencode states too)
**Done when:** an opencode session started both ways is listed with its folder, branch and `opencode` model; its
state follows a real prompt (working while it answers, idle after, waiting on a permission it asks); exiting
opencode leaves the resume prompt; profiles without opencode still work and say so in `profile test`.
**Status:** Pending

Notes for elaboration: profile key `opencode=` (path found at `profile add`, not required); tmux option
`@lzr1_agent=opencode` beside `@lzr1_model`; no Remote Control link or folder-trust step for opencode. The screen
markers for opencode's working / waiting states are unknown: start this epic with a spike that captures real
opencode panes (`tmux capture-pane`) while it answers, asks and sits idle, and write the markers into `LIST_SCRIPT`
for sessions with `@lzr1_agent=opencode` only.

### Epic 2.2: Panel keys, menus and shell shortcuts for opencode

**Goal:** a new opencode session is one key, one footer click, or one menu item away in the panel, and one shell
command away (`cl-opencode` in this folder, `cl-<profile>-opencode NAME` on a profile), documented everywhere the
Claude ones are.
**Scope:** `bin/lzr1-panel` (`_new`, `_menu`, `_panel` binds and footer, `keys`, `doctor`), `install.sh` (shell
hook), `README.md`, `docs/troubleshooting.md`
**Dependencies:** Epic 2.1
**Done when:** `c` and `[+opencode]` open the new-session picker for opencode; the machine and session menus offer
"new opencode session"; after `./install.sh`, `cl-opencode` and `cl-<profile>-opencode` exist (yours win, as with
`cl-opus`); `bash -n install.sh` passes on bash 3.2.
**Status:** Pending

Notes for elaboration: `o` is taken (open link), hence `c`; `_new` currently accepts `opus|sonnet` and must take
`opencode`; the footer must fit 56 columns.
