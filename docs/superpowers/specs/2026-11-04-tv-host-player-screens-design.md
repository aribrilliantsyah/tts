# Desain Pemisahan Tampilan: Layar TV, Layar Host, dan Layar Peserta (TTS Kuis)

## 1. Latar Belakang & Tujuan (Intent & Why)

TTS Kuis dirancang untuk acara kuis teka-teki silang interaktif secara offline di satu jaringan lokal (LAN).
Pada implementasi sebelumnya, [`web/host.html`](file:///Users/rzkfyn/Document/tts/web/host.html) berfungsi ganda sebagai:
1. Layar utama proyektor/TV untuk penonton dan peserta.
2. Panel kontrol operator (memilih soal, tombol lewati, tombol buka jawaban, kick pemain, dan pengaturan room).

Ketika layar laptop operator disambungkan ke TV/proyektor (terutama mode duplikasi/mirror):
- Penonton melihat kursor mouse bergerak dan tombol-tombol administratif.
- Host/MC tidak memiliki layar privat untuk melihat contekan kunci jawaban atau catatan petunjuk tanpa membocorkannya ke TV.
- Host tidak bisa membawa HP/tablet berkeliling panggung sambil tetap mengontrol jalannya permainan dari genggaman.

Dokumen desain ini memisahkan arsitektur tampilan menjadi **3 peran layar mandiri**:
1. **Layar TV / Proyektor (`/tv.html`)**: Layar tayang bersih untuk penonton & panggung.
2. **Layar Host / MC (`/host.html`)**: Panel kontrol mobile/desktop untuk MC dengan contekan jawaban.
3. **Layar Peserta (`/play.html`)**: Antarmuka HP peserta untuk memencet bel dan mengetik jawaban.

---

## 2. Sasaran (Goals), Non-Sasaran (Non-Goals), & Anti-Sasaran (Anti-Goals)

### Sasaran (Goals)
- **Tampilan TV Bersih (`/tv.html`)**: Bebas dari kontrol admin, kursor, atau elemen konfigurasi. Fokus pada papan TTS besar beranimasi, timer hitung mundur, status bel live, papan skor, dan QR code lobby. TV bertindak sebagai sumber suara utama (BGM & SFX) ke pengeras suara panggung.
- **TV Siap Ditampilkan Tanpa Login Admin**: Perangkat TV, smart TV, atau proyektor display dapat membuka `http://<ip>:3000/tv.html#<code>` langsung tanpa perlu memasukkan password admin (role: `tv`/`display`, read-only).
- **Panel Host Informatif (`/host.html`)**: Responsif untuk laptop maupun smartphone/tablet MC. Memiliki contekan jawaban untuk setiap nomor soal (tersembunyi dari TV) agar MC dapat memandu acara dan memberikan *clue* tambahan dengan percaya diri.
- **Kontrol Penuh Host**: Host dapat memilih soal yang ingin dibuka, melewati soal (*skip*), membuka jawaban (*reveal*), mengakhiri permainan (*end*), mengatur poin/waktu di lobby, serta mengeluarkan pemain (*kick*).
- **Peningkatan Layar Peserta (`/play.html`)**: Memberikan kejelasan status di HP peserta: siapa pemain yang sedang memencet bel/menjawab, sisa waktu menjawab, serta status skor dan peringkat live.
- **Tetap Ringan & Zero-Dependency**: Berjalan di binary Go tunggal tanpa framework JS eksternal tambahan.

### Non-Sasaran (Non-Goals)
- Mengubah aturan inti permainan TTS (misal: sistem poin, tie-breaker, atau algoritma grid generator tetap sama).
- Menambah sistem tim multi-orang (permainan tetap individu per akun/tamu).
- Menambahkan fitur video streaming atau WebRTC (semua sinkronisasi tetap menggunakan WebSocket JSON).

### Anti-Sasaran (Anti-Goals)
- Jangan sampai kunci jawaban bocor ke klien penonton (TV) atau peserta sebelum soal diselesaikan atau dibuka.
- Jangan mewajibkan perangkat TV untuk login admin (banyak smart TV/proyektor tidak memiliki keyboard yang nyaman untuk login akun).
- Jangan membuat host bergantung pada koneksi internet (semua font, ikon, avatar, dan audio tetap 100% offline).

---

## 3. Batasan Teknis (Constraints)
- **Binary Go Tunggal**: Semua file frontend di-embed lewat `embed.FS` di package `web`.
- **WebSocket Protocol**: Protokol WS di `internal/live` harus mendukung koneksi multi-klien (TV display, Host kontrol, dan para Pemain).
- **Web Audio Policy**: Browser membatasi autoplay suara sebelum ada interaksi pengguna. Layar TV harus menyediakan tombol interaksi sekali sentuh/klik ("Aktifkan Suara") di awal sebelum tayangan dimulai.

---

## 4. Pendekatan yang Dipertimbangkan (Approaches)

### Pendekatan 1: Arsitektur 3 Layar Mandiri (Dipilih)
- Memisahkan secara fisik URL frontend:
  - `/tv.html#<code>` (Display TV/Proyektor)
  - `/host.html#<code>` (Host Controller dengan autentikasi admin)
  - `/play.html#<code>` (Peserta)
- **Kelebihan**: Sangat bersih, MC fleksibel bergerak membawa HP atau mengendalikan dari balik panggung, proyektor menampilkan tayangan yang profesional setara kuis televisi.
- **Trade-off**: Perlu membagi kode tampilan host lama menjadi modul TV display dan modul kontrol host.

### Pendekatan 2: Mode Tunggal Host dengan Toggle Presentasi (Ditolak)
- Mempertahankan `/host.html` dengan tombol "Mode Fullscreen / Sembunyikan Kontrol".
- **Kekurangan**: Host tidak bisa melihat contekan jawaban secara terpisah jika layar di-mirror ke TV; MC harus berada di depan laptop proyektor untuk mengklik tombol.

---

## 5. Rincian Antarmuka & Interaksi (Detailed UI/UX)

### A. Layar TV / Proyektor (`/tv.html`)
- **Tujuan**: Visualisasi panggung untuk dinikmati penonton di TV/proyektor.
- **Fase Ruang Tunggu (Lobby)**:
  - Kode room besar (ubin bergaya TTS) & QR Code besar di tengah layar dengan petunjuk jelas: *"Pindai untuk bergabung"*.
  - Grid avatar pemain yang sudah masuk, bereaksi terhadap emoji dan uji coba suara bel meme secara interaktif.
  - Jumlah pemain yang telah bergabung.
  - Musik latar santai (*lobby BGM*).
- **Fase Bermain (Playing)**:
  - Papan TTS besar di area tengah dengan ukuran ubin adaptif (*pixel-perfect*).
  - Panel soal aktif besar di atas atau samping papan (Nomor, Arah, Panjang kata, dan Teks Petunjuk).
  - Status Bel: Indikator animasi besar saat bel terbuka (*"Siap memencet bel..."*).
  - Status Menjawab: Menampilkan avatar, nama pemain yang menekan bel pertama kali, dan hitung mundur sisa detik.
  - Papan skor mini di samping (peringkat teratas dengan skor realtime).
  - Efek suara: Bel berbunyi nyaring, suara tik-tok hitung mundur, jingle jawaban benar / salah, dan pembukaan kata.
- **Fase Selesai (Finished / Podium)**:
  - Tampilan panggung podium juara 1, 2, dan 3 beranimasi lengkap dengan efek suara sorak dan tepuk tangan.

### B. Layar Host / MC (`/host.html`)
- **Tujuan**: Panel kendali permainan untuk MC/operator (Laptop atau HP/Tablet). Wajib login admin.
- **Fase Ruang Tunggu (Lobby)**:
  - Header: Kode room, status koneksi, tombol cepat *"Buka Layar TV"*.
  - Pengaturan Game: Slider/input poin benar, penalti salah, dan batas waktu jawab.
  - Daftar Pemain: Melihat siapa saja yang terhubung, dengan tombol ✕ (*Kick*) untuk mengeluarkan pemain.
  - Tombol Aksi Utama: **"Mulai Permainan"** (aktif jika ada minimal 1 pemain).
- **Fase Bermain (Playing)**:
  - **Daftar Soal & Contekan Jawaban (Cheat Sheet)**:
    - Menampilkan semua soal Mendatar & Menurun.
    - Menampilkan teks petunjuk dan **kunci jawaban** secara eksplisit khusus untuk host (contoh: *1 Mendatar: HARIMAU (7)*).
    - Status soal: Belum dibuka, Sedang aktif, Terjawab (oleh siapa), Dilewati, atau Dibuka.
    - Host dapat mengklik soal mana pun yang belum terjawab untuk membukanya ke layar TV dan peserta.
  - **Panel Kontrol Soal Aktif**:
    - Tombol **"Lewati Soal"** (*Skip*): Menandai soal dilewati agar bisa dibuka lagi nanti.
    - Tombol **"Buka Jawaban"** (*Reveal*): Membuka jawaban di papan TV tanpa memberikan poin kepada siapa pun.
    - Indikator pemain yang sedang menjawab dan tombol darurat jika diperlukan.
    - Tombol **"Akhiri Permainan"** (*End*): Mengakhiri sesi dan menampilkan podium di TV.
- **Responsif**: Tombol-tombol berukuran sentuh nyaman (*touch-friendly*) sehingga MC bisa memegang smartphone di atas panggung untuk memandu jalannya kuis.

### C. Layar Peserta (`/play.html`)
- **Fase Ruang Tunggu**:
  - Konfirmasi nama & pemilihan avatar Big Smile.
  - Pemilihan bel suara meme unik dan tombol coba bel (mengirim reaksi ke TV).
  - Tombol reaksi instan (👋 😂 🔥 🎉 😬).
- **Fase Bermain**:
  - **Saat Bel Terbuka**: Tombol besar **"PENCET BEL"** (atau bilah Spasi) yang responsif dan bergetar (haptic feedback di HP).
  - **Saat Peserta Lain Menjawab**: Tampilan visual jelas: *"Dewi sedang menjawab..."* dengan countdown timer agar peserta lain bersiap jika Dewi salah.
  - **Saat Giliran Sendiri**: Input ubin huruf dengan huruf-huruf persilangan yang sudah terisi otomatis dari kata lain yang berpotongan. Tombol Kirim Jawaban.
  - **Skor Pribadi**: Terlihat di header setiap saat (Skor & Posisi Peringkat).

---

## 6. Protokol & Backend Engine (`internal/live` & `internal/web`)

### A. Peran Koneksi WebSocket (`Hello`)
Protokol handshake `/ws/live/{code}`:
```json
{
  "type": "hello",
  "role": "tv" | "host" | "player",
  "token": "..." // untuk player resume
}
```
1. **Role `"tv"` (Display View)**:
   - Akses publik (tidak perlu cookie admin).
   - Menerima semua broadcast event (`state`, `buzz`, `result`, `react`).
   - Tidak dapat mengirim aksi kendali permainan (hanya ping/pong untuk sinkronisasi waktu).
2. **Role `"host"` (Controller View)**:
   - Wajib memiliki session cookie admin yang valid.
   - Dapat mengirim pesan kendali: `start`, `settings`, `kick`, `open_clue`, `skip`, `reveal`, `end`.
   - Menerima state permainan lengkap dan data contekan kunci jawaban.
3. **Role `"player"` (Participant View)**:
   - Terikat pada sesi/token pemain.
   - Mengirim `buzz`, `answer`, `set_bell`, `set_avatar`, `react`.

### B. Distribusi Kunci Jawaban Host (Cheat Sheet Security)
- Kunci jawaban **tidak boleh** dikirim di snapshot publik `RoomState` yang diterima oleh TV atau Peserta.
- Untuk Host, server menyediakan kunci jawaban melalui salah satu cara terverifikasi:
  - Pada saat Host bergabung (`role: "host"`), server mengirimkan payload khusus `host_init` atau mengikutsertakan field `answer` pada setiap clue khusus untuk koneksi host terautentikasi.
  - Klien TV dan Player tetap menerima `RoomState` standar di mana `answer` bernilai `null` sampai clue tersebut `solved` atau `revealed`.

---

## 7. Rangkuman File yang Dibuat & Diperbarui

1. **[`web/tv.html`](file:///Users/rzkfyn/Document/tts/web/tv.html) & [`web/live/tv.js`](file:///Users/rzkfyn/Document/tts/web/live/tv.js)**: Halaman display TV mandiri (fokus estetika panggung, animasi, papan besar, dan audio).
2. **[`web/host.html`](file:///Users/rzkfyn/Document/tts/web/host.html) & [`web/live/host.js`](file:///Users/rzkfyn/Document/tts/web/live/host.js)**: Dikonversi menjadi panel kontrol MC yang rapi, touch-friendly, dan dilengkapi contekan jawaban per soal.
3. **[`web/play.html`](file:///Users/rzkfyn/Document/tts/web/play.html) & [`web/live/play.js`](file:///Users/rzkfyn/Document/tts/web/live/play.js)**: Penyempurnaan tampilan status menjawab peserta lain dan countdown.
4. **[`internal/live/room.go`](file:///Users/rzkfyn/Document/tts/internal/live/room.go) & [`internal/live/state.go`](file:///Users/rzkfyn/Document/tts/internal/live/state.go)**: Dukungan role `tv`, serta pengiriman kunci jawaban clue khusus ke koneksi host terverifikasi.
5. **[`internal/web/server.go`](file:///Users/rzkfyn/Document/tts/internal/web/server.go)**: Registrasi rute/file jika diperlukan (didukung otomatis oleh `web/embed.go`).

---

## 8. Hal-Hal yang Didelegasikan ke Implementor (Left to Builder)
- Penataan CSS layout responsif pada panel host untuk ukuran layar ponsel (portrait vs landscape).
- Durasi transisi animasi halus pada ubin TV saat huruf terbuka.
- Opsi tombol pintasan keyboard pada panel host (misal: tombol spasi untuk skip atau angka untuk memilih nomor soal).
