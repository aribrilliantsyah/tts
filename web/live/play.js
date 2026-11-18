// Player client for a live room. Routes: #/join/<code> or #/ (enter code). Protocol: docs/spec.md.
import { api, esc, toast, avatarImg, avatarEditor, Sound, liveSocket, fmtCountdown } from "/common.js";
import { isSpeechRecognitionSupported, createVoiceRecognizer, parseVoiceAnswer } from "/voice.js";

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
let heardJoin = false; // own ev-join plays once per page load, not on every reconnect
let view = null; // key of the rendered view
let buzzedFor = null; // clue id we already pressed BEL for (until the state moves on)
let typed = ""; // letters typed into the empty answer tiles
let activeRecognizer = null; // active voice-to-text speech recognizer
const REACTS = [["wave", "👋", "Lambai"], ["tawa", "😂", "Tawa"], ["api", "🔥", "Api"], ["pesta", "🎉", "Pesta"], ["degdegan", "😬", "Deg-degan"]];
const REACT_EMOJI = Object.fromEntries(REACTS.map(([k, e]) => [k, e]));
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
let lastReactAt = 0; // client-side mirror of the server's rate limit (600 ms any react, 1500 ms bell test)
let lastBellAt = 0;
let heroAv = null; // JSON of the avatar currently drawn in the lobby hero

// Audio needs a user gesture: the first tap or key anywhere (typing the nickname, Space to buzz) unlocks it and starts decoding.
Sound.preload([...Sound.BELLS.map((b) => b.id), "ev-join", "ev-start", "ev-clue", "ev-correct", "ev-wrong", "ev-applause", "ev-yay"]);
Sound.bindControls($("header.top"));
const unlockAudio = () => {
  Sound.unlock();
  $("#tap").classList.add("hidden");
  document.removeEventListener("pointerdown", unlockAudio, true);
  document.removeEventListener("keydown", unlockAudio, true);
};
document.addEventListener("pointerdown", unlockAudio, true);
document.addEventListener("keydown", unlockAudio, true);

// ---------- Voice permission confirm modal ----------

const voiceModal = $("#voiceModal");
const micBtn = $("#micBtn");

function isSecureOrigin() {
  return window.isSecureContext || location.hostname === "localhost" || location.hostname === "127.0.0.1";
}

function updateMicButtonState(granted) {
  if (!micBtn) return;
  micBtn.classList.toggle("granted", !!granted);
  micBtn.title = granted ? "Izin mikrofon aktif ✓" : "Izin suara & mikrofon";
}

async function checkMicPermission() {
  if (navigator.permissions?.query) {
    try {
      const q = await navigator.permissions.query({ name: "microphone" });
      updateMicButtonState(q.state === "granted");
      q.onchange = () => updateMicButtonState(q.state === "granted");
      return q.state;
    } catch (_) {}
  }
  return "prompt";
}
checkMicPermission();

async function requestVoicePermission() {
  Sound.unlock();
  if (!isSecureOrigin()) {
    toast("Mikrofon membutuhkan HTTPS. Ketuk 'Beralih ke HTTPS'.");
    return false;
  }
  try {
    if (navigator.mediaDevices?.getUserMedia) {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      stream.getTracks().forEach((t) => t.stop());
      updateMicButtonState(true);
      toast("✓ Izin mikrofon aktif! Kamu bisa menjawab dengan suara.");
      return true;
    } else if (isSpeechRecognitionSupported()) {
      return new Promise((resolve) => {
        let done = false;
        const testRec = createVoiceRecognizer({
          onStart: () => {
            if (done) return;
            done = true;
            testRec.stop();
            updateMicButtonState(true);
            toast("✓ Izin suara & mikrofon aktif!");
            resolve(true);
          },
          onError: (msg) => {
            if (done) return;
            done = true;
            toast(msg);
            resolve(false);
          },
          onEnd: () => {
            if (!done) {
              done = true;
              resolve(false);
            }
          }
        });
        testRec.start();
        setTimeout(() => {
          if (!done) {
            done = true;
            testRec.abort();
            resolve(false);
          }
        }, 8000);
      });
    } else {
      toast("Browser kamu belum mendukung voice-to-text.");
      return false;
    }
  } catch (err) {
    console.warn("Mic permission error:", err);
    updateMicButtonState(false);
    if (err.name === "NotAllowedError" || err.name === "PermissionDeniedError") {
      toast("Izin mikrofon ditolak. Kamu tetap bisa mengetik jawaban secara manual.");
    } else {
      toast("Gagal mengaktifkan mikrofon: " + (err.message || "error"));
    }
    return false;
  }
}

function renderVoiceModal() {
  const permBox = $("#permBox");
  if (!permBox) return;

  if (!isSecureOrigin()) {
    permBox.innerHTML = `
      <div class="perm-icon">🔒</div>
      <h2 id="permTitle">Beralih ke HTTPS untuk Suara</h2>
      <p>Browser HP memblokir izin mikrofon pada koneksi HTTP biasa (<code>${esc(location.host)}</code>).</p>
      <p class="sub">Agar kamu bisa menjawab teka-teki dengan suara, buka TTS lewat <b>HTTPS</b>.</p>
      <div class="perm-tip">
        💡 <b>Catatan:</b> Saat membuka HTTPS, jika browser memunculkan pesan <i>"Koneksi Tidak Privat / Not Secure"</i>, cukup ketuk <b>Lanjutan (Advanced) → Lanjutkan ke situs (Proceed)</b>.
      </div>
      <div class="perm-actions">
        <button type="button" class="btn go big" id="switchHttpsBtn">🔒 Beralih ke HTTPS</button>
        <button type="button" class="btn" id="skipMicBtn">Tetap di HTTP (Ketik Manual)</button>
      </div>`;

    $("#switchHttpsBtn")?.addEventListener("click", () => {
      const httpsUrl = `https://${location.hostname}:${location.port}${location.pathname}${location.search}${location.hash}`;
      location.href = httpsUrl;
    });
  } else {
    permBox.innerHTML = `
      <div class="perm-icon">🎙️</div>
      <h2 id="permTitle">Izin Suara & Mikrofon</h2>
      <p>TTS Kuis dilengkapi fitur <b>Voice-to-Text</b> agar kamu bisa menjawab teka-teki cukup dengan berbicara!</p>
      <p class="sub">Aktifkan izin mikrofon sekarang agar saat bel ditekan, kamu bisa langsung bicara tanpa terhambat popup browser.</p>
      <div class="perm-actions">
        <button type="button" class="btn go big" id="allowMicBtn">🎙️ Izinkan Mikrofon</button>
        <button type="button" class="btn" id="skipMicBtn">Nanti Saja</button>
      </div>`;

    $("#allowMicBtn")?.addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      btn.disabled = true;
      const prevText = btn.innerHTML;
      btn.textContent = "Meminta izin…";
      await requestVoicePermission();
      btn.disabled = false;
      btn.innerHTML = prevText;
      hideVoiceModal();
    });
  }

  $("#skipMicBtn")?.addEventListener("click", () => {
    Sound.unlock();
    hideVoiceModal();
  });
}

async function showVoicePermissionConfirm(force = false) {
  if (!voiceModal) return;
  const sessionKey = `tts_mic_prompted_${code}`;
  if (!force && sessionStorage.getItem(sessionKey)) return;
  sessionStorage.setItem(sessionKey, "1");

  const state = await checkMicPermission();
  if (!force && state === "granted") return;

  renderVoiceModal();
  voiceModal.classList.remove("hidden");
}

function hideVoiceModal() {
  if (!voiceModal) return;
  voiceModal.classList.add("hidden");
}

voiceModal?.addEventListener("click", (e) => {
  if (e.target === voiceModal) hideVoiceModal();
});

micBtn?.addEventListener("click", () => {
  showVoicePermissionConfirm(true);
});

document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && voiceModal && !voiceModal.classList.contains("hidden")) {
    hideVoiceModal();
  }
});

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
      $("#tap").classList.remove("hidden");
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
    Sound.unlock();
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
      if (!heardJoin) { heardJoin = true; Sound.play("ev-join"); }
      showVoicePermissionConfirm();
      break;
    case "state": {
      const prev = st;
      st = msg.state;
      cues(prev, st);
      render();
      break;
    }
    case "buzz":
      if (msg.player_id === myId) Sound.play(msg.bell); // authoritative cue for our own bell; the host plays everyone's
      break;
    case "react":
      onReact(msg);
      break;
    case "result":
      Sound.play(msg.correct ? "ev-correct" : "ev-wrong");
      if (msg.player_id === myId) {
        showDelta(msg);
      } else if (!msg.correct) {
        const p = player(msg.player_id);
        const name = p?.nickname ?? "Pemain";
        const guessTxt = msg.guess ? `"${msg.guess}"` : "(waktu habis)";
        toast(`${name} menebak ${guessTxt} · Salah!`);
      }
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

// Sound cues come from diffs between consecutive snapshots, so a page load or reconnect never replays old events.
function cues(prev, next) {
  if (!prev) return;
  if (prev.phase !== next.phase) {
    if (next.phase === "playing") { Sound.play("ev-start"); startCountdown(); }
    else if (next.phase === "finished") {
      Sound.play("ev-applause");
      const rank = next.podium?.find((e) => e.player_id === myId)?.rank;
      if (rank && rank <= 3) Sound.play("ev-yay");
    }
  }
  if (next.active && next.active.clue_id !== prev.active?.clue_id) Sound.play("ev-clue");
}

// ---------- helpers ----------

const mine = () => st.players.find((p) => p.id === myId);
const player = (id) => st.players.find((p) => p.id === id);
const countdown = (deadline) => fmtCountdown(deadline - sock.serverNow());

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
    <text x="38" y="39">${countdown(deadline).text}</text></svg>`;
}

// Countdown label and ring progress are redrawn every animation frame from the synced server clock.
function tickFrame() {
  if (st && sock) {
    const t = sock.serverNow();
    for (const el of app.querySelectorAll("[data-deadline]")) {
      const { text } = fmtCountdown(Number(el.dataset.deadline) - t);
      if (el.textContent !== text) el.textContent = text;
    }
    const total = st.settings.answer_seconds * 1000;
    for (const svg of app.querySelectorAll("[data-ring]")) {
      const left = Math.max(0, Number(svg.dataset.ring) - t);
      const { text, low } = fmtCountdown(left);
      svg.querySelector(".fg").setAttribute("stroke-dashoffset", String(RING_C * (1 - Math.min(1, left / total))));
      const label = svg.querySelector("text");
      if (label.textContent !== text) label.textContent = text;
      svg.classList.toggle("low", low);
    }
  }
  requestAnimationFrame(tickFrame);
}
requestAnimationFrame(tickFrame);

function showDelta(msg) {
  const el = document.createElement("div");
  el.className = `delta ${msg.correct ? "good" : "bad"}`;
  const d = msg.delta > 0 ? `+${msg.delta}` : msg.delta < 0 ? `−${Math.abs(msg.delta)}` : "0";
  const guessHtml = !msg.correct && msg.guess ? `<div class="delta-guess"><s>${esc(msg.guess)}</s></div>` : "";
  el.innerHTML = `${d}<small>${msg.correct ? "Benar!" : "Salah"}</small>${guessHtml}`;
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
  if (you) {
    const sorted = [...st.players].sort((a, b) => b.score - a.score || a.nickname.localeCompare(b.nickname));
    const rank = sorted.findIndex((p) => p.id === myId) + 1;
    chip.textContent = `#${rank} · ${you.score} poin`;
  }
  if (!you) {
    // State before our welcome (or our slot vanished): nothing personal to show yet.
    if (view !== "pending") { view = "pending"; app.innerHTML = `<p class="muted">Menghubungkan…</p>`; }
    return;
  }
  const key = viewKey();
  const changed = key !== view;
  if (changed && activeRecognizer) {
    activeRecognizer.abort();
    activeRecognizer = null;
  }
  view = key;
  if (key !== `buzz:${buzzedFor}`) buzzedFor = null;
  if (key === "lobby") { if (changed) skeletonLobby(); updateLobby(); return; }
  if (key.startsWith("answer:")) { if (changed) renderAnswer(); return; }
  if (key === "finished") renderFinished();
  else renderPlaying();
}

// ----- lobby -----

/** Returns true when the react was sent (not throttled). */
function sendReact(kind) {
  const now = performance.now();
  if (now - lastReactAt < 600 || (kind === "bell" && now - lastBellAt < 1500)) return false;
  lastReactAt = now;
  if (kind === "bell") lastBellAt = now;
  sock.send({ type: "react", kind });
  return true;
}

function hop(el) {
  if (!el) return;
  el.classList.remove("hop");
  void el.offsetWidth;
  el.classList.add("hop");
  setTimeout(() => el.classList.remove("hop"), 520);
}

/** Own reactions come back from the server (so a throttled tap shows nothing); others' reactions hop their row. */
function onReact(msg) {
  if (!st || st.phase !== "lobby" || !REACT_EMOJI[msg.kind] && msg.kind !== "bell") return;
  const emoji = msg.kind === "bell" ? "🔔" : REACT_EMOJI[msg.kind];
  if (msg.player_id === myId) {
    const slot = $("#heroBubble");
    if (!slot) return;
    hop($("#poke"));
    clearTimeout(slot._t);
    slot.innerHTML = `<span class="bubble">${emoji}</span>`;
    slot._t = setTimeout(() => { slot.innerHTML = ""; }, 1800);
    return;
  }
  const row = $(`.others li[data-pid="${CSS.escape(msg.player_id)}"]`);
  if (!row) return;
  hop(row.querySelector(".avatar"));
  const rx = row.querySelector(".rx");
  clearTimeout(rx._t);
  rx.textContent = emoji;
  rx._t = setTimeout(() => { rx.textContent = ""; }, 1800);
}

let countdownTimer = null;
/** 3-2-1 over the whole screen when the game starts. Visual only (≤ 2.3 s); the overlay ignores pointer events and removes itself. */
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

function skeletonLobby() {
  heroAv = null;
  app.innerHTML = `
    <div class="stack">
      <section class="panel hero">
        <button type="button" class="hero-av" id="poke" aria-label="Colek avatarmu"><span id="heroImg"></span><span class="bubble-slot" id="heroBubble"></span></button>
        <div class="hero-info"><div class="nm" id="meName"></div><div class="ord" id="meOrd"></div><span class="chip mint" id="meBell"></span></div>
      </section>
      <div>
        <p class="react-hint">Ketuk, reaksimu muncul di layar utama.</p>
        <div class="react-row" id="reacts" role="group" aria-label="Kirim reaksi ke layar utama">${REACTS.map(([k, e, l]) => `<button type="button" class="react-btn" data-react="${k}" aria-label="${l}" title="${l}">${e}</button>`).join("")}</div>
      </div>
      <details class="av-det"><summary class="btn small">Ubah avatar</summary><div id="lobbyAv" class="av-body"></div></details>
      <h2 class="wait pulse">Menunggu admin memulai…</h2>
      <section class="panel">
        <h2>Pilih bel kamu</h2>
        <p class="muted">Ketuk untuk mencoba: belmu juga berbunyi di layar utama. Bel yang sudah dipakai pemain lain tidak bisa dipilih.</p>
        <div class="bell-grid" id="bells"></div>
      </section>
      <section class="panel"><h2>Pemain lain <span class="chip" id="otherCount"></span></h2><ul class="others" id="others"></ul></section>
    </div>`;
  avatarEditor($("#lobbyAv"), mine().avatar, (a) => {
    avatar = a;
    localStorage.setItem("tts_avatar", JSON.stringify(a));
    sock.send({ type: "set_avatar", avatar: a });
  });
  $("#poke").addEventListener("click", () => {
    Sound.unlock();
    hop($("#poke"));
    sendReact("wave");
  });
  $("#reacts").addEventListener("click", (e) => {
    const b = e.target.closest("[data-react]");
    if (!b) return;
    Sound.unlock();
    sendReact(b.dataset.react);
  });
  $("#bells").addEventListener("click", (e) => {
    const b = e.target.closest("[data-bell]");
    if (!b || b.disabled) return;
    Sound.unlock();
    Sound.play(b.dataset.bell);
    if (b.dataset.bell !== mine()?.bell) sock.send({ type: "set_bell", bell: b.dataset.bell });
    sendReact("bell"); // same ordered stream: the host sees the new bell before the test
  });
}

function updateLobby() {
  const you = mine();
  const av = JSON.stringify(you.avatar);
  if (av !== heroAv) { heroAv = av; $("#heroImg").innerHTML = avatarImg(you.avatar, 128, you.nickname); }
  $("#meName").textContent = you.nickname;
  $("#meOrd").textContent = `Kamu pemain ke-${st.players.findIndex((p) => p.id === myId) + 1}`;
  $("#meBell").textContent = `🔔 ${Sound.label(you.bell)}`;
  const takenBy = new Map(st.players.filter((p) => p.id !== myId).map((p) => [p.bell, p.nickname]));
  $("#bells").innerHTML = st.bells.map((id) => {
    const on = id === you.bell;
    const other = takenBy.get(id);
    return `<button type="button" class="bell-btn${on ? " on" : ""}" data-bell="${esc(id)}" ${other ? "disabled" : ""} aria-pressed="${on}">
      <b>${esc(Sound.label(id))}</b><small>${on ? "punyamu" : other ? `dipakai ${esc(other)}` : ""}</small></button>`;
  }).join("");
  const others = st.players.filter((p) => p.id !== myId);
  $("#otherCount").textContent = others.length;
  $("#others").innerHTML = others.length
    ? others.map((p) => `<li data-pid="${esc(p.id)}" class="${p.connected ? "" : "dim"}">${avatarImg(p.avatar, 40, p.nickname)}<span class="on">${esc(p.nickname)}</span><span class="rx" aria-hidden="true"></span><span class="chip">🔔 ${esc(Sound.label(p.bell))}</span></li>`).join("")
    : `<li class="muted">Kamu yang pertama. Yang lain menyusul.</li>`;
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
    action = `<div class="opponent-box">
      <div class="row" style="justify-content:center; gap:.6rem;">
        ${avatarImg(p?.avatar, 52, p?.nickname)}
        <div style="text-align:left;">
          <b>${esc(p?.nickname ?? "?")}</b>
          <div class="muted">sedang menjawab…</div>
        </div>
      </div>
      ${a.deadline ? `<span class="cd" data-deadline="${a.deadline}">${countdown(a.deadline).text}</span>` : ""}
    </div>`;
  } else if (a.locked_out.includes(myId)) {
    const attemptsMap = new Map((a.attempts || []).map((x) => [x.player_id, x.guess]));
    const myGuess = attemptsMap.get(myId);
    const guessInfo = myGuess !== undefined
      ? (myGuess ? `<p class="wrong-info">Tebakanmu: <span class="chip wrong-guess"><s>${esc(myGuess)}</s></span> (Salah)</p>` : `<p class="wrong-info muted">Waktu menjawab habis</p>`)
      : "";
    action = `<div class="locked-box">
      <div class="chip sun">🔒 Terkunci</div>
      ${guessInfo}
      <p>Kamu sudah mencoba di soal ini.<br><small class="muted">Tunggu jika bel terbuka lagi untuk soal berikutnya.</small></p>
    </div>`;
  } else {
    const pressed = buzzedFor === a.clue_id;
    const attemptsMap = new Map((a.attempts || []).map((x) => [x.player_id, x.guess]));
    const prevAttempts = a.locked_out.length
      ? `<div class="locked-attempts">
          <span class="muted">Tebakan salah:</span>
          ${a.locked_out.map((id) => {
            const p = player(id);
            const g = attemptsMap.get(id);
            const gHtml = g !== undefined
              ? (g ? `<span class="chip wrong-guess"><s>${esc(g)}</s></span>` : `<span class="chip wrong-guess muted">waktu habis</span>`)
              : "";
            return `<span class="row attempt-chip" style="gap:.3rem">${avatarImg(p?.avatar, 22, p?.nickname)}<span>${esc(p?.nickname ?? "?")}</span>${gHtml}</span>`;
          }).join("")}
        </div>`
      : "";
    action = `<button type="button" class="bel${pressed ? " pressed" : ""}" id="bel" ${pressed ? "disabled" : ""}>BEL!</button>
      <p class="muted">Tekan tombol atau <kbd>Spasi</kbd></p>
      ${prevAttempts}`;
  }
  app.innerHTML = `<div class="stack">
    <section class="panel clue-card">${clueHeader(clue)}</section>
    <div class="buzz-area">${action}</div>
    <section class="panel"><h2>Skor</h2>${miniScores()}</section></div>`;
  // Buzz on pointerdown, not click: saves the finger-lift (~50–100 ms). click stays as the keyboard (Enter) path; buzz() is idempotent.
  const bel = $("#bel");
  bel?.addEventListener("pointerdown", (e) => {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    e.preventDefault();
    buzz();
  });
  bel?.addEventListener("click", buzz);
}

function buzz() {
  const a = st?.active;
  if (!a || a.state !== "waiting" || a.locked_out.includes(myId) || buzzedFor === a.clue_id) return;
  Sound.unlock();
  buzzedFor = a.clue_id;
  sock.send({ type: "buzz" });
  const b = $("#bel");
  if (b) { b.classList.add("pressed"); b.disabled = true; } // optimistic: the next snapshot replaces it
  const clueId = a.clue_id;
  setTimeout(() => {
    // Server ignored the buzz (late/ineligible) and nothing changed: release the button.
    if (buzzedFor === clueId && view === `buzz:${clueId}`) { buzzedFor = null; renderPlaying(); }
  }, 1500);
}

document.addEventListener("keydown", (e) => {
  if (e.code !== "Space" || e.repeat || !view?.startsWith("buzz:")) return;
  if (e.target.closest?.("input, textarea")) return;
  e.preventDefault();
  buzz();
});

// ----- answering -----

function renderAnswer() {
  if (activeRecognizer) {
    activeRecognizer.abort();
    activeRecognizer = null;
  }
  const a = st.active;
  const clue = st.quiz.clues.find((c) => c.id === a.clue_id);
  const cells = cellsOf(clue);
  const fixed = cells.map((k) => st.letters[k] || "");
  const empties = fixed.filter((l) => !l).length;
  typed = "";
  const supported = isSpeechRecognitionSupported();
  app.innerHTML = `<div class="stack">
    <section class="panel clue-card">${clueHeader(clue)}</section>
    <form class="panel answer" id="ansForm" autocomplete="off">
      <h2>Giliranmu! Jawab teka-teki</h2>
      ${a.deadline ? ring(a.deadline) : ""}
      <div class="ans-tiles" id="tiles" style="--len:${clue.length}">
        ${fixed.map((l, i) => `<div class="tile open${l ? " lock" : ""}" data-i="${i}">${esc(l)}</div>`).join("")}
        <input class="ans-input" id="ans" autocapitalize="characters" autocorrect="off" spellcheck="false" enterkeyhint="send" aria-label="Jawaban (${clue.length} huruf)">
      </div>
      <div class="voice-status hidden" id="voiceStatus" aria-live="polite"></div>
      <div class="ans-actions">
        <button class="btn voice-btn" type="button" id="voiceBtn" aria-label="Bicara untuk menjawab" title="${supported ? "Jawab dengan suara (Voice to Text)" : "Voice-to-text tidak didukung di browser ini"}">
          <span class="voice-icon">🎙️</span>
          <span class="voice-text">Bicara</span>
        </button>
        <button class="btn go big" type="submit" id="sendBtn" disabled>Kirim</button>
      </div>
    </form></div>`;
  const input = $("#ans");
  const voiceBtn = $("#voiceBtn");
  const voiceStatus = $("#voiceStatus");

  const showVoiceStatus = (text, kind = "listening") => {
    if (!voiceStatus) return;
    voiceStatus.className = `voice-status ${kind}`;
    voiceStatus.textContent = text;
  };
  const hideVoiceStatus = () => {
    if (!voiceStatus) return;
    voiceStatus.className = "voice-status hidden";
    voiceStatus.textContent = "";
  };

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

  const recognizer = createVoiceRecognizer({
    lang: "id-ID",
    onStart: () => {
      voiceBtn.classList.add("listening");
      voiceBtn.querySelector(".voice-icon").textContent = "🔴";
      voiceBtn.querySelector(".voice-text").textContent = "Mendengarkan…";
      showVoiceStatus("Mendengarkan… Ucapkan jawabanmu", "listening");
    },
    onInterim: (interim) => {
      showVoiceStatus(`Mendengar: "${interim}"…`, "listening");
      const res = parseVoiceAnswer(interim, clue.length, fixed, empties);
      if (res && res.typed) {
        typed = res.typed.slice(0, empties);
        input.value = typed;
        paint();
      }
    },
    onResult: ({ transcript, isFinal }) => {
      const res = parseVoiceAnswer(transcript, clue.length, fixed, empties);
      if (res && res.typed) {
        typed = res.typed.slice(0, empties);
        input.value = typed;
        paint();
        if (isFinal) {
          showVoiceStatus(`✓ Terdeteksi: "${res.candidate}"`, "heard");
        }
      } else if (isFinal && transcript) {
        showVoiceStatus(`Mendengar: "${transcript}" (tidak pas dengan ${clue.length} huruf)`, "listening");
      }
    },
    onError: (msg) => {
      toast(msg);
      hideVoiceStatus();
    },
    onEnd: () => {
      voiceBtn.classList.remove("listening");
      voiceBtn.querySelector(".voice-icon").textContent = "🎙️";
      voiceBtn.querySelector(".voice-text").textContent = "Bicara";
    }
  });
  activeRecognizer = recognizer;

  const autoVoice = supported && isSecureOrigin();

  // Automatically start listening so player can immediately speak the answer!
  if (autoVoice) {
    setTimeout(() => {
      if (activeRecognizer === recognizer && view?.startsWith("answer:")) {
        try {
          recognizer.start();
        } catch (e) {
          console.warn("Auto-start mic failed:", e);
        }
      }
    }, 180);
  }

  let readyForClicks = false;
  setTimeout(() => { readyForClicks = true; }, 450);

  voiceBtn.addEventListener("click", (e) => {
    e.preventDefault();
    if (!readyForClicks) {
      // Ignore rapid tap or click bleed from the bell button
      return;
    }
    Sound.unlock();
    if (!isSecureOrigin()) {
      showVoicePermissionConfirm(true);
      toast("Fitur suara membutuhkan koneksi HTTPS. Beralih ke HTTPS untuk menggunakan suara.");
      return;
    }
    if (!supported) {
      toast("Browser kamu belum mendukung voice-to-text. Silakan ketik langsung.");
      return;
    }
    if (recognizer.isListening()) {
      recognizer.stop();
    } else {
      recognizer.start();
    }
  });

  input.addEventListener("input", () => {
    typed = input.value.toUpperCase().replace(/[^A-Z]/g, "").slice(0, empties);
    input.value = typed;
    paint();
  });
  $("#tiles").addEventListener("click", () => input.focus());
  input.addEventListener("focus", () => {
    $("#tiles")?.scrollIntoView({ block: "center", behavior: "smooth" });
  });

  const onKeyType = (e) => {
    if (e.target !== input && e.key && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      input.focus();
    }
  };
  window.addEventListener("keydown", onKeyType, { once: true });

  let sent = false;
  $("#ansForm").addEventListener("submit", (e) => {
    e.preventDefault();
    if (sent) return;
    if (typed.length !== empties) { toast("Lengkapi semua kotak dulu."); return; }
    sent = true; // Enter + Kirim in quick succession must not send twice
    window.removeEventListener("keydown", onKeyType);
    if (activeRecognizer) {
      activeRecognizer.abort();
      activeRecognizer = null;
    }
    let t = 0;
    const word = fixed.map((l) => l || typed[t++]).join("");
    sock.send({ type: "answer", text: word });
    $("#sendBtn").disabled = true;
    input.blur();
  });
  paint();
  if (!autoVoice) {
    input.focus();
  }
  $("#tiles")?.scrollIntoView({ block: "center" });
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
