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

/**
 * Bell sounds. Plays /sounds/<id>.mp3 if present (user-supplied), else a synthesized fallback.
 * Call Bells.unlock() from a user gesture once so browsers allow audio.
 */
export const Bells = (() => {
  let ctx;
  const files = {};
  const NAMES = {
    ding: "Ding", klakson: "Klakson", terompet: "Terompet", bebek: "Bebek", gong: "Gong", laser: "Laser",
    kucing: "Kucing", drum: "Drum", peluit: "Peluit", "bel-sepeda": "Bel sepeda", pop: "Pop", koin: "Koin",
  };
  // [waveform, start Hz, end Hz, duration s, repeats]
  const SYNTH = {
    ding: ["sine", 1320, 1320, 0.5, 1], klakson: ["sawtooth", 330, 300, 0.45, 2], terompet: ["square", 523, 784, 0.5, 1],
    bebek: ["sawtooth", 600, 380, 0.18, 2], gong: ["sine", 196, 180, 1.0, 1], laser: ["square", 1800, 200, 0.35, 1],
    kucing: ["triangle", 700, 1100, 0.4, 1], drum: ["triangle", 160, 50, 0.25, 3], peluit: ["sine", 2400, 2600, 0.5, 1],
    "bel-sepeda": ["sine", 2100, 2100, 0.12, 3], pop: ["sine", 900, 150, 0.12, 1], koin: ["square", 988, 1319, 0.3, 1],
  };
  function audio() {
    ctx ??= new (window.AudioContext || window.webkitAudioContext)();
    return ctx;
  }
  function synth(id) {
    const [type, f0, f1, dur, reps] = SYNTH[id] || SYNTH.ding;
    const ac = audio();
    for (let i = 0; i < reps; i++) {
      const t = ac.currentTime + i * (dur * 0.8);
      const osc = ac.createOscillator();
      const gain = ac.createGain();
      osc.type = type;
      osc.frequency.setValueAtTime(f0, t);
      osc.frequency.exponentialRampToValueAtTime(Math.max(f1, 1), t + dur);
      gain.gain.setValueAtTime(0.0001, t);
      gain.gain.exponentialRampToValueAtTime(0.35, t + 0.01);
      gain.gain.exponentialRampToValueAtTime(0.0001, t + dur);
      osc.connect(gain).connect(ac.destination);
      osc.start(t);
      osc.stop(t + dur + 0.05);
    }
  }
  return {
    NAMES,
    label: (id) => NAMES[id] || id,
    unlock() { audio().resume(); },
    async play(id) {
      if (files[id] === undefined) {
        const el = new Audio(`/sounds/${id}.mp3`);
        files[id] = await new Promise((ok) => {
          el.addEventListener("canplaythrough", () => ok(el), { once: true });
          el.addEventListener("error", () => ok(null), { once: true });
          el.load();
        });
      }
      if (files[id]) {
        const el = files[id].cloneNode();
        el.play().catch(() => synth(id));
      } else synth(id);
    },
  };
})();

/**
 * Live room socket with auto-reconnect. onMessage(msg) receives every server message.
 * hello() must return the hello payload (re-sent on every reconnect).
 */
export function liveSocket(code, hello, onMessage, onStatus = () => {}) {
  let ws;
  let closed = false;
  let retry = 0;
  function connect() {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/ws/live/${encodeURIComponent(code)}`);
    ws.onopen = () => { retry = 0; onStatus("open"); ws.send(JSON.stringify(hello())); };
    ws.onmessage = (e) => { try { onMessage(JSON.parse(e.data)); } catch { /* ignore malformed */ } };
    ws.onclose = () => {
      onStatus("closed");
      if (closed) return;
      setTimeout(connect, Math.min(4000, 300 * 2 ** retry++));
    };
  }
  connect();
  return {
    send(msg) { if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg)); },
    close() { closed = true; ws.close(); },
  };
}
