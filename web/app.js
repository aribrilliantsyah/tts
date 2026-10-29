// TTS Kuis main SPA: auth, solo play, leaderboard, profile, admin. Hash routing.
import { esc, api, toast, formatDuration, formatDate, avatarImg, avatarEditor } from "/common.js";

const app = document.getElementById("app");
const nav = document.getElementById("nav");
let me = null;
// Teardown hook of the current view (timers, pending saves). Replaced on every route.
let cleanup = () => {};

const isAdmin = () => me?.role === "admin";
const norm = (s) => String(s).toUpperCase().replace(/[^A-Z]/g, "");
const DIR_LABEL = { across: "Mendatar", down: "Menurun" };
const ARROW = { across: "→", down: "↓" };

function setError(el, msg) {
  el.textContent = msg || "";
  el.hidden = !msg;
}

// ---------- Nav ----------
function renderNav(page) {
  const on = (cond) => (cond ? ' class="on" aria-current="page"' : "");
  if (!me) {
    nav.innerHTML = `<a href="#/login"${on(page === "login")}>Masuk</a><a href="#/register"${on(page === "register")}>Daftar</a>`;
    return;
  }
  nav.innerHTML = `
    <a href="#/"${on(page === "" || page === "play" || page === "leaderboard")}>Beranda</a>
    ${isAdmin() ? "" : `<a href="/play.html">Gabung live</a>`}
    ${isAdmin() ? `<a href="#/admin"${on(page === "admin")}>Admin</a>` : ""}
    <a href="#/profile" class="me${page === "profile" ? " on" : ""}" title="Profil">${avatarImg(me.avatar, 32, "")}<span>${esc(me.username)}</span></a>
    <a href="#" id="logout">Keluar</a>`;
  document.getElementById("logout").onclick = async (e) => {
    e.preventDefault();
    try {
      await api("/api/logout", { method: "POST" });
    } catch (ex) {
      toast(ex.message);
      return;
    }
    me = null;
    location.hash = "#/login";
  };
}

// ---------- Auth ----------
function authView(mode) {
  const isLogin = mode === "login";
  document.title = `${isLogin ? "Masuk" : "Daftar"} · TTS Kuis`;
  app.innerHTML = `
    <section class="auth">
      <div class="panel">
        <h1>${isLogin ? "Masuk" : "Buat akun"}</h1>
        <p class="muted">${isLogin ? "Lanjutkan teka-teki silangmu atau kelola kuis." : "Satu akun menyimpan progres, waktu terbaik, dan avatarmu."}</p>
        <form id="auth" novalidate>
          <label for="u">Username</label>
          <input class="field" id="u" type="text" autocomplete="username" required minlength="3" maxlength="32" pattern="[a-zA-Z0-9_.\\-]{3,32}">
          ${isLogin ? "" : `<p class="muted small">3–32 karakter: huruf, angka, titik, garis bawah, atau strip.</p>`}
          <label for="p">Password</label>
          <input class="field" id="p" type="password" autocomplete="${isLogin ? "current-password" : "new-password"}" required minlength="6" maxlength="200">
          <p class="error" id="err" role="alert" hidden></p>
          <div class="row" style="margin-top:1.2rem">
            <button class="btn go" type="submit">${isLogin ? "Masuk" : "Daftar"}</button>
            <a href="#/${isLogin ? "register" : "login"}">${isLogin ? "Belum punya akun? Daftar" : "Sudah punya akun? Masuk"}</a>
          </div>
        </form>
      </div>
      <div class="panel guest">
        <h2>Ikut kuis live?</h2>
        <p>Tidak perlu akun. Masukkan kode room dari layar utama dan pilih nama panggilan.</p>
        <a class="btn sun" href="/play.html">Gabung live sebagai tamu</a>
      </div>
    </section>`;
  const err = document.getElementById("err");
  document.getElementById("auth").onsubmit = async (e) => {
    e.preventDefault();
    setError(err, "");
    const username = document.getElementById("u").value.trim();
    const password = document.getElementById("p").value;
    if (!username || !password) return setError(err, "Isi username dan password.");
    const btn = e.submitter;
    if (btn) btn.disabled = true;
    try {
      const { user } = await api(`/api/${mode}`, { method: "POST", body: { username, password } });
      me = user;
      location.hash = "#/";
    } catch (ex) {
      setError(err, ex.message);
    } finally {
      if (btn) btn.disabled = false;
    }
  };
  document.getElementById("u").focus();
}

// ---------- Grid helpers ----------
function wordCells(w) {
  const len = w.length ?? w.answer.length;
  return Array.from({ length: len }, (_, i) =>
    `${w.row + (w.direction === "down" ? i : 0)},${w.col + (w.direction === "across" ? i : 0)}`);
}

/** Static board markup for placed words (answers visible). Numbering: start cells in row-major order. */
function layoutBoard(words, width, height, cell = "1.7rem") {
  const letters = new Map();
  for (const w of words) wordCells(w).forEach((k, i) => letters.set(k, w.answer[i]));
  const starts = new Map();
  [...new Set(words.map((w) => `${w.row},${w.col}`))]
    .map((k) => k.split(",").map(Number))
    .sort((a, b) => a[0] - b[0] || a[1] - b[1])
    .forEach(([r, c], i) => starts.set(`${r},${c}`, i + 1));
  let html = "";
  for (let r = 0; r < height; r++)
    for (let c = 0; c < width; c++) {
      const k = `${r},${c}`;
      html += letters.has(k)
        ? `<div class="tile open">${starts.has(k) ? `<span class="n">${starts.get(k)}</span>` : ""}${esc(letters.get(k))}</div>`
        : `<div class="tile"></div>`;
    }
  return `<div class="board-wrap"><div class="board preview" style="--cell:${cell};grid-template-columns:repeat(${width},var(--cell))" role="img" aria-label="Pratinjau grid ${width}×${height}">${html}</div></div>`;
}

/** Decorative mini grid for list cards (the list API has no cell data). Deterministic per quiz id. */
function miniGrid(q) {
  const w = Math.max(3, Math.min(q.width, 11));
  const h = Math.max(3, Math.min(q.height, 8));
  let seed = (q.id * 2654435761) >>> 0 || 1;
  const rnd = () => (seed = (Math.imul(seed, 1103515245) + 12345) >>> 0) / 4294967296;
  let html = "";
  for (let i = 0; i < w * h; i++) {
    const x = rnd();
    html += x < 0.42 ? `<i class="${x < 0.08 ? "s" : x < 0.14 ? "m" : ""}"></i>` : "<i class=\"x\"></i>";
  }
  return `<div class="mini" aria-hidden="true" style="grid-template-columns:repeat(${w},1fr)">${html}</div>`;
}

// ---------- Player: list ----------
async function listView() {
  const { quizzes } = await api("/api/quizzes");
  document.title = "Teka-teki · TTS Kuis";
  const chip = (q) => ({
    new: `<span class="chip">Baru</span>`,
    playing: `<span class="chip sun">Sedang dikerjakan</span>`,
    done: `<span class="chip mint">Selesai · ${formatDuration(q.duration_ms)}</span>`,
  })[q.status];
  const action = { new: "Latihan sendiri", playing: "Lanjutkan latihan", done: "Lihat jawaban" };
  const liveIntro = isAdmin()
    ? `<section class="live-intro panel">
        <div>
          <h2>Main bareng pakai bel</h2>
          <p>Pilih kuis lalu tekan <b>Mulai live</b>. Layar utama menampilkan kode room. Pemain gabung dari HP/laptop masing-masing, yang paling cepat menekan bel dapat giliran menjawab.</p>
        </div>
      </section>`
    : `<section class="live-intro panel">
        <div>
          <h2>Ikut kuis live</h2>
          <p>Masukkan kode room yang tampil di layar utama.</p>
        </div>
        <form class="row" id="joinLive">
          <input class="field code" id="liveCode" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" placeholder="Kode 6 angka" aria-label="Kode room" required>
          <button class="btn go" type="submit">Gabung</button>
        </form>
      </section>`;
  app.innerHTML = `
    ${liveIntro}
    <div class="page-head">
      <h1>${isAdmin() ? "Pilih kuis" : "Latihan sendiri"}</h1>
      <p class="sub">${isAdmin()
        ? "Mulai live untuk main bareng, atau coba sendiri dulu lewat latihan."
        : "Isi teka-teki sendiri tanpa bel. Waktu tercepat masuk papan peringkat."}</p>
    </div>
    <p class="error" id="err" hidden></p>
    ${quizzes.length === 0
      ? `<div class="empty panel"><h2>Belum ada teka-teki</h2><p>${isAdmin()
          ? `Buat kuis di editor atau impor file .md, lalu centang “Terbitkan”.</p><div class="row"><a class="btn go" href="#/admin/new">Buat kuis</a><a class="btn sun" href="#/admin/import">Impor .md</a></div>`
          : `Admin belum menerbitkan kuis.</p>`}</div>`
      : `<div class="booklets">${quizzes.map((q) => `
        <article class="booklet">
          <a class="booklet-cover" href="#/play/${q.id}" aria-label="${esc(action[q.status])}: ${esc(q.title)}">
            ${miniGrid(q)}
          </a>
          <div class="booklet-body">
            <div class="row">${chip(q)}</div>
            <h2><a href="#/play/${q.id}">${esc(q.title)}</a></h2>
            ${q.description ? `<p>${esc(q.description)}</p>` : ""}
            <p class="muted meta">${q.wordCount} kata · ${q.width}×${q.height} · ${q.solvers} penyelesai</p>
            <div class="row">
              ${isAdmin() ? `<button class="btn small go" type="button" data-live="${q.id}">Mulai live</button>` : ""}
              <a class="btn small ${isAdmin() || q.status === "done" ? "" : "go"}" href="#/play/${q.id}">${action[q.status]}</a>
              <a class="btn small" href="#/leaderboard/${q.id}">Peringkat</a>
            </div>
          </div>
        </article>`).join("")}</div>`}`;
  const err = document.getElementById("err");
  app.querySelectorAll("[data-live]").forEach((b) => {
    b.onclick = () => startLive(Number(b.dataset.live), b, err);
  });
  document.getElementById("joinLive")?.addEventListener("submit", (e) => {
    e.preventDefault();
    location.href = `/play.html#/join/${encodeURIComponent(document.getElementById("liveCode").value.trim())}`;
  });
}

// ---------- Player: play ----------
async function playView(id) {
  const { puzzle, attempt, solution } = await api(`/api/quizzes/${id}`);
  document.title = `${puzzle.title} · TTS Kuis`;
  const cells = { ...attempt.cells, ...(solution ?? {}) };
  let completedAt = attempt.completed_at;
  const open = new Map(puzzle.cells.map((c) => [`${c.row},${c.col}`, c]));
  const clues = puzzle.clues.map((c) => ({ ...c, keys: wordCells(c) }));
  if (clues.length === 0) throw new Error("Kuis ini belum punya petunjuk.");
  let focus = clues[0].keys[0];
  let dir = clues[0].direction;
  let wrong = new Set();
  const cellSize = `clamp(1.6rem, calc((min(100vw, 760px) - 4rem) / ${puzzle.width}), 3rem)`;

  app.innerHTML = `
    <div class="page-head row">
      <div><h1>${esc(puzzle.title)}</h1>${puzzle.description ? `<p class="sub">${esc(puzzle.description)}</p>` : ""}</div>
      <span class="spacer"></span>
      <a class="btn small" href="#/">← Semua teka-teki</a>
    </div>
    <div class="play">
      <section class="play-main">
        <div class="row toolbar">
          <span class="chip">Waktu <b id="timer">–</b></span>
          <span class="chip" id="progress"></span>
          <span class="spacer"></span>
          <button class="btn mint" id="check" type="button">Periksa jawaban</button>
        </div>
        <div class="bubble" id="current" aria-live="polite"></div>
        <div class="board-wrap"><div class="board" id="board" style="--cell:${cellSize};grid-template-columns:repeat(${puzzle.width},var(--cell))"></div></div>
        <p class="hint muted">Ketik untuk mengisi · panah untuk pindah · klik kotak yang sama atau Spasi untuk ganti arah · Tab ke petunjuk berikutnya</p>
        <p class="error" id="err" role="alert" hidden></p>
        <div id="result"></div>
      </section>
      <aside class="clue-panels">
        <section class="panel clue-panel"><h3><span class="badge across">→</span> Mendatar</h3><ol id="across"></ol></section>
        <section class="panel clue-panel"><h3><span class="badge down">↓</span> Menurun</h3><ol id="down"></ol></section>
      </aside>
    </div>`;

  const board = document.getElementById("board");
  const err = document.getElementById("err");
  const inputs = new Map();
  for (let r = 0; r < puzzle.height; r++) {
    for (let c = 0; c < puzzle.width; c++) {
      const key = `${r},${c}`;
      const tile = document.createElement("div");
      tile.className = "tile";
      const info = open.get(key);
      if (info) {
        tile.classList.add("open");
        tile.style.setProperty("--i", String(r + c));
        if (info.number) tile.insertAdjacentHTML("beforeend", `<span class="n">${info.number}</span>`);
        const input = document.createElement("input");
        input.maxLength = 1;
        input.autocomplete = "off";
        input.spellcheck = false;
        input.setAttribute("autocapitalize", "characters");
        input.setAttribute("aria-label", `Baris ${r + 1} kolom ${c + 1}`);
        input.dataset.key = key;
        tile.appendChild(input);
        inputs.set(key, input);
      }
      board.appendChild(tile);
    }
  }
  for (const d of ["across", "down"]) {
    document.getElementById(d).innerHTML = clues
      .filter((c) => c.direction === d)
      .map((c) => `<li data-id="${esc(c.id)}"><button type="button"><b>${c.number}</b><span>${esc(c.clue)} <span class="muted">(${c.length})</span></span></button></li>`)
      .join("");
  }
  const clueItems = [...document.querySelectorAll(".clue-panels li")].map((li) => [li, clues.find((c) => c.id === li.dataset.id)]);

  const activeClue = () =>
    clues.find((c) => c.direction === dir && c.keys.includes(focus)) ?? clues.find((c) => c.keys.includes(focus));

  function paint() {
    const clue = activeClue();
    dir = clue.direction;
    const inWord = new Set(clue.keys);
    let filled = 0;
    for (const [key, input] of inputs) {
      const tile = input.parentElement;
      tile.classList.toggle("focus", key === focus && !completedAt);
      tile.classList.toggle("word", inWord.has(key) && key !== focus && !completedAt);
      tile.classList.toggle("bad", wrong.has(key));
      tile.classList.toggle("solved", !!completedAt);
      input.value = cells[key] ?? "";
      input.readOnly = !!completedAt;
      if (cells[key]) filled++;
    }
    document.getElementById("progress").textContent = `${filled}/${inputs.size} terisi`;
    document.getElementById("current").innerHTML =
      `<span class="badge ${clue.direction}">${ARROW[clue.direction]}</span><b>${clue.number} ${DIR_LABEL[clue.direction]}</b><span>${esc(clue.clue)} <span class="len">(${clue.length})</span></span>`;
    for (const [li, c] of clueItems) {
      li.classList.toggle("on", c === clue);
      li.classList.toggle("filled", c.keys.every((k) => cells[k]));
    }
    inputs.get(focus).focus({ preventScroll: true });
  }

  let saveTimer = null;
  function save() {
    saveTimer = null;
    return api(`/api/quizzes/${id}/progress`, { method: "PUT", body: { cells } }).catch((e) => toast(e.message));
  }
  function scheduleSave() {
    clearTimeout(saveTimer);
    saveTimer = setTimeout(save, 600);
  }
  function flushSave() {
    if (saveTimer === null) return Promise.resolve();
    clearTimeout(saveTimer);
    return save();
  }

  function setLetter(ch) {
    cells[focus] = ch;
    wrong.delete(focus);
    const tile = inputs.get(focus).parentElement;
    tile.classList.remove("pop");
    void tile.offsetWidth;
    tile.classList.add("pop");
    step(1);
    scheduleSave();
  }

  function step(delta) {
    const [r, c] = focus.split(",").map(Number);
    const next = dir === "across" ? `${r},${c + delta}` : `${r + delta},${c}`;
    if (open.has(next)) focus = next;
  }

  function move(dr, dc) {
    let [r, c] = focus.split(",").map(Number);
    for (;;) {
      r += dr;
      c += dc;
      if (r < 0 || c < 0 || r >= puzzle.height || c >= puzzle.width) return;
      if (open.has(`${r},${c}`)) {
        focus = `${r},${c}`;
        return;
      }
    }
  }

  function jumpClue(delta) {
    const ordered = [...clues.filter((c) => c.direction === "across"), ...clues.filter((c) => c.direction === "down")];
    const i = ordered.indexOf(activeClue());
    const next = ordered[(i + delta + ordered.length) % ordered.length];
    dir = next.direction;
    focus = next.keys.find((k) => !cells[k]) ?? next.keys[0];
  }

  board.addEventListener("mousedown", (e) => {
    const key = e.target.dataset?.key;
    if (!key) return;
    e.preventDefault();
    if (key === focus) dir = dir === "across" ? "down" : "across";
    focus = key;
    paint();
  });
  board.addEventListener("animationend", (e) => e.target.classList?.remove("pop"));

  board.addEventListener("keydown", (e) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const k = e.key;
    if (/^[a-zA-Z]$/.test(k)) {
      e.preventDefault();
      if (completedAt) return;
      setLetter(k.toUpperCase());
    } else if (k === "Backspace" || k === "Delete") {
      e.preventDefault();
      if (completedAt) return;
      if (!cells[focus] && k === "Backspace") step(-1);
      delete cells[focus];
      wrong.delete(focus);
      scheduleSave();
    } else if (k === "ArrowRight") { e.preventDefault(); dir = "across"; move(0, 1); }
    else if (k === "ArrowLeft") { e.preventDefault(); dir = "across"; move(0, -1); }
    else if (k === "ArrowDown") { e.preventDefault(); dir = "down"; move(1, 0); }
    else if (k === "ArrowUp") { e.preventDefault(); dir = "down"; move(-1, 0); }
    else if (k === " ") { e.preventDefault(); dir = dir === "across" ? "down" : "across"; }
    else if (k === "Tab") { e.preventDefault(); jumpClue(e.shiftKey ? -1 : 1); }
    else return;
    paint();
  });

  // Mobile keyboards fire `input` instead of reliable keydown letters.
  board.addEventListener("input", (e) => {
    const ch = e.target.value.toUpperCase().replace(/[^A-Z]/g, "").slice(-1);
    if (ch && !completedAt) setLetter(ch);
    paint();
  });

  document.querySelector(".clue-panels").addEventListener("click", (e) => {
    const li = e.target.closest("li");
    if (!li) return;
    const c = clues.find((x) => x.id === li.dataset.id);
    dir = c.direction;
    focus = c.keys.find((k) => !cells[k]) ?? c.keys[0];
    paint();
  });

  function showDone(durationMs) {
    document.getElementById("result").innerHTML = `
      <div class="panel done">
        <h2>Selesai dalam ${formatDuration(durationMs)}!</h2>
        <p>Semua kotak benar. Bandingkan waktumu dengan pemain lain.</p>
        <a class="btn sun" href="#/leaderboard/${id}">Lihat peringkat</a>
      </div>`;
    document.getElementById("check").disabled = true;
  }

  const checkBtn = document.getElementById("check");
  checkBtn.onclick = async () => {
    setError(err, "");
    checkBtn.disabled = true;
    try {
      await flushSave();
      const r = await api(`/api/quizzes/${id}/check`, { method: "POST", body: { cells } });
      wrong = new Set(r.wrong);
      if (r.completed) {
        completedAt = r.completed_at;
        paint();
        for (const input of inputs.values()) input.parentElement.classList.add("win");
        showDone(r.duration_ms ?? completedAt - attempt.started_at);
        toast("Semua benar!");
        return;
      }
      toast(r.wrong.length || r.empty ? `${r.wrong.length} huruf salah, ${r.empty} kotak masih kosong` : "Belum lengkap");
      paint();
    } catch (e) {
      setError(err, e.message);
    }
    checkBtn.disabled = !!completedAt;
  };

  const timerEl = document.getElementById("timer");
  const tick = () => {
    timerEl.textContent = formatDuration((completedAt ?? Date.now()) - attempt.started_at);
  };
  const timer = setInterval(tick, 1000);
  tick();
  cleanup = () => {
    clearInterval(timer);
    flushSave();
  };
  if (completedAt) showDone(completedAt - attempt.started_at);
  paint();
}

// ---------- Leaderboard ----------
async function leaderboardView(id) {
  const { title, entries } = await api(`/api/quizzes/${id}/leaderboard`);
  document.title = `Peringkat ${title} · TTS Kuis`;
  const podiumOrder = [1, 0, 2].filter((i) => entries[i]);
  const rest = entries.slice(3);
  app.innerHTML = `
    <div class="page-head row">
      <div><h1>Peringkat</h1><p class="sub">${esc(title)}</p></div>
      <span class="spacer"></span>
      <a class="btn small" href="#/play/${id}">Buka teka-teki</a>
      <a class="btn small" href="#/">← Semua teka-teki</a>
    </div>
    ${entries.length === 0
      ? `<div class="empty panel"><h2>Belum ada yang selesai</h2><p>Jadilah yang pertama menyelesaikan teka-teki ini.</p><a class="btn go" href="#/play/${id}">Main sekarang</a></div>`
      : `<div class="podium">${podiumOrder.map((i) => {
          const e = entries[i];
          return `<div class="step p${i + 1}${e.username === me.username ? " you" : ""}">
            ${avatarImg(e.avatar, i === 0 ? 96 : 76, e.username)}
            <b class="name">${esc(e.username)}</b>
            <span class="time">${formatDuration(e.duration_ms)}</span>
            <div class="block"><span>${i + 1}</span></div>
          </div>`;
        }).join("")}</div>
      ${rest.length ? `
      <table class="list">
        <thead><tr><th class="num">#</th><th>Pemain</th><th class="num">Waktu</th><th class="num">Kali periksa</th><th>Selesai</th></tr></thead>
        <tbody>${rest.map((e, i) => `
          <tr${e.username === me.username ? ' class="you"' : ""}>
            <td class="num">${i + 4}</td>
            <td><span class="who">${avatarImg(e.avatar, 32, "")}${esc(e.username)}</span></td>
            <td class="num">${formatDuration(e.duration_ms)}</td>
            <td class="num">${e.checks}</td>
            <td>${formatDate(e.completed_at)}</td>
          </tr>`).join("")}</tbody>
      </table>` : ""}`}`;
}

// ---------- Profile ----------
function profileView() {
  document.title = "Profil · TTS Kuis";
  app.innerHTML = `
    <div class="page-head"><h1>Profil</h1><p class="sub">Avatarmu tampil di papan peringkat dan saat bermain live.</p></div>
    <section class="panel profile">
      <h2>${esc(me.username)} <span class="chip">${isAdmin() ? "Admin" : "Pemain"}</span></h2>
      <div id="editor"></div>
      <p class="error" id="err" role="alert" hidden></p>
      <div class="row" style="margin-top:1rem"><button class="btn go" id="save" type="button" disabled>Simpan avatar</button><span class="muted" id="state"></span></div>
    </section>`;
  const saveBtn = document.getElementById("save");
  const stateEl = document.getElementById("state");
  const err = document.getElementById("err");
  const editor = avatarEditor(document.getElementById("editor"), me.avatar, () => {
    saveBtn.disabled = false;
    stateEl.textContent = "Belum disimpan";
  });
  saveBtn.onclick = async () => {
    setError(err, "");
    saveBtn.disabled = true;
    const avatar = editor.get();
    try {
      await api("/api/me/avatar", { method: "PUT", body: { avatar } });
      me = { ...me, avatar };
      renderNav("profile");
      stateEl.textContent = "Tersimpan";
      toast("Avatar disimpan");
    } catch (e) {
      setError(err, e.message);
      saveBtn.disabled = false;
    }
  };
}

// ---------- Admin shared ----------
function adminTabs(active) {
  const tabs = [
    ["", "Kuis"],
    ["import", "Impor .md"],
    ["live", "Riwayat live"],
    ["users", "Pengguna"],
    ["settings", "Pengaturan"],
  ];
  return `<nav class="subnav" aria-label="Menu admin">${tabs.map(([k, label]) =>
    `<a href="#/admin${k ? `/${k}` : ""}"${k === active ? ' class="on" aria-current="page"' : ""}>${label}</a>`).join("")}</nav>`;
}

/** Creates a live room and opens the host screen in a new tab. */
async function startLive(quizId, btn, errEl) {
  setError(errEl, "");
  btn.disabled = true;
  // Open synchronously within the click so popup blockers allow it.
  const win = window.open("about:blank", "_blank");
  try {
    const { code } = await api("/api/live/rooms", { method: "POST", body: { quiz_id: quizId } });
    const url = `/host.html#${code}`;
    if (win) win.location.href = url;
    else window.open(url);
    toast(`Room ${code} dibuka di tab baru`);
  } catch (e) {
    win?.close();
    setError(errEl, e.message);
  } finally {
    btn.disabled = false;
  }
}

function quizBody({ title, description, published, points_correct, penalty_wrong, answer_seconds, words }) {
  return { title, description, published, points_correct, penalty_wrong, answer_seconds, layout: { words } };
}

// ---------- Admin: list ----------
async function adminListView() {
  const { quizzes } = await api("/api/admin/quizzes");
  document.title = "Kelola kuis · TTS Kuis";
  app.innerHTML = `
    ${adminTabs("")}
    <div class="page-head row">
      <h1>Kelola kuis</h1>
      <span class="spacer"></span>
      <a class="btn sun" href="#/admin/import">Impor .md</a>
      <a class="btn go" href="#/admin/new">Buat kuis baru</a>
    </div>
    <p class="error" id="err" role="alert" hidden></p>
    ${quizzes.length === 0
      ? `<div class="empty panel"><h2>Belum ada kuis</h2><p>Klik “Buat kuis baru”, isi daftar kata dan petunjuk, lalu susun gridnya otomatis. Punya banyak kuis dalam file .md? Pakai “Impor .md”.</p></div>`
      : `<div class="table-wrap"><table class="list">
      <thead><tr><th>Judul</th><th>Status</th><th class="num">Kata</th><th class="num">Pemain</th><th class="num">Selesai</th><th class="num">Game live</th><th>Diubah</th><th><span class="sr">Aksi</span></th></tr></thead>
      <tbody>${quizzes.map((q) => `
        <tr>
          <td><a href="#/admin/edit/${q.id}"><b>${esc(q.title)}</b></a></td>
          <td>${q.published ? `<span class="chip mint">Terbit</span>` : `<span class="chip">Draf</span>`}</td>
          <td class="num">${q.wordCount}</td><td class="num">${q.players}</td><td class="num">${q.solvers}</td><td class="num">${q.liveGames}</td>
          <td>${formatDate(q.updated_at)}</td>
          <td><div class="row nowrap">
            <button class="btn small go" type="button" data-live="${q.id}">Mulai live</button>
            <a class="btn small" href="#/admin/edit/${q.id}">Ubah</a>
            <a class="btn small" href="#/admin/results/${q.id}">Hasil</a>
            <button class="btn small danger" type="button" data-del="${q.id}" data-title="${esc(q.title)}">Hapus</button>
          </div></td>
        </tr>`).join("")}</tbody>
    </table></div>`}`;
  const err = document.getElementById("err");
  app.querySelectorAll("[data-live]").forEach((b) => {
    b.onclick = () => startLive(Number(b.dataset.live), b, err);
  });
  app.querySelectorAll("[data-del]").forEach((b) => {
    b.onclick = async () => {
      if (!confirm(`Hapus “${b.dataset.title}” beserta semua progres pemain?`)) return;
      setError(err, "");
      try {
        await api(`/api/admin/quizzes/${b.dataset.del}`, { method: "DELETE" });
        toast("Kuis dihapus");
        await adminListView();
      } catch (e) {
        setError(err, e.message);
      }
    };
  });
}

// ---------- Admin: editor ----------
const OVERRIDES = [
  ["points_correct", "Poin benar", 1, 100],
  ["penalty_wrong", "Penalti salah", 0, 100],
  ["answer_seconds", "Batas waktu (detik)", 5, 60],
];

/** Reads optional integer inputs; returns [values, errorMessage]. Empty → null. */
function readOverrides(prefix) {
  const out = {};
  for (const [key, label, min, max] of OVERRIDES) {
    const raw = document.getElementById(`${prefix}-${key}`).value.trim();
    if (raw === "") { out[key] = null; continue; }
    const n = Number(raw);
    if (!Number.isInteger(n) || n < min || n > max) return [null, `${label} harus bilangan bulat ${min}–${max}, atau kosongkan untuk ikut global.`];
    out[key] = n;
  }
  return [out, ""];
}

async function editorView(id) {
  const [quiz, globals] = await Promise.all([id ? api(`/api/admin/quizzes/${id}`) : null, api("/api/admin/settings")]);
  const state = {
    title: quiz?.title ?? "",
    description: quiz?.description ?? "",
    published: quiz?.published ?? false,
    rows: quiz ? quiz.words.map((w) => ({ answer: w.answer, clue: w.clue })) : [],
    layout: quiz ? { width: quiz.width, height: quiz.height, words: quiz.words, unplaced: [] } : null,
  };
  if (state.rows.length === 0) state.rows = Array.from({ length: 5 }, () => ({ answer: "", clue: "" }));
  document.title = `${id ? "Ubah kuis" : "Kuis baru"} · TTS Kuis`;

  app.innerHTML = `
    ${adminTabs("")}
    <div class="page-head row">
      <h1>${id ? "Ubah kuis" : "Kuis baru"}</h1>
      <span class="spacer"></span>
      ${id ? `<button class="btn go small" type="button" id="live">Mulai live</button><a class="btn small" href="#/admin/results/${id}">Hasil solo</a>` : ""}
      <a class="btn small" href="#/admin">← Kembali</a>
    </div>
    <div class="editor">
      <div class="editor-form">
        <section class="panel">
          <h2>Informasi</h2>
          <label for="title">Judul</label>
          <input class="field" id="title" type="text" maxlength="120" value="${esc(state.title)}" placeholder="Misal: Kebun binatang">
          <label for="desc">Deskripsi (opsional)</label>
          <textarea class="field" id="desc" rows="2" maxlength="1000" placeholder="Tema atau keterangan untuk pemain">${esc(state.description)}</textarea>
          <label class="check"><input id="pub" type="checkbox" ${state.published ? "checked" : ""}> Terbitkan untuk mode solo</label>
        </section>
        <section class="panel">
          <h2>Aturan live</h2>
          <p class="muted">Kosongkan untuk ikut pengaturan global. Masih bisa diubah lagi di lobby room.</p>
          <div class="grid3">${OVERRIDES.map(([key, label, min, max]) => `
            <div><label for="ov-${key}">${label}</label>
            <input class="field" id="ov-${key}" type="number" inputmode="numeric" min="${min}" max="${max}" step="1" value="${quiz?.[key] ?? ""}" placeholder="ikut global: ${globals[key]}"></div>`).join("")}
          </div>
        </section>
        <section class="panel">
          <h2>Kata &amp; petunjuk</h2>
          <p class="muted">Jawaban hanya huruf A–Z (spasi dan tanda baca dibuang). Grid disusun otomatis agar kata saling bersilangan.</p>
          <div class="word-rows" id="rows"></div>
          <div class="row" style="margin-top:.8rem">
            <button class="btn small" type="button" id="add">Tambah baris</button>
            <button class="btn small" type="button" id="bulk-toggle" aria-expanded="false" aria-controls="bulk">Tempel banyak</button>
          </div>
          <div id="bulk" class="bulk" hidden>
            <label for="bulk-text">Satu kata per baris, format <code>JAWABAN = petunjuk</code></label>
            <textarea class="field" id="bulk-text" rows="6" placeholder="KUCING = Hewan peliharaan yang mengeong&#10;GAJAH = Hewan berbelalai"></textarea>
            <p class="error" id="bulk-err" role="alert" hidden></p>
            <button class="btn small sun" type="button" id="bulk-add">Tambahkan ke daftar</button>
          </div>
        </section>
        <div class="row">
          <button class="btn sun" type="button" id="gen">Susun grid</button>
          <button class="btn go" type="button" id="save">Simpan</button>
        </div>
        <p class="error" id="err" role="alert" hidden></p>
      </div>
      <section class="panel preview-panel">
        <h2>Pratinjau</h2>
        <div id="preview"></div>
      </section>
    </div>`;

  const rowsEl = document.getElementById("rows");
  const err = document.getElementById("err");

  function renderRows() {
    rowsEl.innerHTML = state.rows.map((r, i) => `
      <div class="word-row">
        <input class="field" type="text" data-i="${i}" data-f="answer" value="${esc(r.answer)}" placeholder="Jawaban" aria-label="Jawaban ${i + 1}">
        <input class="field" type="text" data-i="${i}" data-f="clue" value="${esc(r.clue)}" maxlength="300" placeholder="Petunjuk" aria-label="Petunjuk ${i + 1}">
        <button class="btn small" type="button" data-rm="${i}" aria-label="Hapus baris ${i + 1}">×</button>
      </div>`).join("");
  }

  const validRows = () => state.rows.filter((r) => norm(r.answer).length >= 2 && r.clue.trim());
  const answerKey = (list) => list.map((a) => norm(a)).sort().join("|");
  const isStale = () => !state.layout || answerKey(validRows().map((r) => r.answer)) !== answerKey(state.layout.words.map((w) => w.answer));

  function renderPreview() {
    const el = document.getElementById("preview");
    const L = state.layout;
    if (!L || L.words.length === 0) {
      el.innerHTML = `<p class="muted">Isi kata dan petunjuk, lalu klik “Susun grid”.</p>`;
      return;
    }
    el.innerHTML = `
      ${isStale() ? `<p class="warn">Daftar kata berubah. Klik “Susun grid” lagi sebelum menyimpan.</p>` : ""}
      ${L.unplaced?.length ? `<p class="warn">Tidak muat (tidak ada huruf yang bisa disilangkan): ${L.unplaced.map((u) => esc(u.answer)).join(", ")}. Ganti atau hapus kata ini, lalu susun ulang.</p>` : ""}
      <p class="muted">${L.words.length} kata · ${L.width}×${L.height}</p>
      ${layoutBoard(L.words, L.width, L.height)}`;
  }

  rowsEl.addEventListener("input", (e) => {
    const { i, f } = e.target.dataset;
    if (i == null) return;
    state.rows[i][f] = e.target.value;
    renderPreview();
  });
  rowsEl.addEventListener("click", (e) => {
    const i = e.target.closest("[data-rm]")?.dataset.rm;
    if (i == null) return;
    state.rows.splice(Number(i), 1);
    if (state.rows.length === 0) state.rows.push({ answer: "", clue: "" });
    renderRows();
    renderPreview();
  });
  document.getElementById("add").onclick = () => {
    state.rows.push({ answer: "", clue: "" });
    renderRows();
    rowsEl.querySelector(`[data-i="${state.rows.length - 1}"]`).focus();
  };
  const bulk = document.getElementById("bulk");
  const bulkToggle = document.getElementById("bulk-toggle");
  bulkToggle.onclick = () => {
    bulk.hidden = !bulk.hidden;
    bulkToggle.setAttribute("aria-expanded", String(!bulk.hidden));
    if (!bulk.hidden) document.getElementById("bulk-text").focus();
  };
  document.getElementById("bulk-add").onclick = () => {
    const bulkErr = document.getElementById("bulk-err");
    const text = document.getElementById("bulk-text").value;
    const parsed = text.split("\n").map((l) => l.split(/=|\t/)).filter((p) => p.length >= 2)
      .map(([a, ...rest]) => ({ answer: a.trim(), clue: rest.join("=").trim() }))
      .filter((r) => r.answer && r.clue);
    if (parsed.length === 0) return setError(bulkErr, "Format tidak dikenali. Contoh: KUCING = Hewan peliharaan yang mengeong");
    setError(bulkErr, "");
    state.rows = [...state.rows.filter((r) => r.answer.trim() || r.clue.trim()), ...parsed];
    document.getElementById("bulk-text").value = "";
    bulk.hidden = true;
    bulkToggle.setAttribute("aria-expanded", "false");
    renderRows();
    renderPreview();
    toast(`${parsed.length} kata ditambahkan`);
  };
  document.getElementById("gen").onclick = async (e) => {
    setError(err, "");
    const words = validRows().map((r) => ({ answer: r.answer, clue: r.clue.trim() }));
    if (words.length === 0) return setError(err, "Isi minimal satu kata (≥2 huruf) beserta petunjuknya.");
    e.currentTarget.disabled = true;
    try {
      const L = await api("/api/admin/generate", { method: "POST", body: { words } });
      state.layout = { ...L, words: L.words ?? [], unplaced: L.unplaced ?? [] };
      renderPreview();
    } catch (ex) {
      setError(err, ex.message);
    } finally {
      document.getElementById("gen").disabled = false;
    }
  };
  document.getElementById("save").onclick = async () => {
    setError(err, "");
    if (isStale()) return setError(err, "Susun grid dulu setelah mengubah daftar kata.");
    if (state.layout.unplaced?.length) return setError(err, "Masih ada kata yang tidak muat di grid.");
    const [ov, ovErr] = readOverrides("ov");
    if (ovErr) return setError(err, ovErr);
    // Clue edits don't need a relayout: copy current clues onto placed words by answer.
    const pool = validRows().map((r) => ({ answer: norm(r.answer), clue: r.clue.trim() }));
    const words = state.layout.words.map((w) => {
      const [match] = pool.splice(pool.findIndex((p) => p.answer === w.answer), 1);
      return { ...w, clue: match.clue };
    });
    const body = quizBody({
      title: document.getElementById("title").value.trim(),
      description: document.getElementById("desc").value.trim(),
      published: document.getElementById("pub").checked,
      ...ov,
      words,
    });
    const btn = document.getElementById("save");
    btn.disabled = true;
    try {
      if (id) {
        const r = await api(`/api/admin/quizzes/${id}`, { method: "PUT", body });
        state.layout = { ...state.layout, words };
        toast(r.resetAttempts ? "Tersimpan. Grid berubah, progres solo pemain direset." : "Tersimpan");
      } else {
        const r = await api("/api/admin/quizzes", { method: "POST", body });
        toast("Kuis dibuat");
        location.hash = `#/admin/edit/${r.id}`;
      }
    } catch (ex) {
      setError(err, ex.message);
    } finally {
      btn.disabled = false;
    }
  };
  const liveBtn = document.getElementById("live");
  if (liveBtn) liveBtn.onclick = () => startLive(Number(id), liveBtn, err);

  renderRows();
  renderPreview();
}

// ---------- Admin: import ----------
const MD_EXAMPLE = `---
judul: Kebun Binatang
deskripsi: 5 hewan bersembunyi di grid
poin_benar: 10
penalti_salah: 5
batas_waktu: 10
---

- HARIMAU: Raja hutan yang belang
- KUDA: Ditunggangi joki di pacuan`;

function importView() {
  document.title = "Impor .md · TTS Kuis";
  let results = [];
  app.innerHTML = `
    ${adminTabs("import")}
    <div class="page-head">
      <h1>Impor kuis dari .md</h1>
      <p class="sub">Satu file satu kuis. Periksa hasilnya dulu, lalu pilih file yang mau disimpan.</p>
    </div>
    <label class="drop panel" id="drop" for="files">
      <b>Seret file .md ke sini</b>
      <span class="muted">atau klik untuk memilih (maks. 20 file, masing-masing ≤ 64 KB)</span>
      <input id="files" type="file" accept=".md,text/markdown" multiple class="sr">
    </label>
    <details class="panel format">
      <summary>Lihat contoh format</summary>
      <pre><code>${esc(MD_EXAMPLE)}</code></pre>
      <ul class="muted">
        <li>Front matter di antara dua baris <code>---</code>. <code>judul</code> wajib; kunci angka opsional.</li>
        <li>Baris kata: <code>- JAWABAN: petunjuk</code> atau <code>* JAWABAN: petunjuk</code>.</li>
        <li>Baris kosong dan baris diawali <code>#</code> diabaikan.</li>
      </ul>
    </details>
    <p class="error" id="err" role="alert" hidden></p>
    <div id="results"></div>`;
  const err = document.getElementById("err");
  const drop = document.getElementById("drop");
  const input = document.getElementById("files");
  const resultsEl = document.getElementById("results");

  async function handle(fileList) {
    setError(err, "");
    const files = [...fileList].filter((f) => f.name.toLowerCase().endsWith(".md"));
    if (files.length === 0) return setError(err, "Pilih file berakhiran .md.");
    if (files.length > 20) return setError(err, `Maksimal 20 file sekali impor (kamu memilih ${files.length}).`);
    const big = files.filter((f) => f.size > 64 * 1024);
    if (big.length) return setError(err, `File terlalu besar (maks. 64 KB): ${big.map((f) => f.name).join(", ")}`);
    resultsEl.innerHTML = `<p class="muted">Membaca ${files.length} file…</p>`;
    try {
      const payload = await Promise.all(files.map(async (f) => ({ name: f.name, content: await f.text() })));
      const res = await api("/api/admin/import", { method: "POST", body: { files: payload } });
      // Go may encode empty slices as null; normalize once here.
      results = res.results.map((r) => ({
        ...r,
        errors: r.errors ?? [],
        quiz: r.quiz ? { ...r.quiz, layout: { ...r.quiz.layout, words: r.quiz.layout?.words ?? [], unplaced: r.quiz.layout?.unplaced ?? [] } } : null,
        selected: r.ok,
        saved: null,
        saveError: "",
      }));
      render();
    } catch (e) {
      resultsEl.innerHTML = "";
      setError(err, e.message);
    }
  }

  function render() {
    const selectable = results.filter((r) => r.ok && !r.saved);
    const count = selectable.filter((r) => r.selected).length;
    resultsEl.innerHTML = `
      <div class="file-results">${results.map((r, i) => {
        const q = r.quiz;
        const L = q?.layout;
        return `
        <article class="panel file-result ${r.ok ? "ok" : "bad"}">
          <header class="row">
            ${r.ok && !r.saved
              ? `<label class="check"><input type="checkbox" data-sel="${i}" ${r.selected ? "checked" : ""}> <b>${esc(r.name)}</b></label>`
              : `<b>${esc(r.name)}</b>`}
            <span class="spacer"></span>
            ${r.saved
              ? `<span class="chip mint">Tersimpan</span><a class="btn small" href="#/admin/edit/${r.saved.id}">Buka di editor</a>`
              : r.ok ? `<span class="chip mint">Siap disimpan</span>` : `<span class="chip tomato">${r.errors.length} kesalahan</span>`}
          </header>
          ${r.errors.length ? `<ul class="err-list">${r.errors.map((e) => `<li>${e.line > 0 ? `<b>Baris ${e.line}:</b> ` : ""}${esc(e.message)}</li>`).join("")}</ul>` : ""}
          ${q ? `
            <h3>${esc(q.title)}</h3>
            ${q.description ? `<p>${esc(q.description)}</p>` : ""}
            <p class="muted">${L.words.length} kata · ${L.width}×${L.height}${OVERRIDES.filter(([k]) => q[k] != null).map(([k, label]) => ` · ${label}: ${q[k]}`).join("")}</p>
            ${L.unplaced?.length ? `<p class="warn">Tidak muat di grid dan tidak ikut disimpan: ${L.unplaced.map((u) => esc(u.answer)).join(", ")}</p>` : ""}
            ${L.words.length ? layoutBoard(L.words, L.width, L.height, "1.35rem") : ""}` : ""}
          ${r.saveError ? `<p class="error" role="alert">${esc(r.saveError)}</p>` : ""}
        </article>`;
      }).join("")}</div>
      ${selectable.length ? `
      <div class="panel save-bar row">
        <label class="check"><input type="checkbox" id="pub"> Terbitkan langsung untuk mode solo</label>
        <span class="spacer"></span>
        <button class="btn go" type="button" id="save-all" ${count ? "" : "disabled"}>Simpan ${count} kuis</button>
      </div>` : ""}`;
  }

  resultsEl.addEventListener("change", (e) => {
    const i = e.target.dataset.sel;
    if (i == null) return;
    results[i].selected = e.target.checked;
    const count = results.filter((r) => r.ok && !r.saved && r.selected).length;
    const btn = document.getElementById("save-all");
    btn.textContent = `Simpan ${count} kuis`;
    btn.disabled = count === 0;
  });
  resultsEl.addEventListener("click", async (e) => {
    if (e.target.id !== "save-all") return;
    const published = document.getElementById("pub").checked;
    e.target.disabled = true;
    let saved = 0;
    for (const r of results.filter((x) => x.ok && !x.saved && x.selected)) {
      const q = r.quiz;
      try {
        const { id } = await api("/api/admin/quizzes", {
          method: "POST",
          body: quizBody({ ...q, published, words: q.layout.words }),
        });
        r.saved = { id };
        r.saveError = "";
        saved++;
      } catch (ex) {
        r.saveError = ex.message;
      }
    }
    render();
    const pub = document.getElementById("pub");
    if (pub) pub.checked = published;
    toast(`${saved} kuis disimpan`);
  });

  input.onchange = () => {
    handle(input.files);
    input.value = "";
  };
  drop.addEventListener("dragover", (e) => {
    e.preventDefault();
    drop.classList.add("over");
  });
  drop.addEventListener("dragleave", () => drop.classList.remove("over"));
  drop.addEventListener("drop", (e) => {
    e.preventDefault();
    drop.classList.remove("over");
    handle(e.dataTransfer.files);
  });
}

// ---------- Admin: settings ----------
async function settingsView() {
  const s = await api("/api/admin/settings");
  document.title = "Pengaturan · TTS Kuis";
  app.innerHTML = `
    ${adminTabs("settings")}
    <div class="page-head"><h1>Pengaturan live</h1><p class="sub">Nilai bawaan untuk semua room. Kuis bisa punya nilai sendiri, dan tiap room masih bisa diubah di lobby.</p></div>
    <form class="panel settings" id="form" novalidate>
      <div class="grid3">${OVERRIDES.map(([key, label, min, max]) => `
        <div><label for="set-${key}">${label}</label>
        <input class="field" id="set-${key}" type="number" inputmode="numeric" required min="${min}" max="${max}" step="1" value="${s[key]}">
        <p class="muted small">${min}–${max}</p></div>`).join("")}
      </div>
      <p class="error" id="err" role="alert" hidden></p>
      <button class="btn go" type="submit">Simpan pengaturan</button>
    </form>`;
  const err = document.getElementById("err");
  document.getElementById("form").onsubmit = async (e) => {
    e.preventDefault();
    setError(err, "");
    const [vals, msg] = readOverrides("set");
    if (msg) return setError(err, msg);
    const missing = OVERRIDES.find(([k]) => vals[k] === null);
    if (missing) return setError(err, `${missing[1]} wajib diisi.`);
    try {
      await api("/api/admin/settings", { method: "PUT", body: vals });
      toast("Pengaturan disimpan");
    } catch (ex) {
      setError(err, ex.message);
    }
  };
}

// ---------- Admin: live history ----------
async function liveHistoryView() {
  const { games } = await api("/api/admin/live-games");
  document.title = "Riwayat live · TTS Kuis";
  app.innerHTML = `
    ${adminTabs("live")}
    <div class="page-head"><h1>Riwayat live</h1><p class="sub">50 game live terakhir beserta peringkat pemain.</p></div>
    ${games.length === 0
      ? `<div class="empty panel"><h2>Belum ada game live</h2><p>Buka daftar kuis lalu tekan “Mulai live”. Hasil game muncul di sini setelah game selesai.</p><a class="btn go" href="#/admin">Ke daftar kuis</a></div>`
      : `<div class="games">${games.map((g) => {
          const players = [...(g.players ?? [])].sort((a, b) => a.rank - b.rank);
          const top = players.slice(0, 5);
          return `
          <article class="panel game">
            <header class="row">
              <h3>${esc(g.quiz_title)}</h3>
              <span class="spacer"></span>
              <span class="chip">${players.length} pemain</span>
            </header>
            <p class="muted">${formatDate(g.started_at)} · durasi ${formatDuration(g.ended_at - g.started_at)}</p>
            ${top.length ? `<ol class="game-top">${top.map((p) => `
              <li class="${p.rank <= 3 ? `r${p.rank}` : ""}">
                <span class="rank">${p.rank}</span>${avatarImg(p.avatar, 40, "")}
                <span class="name">${esc(p.nickname)}</span>
                <b class="score">${p.score}</b>
              </li>`).join("")}</ol>` : `<p class="muted">Tidak ada pemain.</p>`}
            ${players.length > top.length ? `<p class="muted small">+${players.length - top.length} pemain lain</p>` : ""}
          </article>`;
        }).join("")}</div>`}`;
}

// ---------- Admin: results ----------
async function resultsView(id) {
  const { title, totalCells, results } = await api(`/api/admin/quizzes/${id}/results`);
  document.title = `Hasil ${title} · TTS Kuis`;
  app.innerHTML = `
    ${adminTabs("")}
    <div class="page-head row">
      <div><h1>Hasil solo</h1><p class="sub">${esc(title)}</p></div>
      <span class="spacer"></span>
      <a class="btn small" href="#/admin/edit/${id}">Ubah kuis</a>
      <a class="btn small" href="#/admin">← Kembali</a>
    </div>
    ${results.length === 0
      ? `<div class="empty panel"><h2>Belum ada pemain</h2><p>Belum ada yang membuka kuis ini. Pastikan statusnya “Terbit” agar muncul di daftar teka-teki pemain.</p><a class="btn" href="#/admin/edit/${id}">Buka editor</a></div>`
      : `<div class="table-wrap"><table class="list">
      <thead><tr><th>Pemain</th><th>Status</th><th class="num">Terisi</th><th class="num">Waktu</th><th class="num">Kali periksa</th><th>Mulai</th></tr></thead>
      <tbody>${results.map((r) => `
        <tr>
          <td>${esc(r.username)}</td>
          <td>${r.completed_at ? `<span class="chip mint">Selesai</span>` : `<span class="chip sun">Belum selesai</span>`}</td>
          <td class="num">${r.completed_at ? totalCells : r.filled}/${totalCells}</td>
          <td class="num">${formatDuration(r.duration_ms)}</td>
          <td class="num">${r.checks}</td>
          <td>${formatDate(r.started_at)}</td>
        </tr>`).join("")}</tbody>
    </table></div>`}`;
}

// ---------- Admin: users ----------
async function usersView() {
  const { users } = await api("/api/admin/users");
  document.title = "Pengguna · TTS Kuis";
  app.innerHTML = `
    ${adminTabs("users")}
    <div class="page-head"><h1>Pengguna</h1><p class="sub">Pemain mendaftar sendiri dari halaman Daftar. Jadikan admin untuk memberi akses kelola kuis.</p></div>
    <p class="error" id="err" role="alert" hidden></p>
    <div class="table-wrap"><table class="list">
      <thead><tr><th>Pengguna</th><th>Role</th><th class="num">Kuis selesai</th><th>Terdaftar</th><th><span class="sr">Aksi</span></th></tr></thead>
      <tbody>${users.map((u) => `
        <tr>
          <td><span class="who">${avatarImg(u.avatar, 32, "")}${esc(u.username)}</span></td>
          <td>
            <select class="field compact" data-role="${u.id}" ${u.id === me.id ? "disabled" : ""} aria-label="Role ${esc(u.username)}">
              <option value="player" ${u.role === "player" ? "selected" : ""}>Pemain</option>
              <option value="admin" ${u.role === "admin" ? "selected" : ""}>Admin</option>
            </select>
          </td>
          <td class="num">${u.solved}</td>
          <td>${formatDate(u.created_at)}</td>
          <td>${u.id === me.id ? `<span class="muted">Anda</span>` : `<button class="btn small danger" type="button" data-del="${u.id}" data-name="${esc(u.username)}">Hapus</button>`}</td>
        </tr>`).join("")}</tbody>
    </table></div>`;
  const err = document.getElementById("err");
  app.querySelectorAll("[data-role]").forEach((s) => {
    s.onchange = async () => {
      setError(err, "");
      try {
        await api(`/api/admin/users/${s.dataset.role}`, { method: "PATCH", body: { role: s.value } });
        toast("Role diperbarui");
      } catch (e) {
        await usersView();
        setError(document.getElementById("err"), e.message);
      }
    };
  });
  app.querySelectorAll("[data-del]").forEach((b) => {
    b.onclick = async () => {
      if (!confirm(`Hapus pengguna “${b.dataset.name}” beserta progresnya?`)) return;
      setError(err, "");
      try {
        await api(`/api/admin/users/${b.dataset.del}`, { method: "DELETE" });
        toast("Pengguna dihapus");
        await usersView();
      } catch (e) {
        setError(err, e.message);
      }
    };
  });
}

// ---------- Router ----------
async function route() {
  const [page = "", a, b] = location.hash.replace(/^#\/?/, "").split("/");
  cleanup();
  cleanup = () => {};
  if (page === "join") return void location.replace(`/play.html#/join/${encodeURIComponent(a ?? "")}`);
  renderNav(page);
  try {
    if (!me && page !== "login" && page !== "register") return void (location.hash = "#/login");
    if (me && (page === "login" || page === "register")) return void (location.hash = "#/");
    if (page === "admin" && !isAdmin()) return void (location.hash = "#/");
    if (page === "login" || page === "register") return authView(page);
    if (page === "play" && a) return await playView(a);
    if (page === "leaderboard" && a) return await leaderboardView(a);
    if (page === "profile") return profileView();
    if (page === "admin") {
      if (a === "new") return await editorView(null);
      if (a === "edit" && b) return await editorView(b);
      if (a === "results" && b) return await resultsView(b);
      if (a === "import") return importView();
      if (a === "settings") return await settingsView();
      if (a === "users") return await usersView();
      if (a === "live") return await liveHistoryView();
      return await adminListView();
    }
    return await listView();
  } catch (e) {
    if (e.status === 401) {
      me = null;
      location.hash = "#/login";
      return;
    }
    app.innerHTML = `<div class="empty panel"><h2>Tidak bisa memuat halaman</h2><p class="error">${esc(e.message)}</p><a class="btn" href="#/">Kembali ke daftar teka-teki</a></div>`;
  }
}

window.addEventListener("hashchange", route);
window.addEventListener("pagehide", () => cleanup());
api("/api/me")
  .then(({ user }) => { me = user; })
  .catch((e) => toast(e.message))
  .finally(route);
