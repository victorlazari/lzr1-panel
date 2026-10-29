#!/usr/bin/env bash
# lzr1-panel installer, for macOS and Linux. Safe to run again: every step checks before it changes anything.
#
#   ./install.sh              install and configure everything, asking before anything big
#   ./install.sh --yes        same, answering yes (Claude Code and Ghostty installs included)
#   ./install.sh --no-shell   leave your shell rc files alone
#   ./install.sh --uninstall  remove the links, the shell hook and the Claude skill (profiles stay)
#
# What it sets up: zsh, tmux, fzf (0.74+), curl, openssl, git, gh and jq; Claude Code if it is missing; Ghostty on macOS if
# you want it; the lzr1-panel command in ~/.local/bin; the Claude Code skill that creates profiles; a shell hook
# with cl-* helpers and (optionally) the panel opening with Ghostty; Ctrl+C copying in Ghostty; GitHub sign-in; your
# first profile; then lzr1-panel doctor checks it all.
set -euo pipefail

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
BIN=$HOME/.local/bin
CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}/lzr1-panel
SKILLS=$HOME/.claude/skills
FZF_MIN=0.74
# Tools installed below land in ~/.local/bin, which a fresh account often has off PATH: make them visible to this run.
case ":$PATH:" in *":$BIN:"*) ;; *) export PATH="$BIN:$PATH" ;; esac
YES=0 SHELL_HOOK=1 UNINSTALL=0
for a in "$@"; do
  case $a in
    --yes|-y) YES=1 ;;
    --no-shell) SHELL_HOOK=0 ;;
    --uninstall) UNINSTALL=1 ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $a" >&2; exit 1 ;;
  esac
done

c_ok=$'\e[32m' c_dim=$'\e[90m' c_warn=$'\e[33m' c_bold=$'\e[1m' c_off=$'\e[0m'
step() { printf '\n%s▸ %s%s\n' "$c_bold" "$*" "$c_off"; }
ok()   { printf '  %s✓%s %s\n' "$c_ok" "$c_off" "$*"; }
info() { printf '  %s%s%s\n' "$c_dim" "$*" "$c_off"; }
warn() { printf '  %s!%s %s\n' "$c_warn" "$c_off" "$*"; }
ask()  { [[ $YES == 1 ]] && return 0; local r; read -r -p "  $1 [Y/n] " r </dev/tty; [[ -z $r || $r == [Yy]* ]]; }
have() { command -v "$1" >/dev/null 2>&1; }

OS=$(uname -s)
HOOK_BEGIN='# >>> lzr1-panel >>>'
HOOK_END='# <<< lzr1-panel <<<'

remove_hook() {
  local rc=$1
  [[ -f $rc ]] && grep -qF "$HOOK_BEGIN" "$rc" || return 0
  local tmp; tmp=$(mktemp)
  awk -v b="$HOOK_BEGIN" -v e="$HOOK_END" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$rc" >"$tmp" && cat "$tmp" >"$rc" && rm -f "$tmp"
}

if [[ $UNINSTALL == 1 ]]; then
  step "Uninstalling lzr1-panel"
  rm -f "$BIN/lzr1-panel" && ok "removed $BIN/lzr1-panel"
  rm -f "$SKILLS/lzr1-panel-profiles" && ok "removed the Claude skill link"
  for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do remove_hook "$rc"; done
  ok "removed the shell hook"
  GCONF="$HOME/Library/Application Support/com.mitchellh.ghostty/config.ghostty"
  if grep -qs 'lzr1-panel/install.sh' "$GCONF"; then
    grep -v -e 'Added by lzr1-panel/install.sh' -e '^keybind = performable:ctrl+c=copy_to_clipboard$' "$GCONF" >"$GCONF.tmp" || true
    mv "$GCONF.tmp" "$GCONF" && ok "removed the Ghostty Ctrl+C copy key"
  fi
  info "profiles kept in $CONFIG (delete that folder to remove them)"
  exit 0
fi

printf '%s\n' "${c_bold}lzr1-panel installer${c_off} ${c_dim}($OS, from $REPO)${c_off}"

# ---- packages -------------------------------------------------------------------------------------------------------

pkg_install() {  # pkg_install NAME...: through the system's package manager
  if [[ $OS == Darwin ]]; then
    have brew || { warn "Homebrew is needed on macOS: https://brew.sh — install it, then run ./install.sh again"; exit 1; }
    brew install "$@"
  elif have apt-get; then sudo apt-get update -qq && sudo apt-get install -y "$@"
  elif have dnf; then sudo dnf install -y "$@"
  elif have pacman; then sudo pacman -S --noconfirm "$@"
  elif have zypper; then sudo zypper install -y "$@"
  elif have apk; then sudo apk add "$@"
  else warn "no known package manager: install $* yourself, then run ./install.sh again"; exit 1
  fi
}

version_ge() { [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]; }

step "Checking tools"
for t in zsh tmux curl openssl git; do
  if have "$t"; then ok "$t"; else info "installing $t"; pkg_install "$t"; ok "$t installed"; fi
done

# gh lists your pull requests under the sessions; jq reads them when gh is not signed in. Neither is required.
for t in gh jq; do
  if have "$t"; then ok "$t"; continue; fi
  pkg=$t
  [[ $t == gh ]] && { have pacman || have apk; } && pkg=github-cli
  info "installing $t"
  if ( pkg_install "$pkg" ) >/dev/null 2>&1; then ok "$t installed"
  else warn "could not install $t: your pull requests need it (https://cli.github.com)"; fi
done

fzf_version() { fzf --version 2>/dev/null | awk '{print $1}'; }
if have fzf && version_ge "$(fzf_version)" "$FZF_MIN"; then
  ok "fzf $(fzf_version)"
else
  info "fzf $FZF_MIN or newer is needed (found: $(fzf_version || true))"
  if [[ $OS == Darwin ]]; then
    have fzf && brew upgrade fzf || pkg_install fzf
  else
    # Distribution packages lag behind; take the latest release binary.
    arch=$(uname -m); case $arch in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; armv7l) arch=armv7 ;; esac
    tag=$(curl -fsSL https://api.github.com/repos/junegunn/fzf/releases/latest | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -1)
    [[ -n $tag ]] || { warn "could not find the latest fzf release"; exit 1; }
    mkdir -p "$BIN"
    curl -fsSL "https://github.com/junegunn/fzf/releases/download/v${tag}/fzf-${tag}-linux_${arch}.tar.gz" | tar -xz -C "$BIN" fzf
    hash -r
  fi
  version_ge "$(fzf_version)" "$FZF_MIN" && ok "fzf $(fzf_version)" || { warn "fzf is still older than $FZF_MIN"; exit 1; }
fi

if have claude || [[ -x $HOME/.local/bin/claude ]]; then
  ok "Claude Code"
else
  warn "Claude Code is not installed on this machine"
  if ask "Install Claude Code now (official installer from claude.ai)?"; then
    curl -fsSL https://claude.ai/install.sh | bash
    ok "Claude Code installed — run 'claude' once to sign in"
  else
    info "skipped: local profiles need it; remote machines need their own install"
  fi
fi

if [[ $OS == Darwin ]]; then
  if [[ -d /Applications/Ghostty.app ]] || have ghostty; then
    ok "Ghostty"
  elif ask "Install Ghostty (the panel opens as a split there; without it, it uses a tmux layout)?"; then
    brew install --cask ghostty && ok "Ghostty installed"
  else
    info "skipped: lzr1-panel will use its tmux layout"
  fi
  # Ghostty reads this file as well as ~/.config/ghostty; "performable" passes Ctrl+C on when nothing is selected.
  GCONF="$HOME/Library/Application Support/com.mitchellh.ghostty/config.ghostty"
  GBIND='keybind = performable:ctrl+c=copy_to_clipboard'
  if [[ -d /Applications/Ghostty.app ]]; then
    if grep -qsF "$GBIND" "$GCONF"; then
      ok "Ghostty: Ctrl+C copies selected text"
    elif ask "Let Ctrl+C copy selected text in Ghostty (with nothing selected it still stops Claude)?"; then
      mkdir -p "$(dirname "$GCONF")"
      printf '\n# Added by lzr1-panel/install.sh: Ctrl+C copies a selection, and is Ctrl+C otherwise.\n%s\n' "$GBIND" >>"$GCONF"
      ok "Ghostty: Ctrl+C copies selected text (reload Ghostty's config, Cmd+Shift+,)"
    fi
  fi
fi

# ---- lzr1-panel itself ----------------------------------------------------------------------------------------------

step "Installing lzr1-panel"
chmod +x "$REPO/bin/lzr1-panel"
mkdir -p "$BIN" "$CONFIG/profiles"
chmod 700 "$CONFIG" "$CONFIG/profiles"
ln -sfn "$REPO/bin/lzr1-panel" "$BIN/lzr1-panel"
ok "$BIN/lzr1-panel → $REPO/bin/lzr1-panel"
case ":$PATH:" in *":$BIN:"*) ;; *) warn "$BIN is not on your PATH yet: the shell hook below adds it" ;; esac

if [[ -d $HOME/.claude ]] || have claude; then
  mkdir -p "$SKILLS"
  ln -sfn "$REPO/skills/lzr1-panel-profiles" "$SKILLS/lzr1-panel-profiles"
  ok "Claude Code skill: ask Claude to \"create a new lzr1-panel profile\""
fi

# ---- shell hook -----------------------------------------------------------------------------------------------------

if [[ $SHELL_HOOK == 1 ]]; then
  step "Shell hook"
  autoopen=0
  if [[ $OS == Darwin && ( -d /Applications/Ghostty.app ) ]] && ask "Open the panel automatically when you open Ghostty?"; then autoopen=1; fi
  for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
    [[ -f $rc || $rc == "$HOME/.zshrc" ]] || continue
    remove_hook "$rc"
    {
      echo "$HOOK_BEGIN"
      echo "# Managed by lzr1-panel/install.sh; ./install.sh --uninstall removes it."
      echo 'case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac'
      echo "# cl-<profile>-opus / -sonnet / -opencode NAME: start a session on a profile without opening the panel"
      echo 'for _p in ${XDG_CONFIG_HOME:-$HOME/.config}/lzr1-panel/profiles/*.conf; do'
      echo '  [ -e "$_p" ] || continue; _p=${_p##*/}; _p=${_p%.conf}'
      echo '  eval "cl-$_p-opus() { lzr1-panel start $_p opus \"\$@\"; }; cl-$_p-sonnet() { lzr1-panel start $_p sonnet \"\$@\"; }"'
      echo '  eval "cl-$_p-opencode() { lzr1-panel start $_p opencode \"\$@\"; }"'
      echo 'done; unset _p'
      echo "# cl-opus / cl-sonnet [claude args], cl-opencode [opencode args]: Claude, or opencode with auto-approve on, in a"
      echo "# new tmux session in this folder; the panel lists it inside a profile's repositories folder. Your own,"
      echo "# defined earlier in this file, wins."
      echo 'type cl-opus >/dev/null 2>&1 || cl-opus() { lzr1-panel run --model opus "$@"; }'
      echo 'type cl-sonnet >/dev/null 2>&1 || cl-sonnet() { lzr1-panel run --model sonnet "$@"; }'
      echo 'type cl-opencode >/dev/null 2>&1 || cl-opencode() { lzr1-panel run --opencode "$@"; }'
      if [[ $autoopen == 1 && $rc == "$HOME/.zshrc" ]]; then
        echo "# Opens the panel in the first Ghostty shell; LZR1_PANEL=0 (in ~/.zshenv) turns it off."
        echo 'if [[ -o interactive && $TERM_PROGRAM == ghostty && ${LZR1_PANEL:-1} == 1 && -z $TMUX ]] &&'
        echo '   ! pgrep -f "lzr1-panel _panel" >/dev/null; then'
        echo '  lzr1-panel open'
        echo 'fi'
      fi
      echo "$HOOK_END"
    } >>"$rc"
    ok "hook in $rc"
  done
fi

# ---- first profile --------------------------------------------------------------------------------------------------

step "Profiles"
if compgen -G "$CONFIG/profiles/*.conf" >/dev/null; then
  "$BIN/lzr1-panel" profile list | sed 's/^/  /'
elif [[ -t 0 ]] && ask "Create your first profile now?"; then
  "$BIN/lzr1-panel" profile add || warn "profile not created: run 'lzr1-panel profile add' later"
else
  info "none yet: run 'lzr1-panel profile add', or ask Claude to create a new lzr1-panel profile"
fi

# ---- GitHub ---------------------------------------------------------------------------------------------------------

step "GitHub (your pull requests under the sessions)"
if ! have gh; then
  info "no gh: the panel falls back to your SSH key's account (public PRs only)"
elif gh auth status >/dev/null 2>&1; then
  ok "gh is signed in as $(gh api user -q .login 2>/dev/null)"
elif [[ -t 0 && $YES == 0 ]] && ask "Sign in to GitHub now (gh auth login), so the panel lists your pull requests?"; then
  gh auth login </dev/tty || warn "not signed in: run 'gh auth login' later"
else
  info "not signed in: run 'gh auth login' to list your pull requests"
fi

[[ -t 1 ]] && "$BIN/lzr1-panel" splash --install || true

step "Checking everything"
"$BIN/lzr1-panel" doctor || warn "fix the items marked ✗ above (docs/troubleshooting.md explains each)"

step "Done"
info "open a new terminal (or: source ~/.zshrc), then run: lzr1-panel open"
info "keys: lzr1-panel keys · profiles: lzr1-panel profile help"
