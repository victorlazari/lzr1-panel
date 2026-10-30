// lzr1-client.js: the gateway adds this to ttyd's own page, before ttyd's bundle runs (see inject.go).
// 1. When the terminal's WebSocket closes and the session is gone (logout, idle logout, kill switch), go to the
//    login form instead of letting ttyd reconnect forever.
// 2. Copies made in the panel or a session (OSC 52) land on the browser's clipboard; nothing can read it.
// 3. Option-drag selects in xterm.js on a Mac, over the panel's mouse mode.
// 4. The right button over the terminal opens only the panel's menu, not the browser's.
// 5. On a touch screen, native navigation around the terminal: a slim header while a session shows (back, its state,
//    its name), a key bar with the keys a phone keyboard lacks, and tabs for Sessions, Pull requests and Remote (who
//    is signed in, when the remote ends, log out).
(() => {
  'use strict';
  const MAX = 1 << 20; // a copy larger than 1 MiB is dropped

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
  let onTitle = () => {}; // section 5 sets it on a phone
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

  // 5. A phone: native navigation around the terminal, top to bottom: a slim header while a session shows (back, its
  // state, its name), the terminal, a key bar with the keys a phone keyboard lacks (Esc, Tab, Ctrl-C, arrows), and tabs
  // for Sessions, Pull requests and Remote. The panel's title says what shows (lzr1|list or lzr1|view|KEY|STATE: _fit
  // and remote_view in bin/lzr1-panel); the tabs drive the panel with keys it binds for them (alt-s: the top of the
  // list, alt-p: the pull requests), leaving a session first as back does (Ctrl-b d). Remote is a page of its own, read
  // from the gateway (lzr1-status): nothing on it changes the remote but Log out. Every key acts on pointerdown with the
  // default prevented, so the focus, and the on-screen keyboard with it, stays where it was. Text from the panel goes
  // in with textContent only.
  if (window.matchMedia && window.matchMedia('(pointer: coarse)').matches) {
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
    let paged = false; // the Remote page shows: no keys, which would type into a terminal out of sight
    const showPage = on => {
      paged = on;
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
    onTitle = title => {
      const f = String(title || '').split('|');
      if (f[0] !== 'lzr1') return;
      view = f[1] === 'view' ? { list: false, key: f[2] || '', state: f[3] || '' } : { list: true, key: '', state: '' };
      glyph.textContent = STATES[view.state] || '';
      glyph.style.color = view.state === 'wait' ? ACCENT : view.state === 'work' ? '#64d2ff' : DIM;
      name.textContent = view.key.slice(view.key.indexOf('/') + 1);
      layout();
    };

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
      keys.style.display = paged ? 'none' : 'flex';
      head.style.top = top + 'px';
      tabs.style.top = (top + h - tabH) + 'px';
      keys.style.top = (top + h - tabH - keyH) + 'px';
      page.style.top = top + 'px';
      page.style.height = (h - tabH) + 'px';
      const box = document.getElementById('terminal-container');
      if (box) {
        Object.assign(box.style, { position: 'fixed', left: '0', right: '0', top: (top + headH) + 'px',
          height: Math.max(0, h - headH - keyH - tabH) + 'px' });
      }
      window.dispatchEvent(new Event('resize'));
    };
    const start = () => { document.body.append(page, head, keys, tabs); layout(); };
    if (document.body) start(); else document.addEventListener('DOMContentLoaded', start);
    if (window.visualViewport) {
      window.visualViewport.addEventListener('resize', layout);
      window.visualViewport.addEventListener('scroll', layout);
    } else {
      window.addEventListener('orientationchange', layout);
    }
  }
})();
