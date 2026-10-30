// Main screen (projected) for a live room. Route: /host.html#<code>. Protocol: docs/spec.md.
import { api, esc, toast, avatarImg, Sound, liveSocket, fmtCountdown, setBoardCell, fitCell, snapBoard } from "/common.js";

const $ = (sel, root = document) => root.querySelector(sel);
const app = $("#app");
const code = location.hash.replace(/^#\/?/, "").trim();
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
const DIR = { across: "Mendatar", down: "Menurun" };

let sock = null;
let st = null; // latest RoomState
let resizeWatch = null; // ResizeObserver: re-snaps the board whenever content above it changes height
let phase = null; // phase currently rendered (skeleton)
let joinUrl = "";
let selected = null; // clue id last picked by the operator
let lastCell = null; // { key, dir } for across/down toggling on grid clicks
const known = new Set(); // player ids already shown in the lobby
let outcome = null; // { text, bad, until }
let outcomeKey; // undefined until the first snapshot so stale outcomes are not replayed on load
const anim = new Map(); // cell key -> { cls, i, until }
const flash = new Map(); // player id -> until
let shakeUntil = 0;
let gotState = false;

// ---------- boot ----------

$("#unlockBtn").addEventListener("click", () => {
  Sound.unlock();
  $("#unlock").classList.add("hidden");
});
Sound.bindControls($("#soundCtl"));
// Host plays everything: events, every bell (any player can buzz) and the music beds.
Sound.preload([
  "ev-join", "ev-start", "ev-clue", "ev-ticktock", "ev-correct", "ev-wrong", "ev-reveal", "ev-winner", "ev-applause",
  ...Sound.BELLS.map((b) => b.id), "mu-elevator", "mu-carefree", "mu-credits",
]);

boot();

async function boot() {
  if (!/^\d{6}$/.test(code)) return message("Kode room tidak valid", "Buka layar utama dari halaman admin (tombol “Mulai live”).", true);
  $("#codeChip").textContent = code;
  let me;
  try {
    me = (await api("/api/me")).user;
  } catch {
    me = null;
  }
  if (!me || me.role !== "admin") return notAdmin();
  try {
    await api(`/api/live/rooms/${code}`);
  } catch (err) {
    return message(err.status === 404 ? "Room tidak ditemukan" : "Gagal memuat room", err.message, true);
  }
  try {
    const info = await api("/api/server-info");
    joinUrl = `${info.urls?.[0] || location.origin}/play.html#/join/${code}`;
  } catch {
    joinUrl = `${location.origin}/play.html#/join/${code}`;
  }
  sock = liveSocket(code, () => ({ type: "hello", role: "host" }), onMessage, onStatus);
}

function notAdmin() {
  sock?.close();
  message("Khusus admin", "Layar utama hanya bisa dibuka oleh admin. Masuk dulu dengan akun admin, lalu buka lagi room ini dari halaman admin.", false);
}

function message(title, text, adminLink) {
  $("#unlock").classList.add("hidden");
  $("#banner").classList.add("hidden");
  phase = "message";
  app.innerHTML = `<div class="panel message">
    <h1>${esc(title)}</h1><p>${esc(text)}</p>
    <div class="row">${adminLink ? `<a class="btn go" href="/#/admin">Ke halaman admin</a>` : `<a class="btn go" href="/">Masuk</a>`}</div>
  </div>`;
}

// When the socket fails before any state arrives, find out why (session expired / not admin / room gone).
async function diagnose() {
  try {
    const me = (await api("/api/me")).user;
    if (!me || me.role !== "admin") return notAdmin();
  } catch { /* offline: keep reconnecting */ return; }
  try {
    await api(`/api/live/rooms/${code}`);
  } catch (err) {
    if (err.status === 404) {
      sock?.close();
      message("Room tidak ditemukan", "Room ini sudah ditutup atau server dimulai ulang.", true);
    }
  }
}

function onStatus(s) {
  if (phase === "message") return;
  $("#banner").classList.toggle("hidden", s === "open");
  if (s === "closed" && !gotState) diagnose();
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
    case "result":
      onResult(msg);
      break;
    case "error":
      toast(msg.message);
      if (!gotState) diagnose();
      break;
  }
}

function onResult(msg) {
  const clue = st?.quiz.clues.find((c) => c.id === msg.clue_id);
  if (!clue) return;
  ticking?.handle?.stop(); // the result ends the answer window; do not wait for the next snapshot
  Sound.play(msg.correct ? "ev-correct" : "ev-wrong");
  const until = Date.now() + 1200;
  if (msg.correct) {
    cellsOf(clue).forEach((k, i) => anim.set(k, { cls: "win", i, until }));
  } else {
    cellsOf(clue).forEach((k) => anim.set(k, { cls: "bad", i: 0, until }));
    shakeUntil = until;
  }
  if (st) render();
  setTimeout(() => st && render(), 1250);
}

// ---------- sound cues ----------
// Driven by diffs between consecutive snapshots, so a page load or reconnect never replays old events.

const heard = new Set(); // player ids whose join was already announced
let ticking = null; // { key, handle } for the running ev-ticktock
let finishSeq = 0; // invalidates a pending winner → applause → credits chain

function cues(prev, next) {
  Sound.duck(!!next.active);
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
    } else if (next.phase === "finished") finishSequence(true);
  }
  if (next.active && next.active.clue_id !== prev.active?.clue_id) Sound.play("ev-clue");
  const lo = next.last_outcome;
  if (lo?.kind === "exhausted" && !(prev.last_outcome?.kind === "exhausted" && prev.last_outcome.clue_id === lo.clue_id)) Sound.play("ev-reveal");
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

// ---------- helpers ----------

const player = (id) => st.players.find((p) => p.id === id);
const pname = (id) => player(id)?.nickname ?? "?";
const pickable = (c) => c.status === "open" || c.status === "skipped";

function cellsOf(c) {
  return Array.from({ length: c.length }, (_, i) =>
    c.direction === "across" ? `${c.row},${c.col + i}` : `${c.row + i},${c.col}`);
}

function countdown(deadline) {
  const { text, low } = fmtCountdown(deadline - sock.serverNow());
  return `<span class="cd${low ? " low" : ""}" data-deadline="${deadline}">${text}</span>`;
}

// Countdowns and the outcome banner are driven by animation frames, not a coarse interval.
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

function ranked() {
  return [...st.players].sort((a, b) => b.score - a.score || a.nickname.localeCompare(b.nickname));
}

// ---------- render ----------

function render() {
  document.title = `${st.quiz.title} · Layar Utama`;
  $("#title").textContent = st.quiz.title;
  if (st.phase !== phase) {
    phase = st.phase;
    if (phase === "lobby") skeletonLobby();
    else if (phase === "playing") skeletonPlaying();
    else skeletonFinished();
  }
  if (phase === "lobby") renderLobby();
  else if (phase === "playing") renderPlaying();
  else renderFinished();
}

// ----- lobby -----

function skeletonLobby() {
  app.innerHTML = `
    <div class="lobby">
      <section class="panel join-card">
        <p class="eyebrow">Kode room</p>
        <div class="room-code">${esc(code)}</div>
        <img class="qr" src="/api/live/rooms/${encodeURIComponent(code)}/qr.png" alt="QR untuk bergabung" width="260" height="260">
        <p class="muted">Pindai QR atau buka:</p>
        <p class="join-url">${esc(joinUrl)}</p>
      </section>
      <div class="lobby-main">
        <section class="panel">
          <div class="row"><h2>Pemain <span id="pcount" class="chip"></span></h2></div>
          <div id="pgrid"></div>
        </section>
        <section class="panel">
          <h2>Aturan room</h2>
          <div class="set-grid">
            <label>Poin benar<input class="field" type="number" id="s_points" min="1" max="100" data-k="points_correct"></label>
            <label>Penalti salah<input class="field" type="number" id="s_penalty" min="0" max="100" data-k="penalty_wrong"></label>
            <label>Waktu jawab (detik)<input class="field" type="number" id="s_seconds" min="5" max="60" data-k="answer_seconds"></label>
          </div>
          <div class="row">
            <button type="button" class="btn go big" id="startBtn">Mulai permainan</button>
            <span class="muted" id="startHint"></span>
          </div>
        </section>
      </div>
    </div>`;
  for (const inp of app.querySelectorAll(".set-grid input")) inp.addEventListener("change", sendSettings);
  $("#startBtn").addEventListener("click", () => sock.send({ type: "start" }));
}

function sendSettings() {
  const val = (id) => {
    const inp = $(id);
    const n = Math.round(Number(inp.value));
    const v = Math.min(Number(inp.max), Math.max(Number(inp.min), Number.isFinite(n) ? n : Number(inp.min)));
    inp.value = v;
    return v;
  };
  sock.send({ type: "settings", points_correct: val("#s_points"), penalty_wrong: val("#s_penalty"), answer_seconds: val("#s_seconds") });
}

function renderLobby() {
  const ps = st.players;
  $("#pcount").textContent = ps.length;
  $("#pgrid").innerHTML = ps.length
    ? `<div class="player-grid">${ps.map((p) => `
        <div class="ptile${p.connected ? "" : " dim"}${known.has(p.id) ? "" : " pop-in"}" data-pid="${esc(p.id)}">
          <button type="button" class="kick" data-kick="${esc(p.id)}" aria-label="Keluarkan ${esc(p.nickname)}" title="Keluarkan">✕</button>
          ${avatarImg(p.avatar, 84, p.nickname)}
          <b>${esc(p.nickname)}</b>
          <span class="chip sun">🔔 ${esc(Sound.label(p.bell))}</span>
          ${p.connected ? "" : `<small class="muted">terputus</small>`}
        </div>`).join("")}</div>`
    : `<div class="empty">Belum ada pemain. Ajak peserta memindai QR di samping.</div>`;
  ps.forEach((p) => known.add(p.id));
  const fields = { "#s_points": st.settings.points_correct, "#s_penalty": st.settings.penalty_wrong, "#s_seconds": st.settings.answer_seconds };
  for (const [id, v] of Object.entries(fields)) {
    const inp = $(id);
    if (document.activeElement !== inp) inp.value = v;
  }
  $("#startBtn").disabled = ps.length === 0;
  $("#startHint").textContent = ps.length === 0 ? "Butuh minimal 1 pemain." : `${ps.length} pemain siap.`;
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
    <div class="stage">
      <div class="stage-main">
        <section class="panel active-panel" id="active"></section>
        <div id="outcome"></div>
        <section class="panel board-center"><div class="board-wrap"><div class="board" id="board" style="grid-template-columns:repeat(${q.width}, var(--cell))">${tiles}</div></div></section>
      </div>
      <aside class="stage-side">
        <section class="panel"><h2>Skor</h2><ol class="scores" id="scores"></ol></section>
        <section class="panel clues" id="clues"></section>
      </aside>
    </div>`;
  sizeBoard();
  resizeWatch?.disconnect();
  resizeWatch = new ResizeObserver(() => snapBoard($("#board")));
  for (const sel of ["#active", "#outcome", ".stage-main"]) resizeWatch.observe($(sel));
}

/** Integer cell size so every tile edge falls on a whole pixel; then align the board origin to the device-pixel grid. */
function sizeBoard() {
  const board = $("#board");
  if (!board || !st) return;
  const { width, height } = st.quiz;
  const panel = board.closest(".panel");
  const ps = getComputedStyle(panel);
  const boxW = panel.clientWidth - parseFloat(ps.paddingLeft) - parseFloat(ps.paddingRight);
  setBoardCell(board, fitCell(board, boxW, window.innerHeight * 0.62, width, height, 24, 76));
  snapBoard(board);
}
window.addEventListener("resize", sizeBoard);

function renderPlaying() {
  const q = st.quiz;
  const active = st.active;
  const activeClue = active && q.clues.find((c) => c.id === active.clue_id);
  if (activeClue) selected = activeClue.id;
  const selClue = q.clues.find((c) => c.id === selected);

  // outcome banner (only when it changes after the first snapshot)
  const lo = st.last_outcome;
  // "active" while a clue runs, so a repeated outcome for the same clue (skip twice) still shows.
  const key = st.active ? "active" : lo ? `${lo.clue_id}|${lo.kind}|${lo.player_id}` : null;
  if (key !== outcomeKey) {
    if (lo && !st.active && outcomeKey !== undefined) outcome = { ...outcomeText(lo), until: Date.now() + 5000 };
    outcomeKey = key;
  }
  $("#outcome").innerHTML = outcome ? `<div class="outcome${outcome.bad ? " bad" : ""}">${esc(outcome.text)}</div>` : "";

  // board
  const solvedCells = new Set();
  const revealedCells = new Set();
  for (const c of q.clues) {
    if (c.status === "solved") cellsOf(c).forEach((k) => solvedCells.add(k));
    else if (c.status === "revealed") cellsOf(c).forEach((k) => revealedCells.add(k));
  }
  const focusCells = new Set(selClue && pickable(selClue) ? cellsOf(selClue) : []);
  const canPick = !active;
  const t = Date.now();
  for (const el of $("#board").querySelectorAll("[data-cell]")) {
    const k = el.dataset.cell;
    const a = anim.get(k);
    if (a && a.until < t) anim.delete(k);
    const live = a && a.until >= t ? a : null;
    let cls = "tile open";
    if (solvedCells.has(k)) cls += " solved";
    else if (revealedCells.has(k)) cls += " revealed";
    else if (focusCells.has(k)) cls += activeClue ? " focus" : " word";
    if (canPick && q.clues.some((c) => pickable(c) && cellsOf(c).includes(k))) cls += " pick";
    if (live) cls += ` ${live.cls}`;
    if (el.className !== cls) el.className = cls;
    if (live) el.style.setProperty("--i", live.i);
    const letter = st.letters[k] || "";
    const l = el.querySelector(".l");
    if (l.textContent !== letter) l.textContent = letter;
  }

  // active clue panel
  const ap = $("#active");
  ap.classList.toggle("shake", shakeUntil > t);
  let body;
  if (activeClue) {
    let stateHtml;
    if (active.state === "answering" && active.answering_player) {
      const p = player(active.answering_player);
      stateHtml = `${avatarImg(p?.avatar, 56, p?.nickname)}<span>${esc(p?.nickname ?? "?")} menjawab…</span>${active.deadline ? countdown(active.deadline) : ""}`;
    } else {
      stateHtml = `<span class="pulse">🔔 Menunggu bel…</span>`;
    }
    const locked = active.locked_out.length
      ? `<div class="locked"><span class="muted">Sudah dapat giliran:</span>${active.locked_out.map((id) => {
          const p = player(id);
          return `<span class="row" style="gap:.3rem">${avatarImg(p?.avatar, 28, p?.nickname)}${esc(p?.nickname ?? "?")}</span>`;
        }).join("")}</div>`
      : "";
    body = `
      <p class="eyebrow">${activeClue.number} ${DIR[activeClue.direction]} · ${activeClue.length} huruf</p>
      <div class="clue-big">${esc(activeClue.clue)}</div>
      <div class="astate">${stateHtml}</div>
      ${locked}`;
  } else {
    body = `
      <p class="eyebrow">Belum ada soal aktif</p>
      <div class="clue-big">Pilih soal dari daftar atau klik kotak di papan.</div>
      ${selClue && pickable(selClue) ? `<p class="muted">Terpilih: ${selClue.number} ${DIR[selClue.direction]}</p>` : ""}`;
  }
  const revealTarget = activeClue || (selClue && pickable(selClue) ? selClue : null);
  ap.innerHTML = `${body}
    <div class="row controls">
      <button type="button" class="btn sun" data-act="skip" ${activeClue ? "" : "disabled"}>Lewati</button>
      <button type="button" class="btn mint" data-act="reveal" ${revealTarget ? "" : "disabled"}>Buka jawaban</button>
      <span class="spacer"></span>
      <button type="button" class="btn danger" data-act="end">Akhiri</button>
    </div>`;

  // scoreboard
  const turn = active?.answering_player;
  $("#scores").innerHTML = ranked().map((p, i) => {
    const fl = (flash.get(p.id) || 0) > t;
    return `<li data-pid="${esc(p.id)}" class="${p.connected ? "" : "dim"}${p.id === turn ? " turn" : ""}${fl ? " flash" : ""}">
      <span class="rk">${i + 1}</span>${avatarImg(p.avatar, 40, p.nickname)}<span class="nm">${esc(p.nickname)}</span><span class="sc">${p.score}</span>
    </li>`;
  }).join("");

  // clue lists
  const list = (dir) => q.clues.filter((c) => c.direction === dir).sort((a, b) => a.number - b.number).map((c) => {
    let extra = "";
    if (c.status === "solved") {
      const p = player(c.solved_by);
      extra = `${p ? avatarImg(p.avatar, 26, p.nickname) : ""}<span class="chip mint">${esc(c.answer ?? "")}</span>`;
    } else if (c.status === "revealed") extra = `<span class="chip">dibuka${c.answer ? `: ${esc(c.answer)}` : ""}</span>`;
    else if (c.status === "skipped") extra = `<span class="chip sun">dilewati</span>`;
    const cls = [c.status, c.id === active?.clue_id ? "act" : c.id === selected && pickable(c) ? "sel" : "", canPick && pickable(c) ? "pick" : ""].join(" ");
    return `<li class="${cls}" data-clue="${esc(c.id)}"><span class="n">${c.number}</span><span class="ct">${esc(c.clue)} <span class="muted">(${c.length})</span></span>${extra}</li>`;
  }).join("");
  $("#clues").innerHTML = `<h2>Mendatar</h2><ul class="clue-list">${list("across")}</ul><h2>Menurun</h2><ul class="clue-list">${list("down")}</ul>`;
}

function outcomeText(lo) {
  if (lo.kind === "solved") return { text: `Benar! +${st.settings.points_correct} untuk ${pname(lo.player_id)}`, bad: false };
  if (lo.kind === "exhausted") return { text: `Tidak ada yang benar. Jawabannya ${lo.answer ?? ""}`, bad: true };
  const c = st.quiz.clues.find((x) => x.id === lo.clue_id);
  return { text: c ? `Soal ${c.number} ${DIR[c.direction]} dilewati` : "Soal dilewati", bad: true };
}

function openClue(id) {
  selected = id;
  if (st.active) { renderPlaying(); return; }
  sock.send({ type: "open_clue", clue_id: id });
}

app.addEventListener("click", (e) => {
  if (!st) return;
  const kick = e.target.closest("[data-kick]");
  if (kick && phase === "lobby") {
    const id = kick.dataset.kick;
    if (confirm(`Keluarkan ${pname(id)} dari room?`)) sock.send({ type: "kick", player_id: id });
    return;
  }
  if (phase !== "playing") return;
  const act = e.target.closest("[data-act]");
  if (act) {
    if (act.dataset.act === "skip") sock.send({ type: "skip" });
    else if (act.dataset.act === "reveal") {
      const id = st.active?.clue_id || selected;
      if (id) sock.send({ type: "reveal", clue_id: id });
    } else if (act.dataset.act === "end") {
      if (confirm("Akhiri permainan sekarang? Semua jawaban yang belum terjawab akan dibuka.")) sock.send({ type: "end" });
    }
    return;
  }
  const li = e.target.closest("[data-clue]");
  if (li) {
    const c = st.quiz.clues.find((x) => x.id === li.dataset.clue);
    if (c && pickable(c)) openClue(c.id);
    return;
  }
  const cell = e.target.closest("[data-cell]");
  if (cell && !st.active) {
    const k = cell.dataset.cell;
    const opts = st.quiz.clues.filter((c) => pickable(c) && cellsOf(c).includes(k));
    if (!opts.length) return;
    const across = opts.find((c) => c.direction === "across");
    const down = opts.find((c) => c.direction === "down");
    let dir = across ? "across" : "down";
    if (across && down && lastCell?.key === k) dir = lastCell.dir === "across" ? "down" : "across";
    lastCell = { key: k, dir };
    openClue((dir === "across" ? across : down).id);
  }
});

// ----- finished -----

function skeletonFinished() {
  app.innerHTML = `<div class="finish"><div id="finishBody"></div>
    <div class="row" style="justify-content:center;margin-top:1.4rem"><a class="btn go big" href="/#/admin">Kembali ke admin</a></div></div>`;
  if (!reduceMotion) confetti();
}

function renderFinished() {
  const pod = st.podium || [];
  const byId = new Map(st.players.map((p) => [p.id, p]));
  const step = (e, cls) => e ? `<div class="pod ${cls}">
      ${avatarImg(e.avatar, cls === "r1" ? 120 : 92, e.nickname)}
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
  function frame(t) {
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
    if (t < endAt || bits.some((b) => b.y < canvas.height + 20)) requestAnimationFrame(frame);
    else { canvas.remove(); window.removeEventListener("resize", resize); }
  }
  requestAnimationFrame(frame);
}
