# lzr1-panel splash, "BIOS boot": a retro boot log that checks your real machines (each profile turns [ OK ] with its
# sessions and repositories as it answers), then the 3D LZR1 logo slams in with a screen shake, its second word
# switching between TECH and PANEL. A status line and a progress bar sit at the bottom. Sized to the terminal on
# every frame; a small terminal gets a short log and a one-line logo. About 5s; any key skips. LZR1_SPLASH=0 skips
# it everywhere, LZR1_SPLASH_SECONDS changes its length.
#
# Sourced by bin/lzr1-panel, which calls: lzr1_splash [open|install]

zmodload zsh/datetime

typeset -gA LZR1_GLYPH=(
  L $'██╗     \n██║     \n██║     \n██║     \n███████╗\n╚══════╝'
  Z $'███████╗\n╚══███╔╝\n  ███╔╝ \n ███╔╝  \n███████╗\n╚══════╝'
  R $'██████╗ \n██╔══██╗\n██████╔╝\n██╔══██╗\n██║  ██║\n╚═╝  ╚═╝'
  1 $' ██╗\n███║\n╚██║\n ██║\n ██║\n ╚═╝'
  T $'████████╗\n╚══██╔══╝\n   ██║   \n   ██║   \n   ██║   \n   ╚═╝   '
  E $'███████╗\n██╔════╝\n█████╗  \n██╔══╝  \n███████╗\n╚══════╝'
  C $' ██████╗\n██╔════╝\n██║     \n██║     \n╚██████╗\n ╚═════╝'
  H $'██╗  ██╗\n██║  ██║\n███████║\n██╔══██║\n██║  ██║\n╚═╝  ╚═╝'
  P $'██████╗ \n██╔══██╗\n██████╔╝\n██╔═══╝ \n██║     \n╚═╝     '
  A $' █████╗ \n██╔══██╗\n███████║\n██╔══██║\n██║  ██║\n╚═╝  ╚═╝'
  N $'███╗   ██╗\n████╗  ██║\n██╔██╗ ██║\n██║╚██╗██║\n██║ ╚████║\n╚═╝  ╚═══╝'
)

# _lzr1_word WORD WIDTH: the word's 6 rows, centered in WIDTH, into reply
_lzr1_word() {
  local word=$1 width=$2 r c
  local -a rows=( '' '' '' '' '' '' ) g
  for c in ${(s::)word}; do
    g=( "${(@f)LZR1_GLYPH[$c]}" )
    for r in {1..6}; do rows[r]+="${g[r]} "; done
  done
  local w=$(( ${#rows[1]} - 1 )) pad
  pad=$(( (width - w) / 2 ))
  reply=()
  for r in {1..6}; do reply+=( "${(l:pad:: :)}${rows[r]%?}${(l:width - pad - w:: :)}" ); done
}

# _lzr1_dots LABEL VALUE WIDTH: "LABEL ........ VALUE" padded to WIDTH, into REPLY
_lzr1_dots() {
  local n=$(( $3 - ${#1} - ${#2} - 2 )); (( n < 3 )) && n=3
  REPLY="$1 ${(l:n::.:)} $2"
}

lzr1_splash() {
  [[ ${LZR1_SPLASH:-1} == 0 || ! -t 1 ]] && return 0
  local mode=${1:-open}
  # Box-drawing widths need a UTF-8 locale.
  if [[ ${LC_ALL:-${LC_CTYPE:-$LANG}} != *(#i)utf(-|)8* ]]; then
    local LC_ALL; [[ $OSTYPE == darwin* ]] && LC_ALL=en_US.UTF-8 || LC_ALL=C.UTF-8
  fi

  local W=46 sfile=$(mktemp) dur=${LZR1_SPLASH_SECONDS:-5} boot
  boot=$(( dur * 0.48 ))
  local -a lzr tech panel names=( $(profiles) )
  _lzr1_word LZR1 $W; lzr=( $reply )
  _lzr1_word TECH $W; tech=( $reply )
  _lzr1_word PANEL $W; panel=( $reply )

  # Real work behind the log: reach every profile (this also opens the SSH connections the panel then reuses).
  if [[ $mode == open ]]; then
    ( local p out
      for p in $names; do
        ( if load $p 2>/dev/null; then
            # A profile with only= counts just the sessions inside that folder.
            out=$(on "$EXP"'o=$(exp "$2"); o=${o%/}
              n=$(tmux list-sessions -F "#{pane_current_path}" 2>/dev/null | while read -r d; do
                if [ -n "$o" ]; then case $d in ("$o"|"$o"/*) ;; (*) continue ;; esac; fi; echo; done | wc -l | tr -d " ")
              printf "%s %s\n" "$n" "$(ls -1 "$(exp "$1")" 2>/dev/null | wc -l | tr -d " ")"' "$P_REPOS" "$P_ONLY" 2>/dev/null)
            if [[ -n $out ]]; then print -r -- "reach $p ok $out" >>$sfile; else print -r -- "reach $p fail 0 0" >>$sfile; fi
          fi ) &
      done
      wait; print done >>$sfile ) &!
  else
    print done >>$sfile
  fi

  local -a fun=( "reticulating tmux splines" "teaching Claude where your code lives" "polishing the 3D pixels"
                 "compiling codes (in spirit)" "brewing fresh tokens" "aligning panes to the golden ratio"
                 "convincing ssh to stay awake" "defragmenting the vibes" "warming up the viewer" )
  local -a picks=( "${fun[$(( RANDOM % $#fun + 1 ))]}" "${fun[$(( RANDOM % $#fun + 1 ))]}" )
  local -a grad=( 51 45 39 33 63 99 135 171 207 213 219 225 )
  local host=${HOST%%.*} arch=$(uname -m) t0=$EPOCHREALTIME t frac=0 frame=0 key cols rows done=0 p v
  local spin='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏' i r x y ch col out line cls top left msg bar bw fill pct nses=0 nrep=0 okp=0 failp=0
  local -A res
  local -a log logc vv

  print -n '\e[?1049h\e[?25l'  # alternate screen, hidden cursor
  trap 'print -n "\e[?2026l\e[?25h\e[?1049l"; rm -f $sfile; trap - INT; return 130' INT

  while true; do
    t=$(( EPOCHREALTIME - t0 ))
    read -rs -t 0.045 -k1 key && break
    local -a sz=( $(stty size </dev/tty 2>/dev/null) )
    rows=${sz[1]:-24} cols=${sz[2]:-80}
    (( frame++ ))

    # What the machines answered so far.
    res=() okp=0 failp=0 nses=0 nrep=0
    for line in ${(f)"$(grep '^reach ' $sfile 2>/dev/null)"}; do
      local -a f=( ${=line} )
      res[$f[2]]="$f[3] $f[4] $f[5]"
      if [[ $f[3] == ok ]]; then (( okp++, nses += f[4], nrep += f[5] )); else (( failp++ )); fi
    done
    grep -q '^done' $sfile 2>/dev/null && done=1

    # Progress follows time, but waits (up to 3s more) for the machines to answer.
    frac=$(( t / dur ))
    (( frac > 0.97 && !done && t < dur + 3 )) && frac=0.97
    (( frac >= 1 )) && break

    if   (( frac < 0.12 )); then msg="POST: power-on self test"
    elif (( frac < 0.30 )); then msg="loading your profiles… $#names found"
    elif (( frac < 0.48 )); then msg="dialing your machines… $okp of $#names answered"
    elif (( frac < 0.60 )); then msg="counting your sessions… $nses live on $okp machine$( (( okp == 1 )) || print s)"
    elif (( frac < 0.72 )); then msg="indexing your projects… $nrep repositories"
    elif (( frac < 0.82 )); then msg=${picks[1]}
    elif (( frac < 0.92 )); then msg=${picks[2]}
    elif (( !done ));        then msg="still waiting on a machine…"
    else msg="ready. go build something."; fi
    [[ $mode == install ]] && (( frac >= 0.30 && frac < 0.72 )) && msg="installed: command, Claude skill, shell hook"

    # Each frame is one synchronized update (mode 2026): the terminal shows it whole, never the cleared screen in
    # between, which flashed in Ghostty. Terminals without the mode ignore it.
    out='\e[?2026h'
    for y in {1..$rows}; do out+="\e[${y};1H\e[K"; done

    if (( t < boot )); then
      # ---- the boot log --------------------------------------------------------------------------------------------
      log=() logc=()
      local lw=$(( cols - 6 )); (( lw > 64 )) && lw=64
      log+=( "LZR1 BIOS v${VERSION:-0}  (c) lzr1 tech" ) logc+=( head )
      log+=( "CPU: $arch · host: $host · zsh $ZSH_VERSION" ) logc+=( 250 )
      local mem=$(( t < 0.6 ? t / 0.6 * 640 : 640 )); mem=${mem%.*}
      _lzr1_dots "Memory test" "${mem}K$( (( mem >= 640 )) && print ' OK')" $lw; log+=( "$REPLY" ) logc+=( 250 )
      _lzr1_dots "Detecting profiles" "$#names" $lw; log+=( "$REPLY" ) logc+=( 250 )
      if [[ $mode == open ]]; then
        for p in $names; do
          v=${res[$p]}
          if [[ -z $v ]]; then
            _lzr1_dots "  $p" "[ ${spin[$(( frame % 10 + 1 ))]}${spin[$(( (frame + 5) % 10 + 1 ))]} ]" $lw; logc+=( 250 )
          elif [[ $v == ok* ]]; then
            vv=( ${=v} )
            _lzr1_dots "  $p" "${vv[2]} session$( (( vv[2] == 1 )) || print s) · ${vv[3]} repos  [ OK ]" $lw; logc+=( ok )
          else
            _lzr1_dots "  $p" "offline, skipped  [SKIP]" $lw; logc+=( fail )
          fi
          log+=( "$REPLY" )
        done
        _lzr1_dots "Mounting repositories" "$nrep" $lw; log+=( "$REPLY" ) logc+=( 250 )
      else
        for p in "the lzr1-panel command" "the Claude Code skill" "the shell hook"; do
          _lzr1_dots "Installing $p" "[ OK ]" $lw; log+=( "$REPLY" ) logc+=( ok )
        done
      fi
      _lzr1_dots "Starting tmux" "ok" $lw; log+=( "$REPLY" ) logc+=( 250 )
      log+=( "Press any key to skip" ) logc+=( 240 )

      # Lines appear one by one; only as many as fit above the status area.
      local shown=$(( t / (boot * 0.85) * $#log + 1 )); shown=${shown%.*}; (( shown > $#log )) && shown=$#log
      local room=$(( rows - 6 )); (( room < 1 )) && room=1
      local first=$(( shown > room ? shown - room + 1 : 1 ))
      y=2
      for (( i = first; i <= shown; i++ )); do
        line=${log[i]}; (( ${#line} > cols - 4 )) && line="${line[1,cols - 5]}…"
        case ${logc[i]} in
          head) out+="\e[${y};3H\e[1;97m${line}\e[0m" ;;
          ok)   out+="\e[${y};3H\e[38;5;250m${line%\[*}\e[1;38;5;46m[${line##*\[}\e[0m" ;;
          fail) out+="\e[${y};3H\e[38;5;250m${line%\[*}\e[1;38;5;196m[${line##*\[}\e[0m" ;;
          *)    out+="\e[${y};3H\e[38;5;${logc[i]}m${line}\e[0m" ;;
        esac
        (( y++ ))
      done
      (( frame % 6 < 3 )) && out+="\e[$(( y - 1 ));$(( 3 + ${#line} + 1 ))H\e[97m█\e[0m"  # the blinking cursor
    elif (( cols >= W + 4 && rows >= 22 )); then
      # ---- the logo: slams in with a decaying shake, then the word switches every 1.6s ----------------------------
      local s=$(( t - boot )) shake=0 phase wi
      if (( s < 0.6 )); then shake=$(( (RANDOM % 5 - 2) * (1 - s / 0.6) )); shake=${shake%.*}; fi
      phase=$(( s / 1.6 )); wi=$(( ${phase%.*} % 2 ))
      local -a word; if (( wi )); then word=( $panel ); else word=( $tech ); fi
      local -a logo=( $lzr '' $word )
      top=$(( (rows - 4 - 15) / 2 )); (( top < 1 )) && top=1
      left=$(( (cols - W) / 2 + 1 + shake ))
      for r in {1..13}; do
        line=${logo[r]}; [[ -z ${line// } ]] && continue
        y=$(( top + r + (s < 0.3 && r % 2 ? shake : 0) ))
        out+="\e[${y};${left}H"; cls=
        for (( x = 1; x <= ${#line}; x++ )); do
          ch=${line[x]}
          if [[ $ch == ' ' ]]; then [[ $cls != sp ]] && { out+='\e[0m'; cls=sp }; out+=' '; continue; fi
          if (( s < 0.08 )); then col=231                       # the flash as it lands
          elif [[ $ch == █ ]]; then col=${grad[$(( r <= 6 ? r : r - 1 ))]}
          else col=60; fi                                     # the ╔═╝ edges: the 3D shadow
          [[ $cls != $col ]] && { out+="\e[38;5;${col}m"; cls=$col }
          out+=$ch
        done
        out+='\e[0m'
      done
      local tag="▸ claude code sessions, on every machine you own ◂"
      local tn=$(( s < 0.8 ? ${#tag} * s / 0.8 : ${#tag} )); tn=${tn%.*}
      out+="\e[$(( top + 15 ));$(( (cols - ${#tag}) / 2 + 1 ))H\e[38;5;245m${tag[1,tn]}\e[0m"
    else
      # ---- small terminal: one line of logo ------------------------------------------------------------------------
      local sp=$(( (t - boot) / 1.6 )) small
      if (( ${sp%.*} % 2 )); then small="▟ LZR1 ▸ PANEL ▙"; else small="▟ LZR1 ▸ TECH ▙"; fi
      top=$(( rows / 2 - 2 )); (( top < 1 )) && top=1
      out+="\e[${top};$(( (cols - ${#small}) / 2 + 1 ))H\e[1;38;5;${grad[$(( frame / 3 % 12 + 1 ))]}m${small}\e[0m"
    fi

    # ---- status line, bar and footer, pinned to the bottom -------------------------------------------------------
    bw=$(( cols - 16 )); (( bw > 56 )) && bw=56; (( bw < 10 )) && bw=10
    msg="${spin[$(( frame % 10 + 1 ))]} $msg"
    local x0=$(( (cols - bw - 6) / 2 + 1 )); (( x0 < 1 )) && x0=1
    (( ${#msg} > cols - x0 )) && msg="${msg[1,cols - x0 - 1]}…"
    out+="\e[$(( rows - 3 ));${x0}H\e[38;5;252m${msg}\e[0m"
    fill=$(( bw * frac )); fill=${fill%.*}
    pct=$(( frac * 100 )); pct=${pct%.*}
    bar="\e[38;5;51m${(l:fill::━:)}\e[38;5;201m╸\e[38;5;238m${(l:bw - fill::─:)}\e[0m"
    out+="\e[$(( rows - 2 ));${x0}H${bar} \e[1m${(l:3:)pct}%\e[0m"
    line="$(printf '0x%04x' $RANDOM) · $#names profiles · $nses sessions · $nrep repos · any key skips"
    (( ${#line} > cols - 2 )) && line="any key skips"
    out+="\e[$(( rows - 1 ));$(( (cols - ${#line}) / 2 + 1 ))H\e[38;5;240m${line}\e[0m\e[?2026l"
    print -n -- "$out"
  done

  print -n '\e[?2026l\e[?25h\e[?1049l'
  trap - INT
  rm -f $sfile
  return 0
}
