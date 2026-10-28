# Creating an lzr1-panel profile

This is the guide an AI assistant (Claude Code, Codex, ChatGPT, Gemini or any other) follows when someone asks to
**create a new lzr1-panel profile**. People can follow it too, or run `lzr1-panel profile add` and answer its
questions.

A profile is one machine whose Claude Code sessions the panel lists: this computer, or another one reached over SSH.
Profiles cover Claude Code sessions only, for now.

## What a profile holds

Each profile is a small file, `~/.config/lzr1-panel/profiles/<name>.conf`, readable only by its owner:

```
type=local                      # local: this computer · ssh: another machine
host=ubuntu@203.0.113.10        # ssh only: user@host (an alias from ~/.ssh/config works too)
key=~/.ssh/my-server.key        # ssh only, optional: the private key file
port=22                         # ssh only, optional
repos=~/Github/lerian           # the folder holding your repositories
root=~/Github/lerian            # where sessions that are not a repository open (default: repos)
only=~/Github/lerian            # optional: list only the sessions whose folder is inside it
claude=/Users/me/.local/bin/claude   # found automatically when the profile is created
```

A session named after a folder in `repos` opens inside it (`lerian-clients` opens in `repos/lerian-clients`); any
other name opens in `root`. Paths may start with `~`, which is read on the profile's own machine.

**Profiles never go into git.** They hold hosts, usernames and key paths. They live outside the repository, and the
repository's `.gitignore` refuses `*.conf` files anyway.

## The conversation to have

Ask these questions one at a time, and wait for each answer. Never guess an answer the person has not given.

1. **Name.** "What should this profile be called?" Lowercase letters, digits and dashes, such as
   `macbook-pro-lazari` or `oracle-server`. If a profile with that name exists (`lzr1-panel profile list`), say so
   and ask for another name or whether to replace it.
2. **Target.** "Is it this computer, or another machine over SSH?"
   - This computer: `--local`.
   - SSH: ask for `user@host`, then the key file (optional; empty means their SSH defaults), then the port
     (optional). Never ask for or handle a password or a key's contents: only the path to a key file.
3. **Repositories folder.** "Which folder holds the repositories you work on there?" Always ask: there is no safe
   default. Give an example shaped like their machine, such as `/Users/<them>/Github/lerian` for a local Mac or
   `~/lerian/repositories` for a server, and use exactly what they answer.
4. **Other sessions' folder** (optional). "Where should sessions that are not a repository open? (Enter: the same
   folder.)"
5. **Only this folder's sessions?** Ask only when the machine already has a profile (`lzr1-panel profile list`
   shows its type and host): "Should this profile list only the sessions inside that folder?" A profile lists every
   session on its machine, so two profiles on one machine show the same sessions twice unless each has `--only`.
   If yes, add `--only` with the repositories folder, and offer `--only` for the other profile too (remove and add
   it again).

Then show a one-line summary and ask them to confirm before creating anything.

## The commands

This computer:

```sh
lzr1-panel profile add macbook-pro-lazari --local --repos /Users/lazari/Github/lerian
```

Another machine:

```sh
lzr1-panel profile add oracle-server --ssh ubuntu@203.0.113.10 --key ~/.ssh/my-server.key \
  --repos '~/lerian/repositories' --root '~/lerian'
```

Two profiles on one machine, one folder each, every session listed once:

```sh
lzr1-panel profile add lzr1-personal --local --repos ~/Github/lazari --only ~/Github/lazari
lzr1-panel profile add lzr1-lerian --local --repos ~/Github/lerian --only ~/Github/lerian
```

A session in neither folder then appears in neither profile.

Quote remote paths that start with `~` (`'~/lerian'`), so the local shell does not expand them to the local home.

`profile add` writes the file, finds Claude Code on that machine and runs `profile test`, which prints one line each
for: reaching the machine, tmux, Claude Code, the repositories folder and the root folder. Read that output back to
the person:

- **cannot reach it**: check the host, the key and that the key is accepted (`ssh -i KEY user@host true`).
- **tmux: missing**: install tmux on that machine (`sudo apt-get install -y tmux`, `brew install tmux`).
- **claude: missing**: install Claude Code there (`curl -fsSL https://claude.ai/install.sh | bash`), sign in once
  with `claude`, then `lzr1-panel profile remove NAME` and add the profile again.
- **repos ... missing**: the folder does not exist there; ask for the right path.

## After it is created

- The panel shows the new profile on its next refresh (every 15 seconds) or when it opens: `lzr1-panel open`.
- New shells get `cl-<name>-opus NAME` and `cl-<name>-sonnet NAME`, which start a session there without the panel.
- Change a profile by removing and adding it again: `lzr1-panel profile remove NAME`.

## Other commands

```sh
lzr1-panel profile list          # every profile
lzr1-panel profile show NAME     # its settings
lzr1-panel profile test NAME     # check it again
lzr1-panel profile remove NAME
```
