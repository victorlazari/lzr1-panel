// lzr1-client.js: the gateway adds this to ttyd's own page, before ttyd's bundle runs (see inject.go).
// 1. When the terminal's WebSocket closes and the session is gone (logout, idle logout, kill switch), go to the
//    login form instead of letting ttyd reconnect forever.
// 2. Copies made in the panel or a session (OSC 52) land on the browser's clipboard; nothing can read it.
// 3. Option-drag selects in xterm.js on a Mac, over the panel's mouse mode.
// 4. The right button over the terminal opens only the panel's menu, not the browser's.
// 5. On a touch screen, native navigation around the terminal: a slim header while a session shows (back, its state,
//    its name), a key bar with the keys a phone keyboard lacks, and tabs for Sessions, Pull requests and Remote (who
//    is signed in, when the remote ends, log out).
// 6. A strip along the bottom with a pixel-art Pokémon for every session (docs/pokemon.md): working ones walk, a
//    waiting one stops under a pulsing "!", a finished one shows a check, an idle one strolls and falls asleep. A click
//    shows that session in the panel.
(() => {
  'use strict';
  const MAX = 1 << 20; // a copy larger than 1 MiB is dropped
  const COARSE = !!(window.matchMedia && window.matchMedia('(pointer: coarse)').matches);

  // 1. ttyd's socket. A 401 for this script means no session; a 200 leaves ttyd's own reconnect alone. Anything else (a
  // network error, a 502 while `remote rotate` swaps the gateway) is asked again every second for a minute, so a
  // rotate's logout still reaches the login form.
  const WS = window.WebSocket;
  if (WS) {
    const check = n => {
      fetch('lzr1-client.js', { method: 'HEAD', cache: 'no-store', credentials: 'same-origin' })
        .then(r => r.status, () => 0)
        .then(s => {
          if (s === 401) location.replace('login');
          else if (s !== 200 && n < 60) setTimeout(() => check(n + 1), 1000);
        });
    };
    window.WebSocket = class extends WS {
      constructor(...a) {
        super(...a);
        this.addEventListener('close', () => check(0));
      }
    };
  }

  // 2. Copy. A toast offers the copy when the browser refuses a write without a user gesture (Safari).
  let toast = null, toastTimer = 0;
  const dropToast = () => {
    clearTimeout(toastTimer);
    if (toast) toast.remove();
    toast = null;
  };
  const offer = text => {
    dropToast();
    const t = document.createElement('button');
    t.type = 'button';
    t.textContent = 'Copied text ready — click to copy';
    Object.assign(t.style, {
      position: 'fixed', right: '16px', bottom: '16px', zIndex: 2147483647, padding: '10px 14px',
      font: '14px -apple-system, system-ui, sans-serif', color: '#fff', background: '#333',
      border: '1px solid #888', borderRadius: '6px', cursor: 'pointer',
    });
    t.addEventListener('click', () => {
      navigator.clipboard.writeText(text).then(dropToast, dropToast);
    });
    (document.body || document.documentElement).appendChild(t);
    toast = t;
    toastTimer = setTimeout(dropToast, 15000);
  };
  const copy = text => {
    dropToast();
    if (!navigator.clipboard || !navigator.clipboard.writeText) return offer(text);
    navigator.clipboard.writeText(text).catch(() => offer(text));
  };
  // OSC 52 payload: "Pc;BASE64". "?" asks to read the clipboard: never answered.
  const osc52 = data => {
    const i = data.indexOf(';');
    const b64 = i < 0 ? '' : data.slice(i + 1);
    if (!b64 || b64 === '?' || b64.length > Math.ceil(MAX / 3) * 4 + 4) return true;
    let bin;
    try { bin = atob(b64); } catch { return true; }
    if (bin.length > MAX) return true;
    const bytes = new Uint8Array(bin.length);
    for (let j = 0; j < bin.length; j++) bytes[j] = bin.charCodeAt(j);
    const text = new TextDecoder().decode(bytes);
    if (text) copy(text);
    return true;
  };

  // ttyd sets window.term once its xterm.js is up; hook each terminal it sets.
  const hooked = new WeakSet();
  const titleHooks = []; // sections 5 and 6 listen to the panel's title
  let onTitle = t => titleHooks.forEach(f => f(t));
  const hook = term => {
    if (!term || typeof term !== 'object' || hooked.has(term)) return;
    try {
      term.parser.registerOscHandler(52, osc52);
      term.options.macOptionClickForcesSelection = true; // Option-drag selects; Cmd-C copies it
      if (typeof term.onTitleChange === 'function') term.onTitleChange(t => onTitle(t));
      hooked.add(term);
    } catch { /* not an xterm.js terminal */ }
  };
  let current;
  try {
    Object.defineProperty(window, 'term', {
      configurable: true, enumerable: true,
      get: () => current,
      set: v => { current = v; hook(v); },
    });
  } catch {
    // Something defined it first: poll for it instead (50 ms, 10 s).
    let n = 0;
    const id = setInterval(() => { if (window.term || ++n >= 200) { clearInterval(id); hook(window.term); } }, 50);
  }

  // 4. The browser's own menu would cover the panel's: cancel it over the terminal. The button still reaches xterm.js.
  document.addEventListener('contextmenu', e => {
    if (e.target && e.target.closest && e.target.closest('.xterm')) e.preventDefault();
  }, true);

  // 6. The strip. The panel writes what it listed (and the Pokémon it gave each session) where the gateway reads it
  // (lzr1-sessions); the sprites it cut are served as lzr1-pokemon/NNNN/<name> (only a short list of names). Nothing
  // here comes from the page's own origin but the gateway, and everything from the sessions goes in as text. A click
  // posts the session's key and the panel's id (the last field of its title, id=N) to lzr1-pick; the gateway hands it
  // to that panel only. Sizes and speeds are the reference mod's: a strip 112 px tall (84 on a phone), a column of
  // 128 px (84) per Pokémon, 70 px/s walking, 32 px/s strolling, asleep after 3 minutes idle.
  const PK = (() => {
    const H = COARSE ? 84 : 112, COL = COARSE ? 84 : 128, GROUND = COARSE ? 16 : 20, SCALE = COARSE ? 0.75 : 1;
    const BG = '#1c1c1e', LINE = '#3a3a3c', INK = '#f2f2f7', DIM = '#8e8e93', ACCENT = '#ffd60a';
    const STATE_COLOR = { work: '#64d2ff', wait: ACCENT, done: '#30d158', idle: DIM };
    const reduced = !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
    const api = { onChange: () => {}, height: () => 0, place: () => {}, hide: () => {} };
    let strip = null, shown = 0, panelId = '', selected = '', baseTitle = '';
    const items = new Map(); // key -> { el, img, bub, tag, x, ... }
    const metas = new Map(); // dex -> meta, 'wait' while asked, or { retry: ms }
    let at = 0, receivedAt = 0, timer = 0, poll = 0;
    const box = () => document.getElementById('terminal-container');

    const css = document.createElement('style');
    css.textContent =
      '.lzr1pk{position:fixed;left:0;right:0;bottom:0;overflow:hidden;user-select:none;-webkit-user-select:none;' +
      'background:' + BG + ';border-top:.5px solid ' + LINE + ';z-index:2147483000;touch-action:manipulation}' +
      '.lzr1pk .g{position:absolute;left:0;right:0;height:1px;background:' + LINE + '}' +
      '.lzr1pk .p{position:absolute;cursor:pointer;transition-property:left;transition-timing-function:linear;' +
      'transition-duration:0s;background:none;border:0;padding:0;margin:0;font:inherit;color:inherit}' +
      '.lzr1pk .p:focus-visible{outline:2px solid ' + ACCENT + ';outline-offset:2px}' +
      '.lzr1pk img{position:absolute;image-rendering:pixelated;max-width:none;pointer-events:none}' +
      '.lzr1pk .t{position:absolute;left:0;right:0;text-align:center;white-space:nowrap;overflow:hidden;' +
      'text-overflow:ellipsis;font:10.5px -apple-system,BlinkMacSystemFont,system-ui,sans-serif;color:' + DIM + '}' +
      '.lzr1pk .on .t{color:' + INK + '}' +
      '.lzr1pk .t i{display:inline-block;width:6px;height:6px;border-radius:50%;margin-right:4px;vertical-align:1px}' +
      '.lzr1pk .b{position:absolute;left:50%;transform:translateX(-50%);display:none;white-space:nowrap;' +
      'font:600 12.5px -apple-system,BlinkMacSystemFont,system-ui,sans-serif;color:#fff;background:' + LINE + ';' +
      'border-radius:7px;padding:1px 7px}' +
      '.lzr1pk .b.q{display:block;color:#1c1c1e;background:' + ACCENT + ';animation:lzr1pulse .9s ease-in-out infinite alternate}' +
      '.lzr1pk .b.d{display:block;color:#1c1c1e;background:#30d158}' +
      '.lzr1pk .b.z{display:block;background:none;color:' + DIM + ';font-weight:500}' +
      '.lzr1pk .ball{position:absolute;left:50%;margin-left:-9px;bottom:0;width:18px;height:18px;border-radius:50%;' +
      'border:1.5px solid ' + INK + ';box-sizing:border-box;background:linear-gradient(#e3350d 50%,#f4f4f4 50%);' +
      'animation:lzr1pulse .9s ease-in-out infinite alternate}' +
      '@keyframes lzr1pulse{from{opacity:.6}to{opacity:1}}' +
      '@media (prefers-reduced-motion:reduce){.lzr1pk .b.q,.lzr1pk .ball{animation:none}}';

    const mk = (tag, cls) => { const e = document.createElement(tag); if (cls) e.className = cls; return e; };
    const dexOf = v => (Number.isInteger(v) && v >= 1 && v <= 151) ? ('000' + v).slice(-4) : '';
    const nameOf = key => key.slice(key.indexOf('/') + 1);

    const ensureStrip = () => {
      if (strip || !document.body) return;
      document.head.appendChild(css);
      strip = mk('div', 'lzr1pk');
      strip.style.height = H + 'px';
      strip.style.display = 'none';
      const g = mk('div', 'g'); g.style.bottom = GROUND + 'px';
      strip.appendChild(g);
      document.body.appendChild(strip);
    };

    // The strip's height is what the terminal gives up. A phone's layout (section 5) places it between the terminal and
    // its key bar; elsewhere it sits at the bottom and the terminal is shortened here.
    const apply = () => {
      if (!strip) return;
      const h = items.size ? H : 0;
      if (h === shown) return;
      shown = h;
      strip.style.display = h ? 'block' : 'none';
      if (!COARSE) {
        const tb = box();
        if (tb) tb.style.height = h ? 'calc(100% - ' + h + 'px)' : '';
        window.dispatchEvent(new Event('resize'));
      } else api.onChange();
    };
    api.height = () => shown;
    api.place = top => { if (strip) { strip.style.bottom = 'auto'; strip.style.top = top + 'px'; } };
    api.hide = on => { if (strip) strip.style.visibility = on ? 'hidden' : 'visible'; }; // under the Remote page

    const wantMeta = dex => {
      const m = metas.get(dex), now = Date.now();
      if (m === 'wait' || (m && m.retry && m.retry > now)) return null;
      if (m && m.walkR) return m;
      metas.set(dex, 'wait');
      fetch('lzr1-pokemon/' + dex + '/meta.json', { credentials: 'same-origin' })
        .then(r => r.ok ? r.json() : Promise.reject(new Error(String(r.status))))
        .then(j => metas.set(dex, j && j.walkR ? j : { retry: Date.now() + 60000 }))
        .catch(() => metas.set(dex, { retry: Date.now() + 60000 })); // not cut yet: ask again in a minute
      return null;
    };

    const mood = (s, now) => {
      if (s.state === 'wait') return 'wait';
      if (s.done) return 'done';
      if (s.state === 'idle' && s.idle + (now - receivedAt) / 1000 >= 180) return 'sleep';
      return s.state === 'work' ? 'walk' : 'stroll';
    };

    const rebuild = list => {
      ensureStrip();
      if (!strip) return;
      const seen = new Set();
      list.forEach((s, i) => {
        seen.add(s.key);
        let it = items.get(s.key);
        if (!it) {
          const el = mk('button', 'p'); el.type = 'button';
          const img = mk('img'), ball = mk('div', 'ball'), bub = mk('div', 'b'), tag = mk('div', 't');
          img.alt = ''; img.style.display = 'none';
          el.append(img, ball, bub, tag);
          el.style.width = COL + 'px'; el.style.bottom = GROUND + 'px'; el.style.height = (H - GROUND - 2) + 'px';
          el.dataset.key = s.key;
          el.addEventListener('click', () => pick(s.key));
          strip.appendChild(el);
          const w = Math.max(strip.clientWidth || window.innerWidth, COL); // it may not be shown yet
          const x = Math.max(0, ((i + 0.5) / Math.max(list.length, 1)) * w - COL / 2);
          el.style.left = x + 'px';
          it = { el, img, ball, bub, tag, x, until: 0, moveEnd: 0, mode: '', dir: 1, shot: '', s, dex: '' };
          items.set(s.key, it);
        }
        it.s = s;
        const nm = nameOf(s.key);
        const col = STATE_COLOR[s.state === 'wait' ? 'wait' : s.done ? 'done' : s.state === 'work' ? 'work' : 'idle'];
        it.tag.textContent = '';
        const dot = mk('i'); dot.style.background = col;
        it.tag.append(dot, document.createTextNode(nm));
        it.el.title = nm;
        it.el.setAttribute('aria-label', nm);
      });
      items.forEach((it, key) => { if (!seen.has(key)) { it.el.remove(); items.delete(key); } });
      apply();
      mark();
    };

    const mark = () => items.forEach((it, key) => it.el.classList.toggle('on', key === selected));

    const pick = key => {
      if (!panelId) return;
      fetch('lzr1-pick', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: panelId, key }) }).catch(() => {});
      selected = key; mark(); // the panel's title says so too once the viewer has moved
    };

    const draw = (it, m, mode, now) => {
      const s = it.s, meta = it.dex && metas.get(it.dex);
      if (!meta || !meta.walkR) { it.img.style.display = 'none'; it.ball.style.display = 'block'; return; }
      it.ball.style.display = 'none';
      const moving = now < it.moveEnd && (mode === 'walk' || mode === 'stroll');
      const g = mode === 'sleep' ? 'sleep' : (moving ? 'walk' : 'idle') + (mode === 'sleep' ? '' : (it.dir < 0 ? 'L' : 'R'));
      const gm = meta[g] || meta.walkR;
      const shot = it.dex + g;
      if (it.shot === shot) return;
      it.shot = shot;
      // Pokémon whose walk frame is taller than 64 px (before doubling) are drawn at half size.
      const sc = SCALE * (meta.walkR.h / 2 > 64 ? 0.5 : 1);
      it.img.src = 'lzr1-pokemon/' + it.dex + '/' + g + '.gif';
      it.img.style.display = 'block';
      it.img.style.width = gm.w * sc + 'px'; it.img.style.height = gm.h * sc + 'px';
      // The visible box: feet on the ground, body in the middle of the column.
      it.img.style.left = (COL / 2 - (gm.x + gm.bw / 2) * sc) + 'px';
      it.img.style.bottom = (-(gm.h - (gm.y + gm.bh)) * sc) + 'px';
      it.vh = gm.bh * sc;
    };

    const tick = () => {
      if (!strip || !items.size || document.hidden) return;
      const now = performance.now(), wall = Date.now();
      const room = Math.max(strip.clientWidth - COL, 0);
      items.forEach(it => {
        const s = it.s;
        it.dex = dexOf(s.dex);
        if (it.dex) wantMeta(it.dex);
        let mode = mood(s, wall);
        if (reduced && (mode === 'walk' || mode === 'stroll')) mode = 'stand';
        if (mode === 'wait' || mode === 'done') mode = 'stand';
        if (it.mode !== mode) {
          // A new mood stops it where it is, mid-walk too.
          const cur = it.el.offsetLeft;
          it.el.style.transitionDuration = '0s'; it.el.style.left = cur + 'px'; void it.el.offsetWidth;
          it.x = cur; it.mode = mode; it.until = 0; it.moveEnd = 0; it.shot = '';
        }
        if ((mode === 'walk' || mode === 'stroll') && now >= it.until) {
          let tx = it.x, n = 0;
          while (Math.abs(tx - it.x) < 60 && n++ < 20) tx = Math.random() * room;
          const d = Math.abs(tx - it.x) / (mode === 'walk' ? 70 : 32);
          it.dir = tx > it.x ? 1 : -1;
          it.el.style.transitionDuration = d + 's'; it.el.style.left = tx + 'px'; it.x = tx;
          it.moveEnd = now + d * 1000;
          it.until = it.moveEnd + (mode === 'walk' ? 300 + Math.random() * 1500 : 4000 + Math.random() * 8000);
        }
        draw(it, s, mode, now);
        const m = mood(s, wall);
        const txt = m === 'wait' ? '!' : m === 'done' ? '✓' : m === 'sleep' ? ['z', 'zZ', 'zZz', 'zZ'][Math.floor(now / 700) % 4] : '';
        it.bub.className = 'b' + (m === 'wait' ? ' q' : m === 'done' ? ' d' : m === 'sleep' ? ' z' : '');
        if (it.bub.textContent !== txt) it.bub.textContent = txt;
        it.bub.style.bottom = ((it.vh || 18) + 4) + 'px'; // above the head
        it.tag.style.top = (H - GROUND - 2 + 4) + 'px';
      });
    };

    const fetchSessions = () => {
      if (document.hidden) return;
      fetch('lzr1-sessions', { cache: 'no-store', credentials: 'same-origin' })
        .then(r => r.ok ? r.json() : null)
        .then(j => {
          if (!j || !Array.isArray(j.sessions)) return;
          at = j.at; receivedAt = Date.now();
          rebuild(j.sessions.filter(s => s && typeof s.key === 'string' && s.key.indexOf('/') > 0 &&
            ['work', 'wait', 'idle'].includes(s.state)).map(s => ({
            key: s.key, state: s.state, idle: Number(s.idle) || 0, done: !!s.done, dex: s.dex })));
        })
        .catch(() => {});
    };

    titleHooks.push(t => {
      const f = String(t || '').split('|');
      if (f[0] !== 'lzr1') return;
      const idf = f.find(x => /^id=[0-9]{1,10}$/.test(x));
      if (idf) panelId = idf.slice(3);
      selected = (f[1] === 'view' || f[1] === 'wide') ? (f[2] || '') : '';
      mark();
      // ttyd shows the terminal's title as the page's: a desktop tab keeps its own.
      if (!COARSE) setTimeout(() => { document.title = baseTitle || 'lzr1'; }, 0);
    });

    const start = () => {
      baseTitle = document.title;
      ensureStrip();
      fetchSessions();
      poll = setInterval(fetchSessions, 3000);
      timer = setInterval(tick, 250);
      document.addEventListener('visibilitychange', () => { if (!document.hidden) fetchSessions(); });
      window.addEventListener('resize', () => items.forEach(it => {
        const room = Math.max(strip.clientWidth - COL, 0);
        if (it.x > room) { it.x = room; it.el.style.transitionDuration = '0s'; it.el.style.left = room + 'px'; }
      }));
    };
    if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
    return api;
  })();

  // 5. A phone: native navigation around the terminal, top to bottom: a slim header while a session shows (back, its
  // state, its name), the terminal, a key bar with the keys a phone keyboard lacks (Esc, Tab, Ctrl-C, arrows), and tabs
  // for Sessions, Pull requests and Remote. The panel's title says what shows (lzr1|list or lzr1|view|KEY|STATE: _fit
  // and remote_view in bin/lzr1-panel); the tabs drive the panel with keys it binds for them (alt-s: the top of the
  // list, alt-p: the pull requests), leaving a session first as back does (Ctrl-b d). Remote is a page of its own, read
  // from the gateway (lzr1-status): nothing on it changes the remote but Log out. Every key acts on pointerdown with the
  // default prevented, so the focus, and the on-screen keyboard with it, stays where it was. A swipe over the terminal
  // scrolls it (as wheel events: a phone sends none, and the panel and tmux scroll on them). The on-screen keyboard
  // opens only on a tap inside a session: never in the list, the pull requests or Remote, and not on opening a session.
  // Text from the panel goes in with textContent only.
  if (COARSE) {
    const BG = '#1c1c1e', LINE = '#3a3a3c', INK = '#f2f2f7', DIM = '#8e8e93', ACCENT = '#ffd60a';
    const UI = '-apple-system, BlinkMacSystemFont, "SF Pro Text", system-ui, sans-serif';
    const el = (tag, style, text) => {
      const e = document.createElement(tag);
      if (style) Object.assign(e.style, style);
      if (text != null) e.textContent = text;
      return e;
    };
    const tap = (e, fn) => {
      e.addEventListener('pointerdown', ev => { ev.preventDefault(); fn(); });
      e.addEventListener('click', ev => ev.preventDefault());
    };
    const send = seq => {
      const t = window.term;
      if (!t || typeof t.input !== 'function') return;
      if (seq.length === 1 && 'ABCD'.includes(seq)) { // an arrow, in the cursor-key mode the program asked for
        seq = (t.modes && t.modes.applicationCursorKeysMode ? '\x1bO' : '\x1b[') + seq;
      }
      t.input(seq, true);
    };

    // What shows: the list, or a session (its key and state).
    let view = { list: true, key: '', state: '' };
    let paged = false; // the Remote page shows: no keys, which would type into a terminal out of sight

    // The keyboard: xterm's hidden textarea takes focus on every tap, which opens the on-screen keyboard. inputmode
    // "none" keeps it shut; only a tap inside a session (typing) turns it on, and changing what shows turns it off.
    let typing = false;
    const field = () => document.querySelector('.xterm-helper-textarea');
    const applyKeyboard = () => {
      const f = field();
      if (!f) return;
      const on = typing && !view.list && !paged;
      f.setAttribute('inputmode', on ? 'text' : 'none');
      if (!on && document.activeElement === f) f.blur();
    };
    const closeKeyboard = () => { typing = false; applyKeyboard(); };
    const box = () => document.getElementById('terminal-container');

    // A swipe over the terminal scrolls it. A tap is left alone (it reaches xterm as a click, as before).
    let touch = null;
    document.addEventListener('touchstart', e => {
      const b = box();
      if (e.touches.length !== 1 || !b || !b.contains(e.target)) { touch = null; return; }
      applyKeyboard(); // inputmode is right before xterm's mousedown focuses the field
      touch = { y: e.touches[0].clientY, x: e.touches[0].clientX, total: 0, acc: 0, swipe: false };
    }, { capture: true, passive: true });
    document.addEventListener('touchmove', e => {
      if (!touch || e.touches.length !== 1) return;
      const y = e.touches[0].clientY, dy = touch.y - y;
      touch.y = y;
      touch.total += dy;
      if (!touch.swipe && Math.abs(touch.total) > 8) { touch.swipe = true; touch.acc = touch.total; }
      else if (touch.swipe) touch.acc += dy;
      if (!touch.swipe) return;
      e.preventDefault();
      const t = window.term, rows = (t && t.rows) || 24, b = box();
      const cell = Math.max(8, (b ? b.clientHeight : 400) / rows);
      const lines = Math.trunc(touch.acc / cell);
      if (!lines) return;
      touch.acc -= lines * cell;
      const target = document.querySelector('.xterm-screen') || b;
      if (target) {
        target.dispatchEvent(new WheelEvent('wheel', { deltaY: lines, deltaMode: 1, bubbles: true, cancelable: true,
          clientX: touch.x, clientY: y }));
      }
    }, { capture: true, passive: false });
    document.addEventListener('touchend', e => {
      const was = touch;
      touch = null;
      // A tap in a session asks for the keyboard.
      if (!was || was.swipe || view.list || paged) return;
      typing = true;
      applyKeyboard();
      const f = field();
      if (f) { f.blur(); f.focus(); }
    }, { capture: true, passive: true });
    document.addEventListener('touchcancel', () => { touch = null; }, { capture: true, passive: true });
    // Back to the list: leave the session (Ctrl-b d), then KEYS once the panel has the keyboard again.
    const toList = keys => {
      if (view.list) { if (keys) send(keys); return; }
      send('\x02d');
      if (keys) setTimeout(() => send(keys), 400);
    };

    // The header, while a session shows.
    const head = el('div', {
      position: 'fixed', left: '0', right: '0', top: '0', zIndex: 2147483646, display: 'none', alignItems: 'center',
      gap: '6px', height: '44px', padding: '0 max(8px, env(safe-area-inset-right)) 0 max(8px, env(safe-area-inset-left))',
      background: BG, borderBottom: '0.5px solid ' + LINE, boxSizing: 'border-box', font: '15px ' + UI, color: INK,
    });
    head.setAttribute('role', 'banner');
    const back = el('button', {
      minWidth: '44px', minHeight: '44px', padding: '0 6px', border: '0', background: 'none', color: ACCENT,
      font: '17px ' + UI, touchAction: 'manipulation',
    }, '‹ Sessions');
    back.type = 'button';
    back.setAttribute('aria-label', 'Back to the sessions');
    tap(back, () => toList(''));
    const glyph = el('span', { font: '13px ui-monospace, Menlo, monospace', minWidth: '14px', textAlign: 'center' });
    const name = el('span', { flex: '1', minWidth: '0', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: '600' });
    head.append(back, glyph, name);

    // The key bar: 8 keys of 44px that fit a 390px screen, so it never scrolls and a key is never sent by a swipe.
    const keys = el('div', {
      position: 'fixed', left: '0', right: '0', top: '0', zIndex: 2147483646, display: 'flex', gap: '3px',
      padding: '4px max(4px, env(safe-area-inset-right)) 4px max(4px, env(safe-area-inset-left))',
      background: BG, borderTop: '0.5px solid ' + LINE, boxSizing: 'border-box',
    });
    keys.setAttribute('role', 'toolbar');
    keys.setAttribute('aria-label', 'Keys');
    for (const [label, seq] of [['esc', '\x1b'], ['tab', '\t'], ['^C', '\x03'], ['←', 'D'], ['↑', 'A'], ['↓', 'B'], ['→', 'C'],
                                ['back', '\x02d']]) {
      const b = el('button', {
        flex: '1 1 0', minWidth: '44px', minHeight: '44px', padding: '0', font: '15px ' + UI, color: INK,
        background: '#2c2c2e', border: '0.5px solid ' + LINE, borderRadius: '8px', touchAction: 'manipulation',
      }, label);
      b.type = 'button';
      tap(b, () => send(seq));
      keys.appendChild(b);
    }

    // The tabs.
    const tabs = el('nav', {
      position: 'fixed', left: '0', right: '0', top: '0', zIndex: 2147483646, display: 'flex',
      padding: '0 env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left)',
      background: BG, borderTop: '0.5px solid ' + LINE, boxSizing: 'border-box',
    });
    tabs.setAttribute('aria-label', 'Sections');
    const icon = d => {
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.setAttribute('viewBox', '0 0 24 24');
      svg.setAttribute('width', '24');
      svg.setAttribute('height', '24');
      svg.setAttribute('aria-hidden', 'true');
      const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', d);
      path.setAttribute('fill', 'none');
      path.setAttribute('stroke', 'currentColor');
      path.setAttribute('stroke-width', '1.6');
      path.setAttribute('stroke-linecap', 'round');
      path.setAttribute('stroke-linejoin', 'round');
      svg.appendChild(path);
      return svg;
    };
    let current = 'sessions';
    const tabEls = {};
    const select = id => {
      current = id;
      for (const [k, t] of Object.entries(tabEls)) {
        t.style.color = k === id ? ACCENT : DIM;
        t.setAttribute('aria-current', k === id ? 'page' : 'false');
      }
    };
    const addTab = (id, label, d, fn) => {
      const t = el('button', {
        flex: '1', minHeight: '49px', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center',
        gap: '2px', border: '0', background: 'none', color: DIM, font: '10px ' + UI, touchAction: 'manipulation',
      });
      t.type = 'button';
      t.append(icon(d), el('span', null, label));
      tap(t, () => { select(id); fn(); });
      tabEls[id] = t;
      tabs.appendChild(t);
    };

    // The Remote page: who is signed in, when the remote ends, the idle limit, and Log out.
    const page = el('div', {
      position: 'fixed', left: '0', right: '0', top: '0', zIndex: 2147483645, display: 'none', overflowY: 'auto',
      padding: 'max(16px, env(safe-area-inset-top)) max(16px, env(safe-area-inset-right)) 16px max(16px, env(safe-area-inset-left))',
      background: '#000', color: INK, font: '15px ' + UI, boxSizing: 'border-box',
    });
    page.setAttribute('role', 'region');
    page.setAttribute('aria-label', 'Remote');
    const row = (label, value) => {
      const r = el('div', { display: 'flex', justifyContent: 'space-between', gap: '12px', padding: '12px 16px', borderBottom: '0.5px solid ' + LINE });
      r.append(el('span', { color: DIM }, label), el('span', { textAlign: 'right', minWidth: '0', overflowWrap: 'anywhere' }, value));
      return r;
    };
    const showPage = on => {
      paged = on;
      closeKeyboard();
      page.style.display = on ? 'block' : 'none';
      layout();
      if (!on) return;
      page.replaceChildren(el('div', { font: '600 28px ' + UI, margin: '8px 0 16px' }, 'Remote'), el('div', { color: DIM }, 'Loading…'));
      fetch('lzr1-status', { cache: 'no-store', credentials: 'same-origin' })
        .then(r => (r.status === 401 ? location.replace('login') : r.json()))
        .then(st => {
          if (!st) return;
          const group = el('div', { background: BG, borderRadius: '10px', overflow: 'hidden', marginBottom: '24px' });
          const others = (st.signedIn || []).filter(l => l !== st.you);
          group.append(row('Signed in as', st.you || '—'), row('Also signed in', others.length ? others.join(', ') : 'no one'));
          if (st.expires) {
            const end = new Date(st.expires * 1000), mins = Math.max(0, Math.round((end - Date.now()) / 60000));
            group.append(row('Ends', end.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) +
              ' · in ' + (mins >= 60 ? Math.floor(mins / 60) + 'h ' : '') + (mins % 60) + 'm'));
          }
          group.append(row('Idle logout', Math.round((st.idleSeconds || 0) / 60) + ' min without input'));
          const out = el('button', {
            width: '100%', minHeight: '50px', border: '0', borderRadius: '10px', background: BG, color: '#ff453a',
            font: '17px ' + UI, touchAction: 'manipulation',
          }, 'Log out');
          out.type = 'button';
          out.addEventListener('click', () => {
            fetch('logout', { method: 'POST', credentials: 'same-origin' }).finally(() => location.replace('login'));
          });
          page.replaceChildren(el('div', { font: '600 28px ' + UI, margin: '8px 0 16px' }, 'Remote'), group, out,
            el('p', { color: DIM, font: '13px ' + UI, margin: '12px 4px' },
              'Turning the remote off or changing its password is done on the computer running the panel.'));
        })
        .catch(() => page.replaceChildren(el('div', { color: DIM }, 'Could not reach the remote.')));
    };

    addTab('sessions', 'Sessions', 'M4 6h16M4 12h16M4 18h10', () => { showPage(false); toList('\x1bs'); });
    addTab('prs', 'Pull requests', 'M7 4v16M7 8a3 3 0 1 0 0-.01M17 20v-9a3 3 0 0 0-3-3h-3M17 20a3 3 0 1 0 0-.01', () => {
      showPage(false); toList('\x1bp');
    });
    addTab('remote', 'Remote', 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18', () => showPage(true));
    select('sessions');

    const STATES = { work: '▶', wait: '⧗', idle: '·' };
    titleHooks.push(title => {
      const f = String(title || '').split('|');
      if (f[0] !== 'lzr1') return;
      const was = view.list ? '' : view.key;
      view = f[1] === 'view' ? { list: false, key: f[2] || '', state: f[3] || '' } : { list: true, key: '', state: '' };
      if (view.list || view.key !== was) closeKeyboard(); // a session just opened: the keyboard waits for a tap
      glyph.textContent = STATES[view.state] || '';
      glyph.style.color = view.state === 'wait' ? ACCENT : view.state === 'work' ? '#64d2ff' : DIM;
      name.textContent = view.key.slice(view.key.indexOf('/') + 1);
      layout();
    });

    // Sizes: the terminal gets what stays between the header and the key bar, above the tabs and the on-screen keyboard.
    // While the keyboard is open the tabs step aside, as in a native app.
    const layout = () => {
      const vv = window.visualViewport;
      const h = vv ? vv.height : window.innerHeight, top = vv ? vv.offsetTop : 0;
      const keyboard = h < window.innerHeight * 0.75;
      head.style.display = view.list ? 'none' : 'flex';
      tabs.style.display = keyboard ? 'none' : 'flex';
      const headH = view.list ? 0 : head.offsetHeight, tabH = keyboard ? 0 : tabs.offsetHeight;
      keys.style.display = 'flex';
      const keyH = keys.offsetHeight; // measured even under the Remote page, so the terminal keeps its size there
      const stripH = paged ? 0 : PK.height(); // the Pokémon strip sits between the terminal and the key bar
      keys.style.display = paged ? 'none' : 'flex';
      head.style.top = top + 'px';
      tabs.style.top = (top + h - tabH) + 'px';
      keys.style.top = (top + h - tabH - keyH) + 'px';
      PK.place(top + h - tabH - keyH - stripH);
      PK.hide(paged);
      page.style.top = top + 'px';
      page.style.height = (h - tabH) + 'px';
      const tb = box();
      if (tb) {
        Object.assign(tb.style, { position: 'fixed', left: '0', right: '0', top: (top + headH) + 'px',
          height: Math.max(0, h - headH - keyH - tabH - stripH) + 'px', touchAction: 'none', overscrollBehavior: 'none' });
      }
      document.documentElement.style.overscrollBehavior = 'none';
      applyKeyboard();
      window.dispatchEvent(new Event('resize'));
    };
    const start = () => { document.body.append(page, head, keys, tabs); PK.onChange = layout; layout(); };
    if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
    if (window.visualViewport) {
      window.visualViewport.addEventListener('resize', layout);
      window.visualViewport.addEventListener('scroll', layout);
    } else {
      window.addEventListener('orientationchange', layout);
    }
  }
})();
