package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tsocial/tsocial/internal/config"
	"github.com/tsocial/tsocial/internal/hub"
	"github.com/tsocial/tsocial/internal/ssh"
	"github.com/tsocial/tsocial/internal/store"
)

func main() {
	cfg := config.Load()

	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	if err := os.MkdirAll(filepath.Dir(cfg.DB), 0o700); err != nil {
		log.Error("mkdir db dir", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DB)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// ensure default room exists
	ctx := context.Background()
	if r, err := st.RoomByName(ctx, cfg.DefaultRoom); err != nil || r == nil {
		// create lazily on first registration; nothing to do here
		_ = r
	}

	h := hub.New()

	srv := ssh.New(cfg, st, h, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(ctx); err != nil {
			log.Error("server stopped", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
}
