/**
 * Voice-to-Text (Speech Recognition) helper for TTS Kuis.
 * Uses native Web Speech API (SpeechRecognition / webkitSpeechRecognition).
 */

export function isSpeechRecognitionSupported() {
  return typeof window !== "undefined" && !!(window.SpeechRecognition || window.webkitSpeechRecognition);
}

const COMMON_FILLERS = new Set([
  "JAWABANNYA",
  "JAWABAN",
  "JAWAB",
  "ADALAH",
  "KAYAKNYA",
  "MUNGKIN",
  "ITU",
  "KATA",
  "KATA-KATA",
  "COBA",
  "KIRA-KIRA",
  "KIRAKIRA",
  "SIH",
  "DONG",
  "DEH",
  "YA"
]);

/**
 * Parses raw speech recognition text into answer letters for a crossword clue.
 * 
 * @param {string} rawText - Raw transcript from speech recognition
 * @param {number} clueLen - Total length of the clue (number of letters)
 * @param {string[]} fixed - Array of length clueLen with already-revealed crossing letters, or ""
 * @param {number} empties - Number of empty letter tiles to fill
 * @returns {{ typed: string, candidate: string } | null}
 */
export function parseVoiceAnswer(rawText, clueLen, fixed = [], empties = clueLen) {
  if (!rawText || typeof rawText !== "string") return null;

  const upper = rawText.toUpperCase().trim();
  if (!upper) return null;

  // Split into word tokens, retaining only A-Z
  const rawWords = upper.split(/\s+/).map((w) => w.replace(/[^A-Z]/g, "")).filter(Boolean);
  if (rawWords.length === 0) return null;

  const fullClean = upper.replace(/[^A-Z]/g, "");

  // Build candidate strings in order of likely intent
  const candidates = [];
  const addCandidate = (cand) => {
    if (cand && !candidates.includes(cand)) candidates.push(cand);
  };

  // 1. Full clean string (e.g. "AIR TERJUN" -> "AIRTERJUN", "JAKARTA" -> "JAKARTA")
  addCandidate(fullClean);

  // 2. Candidate without common Indonesian answer filler prefixes
  const wordsWithoutFillers = rawWords.filter((w) => !COMMON_FILLERS.has(w));
  if (wordsWithoutFillers.length > 0 && wordsWithoutFillers.length < rawWords.length) {
    addCandidate(wordsWithoutFillers.join(""));
    for (const w of wordsWithoutFillers) addCandidate(w);
  }

  // 3. Individual words
  for (const w of rawWords) addCandidate(w);

  // 4. Consecutive word combinations (sliding windows)
  for (let span = 2; span <= rawWords.length; span++) {
    for (let start = 0; start <= rawWords.length - span; start++) {
      addCandidate(rawWords.slice(start, start + span).join(""));
    }
  }

  const safeFixed = Array.isArray(fixed) && fixed.length === clueLen ? fixed : Array.from({ length: clueLen }, () => "");

  // Priority 1: Candidate length matches clueLen AND matches all non-empty fixed letters
  for (const cand of candidates) {
    if (cand.length === clueLen) {
      let matches = true;
      for (let i = 0; i < clueLen; i++) {
        if (safeFixed[i] && safeFixed[i] !== cand[i]) {
          matches = false;
          break;
        }
      }
      if (matches) {
        let typed = "";
        for (let i = 0; i < clueLen; i++) {
          if (!safeFixed[i]) typed += cand[i];
        }
        return { typed, candidate: cand };
      }
    }
  }

  // Priority 2: Candidate length matches clueLen (participant spoke the full word)
  for (const cand of candidates) {
    if (cand.length === clueLen) {
      let typed = "";
      for (let i = 0; i < clueLen; i++) {
        if (!safeFixed[i]) typed += cand[i];
      }
      return { typed, candidate: cand };
    }
  }

  // Priority 3: Candidate length matches exact empties count
  for (const cand of candidates) {
    if (cand.length === empties) {
      return { typed: cand, candidate: cand };
    }
  }

  // Priority 4: Best partial candidate (closest length <= clueLen or first candidate sliced)
  const candidatePool = candidates.filter((c) => c.length > 0);
  const bestPartial = candidatePool.find((c) => c.length <= clueLen) || candidatePool[0];
  if (bestPartial) {
    const typed = bestPartial.slice(0, empties);
    return { typed, candidate: bestPartial };
  }

  return null;
}

/**
 * Creates a SpeechRecognition controller with clean start/stop lifecycle.
 */
export function createVoiceRecognizer({
  lang = "id-ID",
  onStart = () => {},
  onInterim = () => {},
  onResult = () => {},
  onError = () => {},
  onEnd = () => {}
} = {}) {
  const SpeechRecognition = typeof window !== "undefined"
    ? (window.SpeechRecognition || window.webkitSpeechRecognition)
    : null;

  let recognition = null;
  let active = false;
  let intentionalStop = false;

  function stop() {
    intentionalStop = true;
    active = false;
    if (!recognition) return;
    try {
      recognition.stop();
    } catch (_) {
      // ignored
    }
  }

  function abort() {
    intentionalStop = true;
    active = false;
    if (!recognition) return;
    try {
      recognition.onstart = null;
      recognition.onresult = null;
      recognition.onerror = null;
      recognition.onend = null;
      recognition.abort();
    } catch (_) {
      // ignored
    }
    recognition = null;
  }

  function start() {
    if (!SpeechRecognition) {
      onError("Browser tidak mendukung voice-to-text.", "not-supported");
      return false;
    }
    if (active) {
      // Already listening; do not stop or re-trigger!
      return true;
    }

    try {
      recognition = new SpeechRecognition();
      recognition.lang = lang;
      recognition.interimResults = true;
      recognition.continuous = true;
      recognition.maxAlternatives = 3;

      intentionalStop = false;
      active = true;

      recognition.onstart = () => {
        active = true;
        onStart();
      };

      recognition.onresult = (event) => {
        let interimTranscript = "";
        let finalTranscript = "";

        for (let i = event.resultIndex; i < event.results.length; ++i) {
          const item = event.results[i];
          const transcript = item[0]?.transcript || "";
          if (item.isFinal) {
            finalTranscript += transcript;
          } else {
            interimTranscript += transcript;
          }
        }

        if (interimTranscript) {
          onInterim(interimTranscript.trim());
        }

        if (finalTranscript) {
          onResult({ transcript: finalTranscript.trim(), isFinal: true });
        } else if (interimTranscript) {
          onResult({ transcript: interimTranscript.trim(), isFinal: false });
        }
      };

      recognition.onerror = (event) => {
        if (intentionalStop) return;
        let message = "Terjadi kesalahan suara.";
        switch (event.error) {
          case "not-allowed":
            active = false;
            if (typeof window !== "undefined" && !window.isSecureContext) {
              message = "Mikrofon diblokir browser karena koneksi belum HTTPS. Silakan buka lewat HTTPS.";
            } else {
              message = "Izin mikrofon tidak diberikan. Izinkan akses mikrofon di browser.";
            }
            onError(message, event.error);
            break;
          case "no-speech":
            // User hasn't spoken yet; keep listening without terminating.
            return;
          case "network":
            active = false;
            message = "Koneksi pengenalan suara terganggu.";
            onError(message, event.error);
            break;
          case "audio-capture":
            active = false;
            message = "Perangkat mikrofon tidak ditemukan.";
            onError(message, event.error);
            break;
          case "aborted":
            return; // clean abort, don't show error
          default:
            active = false;
            onError(message, event.error);
            break;
        }
      };

      recognition.onend = () => {
        if (!intentionalStop && active) {
          // If browser engine ended speech session unexpectedly, re-open listening session
          try {
            recognition.start();
            return;
          } catch (_) {
            // failed to restart, proceed to close
          }
        }
        active = false;
        onEnd();
      };

      recognition.start();
      return true;
    } catch (err) {
      active = false;
      onError("Gagal mengaktifkan mikrofon: " + (err.message || "error"), "start-error");
      return false;
    }
  }

  return {
    start,
    stop,
    abort,
    isListening: () => active,
    isSupported: () => !!SpeechRecognition
  };
}
