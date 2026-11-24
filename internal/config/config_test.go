package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		WorkDir: "/work", FramesPerSecond: 1, Concurrency: 2,
		MaxVideoDuration: 10 * time.Minute, MaxVideoSizeBytes: 500 << 20,
		FFmpegThreads: 1, FFmpegTimeout: 15 * time.Minute,
		HealthAddr: ":8080", ShutdownTimeout: 9 * time.Minute, LogLevel: slog.LevelInfo,
	}
	if cfg != want {
		t.Fatalf("Load() = %+v, want %+v", cfg, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"WORK_DIR": "/tmp/w", "FRAMES_PER_SECOND": "0.5", "WORKER_CONCURRENCY": "4",
		"MAX_VIDEO_DURATION": "30m", "MAX_VIDEO_SIZE_BYTES": "1024", "FFMPEG_THREADS": "2",
		"FFMPEG_TIMEOUT": "90s", "HEALTH_ADDR": ":9090", "SHUTDOWN_TIMEOUT": "1m", "LOG_LEVEL": "debug",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.WorkDir != "/tmp/w" || cfg.FramesPerSecond != 0.5 || cfg.Concurrency != 4 ||
		cfg.MaxVideoDuration != 30*time.Minute || cfg.MaxVideoSizeBytes != 1024 || cfg.FFmpegThreads != 2 ||
		cfg.FFmpegTimeout != 90*time.Second || cfg.HealthAddr != ":9090" ||
		cfg.ShutdownTimeout != time.Minute || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("Load() = %+v", cfg)
	}
}

func TestLoad_ReportsAllInvalidValues(t *testing.T) {
	_, err := Load(env(map[string]string{
		"FRAMES_PER_SECOND":  "abc",
		"WORKER_CONCURRENCY": "0",
		"FFMPEG_TIMEOUT":     "15",
		"LOG_LEVEL":          "verbose",
		"FFMPEG_THREADS":     "-1",
	}))
	if err == nil {
		t.Fatal("Load() want error")
	}
	for _, key := range []string{"FRAMES_PER_SECOND", "WORKER_CONCURRENCY", "FFMPEG_TIMEOUT", "LOG_LEVEL", "FFMPEG_THREADS"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}
