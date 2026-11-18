// TV / Projector Display Screen for a live room. Route: /tv.html#<code>.
import { api, esc, avatarImg, Sound, liveSocket, fmtCountdown, setBoardCell, fitCell, snapBoard } from "/common.js";

const $ = (sel, root = document) => root.querySelector(sel);
const app = $("#app");
const code = location.hash.replace(/^#\/?/, "").trim();
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
const DIR = { across: "Mendatar", down: "Menurun" };
const REACT_EMOJI = { wave: "👋", tawa: "😂", api: "🔥", pesta: "🎉", degdegan: "😬" };
const CODE_TILT = [-6, 4, -3, 5, -4, 3];
const TILE_TILT = [-2, 1.5, -1, 2, -1.5, 1];

let sock = null;
let st = null; // latest RoomState
let resizeWatch = null;
let phase = null;
let joinUrl = "";
let selected = null;
const tiles = new Map();
let outcome = null;
let outcomeKey;
const anim = new Map();
const flash = new Map();
let shakeUntil = 0;
let gotState = false;
const heard = new Set();
let finishSeq = 0;
let ticking = null;


// ---------- boot ----------

$("#unlockBtn").addEventListener("click", () => {
  Sound.unlock();
  $("#unlock").classList.add("hidden");
});
Sound.bindControls($("#soundCtl"));

// TV plays all audio: events, meme bells from buzzers, and BGM beds.
Sound.preload([
  "ev-join", "ev-start", "ev-clue", "ev-ticktock", "ev-correct", "ev-wrong", "ev-reveal", "ev-winner", "ev-applause",
  ...Sound.BELLS.map((b) => b.id), "mu-elevator", "mu-carefree", "mu-credits",
]);

boot();

async function boot() {
  if (!/^\d{6}$/.test(code)) return message("Kode room tidak valid", "Buka layar TV dengan kode 6 angka yang benar.", false);
  $("#codeChip").textContent = code;
  try {
    await api(`/api/live/rooms/${code}`);
  } catch (err) {
    return message(err.status === 404 ? "Room tidak ditemukan" : "Gagal memuat room", err.message, false);
  }
  try {
    const info = await api("/api/server-info");
    joinUrl = `${info.urls?.[0] || location.origin}/play.html#/join/${code}`;
  } catch {
    joinUrl = `${location.origin}/play.html#/join/${code}`;
  }
  // Connect as role "tv" (public read-only presentation)
  sock = liveSocket(code, () => ({ type: "hello", role: "tv" }), onMessage, onStatus);
}

function message(title, text) {
  $("#unlock").classList.add("hidden");
  $("#banner").classList.add("hidden");
  phase = "message";
  app.innerHTML = `<div class="panel message">
    <h1>${esc(title)}</h1><p>${esc(text)}</p>
  </div>`;
}

function onStatus(s) {
  if (phase === "message") return;
  $("#banner").classList.toggle("hidden", s === "open");
  if (s === "closed" && !gotState) {
    api(`/api/live/rooms/${code}`).catch((err) => {
      if (err.status === 404) {
        sock?.close();
        message("Room tidak ditemukan", "Room ini sudah ditutup atau server dimulai ulang.");
      }
    });
  }
}

function onMessage(msg) {
  if (phase === "message") return;
  switch (msg.type) {
    case "state": {
      gotState = true;
      const prev = st;
      st = msg.state;
      cues(prev, st);
      render();
      break;
    }
    case "buzz": {
      Sound.play(msg.bell);
      flash.set(msg.player_id, Date.now() + 900);
      const row = app.querySelector(`[data-pid="${CSS.escape(msg.player_id)}"]`);
      if (row) { row.classList.remove("flash"); void row.offsetWidth; row.classList.add("flash"); }
      break;
    }
    case "result": {
      if (msg.correct) {
        const c = st?.quiz.clues.find((x) => x.id === msg.clue_id);
        if (c) cellsOf(c).forEach((k, i) => anim.set(k, { cls: "flip", i, until: Date.now() + 500 + i * 70 }));
      } else {
        shakeUntil = Date.now() + 500;
        const p = player(msg.player_id);
        const name = p?.nickname ?? "Pemain";
        const guessTxt = msg.guess ? `"${msg.guess}"` : "(waktu habis)";
        outcome = { text: `${name} menebak ${guessTxt} · Salah!`, bad: true, until: Date.now() + 4000 };
        const c = st?.quiz.clues.find((x) => x.id === msg.clue_id);
        if (c && msg.guess) {
          cellsOf(c).forEach((k, i) => {
            const ch = msg.guess[i] || "";
            anim.set(k, { cls: "bad", i, letter: ch, until: Date.now() + 1500 });
          });
        }
      }
      break;
    }
    case "react": {
      onReact(msg);
      break;
    }
  }
}

function cues(prev, next) {
  syncTick(next);
  if (!prev) {
    next.players.forEach((p) => heard.add(p.id));
    if (next.phase === "finished") finishSequence(false);
    else Sound.music(next.phase === "lobby" ? "mu-elevator" : "mu-carefree");
    return;
  }
  if (next.players.some((p) => !heard.has(p.id))) Sound.play("ev-join");
  next.players.forEach((p) => heard.add(p.id));
  if (prev.phase !== next.phase) {
    if (next.phase === "playing") {
      Sound.play("ev-start");
      Sound.music("mu-carefree");
      startCountdown();
    } else if (next.phase === "finished") finishSequence(true);
  }
  if (next.active && next.active.clue_id !== prev.active?.clue_id) Sound.play("ev-clue");
  const lo = next.last_outcome;
  if (lo?.kind === "exhausted" && !(prev.last_outcome?.kind === "exhausted" && prev.last_outcome.clue_id === lo.clue_id)) Sound.play("ev-reveal");
  else if (lo?.kind === "solved" && !(prev.last_outcome?.kind === "solved" && prev.last_outcome.clue_id === lo.clue_id)) Sound.play("ev-correct");
  else if (lo?.kind === "skipped" && !(prev.last_outcome?.kind === "skipped" && prev.last_outcome.clue_id === lo.clue_id)) Sound.play("ev-wrong");
}

/** ev-winner, then ev-applause when it ends, then the credits loop. `live` = the game just ended in front of us. */
function finishSequence(live) {
  const seq = ++finishSeq;
  Sound.music(null);
  if (!live) { Sound.music("mu-credits"); return; }
  Sound.play("ev-winner").ended
    .then(() => (seq === finishSeq ? Sound.play("ev-applause").ended : null))
    .then(() => { if (seq === finishSeq) Sound.music("mu-credits"); });
}

/** Tick-tock is scheduled so its last sample lands on the deadline; late starts seek into the buffer. */
function syncTick(next) {
  const a = next.active;
  const key = a && a.state === "answering" && a.deadline ? `${a.clue_id}|${a.answering_player}|${a.deadline}` : null;
  if (key === (ticking?.key ?? null)) return;
  ticking?.handle?.stop();
  ticking = key ? { key, handle: null } : null;
  if (!key) return;
  const run = ticking;
  Sound.preload(["ev-ticktock"]).then(() => {
    const dur = Sound.duration("ev-ticktock");
    if (ticking !== run || dur === null) return;
    const startIn = a.deadline - dur * 1000 - sock.serverNow(); // ms until the buffer must start
    run.handle = startIn >= 0
      ? Sound.play("ev-ticktock", { when: Sound.time() + startIn / 1000 })
      : Sound.play("ev-ticktock", { offset: -startIn / 1000 });
  });
}

function render() {
  $("#title").textContent = st.quiz.title;
  if (phase !== st.phase) {
    phase = st.phase;
    if (phase === "lobby") skeletonLobby();
    else if (phase === "playing") skeletonPlaying();
    else if (phase === "finished") skeletonFinished();
  }
  if (phase === "lobby") renderLobby();
  else if (phase === "playing") renderPlaying();
  else if (phase === "finished") renderFinished();
}

// ----- lobby -----

function skeletonLobby() {
  const codeTiles = code.split("").map((c, i) =>
    `<span class="code-tile" style="--r:${CODE_TILT[i % CODE_TILT.length]}deg">${c}</span>`
  ).join("");

  app.innerHTML = `
    <div class="lobby tv-lobby">
      <section class="panel join-card">
        <h2>Gabung sekarang</h2>
        <div class="code-tiles" aria-label="Kode room: ${code}">${codeTiles}</div>
        <div class="qr-wrap"><img id="qr" class="qr" alt="QR Code untuk bergabung" src="/api/live/rooms/${code}/qr.png"></div>
        <p class="join-url">${esc(joinUrl)}</p>
        <p class="muted">Pindai QR dengan kamera HP atau buka tautan di atas.</p>
      </section>
      <section class="panel players-card">
        <div class="row"><h2>Peserta <span id="pcount" class="chip sun"></span></h2></div>
        <div class="player-grid" id="pgrid"></div>
        <div class="empty hidden" id="pempty">Belum ada peserta yang masuk. Pindai QR di samping!</div>
      </section>
    </div>`;
}

function renderLobby() {
  const ps = st.players;
  $("#pcount").textContent = ps.length;
  const grid = $("#pgrid");
  const ids = new Set(ps.map((p) => p.id));
  for (const [id, el] of tiles) if (!ids.has(id)) { el.remove(); tiles.delete(id); }
  let k = 0;
  ps.forEach((p, idx) => {
    let el = tiles.get(p.id);
    if (!el) { el = tileEl(p, idx, k++); tiles.set(p.id, el); grid.append(el); }
    el.classList.toggle("dim", !p.connected);
    el.querySelector(".pd").classList.toggle("hidden", p.connected);
    const av = JSON.stringify(p.avatar);
    if (el._av !== av) {
      if (el._av !== undefined) hop(el);
      el._av = av;
      el.querySelector(".pav").innerHTML = avatarImg(p.avatar, 96, p.nickname);
    }
    const bell = `🔔 ${Sound.label(p.bell)}`;
    const pb = el.querySelector(".pb");
    if (pb.textContent !== bell) pb.textContent = bell;
  });
  $("#pempty").classList.toggle("hidden", ps.length > 0);
}

function tileEl(p, idx, k) {
  const el = document.createElement("div");
  el.className = "ptile place";
  el.dataset.pid = p.id;
  el.style.cssText = `--r:${TILE_TILT[idx % TILE_TILT.length]}deg;--i:${idx};--k:${k}`;
  el.innerHTML = `<div class="pav"></div><b class="pn"></b><span class="chip sun pb"></span><small class="muted pd">terputus</small><span class="bubble-slot"></span>`;
  el.querySelector(".pn").textContent = p.nickname;
  el.addEventListener("animationend", (e) => { if (e.target === el) el.classList.remove("place"); });
  return el;
}

function hop(el) {
  if (el.classList.contains("hop")) return;
  el.classList.add("hop");
  setTimeout(() => el.classList.remove("hop"), 520);
}

function bubble(el, html, bell = false) {
  const slot = el.querySelector(".bubble-slot");
  clearTimeout(el._bt);
  slot.innerHTML = `<span class="bubble${bell ? " bell" : ""}">${html}</span>`;
  el._bt = setTimeout(() => { slot.innerHTML = ""; }, 1800);
}

function onReact(msg) {
  if (phase !== "lobby" || !st) return;
  const el = tiles.get(msg.player_id);
  const p = player(msg.player_id);
  if (!el || !p) return;
  if (msg.kind === "bell") {
    Sound.play(p.bell);
    el.classList.remove("bell-hit");
    void el.offsetWidth;
    el.classList.add("bell-hit");
    bubble(el, `🔔 ${esc(Sound.label(p.bell))}`, true);
    return;
  }
  const em = REACT_EMOJI[msg.kind];
  if (em) bubble(el, em);
}

let countdownTimer = null;
function startCountdown() {
  clearTimeout(countdownTimer);
  document.getElementById("count321")?.remove();
  const el = document.createElement("div");
  el.id = "count321";
  el.className = "count321";
  el.setAttribute("aria-hidden", "true");
  document.body.append(el);
  const steps = reduceMotion ? [["Mulai!", 800]] : [["3", 600], ["2", 600], ["1", 600], ["Mulai!", 500]];
  let i = 0;
  const next = () => {
    if (i >= steps.length) { el.remove(); return; }
    const [text, ms] = steps[i++];
    el.innerHTML = `<span class="count-tile" data-n="${reduceMotion ? 4 : i}">${text}</span>`;
    countdownTimer = setTimeout(next, ms);
  };
  next();
}

// ----- playing -----

function skeletonPlaying() {
  const q = st.quiz;
  const numbers = new Map(q.cells.map((c) => [`${c.row},${c.col}`, c.number]));
  let tiles = "";
  for (let r = 0; r < q.height; r++) {
    for (let c = 0; c < q.width; c++) {
      const k = `${r},${c}`;
      tiles += numbers.has(k)
        ? `<div class="tile open" data-cell="${k}">${numbers.get(k) ? `<span class="n">${numbers.get(k)}</span>` : ""}<span class="l"></span></div>`
        : `<div class="tile"></div>`;
    }
  }
  app.innerHTML = `
    <div class="stage tv-stage">
      <section class="panel board-center">
        <div class="board-wrap">
          <div class="board" id="board" style="grid-template-columns:repeat(${q.width}, var(--cell))">${tiles}</div>
        </div>
      </section>
      <aside class="stage-side tv-side">
        <section class="panel active-panel" id="active"></section>
        <div id="outcome"></div>
        <section class="panel scores-panel">
          <h2>Skor</h2>
          <ol class="scores" id="scores"></ol>
        </section>
      </aside>
    </div>`;
  sizeBoard();
  requestAnimationFrame(sizeBoard);
  document.fonts?.ready?.then(sizeBoard);
  resizeWatch?.disconnect();
  resizeWatch = new ResizeObserver(() => snapBoard($("#board")));
  for (const sel of ["#board", ".board-center", ".tv-side", ".tv-stage"]) {
    const el = $(sel);
    if (el) resizeWatch.observe(el);
  }
}

function sizeBoard() {
  const board = $("#board");
  if (!board || !st) return;
  const { width, height } = st.quiz;
  const panel = board.closest(".panel");
  if (!panel) return;
  const ps = getComputedStyle(panel);
  const boxW = panel.clientWidth - parseFloat(ps.paddingLeft) - parseFloat(ps.paddingRight);
  const rect = panel.getBoundingClientRect();
  const innerH = panel.clientHeight - parseFloat(ps.paddingTop) - parseFloat(ps.paddingBottom);
  const boxH = Math.max(300, Math.max(innerH, window.innerHeight - rect.top - parseFloat(ps.paddingBottom) - 24));
  setBoardCell(board, fitCell(board, boxW, boxH, width, height, 24, 110));
  snapBoard(board);
}
window.addEventListener("resize", sizeBoard);

function renderPlaying() {
  const q = st.quiz;
  const active = st.active;
  const activeClue = active && q.clues.find((c) => c.id === active.clue_id);
  if (activeClue) selected = activeClue.id;

  // Outcome banner
  const lo = st.last_outcome;
  const key = st.active ? "active" : lo ? `${lo.clue_id}|${lo.kind}|${lo.player_id}` : null;
  if (key !== outcomeKey) {
    if (lo && !st.active && outcomeKey !== undefined) outcome = { ...outcomeText(lo), until: Date.now() + 5000 };
    outcomeKey = key;
  }
  $("#outcome").innerHTML = outcome ? `<div class="outcome${outcome.bad ? " bad" : ""}">${esc(outcome.text)}</div>` : "";

  // Board
  const solvedCells = new Set();
  const revealedCells = new Set();
  for (const c of q.clues) {
    if (c.status === "solved") cellsOf(c).forEach((k) => solvedCells.add(k));
    else if (c.status === "revealed") cellsOf(c).forEach((k) => revealedCells.add(k));
  }
  const focusCells = new Set(activeClue ? cellsOf(activeClue) : []);
  const t = Date.now();
  for (const el of $("#board").querySelectorAll("[data-cell]")) {
    const k = el.dataset.cell;
    const a = anim.get(k);
    if (a && a.until < t) anim.delete(k);
    const live = a && a.until >= t ? a : null;
    let cls = "tile open";
    if (solvedCells.has(k)) cls += " solved";
    else if (revealedCells.has(k)) cls += " revealed";
    else if (focusCells.has(k)) cls += " focus";
    if (live) cls += ` ${live.cls}`;
    if (el.className !== cls) el.className = cls;
    if (live) el.style.setProperty("--i", live.i);
    const letter = (live && live.letter !== undefined) ? live.letter : (st.letters[k] || "");
    const l = el.querySelector(".l");
    if (l.textContent !== letter) l.textContent = letter;
  }

  // Active clue panel (TV View: purely informational, no admin buttons)
  const ap = $("#active");
  ap.classList.toggle("shake", shakeUntil > t);
  if (activeClue) {
    let stateHtml;
    if (active.state === "answering" && active.answering_player) {
      const p = player(active.answering_player);
      stateHtml = `${avatarImg(p?.avatar, 64, p?.nickname)}<span><b>${esc(p?.nickname ?? "?")}</b> sedang menjawab…</span>${active.deadline ? countdown(active.deadline) : ""}`;
    } else {
      stateHtml = `<span class="pulse">🔔 Bel terbuka! Siapa cepat dia dapat!</span>`;
    }
    const attemptsMap = new Map((active.attempts || []).map((a) => [a.player_id, a.guess]));
    const locked = active.locked_out.length
      ? `<div class="locked"><span class="muted">Sudah mencoba:</span>${active.locked_out.map((id) => {
          const p = player(id);
          const g = attemptsMap.get(id);
          const gHtml = g !== undefined
            ? (g ? `<span class="chip wrong-guess"><s>${esc(g)}</s></span>` : `<span class="chip wrong-guess muted">waktu habis</span>`)
            : "";
          return `<span class="row attempt-chip" style="gap:.3rem">${avatarImg(p?.avatar, 28, p?.nickname)}<span>${esc(p?.nickname ?? "?")}</span>${gHtml}</span>`;
        }).join("")}</div>`
      : "";
    ap.innerHTML = `
      <p class="eyebrow">Soal Aktif · ${activeClue.number} ${DIR[activeClue.direction]} · ${activeClue.length} huruf</p>
      <div class="clue-big clue-text">${esc(activeClue.clue)}</div>
      <div class="astate">${stateHtml}</div>
      ${locked}`;
  } else {
    ap.innerHTML = `
      <p class="eyebrow">TTS Kuis</p>
      <div class="clue-big">Menunggu Host memilih soal…</div>`;
  }

  // Scoreboard
  const elScores = $("#scores");
  if (elScores) {
    const turn = active?.answering_player;
    elScores.innerHTML = ranked().map((p, i) => {
      const fl = (flash.get(p.id) || 0) > t;
      return `<li data-pid="${esc(p.id)}" class="${p.connected ? "" : "dim"}${p.id === turn ? " turn" : ""}${fl ? " flash" : ""}">
        <span class="rk">${i + 1}</span>${avatarImg(p.avatar, 40, p.nickname)}<span class="nm">${esc(p.nickname)}</span><span class="sc">${p.score}</span>
      </li>`;
    }).join("");
  }
}

function outcomeText(lo) {
  if (lo.kind === "solved") return { text: `Benar! +${st.settings.points_correct} untuk ${pname(lo.player_id)}`, bad: false };
  if (lo.kind === "exhausted") return { text: `Tidak ada yang benar. Jawabannya ${lo.answer ?? ""}`, bad: true };
  const c = st.quiz.clues.find((x) => x.id === lo.clue_id);
  return { text: c ? `Soal ${c.number} ${DIR[c.direction]} dilewati` : "Soal dilewati", bad: true };
}

// ----- finished -----

function skeletonFinished() {
  app.innerHTML = `<div class="finish tv-finish"><div id="finishBody"></div></div>`;
  if (!reduceMotion) confetti();
}

function renderFinished() {
  const pod = st.podium || [];
  const byId = new Map(st.players.map((p) => [p.id, p]));
  const step = (e, cls) => e ? `<div class="pod ${cls}">
      ${avatarImg(e.avatar, cls === "r1" ? 140 : 104, e.nickname)}
      <div class="nm">${esc(e.nickname)}</div><div class="sc">${e.score} poin</div>
      <div class="block">${e.rank}</div></div>` : `<div class="pod ${cls}"></div>`;
  const first = pod.filter((e) => e.rank === 1);
  const top = pod.slice(0, 3);
  $("#finishBody").innerHTML = `
    <h1 class="center">${first.length ? `🏆 ${esc(first.map((e) => e.nickname).join(" & "))} menang!` : "Permainan selesai"}</h1>
    <div class="podium">${step(top[1], "r2")}${step(top[0], "r1")}${step(top[2], "r3")}</div>
    <section class="panel">
      <h2>Peringkat lengkap</h2>
      <table class="list">
        <thead><tr><th>#</th><th>Pemain</th><th class="num">Benar</th><th class="num">Salah</th><th class="num">Skor</th></tr></thead>
        <tbody>${pod.map((e) => {
          const p = byId.get(e.player_id);
          return `<tr><td>${e.rank}</td><td><span class="row">${avatarImg(e.avatar, 36, e.nickname)}${esc(e.nickname)}</span></td>
            <td class="num">${p?.correct ?? "–"}</td><td class="num">${p?.wrong ?? "–"}</td><td class="num"><b>${e.score}</b></td></tr>`;
        }).join("")}</tbody>
      </table>
    </section>`;
}

function confetti() {
  const canvas = document.createElement("canvas");
  canvas.id = "confetti";
  document.body.append(canvas);
  const ctx = canvas.getContext("2d");
  const colors = ["#FFD23F", "#FF5A36", "#2EC4A6", "#1C2541", "#FFFFFF"];
  const resize = () => { canvas.width = innerWidth; canvas.height = innerHeight; };
  resize();
  window.addEventListener("resize", resize);
  const bits = Array.from({ length: 180 }, () => ({
    x: Math.random() * canvas.width, y: -Math.random() * canvas.height,
    w: 6 + Math.random() * 8, h: 8 + Math.random() * 10,
    vy: 2 + Math.random() * 3, vx: -1.5 + Math.random() * 3,
    a: Math.random() * Math.PI, va: -0.15 + Math.random() * 0.3,
    c: colors[Math.floor(Math.random() * colors.length)],
  }));
  const endAt = performance.now() + 7000;
  function confettiFrame(t) {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    for (const b of bits) {
      b.x += b.vx; b.y += b.vy; b.a += b.va;
      if (b.y > canvas.height + 20 && t < endAt) { b.y = -20; b.x = Math.random() * canvas.width; }
      ctx.save();
      ctx.translate(b.x, b.y);
      ctx.rotate(b.a);
      ctx.fillStyle = b.c;
      ctx.fillRect(-b.w / 2, -b.h / 2, b.w, b.h);
      ctx.restore();
    }
    if (t < endAt || bits.some((b) => b.y < canvas.height + 20)) requestAnimationFrame(confettiFrame);
    else { canvas.remove(); window.removeEventListener("resize", resize); }
  }
  requestAnimationFrame(confettiFrame);
}

// Countdowns and outcome banner are driven by animation frames
function frame() {
  if (sock && st) {
    const t = sock.serverNow();
    for (const el of app.querySelectorAll("[data-deadline]")) {
      const { text, low } = fmtCountdown(Number(el.dataset.deadline) - t);
      if (el.textContent !== text) el.textContent = text;
      el.classList.toggle("low", low);
    }
  }
  if (outcome && Date.now() > outcome.until) {
    outcome = null;
    const slot = $("#outcome");
    if (slot) slot.innerHTML = "";
  }
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);

// ----- helpers -----

function player(id) { return st?.players.find((p) => p.id === id); }
function pname(id) { return player(id)?.nickname ?? "?"; }
function cellsOf(c) {
  const out = [];
  for (let i = 0; i < c.length; i++) {
    const r = c.row + (c.direction === "down" ? i : 0);
    const col = c.col + (c.direction === "across" ? i : 0);
    out.push(`${r},${col}`);
  }
  return out;
}
function countdown(deadline) {
  const { text, low } = fmtCountdown(deadline - sock.serverNow());
  return `<span class="cd${low ? " low" : ""}" data-deadline="${deadline}">${text}</span>`;
}
function ranked() {
  return [...(st.players || [])].sort((a, b) => b.score - a.score || a.nickname.localeCompare(b.nickname));
}
