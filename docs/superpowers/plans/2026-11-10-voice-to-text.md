# Voice-to-Text Feature Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a voice-to-text (SpeechRecognition) answering feature that allows participants to answer TTS crossword clues using speech in both live quiz mode (`/play.html`) and solo crossword mode (`/`).

**Architecture:** Use the browser's native Web Speech API (`SpeechRecognition` / `webkitSpeechRecognition`) with Indonesian language setting (`id-ID`) and interim transcript results. A smart parsing utility (`parseVoiceAnswer`) cleans and extracts letters, handling filler words (e.g. "jawabannya..."), matching already-placed intersecting grid letters, and handling letter-by-letter spelling. When recognized, empty letter tiles are populated in real-time and the participant can submit or edit.

**Tech Stack:** Native Vanilla JS (ES Modules), Web Speech API (`SpeechRecognition`), CSS3 animations, Go backend embedding (`embed.FS`).

**Spec:** Direct user requirement: "tambahkan fitur voice to text ketika peserta menjawab tts nya".

## Global Constraints

- Zero external third-party CDN scripts or audio APIs; use native browser Web Speech API.
- All user-facing UI text in Indonesian (Bahasa Indonesia).
- Graceful degradation: if a user's browser does not support Web Speech API, manual typing remains fully operational and an informative toast/tooltip is provided.
- Microphones must cleanly stop/abort on answer submission, timeout expiration, or unmount/navigation.
- Visual theme and tokens must match the existing retro-pop design system (`--navy`, `--sun`, `--mint`, `--tomato`, tactile borders and lift shadows).

## Review Focus

1. **Fixed letters preservation:** In live mode, some letters in the word are already revealed (fixed from crossing clues). The voice parser must correctly align spoken words with fixed letters and only fill the empty slots.
2. **Indonesian speech quirks:** Spoken inputs often include fillers like "Jawabannya gajah", "Itu komodo", or pauses/spelling ("G-A-J-A-H"). The extractor must extract the intended answer accurately.
3. **Microphone resource lifecycle:** When the answer timer ends or player submits, active speech recognition must immediately stop and not leak background audio processing.
4. **Unsupported browser / permission errors:** Browsers blocking mic or lacking `SpeechRecognition` must not throw uncaught errors or lock the answering form.
5. **Mobile responsiveness:** The mic button and live transcript indicator must fit comfortably on small screens (down to 320px) alongside the existing tiles and Kirim button.

---

### Task 1: Create Voice Recognition & Answer Extraction Utility

**Files:**
- Create: `web/voice.js`
- Test: Verify with various inputs (full word, word with fixed letters, word with prefix, spelled letters).

**Interfaces:**
- `isSpeechRecognitionSupported()`: returns boolean.
- `parseVoiceAnswer(transcript, clueLength, fixed, empties)`: extracts empty-tile letters and candidate.
- `createVoiceRecognizer(options)`: factory managing SpeechRecognition lifecycle (`onStart`, `onInterim`, `onResult`, `onError`, `onEnd`).

- [ ] **Step 1: Implement `web/voice.js`**
- [ ] **Step 2: Verify `parseVoiceAnswer` with edge cases in Node / browser console or test harness**

---

### Task 2: Live Mode Answering Voice-to-Text Integration

**Files:**
- Modify: `web/live/play.js`
- Modify: `web/live/live.css`

**Interfaces:**
- In `renderAnswer()`:
  - Add voice button (`#voiceBtn`) and live transcript feedback container (`#voiceStatus`).
  - Connect `createVoiceRecognizer` with `id-ID` language.
  - When speech produces candidate letters, update `typed`, update `#ans.value`, and invoke `paint()`.
  - Automatically stop speech recognition when the answer is sent or deadline passes.
- In `web/live/live.css`:
  - Styles for `.voice-btn`, `.voice-btn.listening` with pulsating ring/recording animation.
  - Styles for `.ans-actions` and `.voice-status` bubble.
  - Mobile responsive adjustments.

- [ ] **Step 1: Add voice UI styles and animations in `web/live/live.css`**
- [ ] **Step 2: Integrate voice recognition into `web/live/play.js` `renderAnswer()` and cleanup lifecycle**
- [ ] **Step 3: Test live answering interaction and edge cases**

---

### Task 3: Solo Play Mode Voice-to-Text Integration

**Files:**
- Modify: `web/app.js`
- Modify: `web/app.css`

**Interfaces:**
- Add voice input button in the active clue bubble `#current`.
- When participant speaks, fill the letters into the board cells for the active clue, schedule save, and show feedback toast or status.

- [ ] **Step 1: Add solo voice button styles in `web/app.css`**
- [ ] **Step 2: Integrate voice recognition into `renderPlay` in `web/app.js`**

---

### Task 4: Build, Testing, and Verification

**Files:**
- Verify with Go test suite: `go test ./...`
- Verify static assets compile into binary: `go build -o tts.exe ./cmd/tts`

- [ ] **Step 1: Run Go tests to ensure no backend regressions**
- [ ] **Step 2: Recompile binary with embedded web assets**
- [ ] **Step 3: End-to-end verification of voice-to-text UI, error states, and responsive layout**
