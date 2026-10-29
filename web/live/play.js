// Player client for a live room. Routes: #/join/<code> or #/ (enter code). Protocol: docs/spec.md.
import { api, esc, toast, avatarImg, avatarEditor, Bells, liveSocket } from "/common.js";

const $ = (sel, root = document) => root.querySelector(sel);
const app = $("#app");
const DIR = { across: "Mendatar", down: "Menurun" };
const tokenKey = (c) => `tts_live_${c}`;

let me = null; // logged-in user or null
let code = "";
let nickname = "";
let avatar = null;
let sock = null;
let stopped = false; // socket intentionally closed (kicked / join failed)
let myId = null; // from welcome
let st = null; // latest RoomState
let offset = 0; // server_now − Date.now()
let view = null; // key of the rendered view
let buzzedFor = null; // clue id we already pressed BEL for (until the state moves on)
let typed = ""; // letters typed into the empty answer tiles

window.addEventListener("hashchange", () => location.reload());
boot();

// ---------- boot / join ----------

function routeCode() {
  const h = location.hash.replace(/^#/, "");
  const m = h.match(/^\/join\/(\d{6})$/) || h.match(/^\/?(\d{6})$/);
  return m ? m[1] : "";
}

function storedAvatar() {
  if (me?.avatar && Object.keys(me.avatar).length) return me.avatar;
  try {
    const a = JSON.parse(localStorage.getItem("tts_avatar") || "null");
    if (a && typeof a === "object") return a;
  } catch { /* ignore corrupt value */ }
  return null;
}

async function boot() {
  try {
    me = (await api("/api/me")).user;
  } catch {
    me = null;
  }
  code = routeCode();
  const token = code && localStorage.getItem(tokenKey(code));
  if (token) {
    try {
      await api(`/api/live/rooms/${code}`);
      nickname = me?.username || localStorage.getItem("tts_nickname") || "";
      avatar = window.TTSAvatar.normalize(storedAvatar() || window.TTSAvatar.random());
      const tap = $("#tap");
      tap.classList.remove("hidden");
      tap.addEventListener("click", () => { Bells.unlock(); tap.classList.add("hidden"); }, { once: true });
      connect();
      return;
    } catch (err) {
      localStorage.removeItem(tokenKey(code));
      if (err.status !== 404) toast(err.message);
    }
  }
  renderJoin();
}

function renderJoin(errorMsg = "") {
  view = "join";
  $("#title").textContent = "Main Live";
  app.innerHTML = `
    <form class="panel" id="joinForm" novalidate>
      <h1>Gabung room</h1>
      <p class="sub">Masukkan kode 6 angka yang tampil di layar utama.</p>
      <label for="code">Kode room</label>
      <input class="field code-input" id="code" inputmode="numeric" autocomplete="off" maxlength="6" pattern="\\d{6}" placeholder="000000" value="${esc(code)}">
      <p class="muted" id="roomInfo"></p>
      ${me
        ? `<p>Masuk sebagai <b>${esc(me.username)}</b>.</p>`
        : `<label for="nick">Nama panggilan</label>
           <input class="field" id="nick" maxlength="20" autocomplete="nickname" placeholder="mis. dewi" value="${esc(localStorage.getItem("tts_nickname") || "")}">
           <p class="muted" style="font-size:.9rem">Sudah punya akun? <a href="/">Masuk dulu</a> agar skor tersimpan.</p>`}
      <label>Avatar</label>
      <div id="avEd"></div>
      <p class="error" id="joinErr">${esc(errorMsg)}</p>
      <div class="row join-actions"><button class="btn go big" type="submit" id="joinBtn">Masuk</button></div>
    </form>`;
  const ed = avatarEditor($("#avEd"), storedAvatar(), (a) => localStorage.setItem("tts_avatar", JSON.stringify(a)));
  const codeInput = $("#code");
  codeInput.addEventListener("input", () => {
    codeInput.value = codeInput.value.replace(/\D/g, "").slice(0, 6);
    if (codeInput.value.length === 6) checkRoom(codeInput.value);
    else $("#roomInfo").textContent = "";
  });
  if (code) checkRoom(code);
  else codeInput.focus();
  $("#joinForm").addEventListener("submit", async (e) => {
    e.preventDefault();
    Bells.unlock();
    const err = $("#joinErr");
    err.textContent = "";
    const c = codeInput.value.trim();
    if (!/^\d{6}$/.test(c)) { err.textContent = "Kode room harus 6 angka."; return; }
    const nick = me ? me.username : $("#nick").value.trim();
    if (!me && (nick.length < 1 || nick.length > 20)) { err.textContent = "Nama panggilan 1–20 karakter."; return; }
    const btn = $("#joinBtn");
    btn.disabled = true;
    const room = await checkRoom(c);
    btn.disabled = false;
    if (!room) { err.textContent = $("#roomInfo").textContent; return; }
    if (room.phase === "finished") { err.textContent = "Permainan di room ini sudah selesai."; return; }
    code = c;
    nickname = nick;
    avatar = ed.get();
    localStorage.setItem("tts_avatar", JSON.stringify(avatar));
    if (!me) localStorage.setItem("tts_nickname", nick);
    history.replaceState(null, "", `#/join/${code}`);
    app.innerHTML = `<p class="muted">Menghubungkan…</p>`;
    view = null;
    connect();
  });
}

async function checkRoom(c) {
  const info = $("#roomInfo");
  try {
    const room = await api(`/api/live/rooms/${c}`);
    if (info) info.textContent = `Room: ${room.quiz_title}${room.phase === "playing" ? " · sedang berlangsung" : room.phase === "finished" ? " · sudah selesai" : ""}`;
    return room;
  } catch (err) {
    if (info) info.textContent = err.message;
    return null;
  }
}

// ---------- socket ----------

function connect() {
  stopped = false;
  myId = null;
  sock = liveSocket(code, () => {
    const hello = { type: "hello", role: "player", nickname, avatar };
    const token = localStorage.getItem(tokenKey(code));
    if (token) hello.token = token;
    return hello;
  }, onMessage, onStatus);
}

function stop() {
  stopped = true;
  sock?.close();
  $("#banner").classList.add("hidden");
  $("#tap").classList.add("hidden");
}

function onStatus(s) {
  if (stopped) return;
  $("#banner").classList.toggle("hidden", s === "open");
  if (s === "closed" && !myId) {
    // Never welcomed: the room may be gone. Stop retrying in that case.
    api(`/api/live/rooms/${code}`).catch((err) => {
      if (err.status !== 404 || stopped) return;
      stop();
      localStorage.removeItem(tokenKey(code));
      renderJoin("Room tidak ditemukan");
    });
  }
}

function onMessage(msg) {
  if (stopped) return;
  switch (msg.type) {
    case "welcome":
      myId = msg.you.player_id;
      nickname = msg.you.nickname;
      localStorage.setItem(tokenKey(code), msg.you.token);
      break;
    case "state":
      st = msg.state;
      offset = st.server_now - Date.now();
      render();
      break;
    case "buzz":
      if (msg.player_id === myId) Bells.play(msg.bell);
      break;
    case "result":
      if (msg.player_id === myId) showDelta(msg);
      break;
    case "error":
      if (!myId) {
        // Join rejected (e.g. nickname taken): back to the form.
        stop();
        localStorage.removeItem(tokenKey(code));
        renderJoin(msg.message);
      } else toast(msg.message);
      break;
    case "kicked":
      stop();
      localStorage.removeItem(tokenKey(code));
      view = "kicked";
      app.innerHTML = `<div class="panel message"><h1>Kamu dikeluarkan dari room</h1>
        <p>Admin mengeluarkanmu dari room ${esc(code)}.</p>
        <div class="row"><a class="btn go" href="#/">Gabung room lain</a></div></div>`;
      break;
  }
}

// ---------- helpers ----------

const now = () => Date.now() + offset;
const mine = () => st.players.find((p) => p.id === myId);
const player = (id) => st.players.find((p) => p.id === id);
const secondsLeft = (deadline) => Math.max(0, Math.ceil((deadline - now()) / 1000));

function cellsOf(c) {
  return Array.from({ length: c.length }, (_, i) =>
    c.direction === "across" ? `${c.row},${c.col + i}` : `${c.row + i},${c.col}`);
}

function miniScores() {
  const ps = [...st.players].sort((a, b) => b.score - a.score || a.nickname.localeCompare(b.nickname));
  return `<ol class="scores">${ps.map((p, i) => `
    <li class="${p.id === myId ? "me" : ""}${p.connected ? "" : " dim"}"><span class="rk">${i + 1}</span>${avatarImg(p.avatar, 34, p.nickname)}<span class="nm">${esc(p.nickname)}</span><span class="sc">${p.score}</span></li>`).join("")}</ol>`;
}

const RING_R = 32;
const RING_C = 2 * Math.PI * RING_R;
function ring(deadline) {
  return `<svg class="ring" viewBox="0 0 76 76" data-ring="${deadline}" aria-hidden="true">
    <circle class="bg" cx="38" cy="38" r="${RING_R}"></circle>
    <circle class="fg" cx="38" cy="38" r="${RING_R}" stroke-dasharray="${RING_C}" stroke-dashoffset="0"></circle>
    <text x="38" y="39">${secondsLeft(deadline)}</text></svg>`;
}

setInterval(() => {
  if (!st) return;
  for (const el of app.querySelectorAll("[data-deadline]")) el.textContent = secondsLeft(Number(el.dataset.deadline));
  for (const svg of app.querySelectorAll("[data-ring]")) {
    const left = Math.max(0, Number(svg.dataset.ring) - now());
    const total = st.settings.answer_seconds * 1000;
    svg.querySelector(".fg").setAttribute("stroke-dashoffset", String(RING_C * (1 - Math.min(1, left / total))));
    svg.querySelector("text").textContent = Math.ceil(left / 1000);
    svg.classList.toggle("low", left <= 3000);
  }
}, 100);

function showDelta(msg) {
  const el = document.createElement("div");
  el.className = `delta ${msg.correct ? "good" : "bad"}`;
  const d = msg.delta > 0 ? `+${msg.delta}` : msg.delta < 0 ? `−${Math.abs(msg.delta)}` : "0";
  el.innerHTML = `${d}<small>${msg.correct ? "Benar!" : "Salah"}</small>`;
  document.body.append(el);
  setTimeout(() => el.remove(), 1700);
  if (!msg.correct) {
    const tiles = $(".ans-tiles");
    tiles?.classList.add("shake");
  }
}

// ---------- render ----------

function viewKey() {
  if (st.phase === "lobby") return "lobby";
  if (st.phase === "finished") return "finished";
  const a = st.active;
  if (!a) return `wait:${st.last_outcome?.clue_id ?? ""}:${st.last_outcome?.kind ?? ""}`;
  if (a.state === "answering") return a.answering_player === myId ? `answer:${a.clue_id}:${a.deadline}` : `other:${a.clue_id}:${a.answering_player}:${a.deadline}`;
  if (a.locked_out.includes(myId)) return `locked:${a.clue_id}`;
  return `buzz:${a.clue_id}`;
}

function render() {
  $("#title").textContent = st.quiz.title;
  const you = mine();
  const chip = $("#scoreChip");
  chip.classList.toggle("hidden", !you || st.phase === "lobby");
  if (you) chip.textContent = `${you.score} poin`;
  if (!you) {
    // State before our welcome (or our slot vanished): nothing personal to show yet.
    if (view !== "pending") { view = "pending"; app.innerHTML = `<p class="muted">Menghubungkan…</p>`; }
    return;
  }
  const key = viewKey();
  const changed = key !== view;
  view = key;
  if (key !== `buzz:${buzzedFor}`) buzzedFor = null;
  if (key === "lobby") { if (changed) skeletonLobby(); updateLobby(); return; }
  if (key.startsWith("answer:")) { if (changed) renderAnswer(); return; }
  if (key === "finished") renderFinished();
  else renderPlaying();
}

// ----- lobby -----

function skeletonLobby() {
  app.innerHTML = `
    <div class="stack">
      <section class="panel">
        <div class="me-card" id="meCard"></div>
        <details style="margin-top:.8rem"><summary class="btn small">Ubah avatar</summary><div id="lobbyAv" style="margin-top:.8rem"></div></details>
      </section>
      <section class="panel center"><h2 class="pulse">Menunggu admin memulai…</h2></section>
      <section class="panel">
        <h2>Pilih bel kamu</h2>
        <p class="muted">Ketuk untuk mendengar. Bel yang sudah dipakai pemain lain tidak bisa dipilih.</p>
        <div class="bell-grid" id="bells"></div>
      </section>
      <section class="panel"><h2>Pemain lain <span class="chip" id="otherCount"></span></h2><ul class="others" id="others"></ul></section>
    </div>`;
  avatarEditor($("#lobbyAv"), mine().avatar, (a) => {
    avatar = a;
    localStorage.setItem("tts_avatar", JSON.stringify(a));
    sock.send({ type: "set_avatar", avatar: a });
  });
  $("#bells").addEventListener("click", (e) => {
    const b = e.target.closest("[data-bell]");
    if (!b || b.disabled) return;
    Bells.unlock();
    Bells.play(b.dataset.bell);
    if (b.dataset.bell !== mine()?.bell) sock.send({ type: "set_bell", bell: b.dataset.bell });
  });
}

function updateLobby() {
  const you = mine();
  $("#meCard").innerHTML = `${avatarImg(you.avatar, 120, you.nickname)}<div><p class="eyebrow">Kamu</p><div class="nm">${esc(you.nickname)}</div>
    <span class="chip mint">🔔 ${esc(Bells.label(you.bell))}</span></div>`;
  const takenBy = new Map(st.players.filter((p) => p.id !== myId).map((p) => [p.bell, p.nickname]));
  $("#bells").innerHTML = st.bells.map((id) => {
    const on = id === you.bell;
    const other = takenBy.get(id);
    return `<button type="button" class="bell-btn${on ? " on" : ""}" data-bell="${esc(id)}" ${other ? "disabled" : ""} aria-pressed="${on}">
      <b>${esc(Bells.label(id))}</b><small>${on ? "punyamu" : other ? `dipakai ${esc(other)}` : ""}</small></button>`;
  }).join("");
  const others = st.players.filter((p) => p.id !== myId);
  $("#otherCount").textContent = others.length;
  $("#others").innerHTML = others.length
    ? others.map((p) => `<li class="${p.connected ? "" : "dim"}">${avatarImg(p.avatar, 40, p.nickname)}<span>${esc(p.nickname)}</span><span class="chip">🔔 ${esc(Bells.label(p.bell))}</span></li>`).join("")
    : `<li class="muted">Belum ada pemain lain.</li>`;
}

// ----- playing -----

function clueHeader(c) {
  return `<p class="eyebrow">${c.number} ${DIR[c.direction]} · ${c.length} huruf</p><div class="clue-text">${esc(c.clue)}</div>`;
}

function outcomeLine() {
  const lo = st.last_outcome;
  if (!lo) return "";
  if (lo.kind === "solved") {
    const p = player(lo.player_id);
    const who = lo.player_id === myId ? "Kamu" : esc(p?.nickname ?? "?");
    return `<p class="outcome">${who} benar! ${lo.answer ? `Jawabannya ${esc(lo.answer)}` : ""}</p>`;
  }
  if (lo.kind === "exhausted") return `<p class="outcome bad">Tidak ada yang benar. Jawabannya ${esc(lo.answer ?? "")}</p>`;
  return `<p class="outcome bad">Soal dilewati</p>`;
}

function renderPlaying() {
  const a = st.active;
  if (!a) {
    app.innerHTML = `<div class="stack">
      ${outcomeLine()}
      <section class="panel center"><h2 class="pulse">Menunggu soal berikutnya…</h2></section>
      <section class="panel"><h2>Skor</h2>${miniScores()}</section></div>`;
    return;
  }
  const clue = st.quiz.clues.find((c) => c.id === a.clue_id);
  let action;
  if (a.state === "answering") {
    const p = player(a.answering_player);
    action = `<button type="button" class="bel off" disabled>${esc(p?.nickname ?? "?")} sedang menjawab…${a.deadline ? `<span class="cd" data-deadline="${a.deadline}">${secondsLeft(a.deadline)}</span>` : ""}</button>`;
  } else if (a.locked_out.includes(myId)) {
    action = `<button type="button" class="bel off" disabled>Kamu sudah dapat giliran di soal ini</button>`;
  } else {
    const pressed = buzzedFor === a.clue_id;
    action = `<button type="button" class="bel${pressed ? " pressed" : ""}" id="bel" ${pressed ? "disabled" : ""}>BEL!</button>
      <p class="muted">Tekan tombol atau <kbd>Spasi</kbd></p>`;
  }
  app.innerHTML = `<div class="stack">
    <section class="panel clue-card">${clueHeader(clue)}</section>
    <div class="buzz-area">${action}</div>
    <section class="panel"><h2>Skor</h2>${miniScores()}</section></div>`;
  $("#bel")?.addEventListener("click", buzz);
}

function buzz() {
  const a = st?.active;
  if (!a || a.state !== "waiting" || a.locked_out.includes(myId) || buzzedFor === a.clue_id) return;
  Bells.unlock();
  buzzedFor = a.clue_id;
  sock.send({ type: "buzz" });
  const b = $("#bel");
  if (b) { b.classList.add("pressed"); b.disabled = true; }
}

document.addEventListener("keydown", (e) => {
  if (e.code !== "Space" || e.repeat || !view?.startsWith("buzz:")) return;
  if (e.target.closest?.("input, textarea")) return;
  e.preventDefault();
  buzz();
});

// ----- answering -----

function renderAnswer() {
  const a = st.active;
  const clue = st.quiz.clues.find((c) => c.id === a.clue_id);
  const cells = cellsOf(clue);
  const fixed = cells.map((k) => st.letters[k] || "");
  const empties = fixed.filter((l) => !l).length;
  typed = "";
  app.innerHTML = `<div class="stack">
    <section class="panel clue-card">${clueHeader(clue)}</section>
    <form class="panel answer" id="ansForm" autocomplete="off">
      <h2>Giliranmu! Ketik jawaban</h2>
      ${a.deadline ? ring(a.deadline) : ""}
      <div class="ans-tiles" id="tiles" style="--len:${clue.length}">
        ${fixed.map((l, i) => `<div class="tile open${l ? " lock" : ""}" data-i="${i}">${esc(l)}</div>`).join("")}
        <input class="ans-input" id="ans" autocapitalize="characters" autocorrect="off" spellcheck="false" enterkeyhint="send" aria-label="Jawaban (${clue.length} huruf)">
      </div>
      <button class="btn go big" type="submit" id="sendBtn" disabled>Kirim</button>
    </form></div>`;
  const input = $("#ans");
  const paint = () => {
    let t = 0;
    let cursorSet = false;
    for (const el of $("#tiles").querySelectorAll(".tile")) {
      const i = Number(el.dataset.i);
      if (fixed[i]) continue;
      const ch = typed[t++] || "";
      el.textContent = ch;
      el.className = `tile open${ch ? " typed" : ""}`;
      if (!ch && !cursorSet) { el.classList.add("cursor"); cursorSet = true; }
    }
    $("#sendBtn").disabled = typed.length !== empties;
  };
  input.addEventListener("input", () => {
    typed = input.value.toUpperCase().replace(/[^A-Z]/g, "").slice(0, empties);
    input.value = typed;
    paint();
  });
  $("#tiles").addEventListener("click", () => input.focus());
  $("#ansForm").addEventListener("submit", (e) => {
    e.preventDefault();
    if (typed.length !== empties) { toast("Lengkapi semua kotak dulu."); return; }
    let t = 0;
    const word = fixed.map((l) => l || typed[t++]).join("");
    sock.send({ type: "answer", text: word });
    $("#sendBtn").disabled = true;
    input.blur();
  });
  paint();
  input.focus();
}

// ----- finished -----

function renderFinished() {
  const pod = st.podium || [];
  const mineEntry = pod.find((e) => e.player_id === myId);
  app.innerHTML = `<div class="stack">
    <section class="panel center">
      <p class="eyebrow">Permainan selesai</p>
      ${mineEntry ? `<div class="rank-big">#${mineEntry.rank}</div><h2>${mineEntry.rank === 1 ? "Juara! 🏆" : `dari ${pod.length} pemain`}</h2><p><b>${mineEntry.score}</b> poin</p>` : `<h2>Terima kasih sudah bermain!</h2>`}
    </section>
    <section class="panel"><h2>Peringkat</h2>
      <ol class="scores">${pod.map((e) => `<li class="${e.player_id === myId ? "me" : ""}"><span class="rk">${e.rank}</span>${avatarImg(e.avatar, 34, e.nickname)}<span class="nm">${esc(e.nickname)}</span><span class="sc">${e.score}</span></li>`).join("")}</ol>
    </section>
    <div class="row" style="justify-content:center"><a class="btn sun" href="/">Ke beranda</a><a class="btn" href="#/">Gabung room lain</a></div>
  </div>`;
}
