# For AI assistants

- Asked to **create, register or set up an lzr1-panel profile**: follow [docs/profiles.md](docs/profiles.md)
  exactly. Ask for the name, the target and the repositories folder; never guess them; confirm before running
  `lzr1-panel profile add`.
- Profiles (`~/.config/lzr1-panel/profiles/*.conf`) hold hosts and key paths: never copy them into this repository,
  commit them, or print key contents.
- The code is zsh (`bin/lzr1-panel`, `lib/splash.zsh`) and bash (`install.sh`, which must run on macOS's bash 3.2).
  Check with `zsh -n bin/lzr1-panel lib/splash.zsh` and `bash -n install.sh`.
- Scripts sent to a profile's machine go through `on` as POSIX `sh`: no bashisms or zsh syntax inside them.
