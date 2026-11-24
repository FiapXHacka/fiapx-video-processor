// Package config loads the worker settings from environment variables (12-factor).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting of the worker. Defaults follow ADR-0014 and ADR-0018.
type Config struct {
	WorkDir           string
	FramesPerSecond   float64
	Concurrency       int
	MaxVideoDuration  time.Duration
	MaxVideoSizeBytes int64
	FFmpegThreads     int
	FFmpegTimeout     time.Duration
	HealthAddr        string
	ShutdownTimeout   time.Duration
	LogLevel          slog.Level
}

// Load reads the configuration using getenv (os.Getenv in production, a map in tests)
// and reports every invalid variable at once.
func Load(getenv func(string) string) (Config, error) {
	r := reader{getenv: getenv}
	cfg := Config{
		WorkDir:           r.str("WORK_DIR", "/work"),
		FramesPerSecond:   r.float("FRAMES_PER_SECOND", 1),
		Concurrency:       r.int("WORKER_CONCURRENCY", 2),
		MaxVideoDuration:  r.duration("MAX_VIDEO_DURATION", 10*time.Minute),
		MaxVideoSizeBytes: r.int64("MAX_VIDEO_SIZE_BYTES", 500<<20),
		FFmpegThreads:     r.int("FFMPEG_THREADS", 1),
		FFmpegTimeout:     r.duration("FFMPEG_TIMEOUT", 15*time.Minute),
		HealthAddr:        r.str("HEALTH_ADDR", ":8080"),
		ShutdownTimeout:   r.duration("SHUTDOWN_TIMEOUT", 9*time.Minute),
		LogLevel:          r.level("LOG_LEVEL", slog.LevelInfo),
	}

	if cfg.FramesPerSecond <= 0 {
		r.fail("FRAMES_PER_SECOND", "must be > 0")
	}
	if cfg.Concurrency < 1 {
		r.fail("WORKER_CONCURRENCY", "must be >= 1")
	}
	if cfg.MaxVideoDuration < 0 || cfg.MaxVideoSizeBytes < 0 || cfg.FFmpegThreads < 0 {
		r.fail("MAX_VIDEO_DURATION/MAX_VIDEO_SIZE_BYTES/FFMPEG_THREADS", "must not be negative")
	}
	if cfg.FFmpegTimeout <= 0 {
		r.fail("FFMPEG_TIMEOUT", "must be > 0")
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		r.fail("WORK_DIR", "must not be empty")
	}
	return cfg, errors.Join(r.errs...)
}

type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) fail(key, msg string) {
	r.errs = append(r.errs, fmt.Errorf("%s: %s", key, msg))
}

func (r *reader) str(key, def string) string {
	if v := strings.TrimSpace(r.getenv(key)); v != "" {
		return v
	}
	return def
}

func (r *reader) int(key string, def int) int {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.fail(key, fmt.Sprintf("invalid integer %q", v))
	}
	return n
}

func (r *reader) int64(key string, def int64) int64 {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		r.fail(key, fmt.Sprintf("invalid integer %q", v))
	}
	return n
}

func (r *reader) float(key string, def float64) float64 {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.fail(key, fmt.Sprintf("invalid number %q", v))
	}
	return f
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.fail(key, fmt.Sprintf("invalid duration %q (use e.g. 90s, 15m)", v))
	}
	return d
}

func (r *reader) level(key string, def slog.Level) slog.Level {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		r.fail(key, fmt.Sprintf("invalid level %q (debug, info, warn, error)", v))
	}
	return l
}
