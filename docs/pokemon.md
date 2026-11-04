# A Pokémon for every session

Idea and behavior from [herdr-pokemon-agents](https://github.com/mathieucroset/herdr-pokemon-agents), rebuilt around
what the panel already knows. Every session gets one of the first 151 Pokémon. Its behavior is the session's state:

| State (already tracked by the panel) | In the cards | In the browser strip |
|---|---|---|
| working (`▶`) | no mood | walks back and forth at 70 px/s, facing where it goes |
| waiting on you (`⧗`) | `!` | stops under a pulsing `!` |
| finished, not looked at yet (`✱` done) | `✓` | stops under a `✓` |
| idle | no mood | strolls at 32 px/s with long pauses |
| idle for 3 minutes or more | `zZz` | lies down: `z`, `zZ`, `zZz` |

Opening the session in the viewer clears the `✓`, as it clears the `✱`.

## Choosing how it looks

The theme is on by default. Turn it on or off, or pick the look, from **`[...]`** at the start of the panel's footer
(sprites, text tags or off). The choice is kept in `~/.config/lzr1-panel/pokemon`. `LZR1_POKEMON` in your shell, when
set, wins over it:

- `sprite` (default): a sprite, 10 columns by 3 lines, beside each card (the panel is 66 columns wide, 10 more, unless
  `LZR1_PANEL_WIDTH` says otherwise). In Ghostty and kitty (not through tmux) it is a real picture: a crisp 240x144 PNG
  sent with the Kitty graphics protocol and placed with Unicode placeholders, so it sits inside the lines fzf draws.
  Elsewhere (the browser's terminal, tmux, Terminal.app) it is half blocks, six pixels tall. `LZR1_POKEMON_IMAGES=0`
  forces half blocks, `1` forces pictures where the terminal has them. A Poké Ball stands in until the art is cut. Needs `curl` and
  ImageMagick (`magick`, or ImageMagick 6's `convert`; `./install.sh` installs it with brew on macOS); without them the
  panel shows text tags.
- `text`: the mood at the end of line 2 and the Pokémon's number and name at the end of line 3. No dependency, no
  extra width.
- `off`: nothing at all.

`LZR1_POKEMON_SLEEP` (seconds, default `180`) is how long idle before it sleeps.

## Which Pokémon

The first time the panel sees a session it picks a number from a hash of the session's key, skipping the numbers in
use, and saves it in `~/.cache/lzr1-panel/pokemon/PROFILE/NAME`. The file goes with the session when it ends. To
change it, press `p` on the row (or **choose its Pokémon** in the right-click menu): an fzf list of the 151, type a name
or a number (`pika`, `25`), enter or click picks, and a preview shows the sprite. The browser strip shows the choice at
the panel's next refresh (at most 15 seconds).

## In a browser

While the panel is in a browser (`lzr1-panel remote on`), a strip along the bottom shows every session as its
Pokémon: 112 px tall on a computer, 84 px on a phone (between the terminal and its key bar). The name tag carries the
state color (blue working, yellow waiting, green finished, grey idle), and the one showing in the viewer is brighter.
**Click a Pokémon** and that session shows in the panel, as a click on its row does. With no sessions the strip is gone
and the terminal takes its space back. It needs the art, so it needs `curl` and ImageMagick on the computer running
the remote; until a sprite is cut that Pokémon is a Poké Ball.

How it is wired (all of it behind the gateway's identity, Origin and session checks):

- The panel's refresh writes `~/.cache/lzr1-panel/web-sessions.json` (each session's key, state, idle seconds, done
  flag and Pokémon) while the remote is on. The gateway reads it (`--state`) and serves it as `lzr1-sessions`.
- The sprites are served as `lzr1-pokemon/NNNN/NAME`, only for the names `walkR.gif walkL.gif idleR.gif idleL.gif
  sleep.gif meta.json`, only regular files under `~/.cache/lzr1-panel/pokemon-art`.
- A click posts `{id, key}` to `lzr1-pick`. `id` is the panel's id, the last field of its terminal title (`id=N`, set
  by the panel for browser layouts). The gateway accepts it only when that panel belongs to this remote and the key is a
  session the last refresh listed, then leaves the key in `$STATE/N.follow`, which that panel's ticker already turns
  into a move of its highlight. Nothing in these endpoints starts, ends or types into a session.
- With two browsers open, a click moves only the panel of the browser that was clicked.

## The art

Sprites are [PMD SpriteCollab](https://github.com/PMDCollab/SpriteCollab) (CC BY-NC 4.0, by the PMDCollab artists),
downloaded with `curl` from raw.githubusercontent.com the first time a Pokémon is needed, at most three per refresh,
and cut with ImageMagick into `~/.cache/lzr1-panel/pokemon-art/NNNN/`: GIFs for walking and standing (each facing),
sleeping, a `meta.json` with their sizes and visible boxes, and `term.ans` / `term-sleep.ans` for the cards. Nothing
Pokémon ships in this repository, and nothing else is downloaded or sent anywhere. A failed download is tried again after
a minute. Delete `~/.cache/lzr1-panel/pokemon-art` to cut everything again.

Pokémon and all related names are trademarks of Nintendo, Creatures and GAME FREAK. This is an unofficial fan feature.
