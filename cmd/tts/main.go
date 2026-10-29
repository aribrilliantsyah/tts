// Command tts serves TTS Kuis: crossword quizzes with live buzzer rooms.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"tts/internal/auth"
	"tts/internal/live"
	"tts/internal/store"
	webapi "tts/internal/web"
	"tts/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	port, err := strconv.Atoi(env("PORT", "3000"))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid PORT %q", os.Getenv("PORT"))
	}
	st, err := store.Open(env("DB_PATH", "data.sqlite"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := seedAdmin(ctx, st, env("ADMIN_USERNAME", "admin"), env("ADMIN_PASSWORD", "admin123")); err != nil {
		return err
	}
	if err := st.DeleteExpiredSessions(ctx, time.Now().UnixMilli()); err != nil {
		return err
	}

	hub := live.NewHub(live.RealClock, st)
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           webapi.New(webapi.Config{Store: st, Hub: hub, Static: web.FS, Port: port, Now: time.Now}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	urls := webapi.LANURLs(port)
	if len(urls) == 0 {
		slog.Info("no LAN IPv4 address found", "url", "http://localhost:"+strconv.Itoa(port))
	}
	for _, u := range urls {
		slog.Info("listening", "url", u)
	}

	select {
	case err := <-errc:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	hub.Close() // ends every live socket so Shutdown does not wait on them
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func seedAdmin(ctx context.Context, st *store.Store, username, password string) error {
	has, err := st.HasAdmin(ctx)
	if err != nil || has {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, username, hash, store.RoleAdmin, time.Now().UnixMilli()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("seed admin: no admin exists and username %q is taken by a player; set ADMIN_USERNAME", username)
		}
		return fmt.Errorf("seed admin: %w", err)
	}
	slog.Info("admin account created", "username", username)
	return nil
}
