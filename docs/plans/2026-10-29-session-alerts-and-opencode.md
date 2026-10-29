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
| 1 | A session that finishes its reply or asks a question gets a `✱` on its row, plays a tone and shows an OS notification | 1.1, 1.2, 1.3 | Detailed |
| 2 | Profiles list only their repositories folder and can be edited or removed from the panel; new opencode sessions (`--auto`) from the panel, the command line and the shell shortcuts, listed and alerted like Claude's | 2.1, 2.2, 2.3 | Detailed |

**Decisions already made with the owner (2026-10-29):**

- "Needs you" means **both**: a session goes from working (`▶`) to idle (`·`, Claude finished its reply), or it
  shows a question or permission prompt (`⧗`). "Claude exited" is not an alert (it already shows on the row).
- Profiles (added 2026-10-29): `profile add` no longer asks for a folder for non-repository sessions nor whether to
  list only the folder's sessions: every new profile opens and lists sessions inside its repositories folder
  (`root=` and `only=` = `repos=`). A `_` in a profile name becomes `-`; any other invalid name is asked again. A
  machine heading's right-click menu offers edit and remove.
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
**Status:** Done

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
  session that ended has its `seen` and `unread` files removed by the next refresh its profile answers, so a new
  session with the same name starts with no `prev`; each tracked refresh touches `seen/` and `unread/`, so `open`'s
  day-old sweep never takes them; a renamed session counts as new (no `prev`, record only). A panel's first refresh
  (no `<ID>.seeded`) writes only the seen files that are missing or not written in the last minute (no open panel
  keeps them): it never overwrites one another panel is about to compare, and never alerts.

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
**Status:** Done

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

### Epic 1.3: Alerts survive killed refreshes and two panels

**Goal:** the review's Medium findings are fixed: a refresh fzf kills mid-way never leaves a lock that stalls the
next ones nor loses an alert, and two panels never move the shared state back in time.
**Scope:** `bin/lzr1-panel` (`list_rows` tracking, `open`, `_notify_one`)
**Dependencies:** Epics 1.1, 1.2
**Done when:** the reproductions below pass and every Phase 1 verification script still passes.
**Status:** Done

#### Task 1.3.1: Owned lock, snapshot time, detached notifier, safe names and bodies

- [x] Done

**Context:** `list_rows` takes `$STATE/seen.lock` with `mkdir` after the parallel fetch's `wait`, treats it as
stale after 10 s by mtime (`rmdir` then `mkdir`, a check-then-act race), writes `seen/` files as it compares, and
runs `notify_alerts … &!` at the end. fzf kills a running reload when the next one comes (and `_pos` fires up to 20
reloads 0.8 s apart), killing its process group: a kill can leave the lock, or record a change whose alert never
goes out. The panel ID is `open`'s PID, and `$ID.seeded` is never removed. Paths are built from raw session names,
and a session created by hand with plain tmux may contain `/`. `notify-send` bodies are read as markup by most
Linux daemons.

**Implementation vision:**
- **Lock owner:** the lock directory holds `pid` (the refresh's `$$`). A lock is stale when its pid is not alive
  (`kill -0` fails) or it is older than 10 s; break it by renaming it to `seen.lock.$$` and removing that, so only
  one panel wins the cleanup. Release it in an `always` block / trap on every exit path of the tracking code.
- **Snapshot time:** take `EPOCHSECONDS` (zsh/datetime) before the fetch starts; each seen file becomes
  `ts<US>state<US>waitfor`. Under the lock, when a session's stored ts is newer than this refresh's snapshot, skip
  it (no compare, no write). Old two-field files are read as ts=0.
- **Nothing lost on a kill:** compute all alerts first, write the unread files and start the notifier, then write
  the seen files. A kill in between can at worst repeat an alert on the next refresh, never drop one.
- **Detached notifier:** start `notify_alerts` in its own process group (zsh `setopt local_options monitor` before
  the `&!`, or an equivalent that works non-interactively on macOS and Linux; verify by killing the refresh's
  process group and seeing the stub notifier still run).
- **Fresh seed per panel:** `open` removes `$STATE/$id.seeded` before starting the panel.
- **Safe names:** seen/unread file names encode `/` in session names as `%2F` (and `%` as `%25`); every reader
  (unread mark, prune, `_select` clearing) uses the same encoding.
- **Safe Linux body:** escape `&`, `<`, `>` in the `notify-send` title and body.
- Out of scope (Low, accepted): an empty list from a reachable machine whose tmux failed for a moment prunes its
  files (worst case: one missed alert).

**Files:** Modify `bin/lzr1-panel` (`list_rows`, `_select`, `open`, `_notify_one`)

**Verification:** `zsh -n bin/lzr1-panel`; scratch env as in Phase 1; reproductions: a leftover `seen.lock` whose
pid is dead does not delay `_list` (under 0.5 s over the unlocked time); two refreshes writing out of order (older
snapshot last) give one `done` alert, not two; killing `_list`'s process group right after it computes alerts
still runs the stub notifier; a tmux session `alert-a/b` gets its ✱ and is pruned when killed; a question
`<b>x</b> & y?` reaches the notify-send stub as `&lt;b&gt;x&lt;/b&gt; &amp; y?`. Then re-run verify-111, 112, 121,
122 and c1.

**Done when:** all of the above pass.

---

## Phase 2 — Profiles and opencode sessions

### Epic 2.1: Profiles list their folder only, and can be edited or removed from the panel

**Goal:** `profile add` asks only name, target and repositories folder, re-asks a bad name, and every new profile
lists exactly the sessions inside its repositories folder; `profile edit` and the machine heading's menu edit or
remove a profile.
**Scope:** `bin/lzr1-panel` (`profile_help`, `profile_add`, new `profile_edit`, `profile)` command, `_menu`),
`docs/profiles.md`, `skills/lzr1-panel-profiles/SKILL.md`, `README.md`
**Dependencies:** Phase 1
**Done when:** the guided and flag forms write `root=` and `only=` equal to `repos=`; `my_mac` becomes `my-mac`; an
invalid or taken name is asked again; `profile edit NAME` changes a profile in place; the heading menu offers edit
and remove.
**Status:** Done

#### Task 2.1.1: `profile add`: repositories folder only, names checked and asked again

- [x] Done

**Context:** `profile_add` (`bin/lzr1-panel`, "profile management") asks, in its guided form, name, target, SSH
host/key/port, the repositories folder (example `~/Github/lerian`), a root folder for non-repository sessions, and
whether to list only the folder's sessions (`read -q`); the flag form takes `--root` and `--only`. It then validates
the name once (`[a-z0-9][a-z0-9-]#`, else `die`), refuses an existing name with `die`, writes the file and runs
`profile_test`. `load` keeps reading `root=` and `only=` (old profiles stay as they are).

**Implementation vision:**
- Guided: the root and "only" questions are removed. The repositories prompt reads `Folder with your repositories
  (e.g. ~/Github/my-repositories): `; an empty answer is asked again.
- Name, both forms: `_` is replaced by `-` (say so: `  using my-mac`); then a name that does not match the pattern,
  or that already exists, prints why and, in the guided form, asks again (`die` stays for the flag form, which has
  no one to ask). Uppercase is not converted (only `_` was asked for).
- Both forms write `root=$repos` and `only=$repos`. `--root` and `--only` are removed: passing either dies with
  `profile add: --root and --only are gone: a profile opens and lists sessions inside --repos`.
- `profile_help` drops `--root`/`--only` and says a profile lists only the sessions inside `--repos`.
- Docs: `docs/profiles.md` (what a profile holds, the conversation: questions 4 and 5 go, examples use
  `~/Github/my-repositories`, the "two profiles on one machine" section explains it is now automatic),
  `skills/lzr1-panel-profiles/SKILL.md` (same), README's Profiles section (examples, the `--only` paragraph).
- Edge cases: a name that is only `_` characters becomes dashes and then fails the pattern (asked again); repos
  given with a trailing `/` is written as typed (the list script already strips it from `only`).

**Files:** Modify `bin/lzr1-panel` (`profile_help`, `profile_add`), `docs/profiles.md`,
`skills/lzr1-panel-profiles/SKILL.md`, `README.md`

**Verification:** `zsh -n bin/lzr1-panel`; scratch `LZR1_CONFIG`: `print -l 'bad name' 'my_mac' 1 /tmp | bin/lzr1-panel
profile add` asks the name twice and writes `my-mac.conf` with `repos=/tmp`, `root=/tmp`, `only=/tmp`; adding
`my-mac` again (guided) re-asks; `profile add x --local --repos /tmp --root /a` dies with the message above;
`profile add a_b --local --repos /tmp` writes `a-b.conf`.

**Done when:** new profiles always list only their repositories folder, and names are fixed or asked again.

#### Task 2.1.2: `profile edit` and the heading menu's edit and remove

- [x] Done

**Context:** `profile)` dispatches `list|show|add|test|remove`; `remove` deletes the file. `_menu ID KEY` on a
machine heading (`KS` empty) offers `opus`, `sonnet`, `test`, `reload`. `_kill` shows how a menu action that removes
something first moves the viewer (`first_key`, `_select … force`) and then the highlight (`_pos`).

**Implementation vision:**
- New `profile edit NAME` (and `profile_edit`): loads the profile, then asks with zsh `vared` (current value
  prefilled, Enter keeps it): name, and for ssh host, key, port; then the repositories folder. Same checks as add
  (`_`→`-`, pattern, a new name must not exist, empty repos asked again). Writes the file (a rename writes the new
  file and removes the old), with `root=` and `only=` set to the repositories folder (an edit brings an old profile
  to the new rule), keeps `claude=` (and `opencode=` once Task 2.2.1 exists), then runs `profile_test`.
- `profile_help` lists `profile edit NAME`.
- Heading menu: adds `edit\tedit this profile…` and `remove\t<red>remove this profile…</red>` after `test`.
  - edit → `$SELF profile edit $KP`, then `press a key`.
  - remove → asks `Remove profile $KP? Its sessions keep running in tmux; the panel stops listing them. [y/N]`;
    on yes: if the viewer's target is on this profile, move it first to the first session of another profile
    (`first_key` skipping every `$KP/*` key) or clear it, then `profile remove $KP`, then `$STATE/raw-$KP`,
    `seen/$KP`, `unread/$KP` and `down-$KP` are removed.
- The menu's `reload-sync` after `execute` refreshes the list as today.
- Edge cases: renaming to an existing name re-asks; editing a profile while its machine is offline still saves and
  reports the failed test; removing the last profile leaves the panel's "no profiles yet" rows.

**Files:** Modify `bin/lzr1-panel` (`profile_help`, new `profile_edit`, `profile)`, `_menu`), `README.md` (menu
table: "A machine's heading has a menu too"), `docs/profiles.md` ("Change a profile")

**Verification:** `zsh -n bin/lzr1-panel`; scratch env: `print -l '' '' ~/x | bin/lzr1-panel profile edit t` keeps
the name and sets repos/root/only to `~/x` (vared reads stdin when not a tty: if not, drive it with `script` or a
tmux pane and `send-keys`); a rename `t`→`t2` leaves only `t2.conf`; in a tmux pane running the menu
(`bin/lzr1-panel _menu 1 t/`), the heading menu lists edit and remove, and remove with `y` deletes `t.conf` and
`seen/t`.

**Done when:** profiles can be edited and removed from the command line and the heading's menu, and removing one
never leaves the viewer on a session the panel no longer lists.

### Epic 2.2: Start, run and list opencode sessions

**Goal:** `lzr1-panel start PROFILE opencode NAME` and `lzr1-panel run --opencode` start `opencode --auto` in a new
tmux session, which survives opencode exiting; the list shows it with model `opencode`, its task, and the right
working / waiting / idle state, so Phase 1's alerts work for it.
**Scope:** `bin/lzr1-panel` (`load`, profile commands, `start`, `run`, `LIST_SCRIPT`, alert exclusion)
**Dependencies:** Epic 2.1 (both touch `profile_add`)
**Done when:** Tasks 2.2.1-2.2.3 pass; Claude sessions behave exactly as before.
**Status:** Done

Spike (2026-10-29, opencode 2.0.32 in tmux 200x50): working shows `esc interrupt` in the bottom bar; the question
tool shows a dialog (question line, `1. …` options, `↑↓ select  enter submit  esc dismiss`); idle shows neither;
`pane_title` is `OpenCode`, then `OC | <session title>`; with `--auto` no permission prompt appeared (`rm -rf`,
reading `/etc/hosts`); `/exit` typed in the input did not quit; `--continue` resumes; no Remote Control link and no
folder-trust prompt.

#### Task 2.2.1: The profile knows its opencode

- [x] Done

**Context:** `load` (`bin/lzr1-panel`, "profiles" section) reads `KEY=value` lines into `P_*` and defaults
`P_CLAUDE` to `claude`. `profile_add` finds Claude's path on the machine (`command -v claude ||
$HOME/.local/bin/claude`) and dies without it; `profile_test` prints one `key=value` line per check and counts any
line containing `missing` as a failure; `profile_help` documents the options.

**Implementation vision:**
- New optional profile key `opencode=PATH`, read by `load` into `P_OPENCODE`, default `opencode`.
- `profile add` takes `--opencode PATH`; without it, it looks the path up like Claude's (`command -v opencode ||
  { [ -x "$HOME/.opencode/bin/opencode" ] && echo …; }`) and writes `opencode=` only when found. A machine without
  opencode is fine: never `die`. The guided flow asks nothing new.
- `profile test` prints `opencode=<path>`, or `opencode=not installed (opencode sessions unavailable)`. It must not
  contain the word `missing`, so a profile without opencode still passes (and so `doctor` stays green).
- `profile_help` lists `--opencode PATH`.
- Edge cases: an `opencode=` path that no longer exists → `profile test` reports `not installed`; profiles written
  before this change have no key → the default `opencode` from PATH.

**Files:** Modify `bin/lzr1-panel` (`load`, `profile_help`, `profile_test`, `profile_add`)

**Verification:** `zsh -n bin/lzr1-panel`; in a scratch `LZR1_CONFIG`: `profile add t --local --repos /tmp` writes
`opencode=/opt/homebrew/bin/opencode` (on this Mac) and `profile test t` exits 0 with an `opencode:` line; with
`--opencode /nope` the test still exits 0 and says `not installed`.

**Done when:** profiles carry an optional opencode path, and its absence never fails `profile add`, `profile test`
or `doctor`.

#### Task 2.2.2: `start … opencode` and `run --opencode`

- [x] Done

**Context:** `start)` builds `claude=…` and the resume loop `cmd` (`trap : INT; …; while :; do printf 'Claude
exited. Enter: …'; read -r a …; $claude --continue; done`), then runs a POSIX sh script on the machine that picks
the folder (repos/NAME, repos/REPO for `REPO-prN`, else root), runs `tmux new-session`, sets `@lzr1_model`, then
waits up to 15 s accepting the folder-trust prompt and catching the Remote Control link. `run)` does the same
locally and attaches. `LIST_SCRIPT` and Phase 1's alert rule recognise the exited text `Claude exited · Enter
resumes` exactly.

**Implementation vision:**
- `start PROFILE opus|sonnet|opencode NAME [args]`. For `opencode`: `oc="${(q)P_OPENCODE} --auto"`;
  first run `$oc [args] ${LZR1_PROMPT:+--prompt ${(q)LZR1_PROMPT}}`, resume `$oc --continue`; the loop prints
  `opencode exited. Enter: resume this conversation · type end + Enter: close the session`.
- The remote script gets a 6th argument, the agent (`claude|opencode`), and sets `tmux set-option @lzr1_agent
  opencode` plus `@lzr1_model opencode`. For opencode it skips the trust/Remote Control loop: it checks the
  session is still alive after 2 s (`opencode exited right after start` otherwise) and prints `started`.
  Claude's path is unchanged.
- `run --opencode [args]`: the binary is `${commands[opencode]:-$HOME/.opencode/bin/opencode}`; `--model` and
  `--effort` are refused with it (`die`). Passthrough (exec plain opencode here) for `-h|--help|-v|--version`, a
  first argument that is an opencode subcommand (`run|session|auth|models|mcp|serve|upgrade|update|stats|…`: match
  `[a-z]*` that is not a path), no tty, or no tmux. Otherwise the same naming, new-session, `@lzr1_agent` /
  `@lzr1_model`, `$CLIP`, and attach/switch as Claude's.
- The exited marker becomes agent-neutral: `LIST_SCRIPT` matches `Claude exited|opencode exited`, `waitfor` is
  `<Agent> exited · Enter resumes`, and Phase 1's alert exclusion matches `* exited · Enter resumes`.
- Usage block and `start` usage message list `opencode`.
- Edge cases: opencode missing on the machine → tmux starts, the command fails at once, the 2 s check reports
  `opencode exited right after start` (the session is still there, at the resume prompt); a NAME with `.:/|` is
  refused as today.

**Files:** Modify `bin/lzr1-panel` (usage block, `LIST_SCRIPT` exit lines, `list_rows` alert exclusion, `start)`,
`run)`)

**Verification:** `zsh -n bin/lzr1-panel`; scratch env with a local test profile: `bin/lzr1-panel start t opencode
alert-oc` → `started`; `tmux show-options -v -t '=alert-oc:' @lzr1_agent` → `opencode`; the pane shows opencode's
input box; `tmux send-keys -t '=alert-oc:' C-c` twice, the resume prompt appears; kill `alert-oc`. A Claude
`start` with a stub claude still runs its old path.

**Done when:** opencode sessions start both ways with `--auto`, survive exit, and Claude sessions behave exactly as
before.

#### Task 2.2.3: List opencode sessions with the right state

- [x] Done

**Context:** `LIST_SCRIPT` reads tmux fields split on `|` (title last), decides `st` from Claude's markers (`esc to
interrupt` → work; `Enter to select|Do you want to|Would you like to proceed|…exited` → wait), the model from
`@lzr1_model` or Claude's header, the task from `pane_title`, and the question line for `waitfor`. Spike findings
(opencode 2.0.32 in tmux): working shows `esc interrupt` in its bottom bar; its question tool shows a dialog whose
first line is the question, then `1. …` options, then `↑↓ select  enter submit  esc dismiss`; idle shows neither;
`pane_title` is `OpenCode` before any task and `OC | <session title>` after; no permission prompt appeared under
`--auto`.

**Implementation vision:**
- Add `#{@lzr1_agent}` to the tmux format before `#{pane_title}` (title must stay last) and read it as `agent`.
- `agent=opencode`: `work` if the last 25 lines contain `esc interrupt`; `wait` if they contain `enter submit`,
  `Permission required` or `opencode exited`; else `idle`. `waitfor`: the exited text; else the question, which is
  the last non-empty line before the first line matching `┃[[:space:]]*1\. `, with `┃` and surrounding spaces
  stripped; else `waiting on you`. Title: strip a leading `OC | `; `OpenCode` → empty. No Remote Control grep.
- Any other agent (empty included) keeps Claude's detection untouched.
- POSIX sh only (`sed`, `grep -E`, `awk` are fine; no `[[`).
- Edge cases: a session that is not ours running opencode (no `@lzr1_agent`) is read as Claude and mostly shows
  idle, accepted; `Permission required` is unverified on 2.0.32 (kept as best effort, noted in troubleshooting).

**Files:** Modify `bin/lzr1-panel` (`LIST_SCRIPT`)

**Verification:** scratch env; fake panes with `@lzr1_agent=opencode` set by hand, printing the captured screens
(`esc interrupt` bar; the question dialog; a plain reply) → `_list 1` records `work`, `wait` with waitfor `Do you
like the tmux poem I wrote into poem.md?`, `idle`; a work → idle change gives one `done` alert (stub notifier).
Then one real `start t opencode alert-oc2` with a short prompt shows `work` then `idle`; kill it.

**Done when:** opencode rows show the right glyph, question and task, and alert like Claude's.

### Epic 2.3: Panel keys, menus and shell shortcuts for opencode

**Goal:** a new opencode session is one key, one footer click or one menu item away in the panel, and one shell
command away, documented wherever the Claude ones are.
**Scope:** `bin/lzr1-panel` (`_new`, `_menu`, `_panel`, `keys`, `doctor`), `install.sh`, `README.md`,
`docs/troubleshooting.md`
**Dependencies:** Epic 2.2
**Done when:** Tasks 2.3.1-2.3.2 pass; `bash -n install.sh` passes on bash 3.2.
**Status:** Done

#### Task 2.3.1: Panel: `c`, `[+opencode]` and menus

- [x] Done

**Context:** `_new opus|sonnet ID KEY [NAME [PROMPT]]` picks a machine and a name, then runs `start`; `_panel`
binds `n`/`s` and the footer `[+opus] [+sonnet] [↻] [?]` with `click-footer` matching `*opus*`, `*sonnet*`;
`_menu` offers `opus`/`sonnet` on a machine heading and `new` on a session; `keys` lists the keys.

**Implementation vision:**
- `_new` accepts `opencode` (prompt `new opencode on …`). It does not check that opencode exists on the machine:
  `start` reports that.
- `c` → `execute($S _new opencode $id {1})+reload-sync(…)`; footer `[+opus] [+sonnet] [+opencode] [↻] [?]` (37
  columns, fits 56); `click-footer` adds `*opencode*` before `*opus*`.
- Menus: machine heading adds `opencode\tnew opencode session here`; session menu adds `newoc\tnew opencode
  session on this machine` after `new`; the Esc item reads `interrupt it (Esc)` when the row's model is
  `opencode`.
- `keys`: `c  or [+opencode]  new opencode session (auto-approve on)`.

**Files:** Modify `bin/lzr1-panel` (`_new`, `_menu`, `_panel`, `keys`)

**Verification:** `zsh -n bin/lzr1-panel`; `grep` the bindings; in the scratch env, `bin/lzr1-panel _new opencode
1 t/ alert-oc3` (NAME given, so no picker) starts it and `_list 1` lists `alert-oc3` with model `opencode`; kill it.

**Done when:** opencode sessions start from the key, the footer and both menus.

#### Task 2.3.2: Shell shortcuts, doctor and docs

- [x] Done

**Context:** `install.sh` writes a shell hook (lines ~180-200) with `cl-<profile>-opus/sonnet` (eval loop) and
`cl-opus`/`cl-sonnet` (`type … || …`); it must parse on bash 3.2. `doctor` checks tools under "this computer".

**Implementation vision:**
- Hook: the eval loop also defines `cl-$_p-opencode() { lzr1-panel start $_p opencode "$@"; }`; add
  `type cl-opencode >/dev/null 2>&1 || cl-opencode() { lzr1-panel run --opencode "$@"; }`, with comments updated.
  Existing hooks are rewritten the way the installer already does for its block.
- Doctor: `✓ opencode <version>` or `· no opencode: opencode sessions unavailable` (hint: `brew install
  sst/tap/opencode` or opencode.ai), never `bad`.
- README: tagline/"What you get" mention opencode; the panel table gets `c` / `[+opencode]`; command line gets
  `start PROFILE opus|sonnet|opencode` and `run --opencode`; shortcuts get `cl-opencode` / `cl-<profile>-opencode`;
  footer sample updated. Troubleshooting: "An opencode session shows the wrong state" (markers follow opencode
  2.0.32's screen; newer versions may change them) and "opencode sessions do not start on a machine" (`profile
  test`).

**Files:** Modify `install.sh`, `bin/lzr1-panel` (`doctor`), `README.md`, `docs/troubleshooting.md`

**Verification:** `bash -n install.sh && /bin/bash -n install.sh` (macOS bash 3.2); extract the generated hook into
a scratch zsh and bash, source it with a scratch profile, `type cl-opencode cl-t-opencode` defines both; `doctor`
in the scratch env shows the opencode line and its exit status is unchanged.

**Done when:** `cl-opencode` and `cl-<profile>-opencode` exist after install, and every doc lists opencode where it
lists the Claude ones.

## Deviations

- Phase 1: reviewer agent types are not available to workflow agents here; reviews ran as general-purpose agents
  following the Ring reviewer instructions. The contrarian's two Medium findings (ended sessions' leftover state,
  new panels eating a change) were fixed in 7fc2b9a; the review's lock/ordering Mediums became Epic 1.3.
- An unseeded panel still overwrites a seen file older than 60 s (no open panel keeps it), so a panel reopened hours
  later does not alert for everything since.
- Task 1.3.1: zsh refuses `setopt monitor` without a terminal, so the notifier is detached with `setsid` (Linux) or
  `perl -e 'setpgrp; exec @ARGV'` (macOS) through a new internal `_notify` command; with neither it runs as before.
  The snapshot is `EPOCHREALTIME`, not `EPOCHSECONDS` (`_pos` reloads are 0.8 s apart). A lock with no pid file
  after 1 s also counts as stale (a refresh writes it right after `mkdir`). verify-111 and verify-c1 now read the
  state as the seen file's second field, and verify-111 holds its lock with a live pid.
- Task 2.1.1: the guided questions print their prompt to stderr themselves (zsh's `read "x?prompt"` shows it only on
  a terminal), so piped answers show each question, and the end of input dies with `profile add: no answer` instead
  of looping. The name check is a new `profile_name` (sets `REPLY`, or `WHY`), for `profile edit` to reuse.
  `docs/troubleshooting.md` ("The same session shows under two profiles") also dropped `--only`. The earlier
  verification scripts dropped `--only` from their `profile add` lines (copies kept as `*.pre211`).
- Task 2.1.2: `vared` needs a terminal ("can't access terminal"), so piped answers (agents, tests) show
  `Question [current]` and read a line: empty keeps the value, `-` empties it (key and port). zsh picks vi keys when
  `$EDITOR` says vi, and vi's insert mode cannot delete a prefilled value, so `profile edit` binds Backspace, ^H, ^U
  and ^W in `viins` for its own process. The edit keeps `type=`; a key that is not readable is asked again. Lines
  other than type/host/key/port/repos/root/only are kept as they are (so `claude=` and a later `opencode=`). The
  menu's remove takes the seen lock while it deletes the profile's state. `docs/troubleshooting.md` ("The same
  session shows under two profiles") now says `profile edit NAME`. The task's verification line
  `print -l '' '' ~/x` has one answer too many for a local profile (name, repositories folder); verify-212 uses the
  right count and a resolved scratch folder, and drives `vared` and the menu in tmux panes.
- Task 2.2.1: `profile add` also writes `opencode=` from `--opencode` as given, without checking it (like
  `--claude`); `profile test` then reports a path that does not run as `not installed`. The test checks the path or
  `command -v` on the machine, so a profile with no `opencode=` falls back to `opencode` from that PATH. `profile help`
  also says `--opencode` is optional, and `profile test`'s line says it finds opencode. The verification uses a
  resolved scratch folder, not `/tmp`, and simulates a machine without opencode with a wrapper `sh` whose PATH has no
  opencode, since `bin/lzr1-panel` puts `/opt/homebrew/bin` on PATH itself.
- Task 2.2.2: `start` and `run` build the session's command with a new `_keep AGENT FIRST RESUME` (in `REPLY`), so
  the resume loop is written once; Claude's command is byte-identical (verify-222 compares `pane_start_command` for
  `start` and, with a stub claude in a scratch `HOME`, for `run`). The 2 s check also counts `opencode exited` on
  screen as an exit, since the resume loop keeps the session alive; it exits 1. `run`'s `--effort` defaults to
  medium after the option loop, so `--effort` with `--opencode` is refused. A subcommand for passthrough is a first
  argument matching `[a-z]*` with no `/` that is not a folder here (opencode's positional is a folder). opencode's
  first run gets your args, then `--prompt`; the resume is `--continue` alone. The header usage gained its own
  `run --opencode` line. On the unchanged code, verify-222's `run --opencode` started two Claude sessions named
  after its scratch folder (`unknown option '--opencode'`); they were killed and the script now removes them.
- Task 2.2.3: opencode rows skip Claude's Remote Control grep and header model lookup (their model is
  `@lzr1_model`). The question is looked for only when a `┃ 1. ` option line is on screen, so a `?` in an idle reply
  never becomes one. `Permission required` stays unverified on 2.0.32; noting it in `docs/troubleshooting.md` is left
  to Task 2.3.2, which owns the docs. verify-223 reads each record from `$STATE/raw-t`, and also checks the exited
  screen, an opencode screen in a session without `@lzr1_agent` (read as Claude: idle), that `opencode exited` makes
  no alert, and a question alert carrying the question; its real `start t opencode alert-oc2` went work, then idle,
  with the task title shown without `OC | `.
- Task 2.3.1: the heading menu's opencode item sits after sonnet (opus, sonnet, opencode, test, edit, remove,
  reload), so verify-212's menu moves gained one `Down` (copy kept as `verify-212.pre231`). The session menu offers
  `newoc` on every row, Claude's included; only the Esc label depends on the row's model, and the `end` label still
  says "stop Claude" on an opencode row (left to the docs task if wanted). verify-231 starts sessions with a stub
  opencode (`opencode=` on the profile) and drives both menus in a tmux pane; one real `_new opencode 1 t/ alert-oc6`
  was also run (listed with model `opencode`, then killed).
- Task 2.3.2: the profile shortcut is a second `eval` line in the hook's loop (`cl-$_p-opencode`), and the hook's
  comments name opencode. Doctor finds opencode the way `run --opencode` does (`$commands[opencode]`, then
  `~/.opencode/bin/opencode`) and prints the last word of `opencode --version` without its `v`. The README also
  names opencode in the "How it works" diagram, the session menu row, the heading menu text, `profile test`'s row,
  the SSH machine note and the installer's step 6; troubleshooting's "My Claude session is not in the panel" and
  "I closed Claude by accident" mention it too. The session menu's `end` label still says "stop Claude" on opencode
  rows. verify-232 runs the installer's hook section alone under `/bin/bash` with a scratch `HOME` (an old block is
  replaced, your lines kept), sources the result in zsh and bash 3.2 with a stub `lzr1-panel`, and hides opencode
  from doctor with a copy of the script without `/opt/homebrew/bin` on its PATH.
