package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gossh/internal/app"
	"gossh/internal/config"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	server, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("boot failed", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
