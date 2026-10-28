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
- [Mouse and menus](#mouse-and-menus)
- [Looks wrong](#looks-wrong)
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

### A session opened in the wrong folder

A session opens in a repository only when its name matches a folder in the profile's `repos` exactly
(`lerian-clients` → `repos/lerian-clients`). Any other name opens in `root`.

**Fix:** use the folder's exact name, or click the repository in the new-session list.

### I closed Claude by accident

The session is still there.

**Fix:** press **Enter** in it to resume the same conversation. To really close it, type `end` and Enter, or press
`x` in the panel.

---

## The viewer

### The viewer says "pick a session" and stays empty

**Fix:** click a session in the panel. If you left it with `Ctrl-b d`, click it again or press Enter on it.

### I closed the viewer pane

**Fix:** click another session, or press Enter on one. The viewer comes back.

### My keyboard is stuck in the viewer

**Fix:** press **`Ctrl-b d`** to leave the session, or click the panel.

### Ctrl-C does nothing in Claude

Ctrl-C copies when text is selected. With nothing selected, it goes to Claude as usual.

**Fix:** click once in the viewer to clear the selection, then press Ctrl-C.

---

## Copying text

### Selecting text in the viewer does not copy

The viewer shows the session through tmux, which handles the mouse. The panel sets tmux up so that letting go of the
mouse copies the selection. That setup happens when the viewer attaches to a session.

**Fix:**

1. Click another session and back (this re-attaches, with copying set up).
2. Drag to select, then let go. The text is copied and stays highlighted.
3. Still nothing? Your terminal must allow programs to set the clipboard:
   - **Ghostty**: allowed by default. If you set `clipboard-write = deny`, change it to `allow`.
   - **iTerm2**: Settings → General → Selection → *Applications in terminal may access clipboard*.
   - **Other terminals**: look for "OSC 52" in their settings.

### Copying from an SSH machine does not work

There, the copy travels back through your terminal (OSC 52), so the terminal must allow it (see the previous
answer). Nothing needs installing on the server.

### Ctrl-C does not copy in Ghostty

**Fix:** run `./install.sh` again and say yes to *"Let Ctrl+C copy selected text in Ghostty"*. Then reload
Ghostty's config with **Cmd+Shift+,**. `lzr1-panel doctor` tells you whether it is set.

### I cannot select text in the panel

The panel uses the mouse for clicks.

**Fix:** hold **Shift** while dragging, then press **Cmd-C**.

---

## Pull requests

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

## Mouse and menus

### Right-click does nothing, or opens my terminal's own menu

Some terminals keep the right button for themselves.

**Fix:** press **`m`** on the row instead, or switch menus to the left button:

```sh
export LZR1_MENU_BUTTON=left   # in ~/.zshenv
```

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

## Starting over

```sh
./install.sh --uninstall   # removes the command, shell hook, Claude skill and Ghostty key (keeps profiles)
./install.sh               # installs everything again
```

Your profiles stay in `~/.config/lzr1-panel/profiles/`. Remove one with `lzr1-panel profile remove NAME`.
