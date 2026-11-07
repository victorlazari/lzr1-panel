# lib/pokemon.zsh: a Pokémon for every session (docs/pokemon.md). Sourced by bin/lzr1-panel after $STATE is set.
#
#   LZR1_POKEMON=off|text|sprite   off: nothing; text: a mood and the Pokémon's name on each card; sprite (default):
#                                  a half-block sprite beside each card (needs curl and ImageMagick, else text)
#                                  The [...] menu in the panel's footer sets it for good (CONFIG/pokemon).
#
# A session's Pokémon is a number from 1 to 151 kept in $STATE/pokemon/PROFILE/NAME (NAME as _fname writes it): picked
# from a hash of its name, skipping the ones in use, the first time the panel sees the session; changed from the row's
# menu. The art (PMD SpriteCollab, CC BY-NC 4.0) is downloaded at first use into $STATE/pokemon-art/NNNN and never
# shipped. The mood is the state the panel already tracks: ! waiting, ✓ finished and not looked at yet, zZz idle for
# 3 minutes. In Ghostty and kitty the card's sprite is a real picture (Kitty graphics), elsewhere half blocks.

typeset -ga PK_DEX=(
  bulbasaur ivysaur venusaur charmander charmeleon charizard squirtle wartortle blastoise caterpie metapod butterfree
  weedle kakuna beedrill pidgey pidgeotto pidgeot rattata raticate spearow fearow ekans arbok pikachu raichu sandshrew
  sandslash nidoran-f nidorina nidoqueen nidoran-m nidorino nidoking clefairy clefable vulpix ninetales jigglypuff
  wigglytuff zubat golbat oddish gloom vileplume paras parasect venonat venomoth diglett dugtrio meowth persian psyduck
  golduck mankey primeape growlithe arcanine poliwag poliwhirl poliwrath abra kadabra alakazam machop machoke machamp
  bellsprout weepinbell victreebel tentacool tentacruel geodude graveler golem ponyta rapidash slowpoke slowbro magnemite
  magneton farfetchd doduo dodrio seel dewgong grimer muk shellder cloyster gastly haunter gengar onix drowzee hypno
  krabby kingler voltorb electrode exeggcute exeggutor cubone marowak hitmonlee hitmonchan lickitung koffing weezing
  rhyhorn rhydon chansey tangela kangaskhan horsea seadra goldeen seaking staryu starmie mr-mime scyther jynx
  electabuzz magmar pinsir tauros magikarp gyarados lapras ditto eevee vaporeon jolteon flareon porygon omanyte omastar
  kabuto kabutops aerodactyl snorlax articuno zapdos moltres dratini dragonair dragonite mewtwo mew
)

# LZR1_POKEMON wins; else what the [...] menu saved in $CONFIG/pokemon; else sprite (without curl and ImageMagick the
# panel shows text tags).
PK_MODE=${LZR1_POKEMON:-}
[[ -z $PK_MODE && -r $CONFIG/pokemon ]] && PK_MODE=$(<$CONFIG/pokemon)
[[ $PK_MODE == (off|text|sprite) ]] || PK_MODE=sprite
PK_ART=$STATE/pokemon-art
PK_IDLE=${LZR1_POKEMON_SLEEP:-180}  # seconds idle before it sleeps
[[ $PK_IDLE == <-> ]] || PK_IDLE=180
PK_BASE=https://raw.githubusercontent.com/PMDCollab/SpriteCollab/master/sprite

# _pk_pick PROFILE FNAME KEY: the session's Pokémon number into REPLY; the first time, one from a hash of KEY that no
# other session has (PK_TAKEN, the numbers in use, belongs to the caller), saved at once so it never shifts.
_pk_pick() {
  local f=$STATE/pokemon/$1/$2 n h i
  if [[ -r $f ]]; then n=$(<$f); [[ $n == <1-151> ]] && { REPLY=$n; return 0 }; fi
  h=$(print -rn -- $3 | cksum); h=${h%% *}; n=$(( h % 151 + 1 ))
  for (( i = 0; i < 151; i++ )); do (( ${+PK_TAKEN[$n]} )) || break; n=$(( n % 151 + 1 )); done
  PK_TAKEN[$n]=1
  mkdir -p ${f:h}; print -rn -- $n >$f
  REPLY=$n
}

# _pk_ball: a Poké Ball three lines tall, ten columns wide, in `reply`: what stands in while the art is not there
_pk_ball() {
  local e=$'\e'
  reply=( "  ${e}[0m${e}[31m▄████▄${e}[0m  "
          "  ${e}[31;47m▀▀${e}[30;47m▀▀${e}[31;47m▀▀${e}[0m  "
          "  ${e}[0m${e}[97m▀████▀${e}[0m  " )
}

# The Kitty graphics protocol, where the terminal has it (Ghostty, kitty; not through tmux): the sprite is sent once as
# a PNG (term.png, 240x144) with a virtual placement ten cells by three, and the card's lines hold Unicode placeholders
# (U+10EEEE, a diacritic for the row and one for the column, the image id in the foreground color), so the picture sits
# inside the text that fzf draws. LZR1_POKEMON_IMAGES=0 never, 1 always (where the terminal has it), else auto.
typeset -ga PK_DIAC=( $'\u0305' $'\u030D' $'\u030E' $'\u0310' $'\u0312' $'\u033D' $'\u033E' $'\u033F' $'\u0346' $'\u034A' )

# _pk_kitty_ok: true when the card's art can be a real picture (PK_PANEL names the panel that draws it)
_pk_kitty_ok() {
  [[ -n $PK_PANEL && ${LZR1_POKEMON_IMAGES:-auto} != 0 ]] || return 1
  [[ ${LZR1_POKEMON_IMAGES:-auto} == 1 ]] && return 0
  [[ -z $TMUX && ( $TERM_PROGRAM == ghostty || -n $GHOSTTY_RESOURCES_DIR || -n $KITTY_WINDOW_ID ) ]]
}

# _pk_kitty_send ID FILE: transmit the PNG as image ID with a virtual placement, to the terminal, quietly (q=2: the
# terminal's answers would otherwise arrive as keys)
_pk_kitty_send() {
  local b64 chunk ctrl="a=T,f=100,t=d,i=$1,U=1,c=10,r=3,q=2" more out=
  b64=$(base64 <$2 2>/dev/null | tr -d '\n')
  [[ -n $b64 ]] || return 1
  while [[ -n $b64 ]]; do
    chunk=${b64[1,4096]}; b64=${b64[4097,-1]}
    more=0; [[ -n $b64 ]] && more=1
    out+=$'\e_G'"$ctrl,m=$more;$chunk"$'\e\\'
    ctrl=q=2
  done
  # One write, so fzf's own drawing cannot land between the chunks.
  { print -rn -- $out } >/dev/tty 2>/dev/null
}

# _pk_art_lines N SLEEP: the sprite's three lines in `reply` (the ball while the art is not cut yet); false for the ball.
# A picture (Kitty graphics) when the terminal has them, else half blocks.
_pk_art_lines() {
  local num=${(l:4::0:)1} id marker png row col line f
  if _pk_kitty_ok && [[ -s $PK_ART/$num/term.png ]]; then
    id=$1 png=term.png; [[ -n $2 && -s $PK_ART/$num/term-sleep.png ]] && { id=$(( $1 + 200 )) png=term-sleep.png }
    marker=$STATE/kitty/$PK_PANEL.$id
    local -a fresh=( $marker(Nms-600) )  # sent again every 10 minutes, in case the terminal was reset
    if (( ! $#fresh )); then mkdir -p $STATE/kitty; _pk_kitty_send $id $PK_ART/$num/$png && : >$marker; fi
    if [[ -e $marker ]]; then
      reply=()
      for (( row = 1; row <= 3; row++ )); do
        line=$'\e[38;2;0;0;'${id}m
        for (( col = 1; col <= 10; col++ )); do line+=$'\U10EEEE'$PK_DIAC[row]$PK_DIAC[col]; done
        reply+=( "$line"$'\e[0m' )
      done
      return 0
    fi
  fi
  f=$PK_ART/$num/term${2:+-sleep}.ans
  if [[ -s $f ]]; then reply=( ${(f)"$(<$f)"} ); (( $#reply == 3 )) && return 0; fi
  _pk_ball; return 1
}

# _pk_mood STATE IDLE_SECONDS DONE: the mood in PK_MOOD and its color in PK_MC (D, B, Y, G are the caller's colors)
_pk_mood() {
  PK_MOOD= PK_MC=
  if [[ $1 == wait ]]; then PK_MOOD='!' PK_MC=$B$Y
  elif [[ -n $3 ]]; then PK_MOOD='✓' PK_MC=$G
  elif [[ $1 != work && $2 == <-> ]] && (( $2 >= PK_IDLE )); then PK_MOOD=zZz PK_MC=$D; fi
}

# _jstr TEXT: TEXT as the inside of a JSON string, in REPLY
_jstr() {
  REPLY=${1//[$'\001'-$'\037'$'\177']/ }
  REPLY=${REPLY//\\/\\\\}
  REPLY=${REPLY//\"/\\\"}
}

# _pk_snapshot ROW...: what the browser strip reads (gateway/pokemon.go), written whole and moved into place. A ROW is
# KEY<US>STATE<US>IDLE<US>DONE (0 or 1)<US>DEX.
_pk_snapshot() {
  zmodload zsh/datetime
  local row k st idle fin dex dn out= sep= US=$'\037'
  for row in "$@"; do
    IFS=$US read -r k st idle fin dex <<<$row
    _jstr $k
    dn=false; [[ $fin != 0 ]] && dn=true
    out+="$sep{\"key\":\"$REPLY\",\"state\":\"$st\",\"idle\":$idle,\"done\":$dn,\"dex\":$dex}"
    sep=,
  done
  ( umask 077; print -r -- "{\"at\":$EPOCHSECONDS,\"sessions\":[$out]}" >$STATE/web-sessions.json.$$ &&
      mv -f $STATE/web-sessions.json.$$ $STATE/web-sessions.json )
}

# ---- art -------------------------------------------------------------------------------------------------------------

# _pk_anim NAME: the sheet name, frame size and durations (game frames, 1/60 s) of a SpriteCollab animation in A_SHEET,
# A_FW, A_FH and A_DUR, following <CopyOf>; BLOCKS holds AnimData.xml, one <Anim> per element. False when it has none.
_pk_anim() {
  local want=$1 b i seen=0 d
  while (( seen++ < 3 )); do
    b=; for i in $BLOCKS; do [[ $i == *"<Name>$want</Name>"* ]] && { b=$i; break }; done
    [[ -n $b ]] || return 1
    if [[ $b == *"<CopyOf>"* ]]; then want=${${b#*<CopyOf>}%%</CopyOf>*}; continue; fi
    A_SHEET=$want
    A_FW=${${b#*<FrameWidth>}%%</FrameWidth>*}; A_FH=${${b#*<FrameHeight>}%%</FrameHeight>*}
    d=${${b#*<Durations>}%%</Durations>*}; d=${d//<Duration>/}
    A_DUR=( ${(s:</Duration>:)d} )
    [[ $A_FW == <-> && $A_FH == <-> ]] && (( $#A_DUR )) && [[ ${(j::)A_DUR} == <-> ]] && return 0
    return 1
  done
  return 1
}

# _pk_gif SHEET FW FH ROW OUT DURATION...: one row of a sheet as an animated GIF at twice the size (nearest neighbour).
# IM is the ImageMagick command. A sheet with fewer rows than asked for uses its last one.
_pk_gif() {
  local sheet=$1 fw=$2 fh=$3 row=$4 out=$5 rows expr i n
  shift 5
  local -a d=( "$@" ); n=$#d
  rows=$(( $($IM $sheet -format %h info: 2>/dev/null) / fh )) || return 1
  (( rows >= 1 )) || return 1
  (( row >= rows )) && row=$(( rows - 1 ))
  expr=$(( d[n] * 100 / 60 ))
  for (( i = n - 1; i >= 1; i-- )); do expr="t==$(( i - 1 ))?$(( d[i] * 100 / 60 )):$expr"; done
  $IM $sheet -crop $(( n * fw ))x${fh}+0+$(( row * fh )) +repage -crop ${fw}x${fh} +repage -scale 200% \
    -set delay "%[fx:$expr]" -set dispose Background -loop 0 $out 2>/dev/null
}

# _pk_ans SHEET FW FH OUT: frame 0 of the sheet's first row as three lines of half blocks (ten columns, six pixels)
_pk_ans() {
  local line k x y e=$'\e' px
  local -A P
  local -a t b out
  $IM $1 -crop ${2}x${3}+0+0 +repage -alpha set -trim +repage -background none -gravity center -resize 10x6 \
      -extent 10x6 -depth 8 txt:- 2>/dev/null |
    while IFS= read -r line; do
      [[ $line == (#b)([0-9]##),([0-9]##):\ \(([0-9.]##),([0-9.]##),([0-9.]##),([0-9.]##)\)* ]] || continue
      k="$match[1],$match[2]"; P[$k]="${match[3]%.*} ${match[4]%.*} ${match[5]%.*} ${match[6]%.*}"
    done
  (( $#P )) || return 1
  for (( y = 0; y < 6; y += 2 )); do
    line=
    for (( x = 0; x < 10; x++ )); do
      k="$x,$y"; t=( ${=P[$k]:-0 0 0 0} ); k="$x,$(( y + 1 ))"; b=( ${=P[$k]:-0 0 0 0} )
      if (( t[4] >= 128 && b[4] >= 128 )); then line+="${e}[38;2;$t[1];$t[2];$t[3];48;2;$b[1];$b[2];$b[3]m▀"
      elif (( t[4] >= 128 )); then line+="${e}[0m${e}[38;2;$t[1];$t[2];$t[3]m▀"
      elif (( b[4] >= 128 )); then line+="${e}[0m${e}[38;2;$b[1];$b[2];$b[3]m▄"
      else line+="${e}[0m "; fi
    done
    out+=( "$line${e}[0m" )
  done
  print -rl -- $out >$4
}

# _pk_png SHEET FW FH OUT: frame 0 of the sheet's first row as a 240x144 PNG, the sprite scaled by a whole number (crisp
# pixels) and standing at the bottom, for the Kitty graphics placement (ten cells by three, a cell about twice as tall
# as it is wide)
_pk_png() {
  local f=$tmp/f.png w h k
  $IM $1 -crop ${2}x${3}+0+0 +repage -alpha set -trim +repage $f 2>/dev/null || return 1
  w=$($IM $f -format %w info: 2>/dev/null) h=$($IM $f -format %h info: 2>/dev/null)
  [[ $w == <-> && $h == <-> ]] && (( w > 0 && h > 0 )) || return 1
  k=$(( 132 / h )); (( 220 / w < k )) && k=$(( 220 / w )); (( k < 1 )) && k=1
  $IM $f -scale $(( k * 100 ))% -background none -gravity south -extent 240x144 $4 2>/dev/null
}

# pk_fetch N: cut Pokémon N's art into $PK_ART/NNNN (walkR walkL idleR idleL sleep as GIFs, meta.json with their sizes
# and visible boxes, term.ans and term-sleep.ans for the cards). meta.json is written last: its presence means done.
# False, and no new try for a minute, when it cannot (no network, no curl or ImageMagick, no such sprite).
pk_fetch() {
  local n=$1 num=${(l:4::0:)1} dir tmp rc=1 f
  local -a IM BLOCKS A_DUR
  local A_SHEET A_FW A_FH
  dir=$PK_ART/$num tmp=$PK_ART/.tmp-$num
  [[ $n == <1-151> ]] || return 1
  [[ -s $dir/meta.json && -s $dir/term.png ]] && return 0
  (( $+commands[curl] )) || return 1
  if (( $+commands[magick] )); then IM=( magick ); elif (( $+commands[convert] )); then IM=( convert ); else return 1; fi
  mkdir -p $PK_ART
  local -a recent=( $PK_ART/$num.fail(Nms-60) ); (( $#recent )) && return 1
  if ! mkdir $PK_ART/$num.lock 2>/dev/null; then
    local -a stale=( $PK_ART/$num.lock(Nms+180) )
    (( $#stale )) || return 1
    rm -rf $PK_ART/$num.lock; mkdir $PK_ART/$num.lock 2>/dev/null || return 1
  fi
  {
    rm -rf $tmp; mkdir -p $tmp || return 1
    curl -sf -m 20 -o $tmp/AnimData.xml $PK_BASE/$num/AnimData.xml || return 1
    local flat; flat=$(tr -d ' \t\r\n' <$tmp/AnimData.xml); flat=${flat//<\/Anim>/<\/Anim>$'\n'}
    BLOCKS=( ${(f)flat} )
    local -A SH FW FH DUR
    local nm
    for nm in Walk Idle Sleep; do
      _pk_anim $nm || { [[ $nm == Sleep ]] && continue; return 1 }
      SH[$nm]=$A_SHEET FW[$nm]=$A_FW FH[$nm]=$A_FH DUR[$nm]="$A_DUR"
      [[ -s $tmp/$A_SHEET-Anim.png ]] ||
        curl -sf -m 30 -o $tmp/$A_SHEET-Anim.png $PK_BASE/$num/$A_SHEET-Anim.png || return 1
    done
    # No sleep animation: the first idle frame, eyes and all.
    [[ -n $SH[Sleep] ]] || { SH[Sleep]=$SH[Idle] FW[Sleep]=$FW[Idle] FH[Sleep]=$FH[Idle] DUR[Sleep]=$DUR[Idle] }
    local -A ROW=( walkR 2 walkL 6 idleR 1 idleL 7 sleep 0 ) SRC=( walkR Walk walkL Walk idleR Idle idleL Idle sleep Sleep )
    local g meta= sep= box w h bw bh bx by
    for g in walkR walkL idleR idleL sleep; do
      nm=$SRC[$g]
      _pk_gif $tmp/$SH[$nm]-Anim.png $FW[$nm] $FH[$nm] $ROW[$g] $tmp/$g.gif ${=DUR[$nm]} || return 1
      box=$($IM $tmp/$g.gif -coalesce -background none -flatten -format %@ info: 2>/dev/null)
      if [[ $box == (#b)(<->)x(<->)+(<->)+(<->) ]]; then bw=$match[1] bh=$match[2] bx=$match[3] by=$match[4]
      else bw=0 bh=0 bx=0 by=0; fi
      w=$(( FW[$nm] * 2 )) h=$(( FH[$nm] * 2 ))
      meta+="$sep\"$g\":{\"w\":$w,\"h\":$h,\"x\":$bx,\"y\":$by,\"bw\":$bw,\"bh\":$bh}"
      sep=,
    done
    _pk_ans $tmp/$SH[Idle]-Anim.png $FW[Idle] $FH[Idle] $tmp/term.ans || return 1
    _pk_ans $tmp/$SH[Sleep]-Anim.png $FW[Sleep] $FH[Sleep] $tmp/term-sleep.ans || cp $tmp/term.ans $tmp/term-sleep.ans
    _pk_png $tmp/$SH[Idle]-Anim.png $FW[Idle] $FH[Idle] $tmp/term.png || return 1
    _pk_png $tmp/$SH[Sleep]-Anim.png $FW[Sleep] $FH[Sleep] $tmp/term-sleep.png || cp $tmp/term.png $tmp/term-sleep.png
    rm -f $tmp/f.png
    print -r -- "{$meta}" >$tmp/meta.json
    rm -f $tmp/*-Anim.png $tmp/AnimData.xml
    rm -rf $dir; mv $tmp $dir && rc=0
  } always {
    rm -rf $tmp; rmdir $PK_ART/$num.lock 2>/dev/null
    (( rc )) && : >$PK_ART/$num.fail
  }
  return $rc
}
