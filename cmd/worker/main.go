// Command worker is the video-processor: it consumes video.uploaded events, extracts the
// frames with ffmpeg, uploads the zip and publishes the outcome (see the fiapx repo docs).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FiapXHacka/fiapx-video-processor/internal/config"
	"github.com/FiapXHacka/fiapx-video-processor/internal/health"
)

func main() {
	if err := run(); err != nil {
		slog.Error("video-processor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).
		With("service", "video-processor")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	checks := health.NewRegistry(2 * time.Second)
	srv := &http.Server{Addr: cfg.HealthAddr, Handler: checks.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server failed", "error", err)
			stop()
		}
	}()

	// Wiring of core.NewService comes with its missing adapters, each in its own issue:
	// #4 S3 (core.Storage), #5 AMQP publisher (core.EventPublisher) and #2 the AMQP consumer
	// that calls Process for each video.uploaded. ffmpeg.Extractor, ffmpeg.Probe and
	// archive.Zip are ready to be plugged in.

	log.Info("video-processor started",
		"health_addr", cfg.HealthAddr, "concurrency", cfg.Concurrency, "fps", cfg.FramesPerSecond)
	<-ctx.Done()
	log.Info("shutdown requested, finishing in-flight work")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down health server: %w", err)
	}
	return nil
}
