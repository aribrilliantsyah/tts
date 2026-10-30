// Shared helpers for all pages. ES module.

export const esc = (s) =>
  String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

/** JSON fetch. Throws Error(message) on non-2xx using the server's {error}. */
export async function api(path, { method = "GET", body } = {}) {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "content-type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err = new Error(data.error || `Gagal (${res.status})`);
    err.status = res.status;
    throw err;
  }
  return data;
}

let toastTimer;
/** Shows a transient message. Requires <div id="toast" role="status"></div> on the page. */
export function toast(msg) {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 3000);
}

export function formatDuration(ms) {
  if (ms == null) return "–";
  const s = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${sec}` : `${m}:${sec}`;
}

export const formatDate = (ts) => new Date(ts).toLocaleString("id-ID", { dateStyle: "medium", timeStyle: "short" });

/** <img> markup for an avatar object (Big Smile). Needs /vendor/avatar.js loaded first. */
export function avatarImg(avatar, size = 48, alt = "") {
  return `<img class="avatar" width="${size}" height="${size}" style="width:${size}px;height:${size}px" src="${window.TTSAvatar.dataUri(avatar || {})}" alt="${esc(alt)}">`;
}

/**
 * Avatar editor widget. Renders into `root`, calls onChange(avatar) after each change.
 * Returns { get(), set(avatar) }.
 */
export function avatarEditor(root, initial, onChange = () => {}) {
  const A = window.TTSAvatar;
  let value = A.normalize(initial && Object.keys(initial).length ? initial : A.random());
  const labels = { hair: "Rambut", eyes: "Mata", mouth: "Mulut", accessories: "Aksesoris", skinColor: "Kulit", hairColor: "Warna rambut", backgroundColor: "Latar" };
  function render() {
    root.innerHTML = `
      <div class="av-editor">
        <div class="av-stage">${avatarImg(value, 160, "Avatar")}<button type="button" class="btn sun small" data-act="random">Acak</button></div>
        <div class="av-parts">
          ${Object.entries(A.PARTS).map(([k, list]) => `
            <div class="av-part"><b>${labels[k]}</b>
              <button type="button" class="av-arr" data-k="${k}" data-d="-1" aria-label="${labels[k]} sebelumnya">‹</button>
              <span>${list.indexOf(value[k]) + 1} / ${list.length}</span>
              <button type="button" class="av-arr" data-k="${k}" data-d="1" aria-label="${labels[k]} berikutnya">›</button>
            </div>`).join("")}
          ${Object.entries(A.COLORS).map(([k, list]) => `
            <div class="av-part"><b>${labels[k]}</b>
              <div class="av-sw">${list.map((c) => `<button type="button" style="background:#${c}" data-c="${k}" data-v="${c}" aria-pressed="${value[k] === c}" aria-label="${labels[k]} #${c}"></button>`).join("")}</div>
            </div>`).join("")}
        </div>
      </div>`;
  }
  root.addEventListener("click", (e) => {
    const t = e.target.closest("button");
    if (!t) return;
    if (t.dataset.act === "random") value = A.random();
    else if (t.dataset.k) {
      const list = A.PARTS[t.dataset.k];
      value = { ...value, [t.dataset.k]: list[(list.indexOf(value[t.dataset.k]) + Number(t.dataset.d) + list.length) % list.length] };
    } else if (t.dataset.c) value = { ...value, [t.dataset.c]: t.dataset.v };
    else return;
    render();
    onChange(value);
  });
  render();
  return { get: () => value, set: (a) => { value = A.normalize(a); render(); } };
}

/** Countdown label for `leftMs` remaining: whole seconds (rounded up) above 3 s, tenths in the last 3 s. "0.0" only once time is up. */
export function fmtCountdown(leftMs) {
  if (leftMs > 3000) return { text: String(Math.ceil(leftMs / 1000)), low: false };
  return { text: (Math.max(0, Math.ceil(leftMs / 100)) / 10).toFixed(1), low: true };
}

// ---------- Crossword board pixel metrics ----------
// Tiles must land on whole device pixels: integer cell size, integer font sizes (the CSS reads --fs/--nfs).

/** Largest cell (CSS px, a whole number of device pixels) that fits `avail` px per cell, within [min, max]. */
export function snapCell(avail, min, max) {
  const d = window.devicePixelRatio || 1;
  return Math.max(min, Math.min(max, Math.floor(avail * d + 1e-6) / d));
}

/** Custom properties for a board with cell size `cell` px, as [name, value] pairs. Cell and fonts are whole *device* px (== whole CSS px at DPR 1). */
function boardProps(cell) {
  const d = window.devicePixelRatio || 1;
  const dp = (v) => Math.max(1, Math.round(v * d)) / d;
  const px = dp(cell);
  return [["--cell", `${px}px`], ["--fs", `${dp(px * 0.5)}px`], ["--nfs", `${dp(Math.max(9, px * 0.24))}px`], ["--ly", `${dp(px * 0.035)}px`]];
}

/** Inline style text for markup: `style="${boardStyle(28)}"`. */
export const boardStyle = (cell) => boardProps(cell).map(([k, v]) => `${k}:${v}`).join(";");

export function setBoardCell(board, cell) {
  for (const [k, v] of boardProps(cell)) board.style.setProperty(k, v);
  const d = window.devicePixelRatio || 1;
  const bs = getComputedStyle(board);
  const snap = (v) => Math.max(1, Math.round(parseFloat(v) * d)) / d; // gap/padding to whole device pixels
  board.style.setProperty("--gx", `${snap(bs.getPropertyValue("--g"))}px`);
  board.style.setProperty("--px", `${snap(bs.getPropertyValue("--p"))}px`);
}

/** Cell size that fits a `cols`×`rows` board into a boxW×boxH area, after the board's own padding and gaps. */
export function fitCell(board, boxW, boxH, cols, rows, min, max) {
  const bs = getComputedStyle(board);
  const gap = parseFloat(bs.getPropertyValue("--g")) || 0;
  const pad = parseFloat(bs.getPropertyValue("--p")) || 0;
  const w = (boxW - 2 * pad - gap * (cols - 1)) / cols;
  const h = (boxH - 2 * pad - gap * (rows - 1)) / rows;
  return snapCell(Math.min(w, h), min, max);
}

/** Shift the board by the sub-pixel remainder of its position so every tile edge sits on a device pixel. */
export function snapBoard(board) {
  const d = window.devicePixelRatio || 1;
  board.style.position = "relative";
  board.style.left = board.style.top = "0px";
  const r = board.getBoundingClientRect();
  board.style.left = `${Math.round(r.left * d) / d - r.left}px`;
  board.style.top = `${Math.round(r.top * d) / d - r.top}px`;
}

// ---------- Sound ----------

/**
 * Audio engine: Web Audio with decoded buffers (zero-latency, overlapping playback).
 * Graph: source → [per-sound gain] → sfx | (track gain → duck gain → music) → master → speakers.
 * Nothing plays until unlock() has been called from a user gesture.
 */
export const Sound = (() => {
  const BELLS = Object.freeze([
    ["siuuu", "Siuuu"], ["fahhh", "Fahhh"], ["arghhh", "Arghhh"], ["ack", "Ack"], ["wow", "Wow anime"], ["saiyan", "Saiyan"],
    ["bong", "Bong"], ["setrum", "Setrum"], ["kucing-nangis", "Kucing nangis"], ["dexter", "Dexter"], ["crow-zero", "Crow Zero"],
    ["day-in-life", "A day in my life"],
  ].map(([id, label]) => Object.freeze({ id, label })));
  const LABELS = new Map(BELLS.map((b) => [b.id, b.label]));
  const KEYS = { sfx: "tts_vol_sfx", music: "tts_vol_music", mute: "tts_mute" };
  const FADE = 0.4; // music crossfade, seconds
  const DUCK = 0.25; // music level while a clue is active
  const DUCK_RAMP = 0.3;
  const LATE_MS = 300; // a lazily loaded sound later than this is skipped

  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { localStorage.setItem(k, v); } catch { /* storage unavailable */ } },
  };
  const level = (k) => {
    const n = Number(store.get(KEYS[k]) ?? 1);
    return Number.isFinite(n) ? Math.min(1, Math.max(0, n)) : 1;
  };

  let ctx = null;
  let sfxGain, musicGain, duckGain, master;
  let unlocked = false;
  const vol = { sfx: level("sfx"), music: level("music") };
  let muted = store.get(KEYS.mute) === "1";
  let duckOn = false;
  let want = null; // music id requested by the page
  let cur = null; // { id, src, gain } track currently playing
  let musicSeq = 0;
  const buffers = new Map();
  const loading = new Map();
  const wanted = new Set(); // preload requests made before unlock()
  const listeners = new Set();

  function ensure() {
    if (ctx) return ctx;
    ctx = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: "interactive" });
    master = ctx.createGain();
    sfxGain = ctx.createGain();
    musicGain = ctx.createGain();
    duckGain = ctx.createGain();
    sfxGain.connect(master);
    duckGain.connect(musicGain);
    musicGain.connect(master);
    master.connect(ctx.destination);
    duckGain.gain.value = duckOn ? DUCK : 1;
    applyLevels(true);
    return ctx;
  }

  // Slider position → amplitude (squared: closer to perceived loudness than linear).
  const amp = (v) => v * v;
  function applyLevels(instant) {
    if (!ctx) return;
    const t = ctx.currentTime;
    for (const [node, v] of [[sfxGain, vol.sfx], [musicGain, vol.music]]) {
      const target = muted ? 0 : amp(v);
      node.gain.cancelScheduledValues(t);
      if (instant) node.gain.value = target;
      else node.gain.setTargetAtTime(target, t, 0.015);
    }
  }

  function load(id) {
    if (buffers.has(id)) return Promise.resolve(buffers.get(id));
    let p = loading.get(id);
    if (!p) {
      p = fetch(`/sounds/${id}.mp3`)
        .then((r) => { if (!r.ok) throw new Error(String(r.status)); return r.arrayBuffer(); })
        .then((ab) => ensure().decodeAudioData(ab))
        .then((buf) => { buffers.set(id, buf); return buf; })
        .catch(() => null)
        .finally(() => loading.delete(id));
      loading.set(id, p);
    }
    return p;
  }

  function notify() { for (const fn of listeners) fn({ sfx: vol.sfx, music: vol.music, muted }); }

  const api = {
    BELLS,
    label: (id) => LABELS.get(id) || id,

    /** Call from a user gesture (click, key, pointerdown). Idempotent; then decodes everything queued by preload(). */
    unlock() {
      ensure();
      if (ctx.state !== "running") ctx.resume().catch(() => {});
      if (!unlocked) {
        unlocked = true;
        api.preload([...wanted]);
        syncMusic();
      }
    },

    /** Fetch + decode ids ahead of time. Before unlock() the request is queued. */
    preload(ids) {
      for (const id of ids) wanted.add(id);
      return unlocked ? Promise.all(ids.map(load)).then(() => undefined) : Promise.resolve();
    },

    /** AudioContext time in seconds (for `when`). */
    time: () => ctx?.currentTime ?? 0,
    /** Decoded length in seconds, or null if not decoded yet. */
    duration: (id) => buffers.get(id)?.duration ?? null,

    /**
     * Plays a decoded sound on the effects bus. `when` is an AudioContext time (see time()), `offset` the start position in
     * seconds. Returns { stop(), ended } where `ended` resolves true after a natural end, false if stopped or skipped.
     * An id that is not decoded yet is loaded and played only if that takes ≤ 300 ms.
     */
    play(id, { when = 0, offset = 0 } = {}) {
      let stopped = false;
      let stopNow = null;
      let done;
      const ended = new Promise((r) => { done = r; });
      const handle = {
        ended,
        stop() {
          if (stopped) return;
          stopped = true;
          if (stopNow) stopNow(); else done(false);
        },
      };
      if (!unlocked) { done(false); return handle; }
      const begin = (buf) => {
        if (stopped || offset >= buf.duration) { done(false); return; }
        const src = ctx.createBufferSource();
        const g = ctx.createGain();
        src.buffer = buf;
        src.connect(g).connect(sfxGain);
        src.onended = () => { src.disconnect(); g.disconnect(); done(!stopped); };
        src.start(Math.max(when, ctx.currentTime), Math.max(0, offset));
        stopNow = () => {
          const t = ctx.currentTime;
          g.gain.cancelScheduledValues(t);
          g.gain.setValueAtTime(g.gain.value, t);
          g.gain.linearRampToValueAtTime(0, t + 0.03);
          try { src.stop(t + 0.04); } catch { /* already ended */ }
        };
      };
      const buf = buffers.get(id);
      if (buf) begin(buf);
      else {
        const asked = performance.now();
        const due = asked + Math.max(0, (when - ctx.currentTime) * 1000);
        load(id).then((b) => {
          if (b && !stopped && performance.now() - due <= LATE_MS) begin(b); else done(false);
        });
      }
      return handle;
    },

    /** Loops a music track (crossfading from the current one); null fades to silence. Starts after unlock(). */
    music(id) {
      want = id ?? null;
      syncMusic();
    },

    /** Duck music to 25% while on, fully restore when off. */
    duck(on) {
      duckOn = !!on;
      if (!ctx) return;
      const g = duckGain.gain;
      const t = ctx.currentTime;
      g.cancelScheduledValues(t);
      g.setValueAtTime(g.value, t);
      g.linearRampToValueAtTime(duckOn ? DUCK : 1, t + DUCK_RAMP);
    },

    volume: (kind) => vol[kind],
    setVolume(kind, v) {
      vol[kind] = Math.min(1, Math.max(0, Number(v) || 0));
      store.set(KEYS[kind], String(vol[kind]));
      applyLevels();
      notify();
    },
    get muted() { return muted; },
    setMuted(on) {
      muted = !!on;
      store.set(KEYS.mute, muted ? "1" : "0");
      applyLevels();
      notify();
    },
    onChange(fn) { listeners.add(fn); return () => listeners.delete(fn); },

    /** Wires [data-sound-mute] buttons and input[data-sound-vol="sfx"|"music"] sliders under `root`. */
    bindControls(root = document) {
      const sync = () => {
        for (const b of root.querySelectorAll("[data-sound-mute]")) {
          b.textContent = muted ? "🔇" : "🔊";
          b.setAttribute("aria-pressed", String(muted));
          b.setAttribute("aria-label", muted ? "Nyalakan suara" : "Bisukan suara");
        }
        for (const r of root.querySelectorAll("[data-sound-vol]")) r.value = String(vol[r.dataset.soundVol]);
      };
      for (const b of root.querySelectorAll("[data-sound-mute]")) {
        b.addEventListener("click", () => { api.unlock(); api.setMuted(!muted); });
      }
      for (const r of root.querySelectorAll("[data-sound-vol]")) {
        r.addEventListener("input", () => { api.unlock(); api.setVolume(r.dataset.soundVol, r.value); });
      }
      api.onChange(sync);
      sync();
    },
  };

  async function syncMusic() {
    if (!unlocked) return;
    const seq = ++musicSeq;
    const id = want;
    if ((cur?.id ?? null) === id) return;
    let buf = null;
    if (id) {
      buf = buffers.get(id) ?? (await load(id));
      if (seq !== musicSeq || !buf) return; // superseded or failed
    }
    const t = ctx.currentTime;
    if (cur) {
      const old = cur;
      old.gain.gain.cancelScheduledValues(t);
      old.gain.gain.setValueAtTime(old.gain.gain.value, t);
      old.gain.gain.linearRampToValueAtTime(0, t + FADE);
      old.src.stop(t + FADE + 0.05);
      old.src.onended = () => { old.src.disconnect(); old.gain.disconnect(); };
      cur = null;
    }
    if (buf) {
      const src = ctx.createBufferSource();
      const gain = ctx.createGain();
      src.buffer = buf;
      src.loop = true;
      gain.gain.setValueAtTime(0, t);
      gain.gain.linearRampToValueAtTime(1, t + FADE);
      src.connect(gain).connect(duckGain);
      src.start(t);
      cur = { id, src, gain };
    }
  }

  return api;
})();

/**
 * Live room socket with auto-reconnect. onMessage(msg) receives every server message except `pong`.
 * hello() must return the hello payload (re-sent on every reconnect).
 *
 * Clock sync (NTP-style): 5 pings 100 ms apart after each connect, then one every 10 s. Per sample
 * rtt = t1 − t0 and offset = server − (t0 + rtt/2); of the last 8 samples the one with the lowest RTT wins.
 * serverNow() is the estimated server unix ms. Client time is performance.timeOrigin + performance.now() (monotonic).
 */
export function liveSocket(code, hello, onMessage, onStatus = () => {}) {
  let ws;
  let closed = false;
  let retry = 0;
  let burst = [];
  let every = null;
  const samples = [];
  let naive = null; // server_now − client time of the first state: only used until the first pong
  const clientNow = () => performance.timeOrigin + performance.now();
  const best = () => samples.reduce((a, s) => (!a || s.rtt < a.rtt ? s : a), null);
  const serverNow = () => clientNow() + (best()?.offset ?? naive ?? 0);
  const sendPing = () => { if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "ping", t0: clientNow() })); };
  const stopPings = () => { burst.forEach(clearTimeout); burst = []; clearInterval(every); every = null; };

  function onPong(msg) {
    const t1 = clientNow();
    const rtt = t1 - msg.t0;
    if (!(rtt >= 0) || !Number.isFinite(msg.server)) return;
    // `server` is truncated to whole ms, so the true instant is ~0.5 ms later on average.
    samples.push({ rtt, offset: msg.server + 0.5 - (msg.t0 + rtt / 2) });
    if (samples.length > 8) samples.shift();
  }

  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws/live/${encodeURIComponent(code)}`);
    ws.onopen = () => {
      retry = 0;
      onStatus("open");
      ws.send(JSON.stringify(hello()));
      for (let i = 0; i < 5; i++) burst.push(setTimeout(sendPing, i * 100));
      every = setInterval(sendPing, 10000);
    };
    ws.onmessage = (e) => {
      let msg;
      try { msg = JSON.parse(e.data); } catch { return; /* ignore malformed */ }
      if (msg.type === "pong") { onPong(msg); return; }
      if (msg.type === "state" && !samples.length) naive = msg.state.server_now - clientNow();
      onMessage(msg);
    };
    ws.onclose = () => {
      stopPings();
      onStatus("closed");
      if (closed) return;
      setTimeout(connect, Math.min(4000, 300 * 2 ** retry++));
    };
  }
  connect();
  return {
    send(msg) { if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg)); },
    close() { closed = true; stopPings(); ws.close(); },
    serverNow,
    /** Current estimate, for diagnostics. */
    clock: () => ({ offset: best()?.offset ?? naive ?? 0, rtt: best()?.rtt ?? null, samples: samples.length }),
  };
}
