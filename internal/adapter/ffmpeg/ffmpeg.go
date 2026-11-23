// Package ffmpeg adapts the ffmpeg/ffprobe binaries to the core ports.
// The extraction is the logic of the original FIAP X prototype (ffmpeg -vf fps=1 → PNG frames),
// now behind an interface, bound to a context and without a shell.
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/FiapXHacka/fiapx-video-processor/internal/core"
)

// framePattern keeps the prototype's file names (frame_0001.png, ...).
const framePattern = "frame_%04d.png"

// Extractor implements core.FrameExtractor.
type Extractor struct {
	Binary  string // defaults to "ffmpeg"
	Threads int    // 0 lets ffmpeg decide; set it so concurrency × threads ≈ pod vCPUs (ADR-0014)
}

var _ core.FrameExtractor = Extractor{}

// Extract writes one PNG per 1/fps seconds of video into outDir and returns their paths in order.
func (e Extractor) Extract(ctx context.Context, videoPath, outDir string, fps float64) ([]string, error) {
	if fps <= 0 {
		return nil, fmt.Errorf("fps must be > 0, got %v", fps)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if e.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(e.Threads))
	}
	args = append(args,
		"-i", videoPath,
		"-vf", "fps="+strconv.FormatFloat(fps, 'f', -1, 64),
		"-y", filepath.Join(outDir, framePattern),
	)

	// Arguments are passed directly to the process (no shell) and paths are built by the core
	// from validated ids, so user input never reaches a command line.
	cmd := exec.CommandContext(ctx, binary(e.Binary, "ffmpeg"), args...) // #nosec G204 -- see comment above
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, tail(out))
	}

	frames, err := filepath.Glob(filepath.Join(outDir, "frame_*.png"))
	if err != nil {
		return nil, fmt.Errorf("listing frames: %w", err)
	}
	sort.Strings(frames) // zero-padded names sort correctly up to 9999 frames (well above the duration limit)
	return frames, nil
}

// Probe implements core.VideoProbe using ffprobe: it inspects the content, not the file name.
type Probe struct {
	Binary string // defaults to "ffprobe"
}

var _ core.VideoProbe = Probe{}

type probeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe returns the duration of the file, or an error if it has no video stream.
func (p Probe) Probe(ctx context.Context, videoPath string) (core.VideoInfo, error) {
	cmd := exec.CommandContext(ctx, binary(p.Binary, "ffprobe"), // #nosec G204 -- fixed args, path built by the core
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type",
		"-of", "json",
		videoPath,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return core.VideoInfo{}, ctx.Err()
		}
		return core.VideoInfo{}, fmt.Errorf("ffprobe: %w", err)
	}

	var parsed probeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return core.VideoInfo{}, fmt.Errorf("parsing ffprobe output: %w", err)
	}
	hasVideo := false
	for _, s := range parsed.Streams {
		if s.CodecType == "video" {
			hasVideo = true
			break
		}
	}
	if !hasVideo {
		return core.VideoInfo{}, fmt.Errorf("no video stream found")
	}
	duration, err := strconv.ParseFloat(parsed.Format.Duration, 64)
	if err != nil {
		return core.VideoInfo{}, fmt.Errorf("invalid duration %q: %w", parsed.Format.Duration, err)
	}
	return core.VideoInfo{DurationSeconds: duration}, nil
}

func binary(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

// tail keeps error messages short: ffmpeg can print a lot.
func tail(out []byte) string {
	s := strings.TrimSpace(string(out))
	const limit = 500
	if len(s) > limit {
		return "…" + s[len(s)-limit:]
	}
	return s
}
