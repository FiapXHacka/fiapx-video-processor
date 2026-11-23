package ffmpeg

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These are integration tests with the real binaries. CI installs ffmpeg; locally they are
// skipped when it is missing so `go test ./...` still works.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

// sampleVideo generates a short synthetic video, so no binary fixture is committed.
func sampleVideo(t *testing.T, seconds int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration="+strconv.Itoa(seconds),
		"-pix_fmt", "yuv420p", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generating sample video: %v: %s", err, out)
	}
	return path
}

func TestExtractor_ExtractsOneFramePerSecond(t *testing.T) {
	requireFFmpeg(t)
	video := sampleVideo(t, 3)
	out := t.TempDir()

	frames, err := Extractor{Threads: 1}.Extract(context.Background(), video, out, 1)

	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(frames) < 3 || len(frames) > 4 { // fps filter may round the last frame
		t.Fatalf("got %d frames, want 3 (±1)", len(frames))
	}
	if filepath.Base(frames[0]) != "frame_0001.png" {
		t.Fatalf("first frame = %s", frames[0])
	}
	for i := 1; i < len(frames); i++ {
		if frames[i-1] >= frames[i] {
			t.Fatalf("frames not sorted: %v", frames)
		}
	}
}

func TestExtractor_InvalidFile(t *testing.T) {
	requireFFmpeg(t)
	bogus := filepath.Join(t.TempDir(), "not-a-video.mp4")
	if err := os.WriteFile(bogus, []byte("definitely not a video"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Extractor{}.Extract(context.Background(), bogus, t.TempDir(), 1)

	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("Extract() error = %v, want ffmpeg error", err)
	}
}

func TestExtractor_RespectsCancellation(t *testing.T) {
	requireFFmpeg(t)
	video := sampleVideo(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	_, err := Extractor{}.Extract(ctx, video, t.TempDir(), 1)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Extract() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestExtractor_RejectsInvalidFPS(t *testing.T) {
	if _, err := (Extractor{}).Extract(context.Background(), "x", t.TempDir(), 0); err == nil {
		t.Fatal("want error for fps=0")
	}
}

func TestProbe_ReturnsDuration(t *testing.T) {
	requireFFmpeg(t)
	video := sampleVideo(t, 2)

	info, err := Probe{}.Probe(context.Background(), video)

	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if math.Abs(info.DurationSeconds-2) > 0.2 {
		t.Fatalf("duration = %v, want ~2", info.DurationSeconds)
	}
}

func TestProbe_RejectsNonVideo(t *testing.T) {
	requireFFmpeg(t)
	bogus := filepath.Join(t.TempDir(), "notes.mp4")
	if err := os.WriteFile(bogus, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := (Probe{}).Probe(context.Background(), bogus); err == nil {
		t.Fatal("Probe() want error for a non-video file")
	}
}

func TestTail(t *testing.T) {
	if got := tail([]byte("  short \n")); got != "short" {
		t.Fatalf("tail = %q", got)
	}
	long := strings.Repeat("a", 600) + "END"
	got := tail([]byte(long))
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "END") || len([]rune(got)) != 501 {
		t.Fatalf("tail truncation wrong: len=%d", len([]rune(got)))
	}
}
