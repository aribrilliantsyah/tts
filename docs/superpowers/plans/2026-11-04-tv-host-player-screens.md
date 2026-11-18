# TV, Host, and Player Screens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Separate the live game interface into three dedicated screens: a clean TV/projector display (`/tv.html`), an MC host controller with answer cheat-sheet (`/host.html`), and an enhanced mobile player screen (`/play.html`).

**Architecture:** The server WebSocket handler (`internal/live`) is updated to support a public read-only `tv` role and securely stream answer keys only to authenticated `host` connections. The frontend is split into `web/tv.html` (pure presentation and sound engine for projector/TV), `web/host.html` (mobile-friendly admin controller for the MC), and `web/play.html` (mobile player view with answering status indicators).

**Tech Stack:** Go 1.27+, WebSockets (`github.com/coder/websocket`), Native Vanilla JS (ES Modules), Web Audio API, CSS3.

**Spec:** [`docs/superpowers/specs/2026-11-04-tv-host-player-screens-design.md`](file:///Users/rzkfyn/Document/tts/docs/superpowers/specs/2026-11-04-tv-host-player-screens-design.md)

## Global Constraints

- Single Go binary with embedded static frontend (`web/embed.go`).
- All user-facing text in Bahasa Indonesia.
- Answers must NEVER be broadcast to `tv` or `player` clients before clues are solved or revealed.
- TV screen (`/tv.html`) must not require admin login (public read-only spectator view).
- Host screen (`/host.html`) requires an admin session cookie.
- Offline-first: zero external CDN scripts or fonts.

## Review Focus

1. **Role "tv" unauthorized control:** TV clients attempting to send host commands (`start`, `open_clue`, `skip`, `reveal`, `end`, `kick`, `settings`) must be ignored or rejected without affecting room state.
2. **Answer leakage via WebSocket:** Verify that `snapshot()` sent to `tv` or `player` clients does not contain `answer` on unsolved clues.
3. **Host answer delivery:** Verify that only authenticated `host` clients receive the `host_answers` message mapping each clue ID to its answer.
4. **Multiple TV/spectator connections:** Multiple displays connecting with role `tv` should all receive synchronized updates and sound triggers without disconnecting each other.
5. **Player status visibility:** When another participant presses the buzzer, all non-answering players must see who is answering with an active countdown.

---

### Task 1: Backend WebSocket Support for Role "tv" and Host Answers

**Files:**
- Modify: `internal/live/state.go`
- Modify: `internal/live/room.go:328-364, 460-520`
- Test: `internal/live/room_test.go`

**Interfaces:**
- Consumes: `Hello{Type: "hello", Role: "tv"}`, `Hello{Type: "hello", Role: "host"}`
- Produces:
  - Role `"tv"` connection admitted into `r.clients` without admin credentials (read-only spectator).
  - `hostAnswersMsg{Type: "host_answers", Answers: map[string]string}` dispatched to host clients upon joining.
  - Rejection of game-modifying messages from `"tv"` connections.

- [ ] **Step 1: Write the failing tests in `internal/live/room_test.go`**

```go
func TestTVAndHostRole(t *testing.T) {
	g := simpleGrid()
	r := newRoom("123456", Config{QuizID: 1, Title: "Test", Words: g.Words, Settings: store.Settings{
		PointsCorrect: 10, PenaltyWrong: 5, AnswerSeconds: 10,
	}}, g, RealClock, nil, nil)
	defer r.Close()

	// 1. TV joins without admin session -> succeeds and receives state
	tvClient := NewClient()
	r.Join(tvClient, Hello{Type: "hello", Role: "tv"}, nil)
	msg := readMsg(t, tvClient.Out())
	if msg["type"] != "state" {
		t.Fatalf("expected state msg for TV, got %v", msg["type"])
	}

	// 2. TV sending host action -> rejected
	r.Message(tvClient, Msg{Type: "start"})
	errMsg := readMsg(t, tvClient.Out())
	if errMsg["type"] != "error" {
		t.Fatalf("expected error msg for unauthorized TV action, got %v", errMsg)
	}

	// 3. Host joins with admin -> receives state and host_answers
	hostClient := NewClient()
	adminUser := &store.User{ID: 1, Username: "admin", Role: store.RoleAdmin}
	r.Join(hostClient, Hello{Type: "hello", Role: "host"}, adminUser)
	
	first := readMsg(t, hostClient.Out())
	second := readMsg(t, hostClient.Out())
	
	var answersMsg map[string]any
	if first["type"] == "host_answers" {
		answersMsg = first
	} else if second["type"] == "host_answers" {
		answersMsg = second
	} else {
		t.Fatalf("expected host_answers message for host client, got %v and %v", first["type"], second["type"])
	}
	answers, ok := answersMsg["answers"].(map[string]any)
	if !ok || len(answers) == 0 {
		t.Fatalf("expected non-empty answers map, got %v", answersMsg["answers"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/live -run TestTVAndHostRole`
Expected: FAIL (Role "tv" rejected with "Peran tidak dikenal" or host_answers not received).

- [ ] **Step 3: Implement `tv` role and `host_answers` in `internal/live/room.go` and `state.go`**

In `internal/live/state.go`, add:
```go
type hostAnswersMsg struct {
	Type    string            `json:"type"`
	Answers map[string]string `json:"answers"`
}
```

In `internal/live/room.go`:
1. Add `tvClients map[*Client]struct{}` or handle in `clients map[*Client]*player` (with a distinct marker or separate map).
   A simple approach: use `clients map[*Client]*clientInfo` where info tracks kind: `kindHost`, `kindTV`, `kindPlayer`. Or keep `clients map[*Client]*player` where `nil` = host, and add `displays map[*Client]struct{}`.
2. In `r.join`:
   - `case "tv"`: add client to `r.displays` (or `clients`), send initial `stateMsg{Type: "state", State: r.snapshot()}`.
   - `case "host"`: after sending `stateMsg`, build `answers := map[string]string{}` from `r.clues` (`c.pub.ID -> c.answer`) and send `hostAnswersMsg{Type: "host_answers", Answers: answers}` to `c`.
3. In `r.broadcast` / `r.broadcastState`: ensure both players, hosts, and TV displays receive snapshots and sound/react cues.
4. In `r.message`: if client is a TV display, reject with `sendError(c, "Tampilan TV tidak dapat mengirim aksi")`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/live -run TestTVAndHostRole`
Expected: PASS

- [ ] **Step 5: Run all live tests to ensure backward compatibility**

Run: `go test -v ./internal/live/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/live/state.go internal/live/room.go internal/live/room_test.go
git commit -m "feat(live): add tv role support and host_answers message for host"
```

---

### Task 2: Dedicated TV Display Screen (`web/tv.html` & `web/live/tv.js`)

**Files:**
- Create: `web/tv.html`
- Create: `web/live/tv.js`
- Modify: `web/live/live.css`
- Test: `internal/web/server_test.go`

**Interfaces:**
- Consumes: WebSocket connection to `/ws/live/{code}` with `role: "tv"`.
- Produces: Projector-ready full-screen experience with:
  - Lobby: Big TTS code tiles, large QR code, animated player cards, lobby BGM.
  - Playing: Pixel-perfect dynamic crossword board, large active clue card, big answering countdown, realtime scores.
  - Finished: 3D animated podium, winner fanfare, full leaderboard.
  - Audio Engine: Sound effects (meme bells, tick-tock, chime, buzzer) and BGM playback through venue speakers.
  - ZERO admin/operator buttons or controls.

- [ ] **Step 1: Write tests in `internal/web/server_test.go` for `tv.html` and `tv.js`**

```go
func TestTVScreenStaticServing(t *testing.T) {
	srv := newTestServer(t)
	// Check GET /tv.html returns 200 and contains TV title and script
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/tv.html", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /tv.html, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "live/tv.js") {
		t.Fatalf("expected /tv.html to load live/tv.js")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/web -run TestTVScreenStaticServing`
Expected: FAIL (404 for `/tv.html`).

- [ ] **Step 3: Implement `web/tv.html`**

Create `web/tv.html` with clean HTML:
- Title: `Layar TV · TTS Kuis`
- Meta viewport and theme color.
- CSS links: `/theme.css`, `/live/live.css`.
- Scripts: `/vendor/avatar.js`, `/live/tv.js`.
- Top header: TTS Logo, Quiz title, Room code chip.
- Banner for reconnection.
- Main `#app` container.
- Sound unlock overlay button ("Aktifkan suara untuk TV").
- Floating volume controls (`#soundCtl`).

- [ ] **Step 4: Implement `web/live/tv.js`**

Adapted from the display components of `host.js`, retaining all visual animations and audio playback while completely removing operator controls:
- Connects via `liveSocket(code, () => ({ type: "hello", role: "tv" }), onMessage, onStatus)`.
- Preloads all audio cues and meme bells (`Sound.preload`).
- `renderLobby`: Displays large room code tiles, QR code image (`/api/live/rooms/{code}/qr.png`), player count, and animated player tiles with reaction bubbles and hop animations. (No kick button, no settings inputs, no "Mulai" button).
- `renderPlaying`: Large active clue banner, countdown timer, answering player avatar, scoreboard ranking, pixel-perfect crossword board. (No "Lewati", "Buka jawaban", or "Akhiri" buttons).
- `renderFinished`: 3D-styled animated podium for top 3 and full score list.

- [ ] **Step 5: Add TV specific styles in `web/live/live.css`**

Add CSS for `.live.tv`:
- Enlarged fonts for TV viewing distance.
- Clean presentation without interactive cursor pointer states on non-clickable tiles.
- Prominent active clue banner and countdown indicator.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -v ./internal/web -run TestTVScreenStaticServing`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add web/tv.html web/live/tv.js web/live/live.css internal/web/server_test.go
git commit -m "feat(web): add dedicated TV display screen without admin controls"
```

---

### Task 3: Upgrade Host Screen to MC Controller with Answer Cheat Sheet

**Files:**
- Modify: `web/host.html`
- Modify: `web/live/host.js`
- Modify: `web/live/live.css`
- Test: `internal/web/server_test.go`

**Interfaces:**
- Consumes: `host_answers` WebSocket message mapping `clue_id` to secret answer, `RoomState` updates.
- Produces: Touch-friendly MC Controller with:
  - Header with direct link "Buka Layar TV" (`/tv.html#<code>`).
  - Clue list showing secret answer badge `[JAWABAN]` for each clue (e.g. `1 Mendatar · HARIMAU (7)`).
  - Action buttons: Pick clue, Skip, Reveal, End Game.
  - Lobby controls: Settings, Kick player, Start Game.
  - Mobile responsive layout for MC smartphone or tablet.

- [ ] **Step 1: Write test for host screen in `internal/web/server_test.go`**

```go
func TestHostScreenLinkToTV(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/host.html", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /host.html, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it passes/fails**

Run: `go test -v ./internal/web -run TestHostScreenLinkToTV`

- [ ] **Step 3: Update `web/host.html`**

Update `web/host.html`:
- Title: `Kontrol Host · TTS Kuis`.
- Header: Add a button/link `id="openTvBtn"` with text "📺 Buka Layar TV" target `_blank`.
- Retain audio unlock and sound controls (allowing MC to test bells or mute if controlling from laptop).

- [ ] **Step 4: Update `web/live/host.js`**

1. Listen for `host_answers` message in `onMessage`:
   ```javascript
   let secretAnswers = new Map(); // clue_id -> answer
   ...
   case "host_answers": {
     for (const [id, ans] of Object.entries(msg.answers || {})) {
       secretAnswers.set(id, ans);
     }
     if (phase === "playing") renderPlaying();
     break;
   }
   ```
2. Update clue list rendering in `renderPlaying`:
   - For every clue, display the secret answer in a distinct badge:
     `<span class="chip cheat-sheet">${esc(secretAnswers.get(c.id) || c.answer || "")}</span>`
   - Make it easy for host to see and click any unsolved/skipped clue to activate it.
3. In Header:
   - Wire `#openTvBtn` to open `/tv.html#${code}` in a new tab/window.
4. Streamline layout:
   - Prioritize the clue list, active clue controls (Lewati, Buka Jawaban, Akhiri), and scoreboard so MC can easily scroll on a phone screen.

- [ ] **Step 5: Style MC Controller in `web/live/live.css`**

- Add styles for `.chip.cheat-sheet` (subtle yellow/gold badge showing the secret answer clearly to the host).
- Ensure buttons have generous touch padding (`min-height: 44px`) for phone touchscreens.
- Responsive layout adjustments for mobile screens (columns stack cleanly).

- [ ] **Step 6: Run server tests to verify**

Run: `go test -v ./internal/web -run TestHostScreenLinkToTV`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add web/host.html web/live/host.js web/live/live.css
git commit -m "feat(host): add secret answer cheat sheet and mobile controller UI"
```

---

### Task 4: Enhance Player Screen with Real-Time Buzz and Answering Status

**Files:**
- Modify: `web/play.html`
- Modify: `web/live/play.js`
- Modify: `web/live/live.css`

**Interfaces:**
- Consumes: `RoomState` updates (`active` state, `answering_player`, `deadline`, `locked_out`).
- Produces: Enhanced player view:
  - When buzzer is active: Big vibrant "BEL" button.
  - When someone else buzzed: Status card with avatar + name ("Dewi sedang menjawab...") and live countdown.
  - When player is locked out: Clear notice ("Kamu sudah mencoba soal ini, tunggu bel berikutnya").
  - Real-time score & rank chip.

- [ ] **Step 1: Inspect current `web/live/play.js` playing phase rendering**

Verify how `play.js` currently handles `active.state === "answering"`. Ensure that when `active.answering_player !== you.player_id`, an informative banner is displayed.

- [ ] **Step 2: Update `web/live/play.js` to render opponent answering status**

In `web/live/play.js` `renderPlaying()`:
- When `active.state === "answering"` and answering player is NOT self:
  - Render an opponent card:
    ```javascript
    const opponent = player(active.answering_player);
    opponentCardHtml = `
      <div class="panel opponent-card">
        <div class="row" style="justify-content:center; gap:0.5rem;">
          ${avatarImg(opponent?.avatar, 48, opponent?.nickname)}
          <div>
            <b>${esc(opponent?.nickname ?? "Peserta")}</b> sedang menjawab…
          </div>
        </div>
        ${active.deadline ? countdown(active.deadline) : ""}
      </div>`;
    ```
- When `active.locked_out.includes(you.player_id)`:
  - Display informative badge: "Kamu terkunci untuk soal ini (salah / waktu habis)".
- Update score chip in header with current rank position: e.g. `#2 · 30 poin`.

- [ ] **Step 3: Update `web/live/live.css`**

Add styling for `.opponent-card` and locked status to make it clear at a glance on mobile screens.

- [ ] **Step 4: Run Go test suite to verify no server regressions**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/play.html web/live/play.js web/live/live.css
git commit -m "feat(player): add answering opponent banner, locked notice, and rank display"
```

---

### Task 5: Admin Dashboard Launch Integration and Documentation

**Files:**
- Modify: `web/app.js:680-720`
- Modify: `README.md`
- Test: `internal/web/server_test.go`

**Interfaces:**
- Consumes: Room creation API (`POST /api/live/rooms`).
- Produces: UI in Admin Dashboard showing two distinct launch options:
  1. "Buka Layar TV (Proyektor)" (`/tv.html#<code>`).
  2. "Buka Layar Host (Remote MC)" (`/host.html#<code>`).
  Updated documentation in `README.md` explaining the 3-screen setup.

- [ ] **Step 1: Write test for room creation in `internal/web/server_test.go`**

Confirm `POST /api/live/rooms` returns `{code: "..."}` and room is accessible.

- [ ] **Step 2: Run test to verify**

Run: `go test -v ./internal/web -run TestLiveRoom`

- [ ] **Step 3: Update `web/app.js`**

In the admin room launcher:
- When room is created or opened, provide buttons:
  - **Buka Layar TV (Proyektor)** -> opens `/tv.html#<code>` in new tab.
  - **Buka Layar Host (Remote MC)** -> opens `/host.html#<code>` in new tab.
  - Display join QR and code for quick reference.

- [ ] **Step 4: Update `README.md`**

Update the guide in `README.md`:
- Explain the 3 roles: Layar TV untuk proyektor/TV, Layar Host untuk remote kontrol MC (ada contekan jawaban), Layar Peserta di HP masing-masing.
- Update quickstart instructions.

- [ ] **Step 5: Run tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/app.js README.md
git commit -m "docs & feat(admin): add dual launch options for TV and Host screens"
```

---

### Task 6: End-to-End Build and Verification

**Files:**
- Entire repository

- [ ] **Step 1: Run complete Go test suite**

Run: `go test -v -race ./...`
Expected: All tests PASS.

- [ ] **Step 2: Build executable binary**

Run: `go build -o tts ./cmd/tts`
Expected: Exit code 0, binary `tts` produced.

- [ ] **Step 3: Verify HTTP routes with curl/httptest**

Verify that all three HTML endpoints return 200 OK:
- `/tv.html`
- `/host.html`
- `/play.html`

- [ ] **Step 4: Commit any remaining adjustments**

```bash
git status
# If clean, proceed to completion
```
