# TTS Kuis

Teka-teki silang untuk acara offline. Admin membuat kuis (editor atau impor `.md`) lalu menjalankan **room live** di layar utama. Pemain masuk dari laptop/HP masing-masing lewat Wi-Fi yang sama, menekan bel, dan mengetik jawaban. Ada juga **mode main sendiri** dengan papan peringkat waktu.

Satu file program, tanpa internet: font, avatar, dan QR sudah dibundel.

## Menjalankan

```sh
go build -o tts ./cmd/tts
ADMIN_PASSWORD='ganti-ini' ./tts
```

Terminal menampilkan alamat Wi-Fi, misalnya `http://192.168.1.10:3000`. Buka alamat itu di laptop operator, login sebagai admin, pilih kuis, klik **Mulai live**. Layar utama menampilkan kode room + QR untuk pemain.

| Env | Default | Keterangan |
|---|---|---|
| `PORT` | `3000` | Port HTTP |
| `DB_PATH` | `data.sqlite` | File database |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / `admin123` | Admin pertama, hanya dibuat jika belum ada admin |

Build untuk laptop lain: `GOOS=windows GOARCH=amd64 go build -o tts.exe ./cmd/tts` (atau `GOOS=darwin GOARCH=arm64`).

### Data contoh

```sh
go run ./cmd/seed            # isi data.sqlite (atau -db path/lain.sqlite)
```

Mengisi 4 kuis contoh dari `cmd/seed/samples/*.md` (3 terbit, "Sains Dasar" draf), 5 pemain (`sari`, `budi`, `dewi`, `rina`, `agus`, password `rahasia123`), papan peringkat solo "Kebun Binatang", dan satu riwayat live. Aman dijalankan berulang: data yang sudah ada dilewati. File contoh juga bisa dipakai untuk mencoba **Impor .md**.

## Alur live

1. **Ruang tunggu**: pemain masuk dengan kode (tamu cukup nama panggilan, pemain yang login pakai username), atur avatar, pilih bel. Admin bisa mengubah poin/penalti/waktu untuk sesi ini.
2. **Main**: admin klik petunjuk atau kotak di papan. Bel terbuka; yang pertama menekan dapat giliran mengetik (waktu default 10 detik). Benar → poin, huruf terisi. Salah/waktu habis → penalti, bel dibuka lagi untuk pemain lain. Kalau semua sudah dapat giliran, jawaban dibuka. Admin bisa **Lewati**, **Buka jawaban**, atau **Akhiri**.
3. **Selesai**: podium + peringkat, tersimpan di **Admin → Riwayat live**.

Pengaturan skor berlapis: global (Admin → Pengaturan) → per kuis (opsional) → per room (di ruang tunggu).

## Format impor `.md`

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

Hanya `judul` wajib. Satu file = satu kuis; unggah banyak file sekaligus di **Admin → Impor .md**.

## Suara bel

Taruh file `web/sounds/<id>.mp3` (ID: ding, klakson, terompet, bebek, gong, laser, kucing, drum, peluit, bel-sepeda, pop, koin) lalu build ulang. Bel tanpa file memakai suara sintetis.

## Pengembangan

- Spec & kontrak API/WebSocket: `docs/spec.md`
- Tes: `go test -race ./...`
- Avatar: Big Smile oleh Ashley Seo (CC BY 4.0) via DiceBear, dibundel di `web/vendor/avatar.js`.
