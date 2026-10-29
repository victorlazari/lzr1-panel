// lzr1-client.js: the gateway adds this to ttyd's own page, before ttyd's bundle runs (see inject.go).
// 1. When the terminal's WebSocket closes and the session is gone (logout, idle logout, kill switch), go to the
//    login form instead of letting ttyd reconnect forever.
// 2. Copies made in the panel or a session (OSC 52) land on the browser's clipboard; nothing can read it.
// 3. Option-drag selects in xterm.js on a Mac, over the panel's mouse mode.
// 4. The right button over the terminal opens only the panel's menu, not the browser's.
(() => {
  'use strict';
  const MAX = 1 << 20; // a copy larger than 1 MiB is dropped

  // 1. ttyd's socket. A 401 for this script means no session; anything else (a network error, 200) leaves ttyd's own
  // reconnect alone.
  const WS = window.WebSocket;
  if (WS) {
    window.WebSocket = class extends WS {
      constructor(...a) {
        super(...a);
        this.addEventListener('close', () => {
          fetch('lzr1-client.js', { method: 'HEAD', cache: 'no-store', credentials: 'same-origin' })
            .then(r => { if (r.status === 401) location.replace('login'); }, () => {});
        });
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
  const hook = term => {
    if (!term || typeof term !== 'object' || hooked.has(term)) return;
    try {
      term.parser.registerOscHandler(52, osc52);
      term.options.macOptionClickForcesSelection = true; // Option-drag selects; Cmd-C copies it
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
})();
