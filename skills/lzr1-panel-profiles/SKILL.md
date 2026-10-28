---
name: lzr1-panel-profiles
description: Create, test, list or remove lzr1-panel profiles (the machines whose Claude Code sessions the lzr1 panel shows). Use when the user asks to create, register, add or set up an lzr1-panel profile, for this computer or a server over SSH.
---

# lzr1-panel profiles

A profile is one machine whose Claude Code sessions the lzr1 panel lists: this computer (`--local`) or another one
over SSH (`--ssh user@host`). Profiles cover Claude Code sessions only, for now.

Follow the full guide in `docs/profiles.md` of the lzr1-panel repository. Find it next to this skill:
`readlink -f ~/.claude/skills/lzr1-panel-profiles` (macOS: `realpath`), then `../../docs/profiles.md`.

The short version:

1. Ask, one question at a time, waiting for each answer:
   - the profile **name** (lowercase, digits, dashes; check `lzr1-panel profile list` for clashes);
   - the **target**: this computer, or SSH with `user@host`, an optional key **file path** and an optional port
     (never ask for passwords or key contents);
   - the **repositories folder** on that machine. Always ask; suggest a path shaped like theirs, such as
     `/Users/<them>/Github/lerian`, and use exactly what they answer;
   - optionally, the folder for sessions that are not a repository (default: the same);
   - when the machine already has a profile: whether to list **only the sessions inside the folder** (`--only`),
     since otherwise both profiles list every session on that machine.
2. Show a one-line summary and get a yes.
3. Run one of:
   - `lzr1-panel profile add NAME --local --repos PATH [--root PATH] [--only PATH]`
   - `lzr1-panel profile add NAME --ssh USER@HOST [--key FILE] [--port N] --repos 'PATH' [--root 'PATH'] [--only 'PATH']`
     (single-quote remote paths that start with `~`).
4. Read the `profile test` lines it prints back to the user, and fix anything marked missing with them
   (`docs/profiles.md` lists the fixes).
5. Tell them the panel shows it on its next refresh, and that new shells get `cl-NAME-opus` / `cl-NAME-sonnet`.

Never write profile files into a git repository: they hold hosts and key paths, and live in
`~/.config/lzr1-panel/profiles/`.
