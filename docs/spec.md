# TTS Kuis (Go) — Spec & Contract

Single Go binary serving a crossword quiz app for **offline LAN events**. Admin authors quizzes (editor or `.md` import) and runs **live rooms** projected on a main screen; players join from their own laptops, buzz with a personal bell, and type answers. Also keeps a **solo mode** (play a published quiz alone, leaderboard by time).

Language: all UI copy in **Bahasa Indonesia**. Module path: `tts`. Go 1.27.

## Decisions (agreed with user)

- Only **admin** creates quizzes and runs rooms (operator = admin). No operator role.
- Players: logged-in users join with their username; **guests** join with room code + nickname. Nicknames unique per room (case-insensitive).
- Individual play only (no teams).
- **Operator picks** each clue (click clue list or grid cell). Solved clues struck through; skipped clues stay pickable.
- Round: operator opens clue → buzzer opens for everyone eligible → first buzz (server arrival order) gets the turn → types answer within **answer time limit** → correct: +points, clue solved, letters fill the grid → wrong/timeout: −penalty, player locked out of this clue, buzzer reopens for the rest → ends when someone is correct or every connected player has had a turn (then answer revealed).
- No timer before someone buzzes; operator can skip any time.
- Settings: **global** (admin settings page) → optional **per-quiz override** (null = inherit) → **per-room** adjust in lobby; locked once the game starts.
  - `points_correct` default 10 (1–100), `penalty_wrong` default 5 (0–100), `answer_seconds` default 10 (5–60).
- Score can go negative. Tie-break: lower total ms spent on correct answers (buzz→submit).
- Game ends when all clues solved/revealed or operator presses "Akhiri" → podium. Ending reveals all unsolved answers.
- Answers **never** sent to player or main-screen clients before the clue is solved/revealed.
- Avatars: DiceBear **Big Smile**, rendered client-side by `web/vendor/avatar.js` (`window.TTSAvatar`). Server stores the avatar object as opaque JSON (validated only: object, ≤ 512 bytes JSON).
- Bells: player picks one in the lobby from the built-in list; each player gets a unique default; a bell taken by another player in the same room cannot be picked. Bell sound plays on the main screen and the buzzing player's laptop.
- Fully offline: fonts, avatar lib, QR all bundled. QR PNG generated server-side.
- In-memory game state (one goroutine per room). Server restart loses running games; final results persisted to SQLite.

## Layout

```
cmd/tts/main.go            flags/env, open store, seed admin, detect LAN IP, start server
internal/crossword/        DONE — layout generator + Build (numbering/validation)
internal/mdimport/         Parse(.md) → Quiz draft + per-line errors
internal/store/            SQLite (modernc.org/sqlite): users, sessions, quizzes, settings, attempts, live results
internal/auth/             bcrypt, session cookie, middleware
internal/live/             room engine (pure, clock-injected) + hub
internal/web/              HTTP routes, JSON API, WebSocket, static embed
web/                       embedded frontend (go:embed in package `web`, file web/embed.go)
```

Third-party Go deps allowed: `modernc.org/sqlite`, `github.com/coder/websocket`, `golang.org/x/crypto/bcrypt`, `github.com/skip2/go-qrcode`. Nothing else.

## Runtime config (env)

| Env | Default |
|---|---|
| `PORT` | `3000` |
| `DB_PATH` | `data.sqlite` next to the working dir |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / `admin123` (only seeded when no admin exists) |

On start, log every non-loopback IPv4 as `http://<ip>:<port>`. `GET /api/server-info` returns `{"urls":["http://192.168.1.10:3000", ...]}`.

## Data model (SQLite)

```sql
users(id INTEGER PK, username TEXT UNIQUE COLLATE NOCASE, password_hash TEXT, role TEXT CHECK(role IN('admin','player')), avatar TEXT NOT NULL DEFAULT '{}', created_at INTEGER)
sessions(token TEXT PK, user_id INTEGER REFERENCES users ON DELETE CASCADE, expires_at INTEGER)
settings(key TEXT PK, value INTEGER)          -- points_correct, penalty_wrong, answer_seconds
quizzes(id INTEGER PK, title TEXT, description TEXT, width INTEGER, height INTEGER, words TEXT /*JSON []PlacedWord*/,
        published INTEGER, points_correct INTEGER NULL, penalty_wrong INTEGER NULL, answer_seconds INTEGER NULL,
        created_by INTEGER NULL REFERENCES users ON DELETE SET NULL, created_at INTEGER, updated_at INTEGER)
attempts(user_id, quiz_id, cells TEXT DEFAULT '{}', checks INTEGER DEFAULT 0, started_at INTEGER, completed_at INTEGER NULL, PK(user_id,quiz_id))  -- solo mode
live_games(id INTEGER PK, quiz_id INTEGER NULL REFERENCES quizzes ON DELETE SET NULL, quiz_title TEXT, started_at INTEGER, ended_at INTEGER, settings TEXT /*JSON*/)
live_players(game_id INTEGER REFERENCES live_games ON DELETE CASCADE, user_id INTEGER NULL REFERENCES users ON DELETE SET NULL,
             nickname TEXT, avatar TEXT, score INTEGER, correct INTEGER, wrong INTEGER, correct_ms INTEGER, rank INTEGER)
```
All timestamps: Unix milliseconds. Foreign keys ON, WAL.

## HTTP API

All JSON. Errors: status ≥400 with `{"error":"<pesan Indonesia>"}`. Body must be `application/json` (else 415). Session cookie `tts_session` (HttpOnly, SameSite=Lax, 7 days). Auth levels: **public**, **user** (logged in), **admin**.

### Auth & profile
| Method | Path | Auth | Body → Response |
|---|---|---|---|
| POST | `/api/register` | public | `{username,password}` → `{user}` + cookie. username `^[a-zA-Z0-9_.-]{3,32}$`, password 6–200. 409 if taken |
| POST | `/api/login` | public | `{username,password}` → `{user}` + cookie. 401 `Username atau password salah` |
| POST | `/api/logout` | public | → `{ok:true}`, clears cookie |
| GET | `/api/me` | public | → `{user: User|null}` |
| PUT | `/api/me/avatar` | user | `{avatar:{...}}` → `{ok:true}` |
| GET | `/api/server-info` | public | → `{urls:[...]}` |

`User = {id, username, role: "admin"|"player", avatar: object}`

### Solo mode (published quizzes only; 404 otherwise)
| GET | `/api/quizzes` | user | → `{quizzes:[{id,title,description,width,height,wordCount,solvers,status:"new"|"playing"|"done",duration_ms|null}]}` |
| GET | `/api/quizzes/{id}` | user | starts attempt if none → `{puzzle: PublicPuzzle, attempt:{cells,checks,started_at,completed_at}, solution: {"r,c":"A"}|null}` (solution only when completed) |
| PUT | `/api/quizzes/{id}/progress` | user | `{cells:{"r,c":"A"}}` → `{ok:true}` (ignored if completed; invalid keys/values dropped) |
| POST | `/api/quizzes/{id}/check` | user | `{cells}` → `{completed, wrong:["r,c"], empty:int, completed_at|null, duration_ms|null}` |
| GET | `/api/quizzes/{id}/leaderboard` | user | → `{title, entries:[{username, avatar, duration_ms, checks, completed_at}]}` (top 50, by duration then checks) |

`PublicPuzzle = {id,title,description,width,height, cells:[{row,col,number|null}], clues:[{id:"3-down",number,direction,row,col,length,clue}]}` — **no answers**.

### Admin
| GET | `/api/admin/settings` | admin | → `{points_correct, penalty_wrong, answer_seconds}` |
| PUT | `/api/admin/settings` | admin | same shape → `{ok:true}`; validate ranges |
| POST | `/api/admin/generate` | admin | `{words:[{answer,clue}]}` → `Layout` (crossword.Layout JSON) |
| POST | `/api/admin/import` | admin | `{files:[{name, content}]}` (≤20 files, each ≤64 KB) → `{results:[{name, ok, errors:[{line,message}], quiz?: QuizDraft}]}` — parse + generate layout, **does not save** |
| GET | `/api/admin/quizzes` | admin | → `{quizzes:[{id,title,published,wordCount,players,solvers,liveGames,updated_at}]}` |
| POST | `/api/admin/quizzes` | admin | `QuizInput` → 201 `{id}` |
| GET | `/api/admin/quizzes/{id}` | admin | → `QuizFull` |
| PUT | `/api/admin/quizzes/{id}` | admin | `QuizInput` → `{ok, resetAttempts:bool}` (solo attempts deleted when placed answers/positions change; clue-only edits keep them) |
| DELETE | `/api/admin/quizzes/{id}` | admin | → `{ok}` |
| GET | `/api/admin/quizzes/{id}/results` | admin | solo results → `{title,totalCells,results:[{username,checks,started_at,completed_at,filled,duration_ms}]}` |
| GET | `/api/admin/users` | admin | → `{users:[{id,username,role,avatar,created_at,solved}]}` |
| PATCH | `/api/admin/users/{id}` | admin | `{role}` → `{ok}`; cannot demote self |
| DELETE | `/api/admin/users/{id}` | admin | → `{ok}`; cannot delete self |
| GET | `/api/admin/live-games` | admin | → `{games:[{id,quiz_title,started_at,ended_at,players:[{nickname,avatar,score,rank}]}]}` (latest 50) |

`QuizInput = {title (1–120), description (≤1000), published:bool, points_correct:int|null, penalty_wrong:int|null, answer_seconds:int|null, layout:{words:[PlacedWord]}}` — validated by `crossword.Build`.
`QuizFull = QuizInput fields + {id, width, height, words:[PlacedWord], created_at, updated_at}`.
`QuizDraft = {title, description, points_correct|null, penalty_wrong|null, answer_seconds|null, layout: Layout}`.

### Live rooms
| POST | `/api/live/rooms` | admin | `{quiz_id}` (published or not) → 201 `{code}` — 6-digit numeric code, unique among open rooms |
| GET | `/api/live/rooms/{code}` | public | → `{code, quiz_title, phase}` or 404 `Room tidak ditemukan` |
| GET | `/api/live/rooms/{code}/qr.png` | public | PNG QR of `<first server url>/#/join/<code>` |
| GET | `/ws/live/{code}` | see below | WebSocket upgrade |

## WebSocket protocol (`/ws/live/{code}`)

JSON text frames `{"type": "...", ...}`. First client message must be `hello` within 5 s, else close.

### Client → server
```jsonc
{"type":"hello","role":"host"}                                   // admin cookie required
{"type":"hello","role":"player","nickname":"dewi","avatar":{...},"token":"<resume token or omitted>"}
// player: logged-in cookie → nickname forced to username. Guest → nickname 1–20 chars, trimmed.
// token from a previous `welcome` resumes the same player (score, bell, lockouts) even after refresh.

// player
{"type":"set_bell","bell":"bebek"}
{"type":"set_avatar","avatar":{...}}                              // lobby only
{"type":"buzz"}
{"type":"answer","text":"GAJAH"}                                 // normalized server-side; length must equal clue length, else treated wrong

// host
{"type":"settings","points_correct":10,"penalty_wrong":5,"answer_seconds":10}   // lobby only
{"type":"kick","player_id":"p3"}                                 // lobby only
{"type":"start"}                                                  // lobby → playing; needs ≥1 player
{"type":"open_clue","clue_id":"3-down"}                         // playing, no clue active; clue not solved/revealed
{"type":"skip"}                                                   // closes active clue, marks it skipped (pickable later)
{"type":"reveal","clue_id":"3-down"}                            // reveal answer (no points); marks revealed
{"type":"end"}                                                    // reveal all unsolved, finish, persist results
```

### Server → client
```jsonc
{"type":"welcome","you":{"player_id":"p3","token":"<opaque>","nickname":"dewi"}}   // players only
{"type":"error","message":"Nama sudah dipakai di room ini"}                         // also used for invalid actions; non-fatal
{"type":"kicked"}                                                                      // then close
{"type":"state","state": RoomState}       // full snapshot; sent on join and after EVERY change (rooms are small)
{"type":"buzz","player_id":"p3","bell":"bebek"}                 // fire-and-forget cue to play the sound immediately
{"type":"result","player_id":"p3","correct":true,"clue_id":"3-down","delta":10}     // for animations
```

`RoomState` (identical shape for host and players, **answers only for solved/revealed clues**):
```jsonc
{
  "code":"482913",
  "phase":"lobby" | "playing" | "finished",
  "quiz":{"title":"…","width":7,"height":7,
          "cells":[{"row":0,"col":3,"number":1}],
          "clues":[{"id":"1-down","number":1,"direction":"down","row":0,"col":3,"length":7,"clue":"…",
                    "status":"open"|"skipped"|"solved"|"revealed",
                    "answer":"HARIMAU"|null, "solved_by":"p3"|null}]},
  "letters":{"0,3":"H"},              // all letters of solved/revealed clues (crossings prefilled for players)
  "settings":{"points_correct":10,"penalty_wrong":5,"answer_seconds":10},
  "players":[{"id":"p3","nickname":"dewi","avatar":{...},"bell":"bebek","score":10,
              "correct":1,"wrong":0,"connected":true,"is_guest":true}],
  "bells":["bebek","gong",...],       // full built-in list, order fixed
  "active": null | {
     "clue_id":"3-down",
     "state":"waiting" | "answering",
     "answering_player":"p3"|null,
     "deadline":1793246890461|null,    // unix ms; client computes countdown with server_now offset
     "locked_out":["p1"],              // already had a turn on this clue
  },
  "last_outcome": null | {"clue_id":"3-down","kind":"solved"|"exhausted"|"skipped","player_id":"p3"|null,"answer":"GAJAH"|null},
  "podium": null | [{"player_id":"p3","nickname":"dewi","avatar":{...},"score":30,"rank":1}],  // finished only, full ranking
  "server_now":1793246880461
}
```

`clue.status` "open" = never opened or still pickable. Players never see `answer` unless status is solved/revealed.

### Engine rules (internal/live)
- One goroutine per room owns all state; inputs arrive on a channel (client msgs, joins/leaves, timer fires). Clock injected (`Now func() time.Time` + `AfterFunc`) so tests run without sleeping.
- Buzz accepted only if: phase playing, active.state waiting, player connected, not locked out. First accepted buzz wins; later buzzes in same instant ignored (channel order = arrival order).
- On accept: state answering, deadline = now + answer_seconds; broadcast `buzz` then `state`.
- Answer from answering player: normalize; compare to clue answer. Correct → score += points_correct, correct++, correct_ms += now−buzzAt, clue solved, letters filled, active=null, last_outcome solved. Wrong → score −= penalty_wrong, wrong++, player locked out; if any connected, non-locked player remains → state waiting (buzzer reopens) else clue revealed + last_outcome exhausted + active=null.
- Timeout → same as wrong.
- Answering player disconnects → treated as wrong immediately.
- Player disconnect: keeps slot (connected=false) so token resume works; disconnected players do not count for "everyone had a turn".
- skip: active clue → status skipped, active=null, last_outcome skipped (no answer shown).
- end: all non-solved clues → revealed, phase finished, compute ranking (score desc, correct_ms asc, nickname asc), persist `live_games` + `live_players` (user_id for logged-in players), broadcast.
- Phase transitions to finished automatically when every clue is solved or revealed.
- Host disconnect: room keeps running; host can reconnect (any admin). Room is deleted 30 min after finishing, or 2 h after last activity.
- Max 50 players per room.
- Bells: built-in ids `["ding","klakson","terompet","bebek","gong","laser","kucing","drum","peluit","bel-sepeda","pop","koin"]`. New player gets first free id in list order. `set_bell` to a taken id → error `Bel sudah dipakai pemain lain`.

## Markdown import format

One quiz per file:

```md
---
judul: Kebun Binatang
deskripsi: 5 hewan bersembunyi di grid
poin_benar: 10
penalti_salah: 5
batas_waktu: 10
---

- HARIMAU: Raja hutan yang belang
- KUDA: Ditunggangi joki di pacuan
```

- Front matter between first two `---` lines. Keys case-insensitive. `judul` required. Numeric keys optional; out-of-range → error. Unknown keys → error (catches typos). Inline `# comment` after a value allowed.
- Word lines: `- JAWABAN: petunjuk` or `* JAWABAN: petunjuk`. Split on first `:`. Answer normalized via `crossword.Normalize` (min 2 letters). Clue trimmed, non-empty, ≤300 chars.
- Blank lines and lines starting with `#` (markdown headings / comments) after front matter are ignored. Any other non-blank line → error `baris tidak dikenali`.
- Duplicate answers → error on the second occurrence.
- Errors carry 1-based line numbers. File with any error → `ok:false` (still return quiz draft if title + ≥1 word parsed, for preview).
- After parsing, layout via `crossword.Generate`; unplaced words reported in `layout.unplaced` (not an error, shown as warning in UI).

## Frontend

Plain HTML/CSS/JS, no build step, no CDN. Shared files (DONE, do not fork): `web/theme.css` (tokens, ubin/tile, buttons, chips, panels), `web/common.js` (ES module: `api`, `esc`, `toast`, `formatDuration`, `formatDate`, `avatarImg`, `Bells`), `web/vendor/avatar.js`, `web/fonts/*`.

Pages (hash routing within each HTML):
- `web/index.html` + `web/app.js` — login/register, solo list/play/leaderboard, admin (quizzes, editor, import, settings, users, live history), profile avatar editor. Admin "Mulai live" on a quiz → POST room → open `/host.html#<code>` in a new tab.
- `web/host.html` + `web/live/host.js` — main screen (projected): lobby (QR, code, URL, joined players with avatars + bells, settings adjust, Start) → playing (big grid, clue lists clickable, active clue panel with buzz state + countdown, scoreboard, skip/reveal/end) → podium.
- `web/play.html` + `web/live/play.js` — player: join form (code, nickname for guests, avatar editor, bell picker with preview) → lobby wait → big BEL button (Space key too) + active clue + status → answer tiles (crossing letters prefilled, type to fill empties, Enter submits, countdown) → podium/own rank.
- Visual language: approved "fun" mockup — sky background, navy 3px outlines, tile shadows `0 4px 0 navy`, sun/tomato/mint accents, Bagel Fat One display + Gabarito body. Respect `prefers-reduced-motion`.
