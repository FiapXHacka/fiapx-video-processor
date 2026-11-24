package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Limits protect the worker from videos that would exhaust disk or time (ADR-0018).
type Limits struct {
	MaxDurationSeconds float64 // 0 disables the check
	MaxSizeBytes       int64   // 0 disables the check
}

// Deps groups the ports the service needs.
type Deps struct {
	Storage   Storage
	Probe     VideoProbe
	Extractor FrameExtractor
	Archiver  Archiver
	Publisher EventPublisher
}

// Service is the "process uploaded video" use case.
type Service struct {
	deps    Deps
	workDir string
	limits  Limits
	log     *slog.Logger
	now     func() time.Time
}

// NewService builds the use case. workDir must be writable; each job uses its own sub directory.
func NewService(deps Deps, workDir string, limits Limits, log *slog.Logger) *Service {
	return &Service{deps: deps, workDir: workDir, limits: limits, log: log, now: time.Now}
}

// Process handles one job and tells the consumer what to do with the message:
//
//   - nil: the outcome (processed or failed) was published, ack the message.
//   - ErrMalformedJob: nothing can be reported, ack and log.
//   - any other error: transient (storage, broker, cancellation), do NOT ack, so it is redelivered.
func (s *Service) Process(ctx context.Context, job Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	log := s.log.With("video_id", job.VideoID, "user_id", job.UserID, "attempt", job.Attempt)
	key := ResultKey(job.UserID, job.VideoID)

	if s.limits.MaxSizeBytes > 0 && job.SizeBytes > s.limits.MaxSizeBytes {
		return s.reportFailure(ctx, log, job, permanent(ErrVideoTooLarge,
			fmt.Sprintf("o vídeo tem %d bytes; o limite é %d", job.SizeBytes, s.limits.MaxSizeBytes), nil))
	}

	existing, found, err := s.deps.Storage.Stat(ctx, key)
	if err != nil {
		return fmt.Errorf("checking existing result: %w", err)
	}
	if found {
		log.Info("result already in storage, republishing processed event")
		return s.deps.Publisher.Processed(ctx, job, reusedResult(key, existing))
	}

	if err := s.deps.Publisher.ProcessingStarted(ctx, job); err != nil {
		return fmt.Errorf("publishing processing-started: %w", err)
	}

	result, err := s.run(ctx, job, key)
	var failure *ProcessingError
	switch {
	case errors.As(err, &failure):
		return s.reportFailure(ctx, log, job, failure)
	case err != nil:
		log.Warn("transient failure, message will be redelivered", "error", err)
		return err
	}

	if err := s.deps.Publisher.Processed(ctx, job, result); err != nil {
		return fmt.Errorf("publishing processed: %w", err)
	}
	log.Info("video processed", "frames", result.FrameCount, "bytes", result.SizeBytes,
		"duration_ms", result.ProcessingTime.Milliseconds())
	return nil
}

func (s *Service) run(ctx context.Context, job Job, key string) (Result, error) {
	start := s.now()
	dir := filepath.Join(s.workDir, job.VideoID)
	framesDir := filepath.Join(dir, "frames")
	if err := os.MkdirAll(framesDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("creating work dir: %w", err)
	}
	// Always clean up, also on failure: frames of a long video take gigabytes.
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			s.log.Error("cleaning work dir", "dir", dir, "error", err)
		}
	}()

	source := filepath.Join(dir, "source"+safeExt(job.FileName))
	if err := s.deps.Storage.Download(ctx, job.SourceKey, source); err != nil {
		return Result{}, fmt.Errorf("downloading source: %w", err)
	}

	info, err := s.deps.Probe.Probe(ctx, source)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, permanent(ErrInvalidVideo, "o arquivo enviado não é um vídeo válido", err)
	}
	if s.limits.MaxDurationSeconds > 0 && info.DurationSeconds > s.limits.MaxDurationSeconds {
		return Result{}, permanent(ErrVideoTooLong, fmt.Sprintf(
			"o vídeo tem %.0f s; o limite é %.0f s", info.DurationSeconds, s.limits.MaxDurationSeconds), nil)
	}

	frames, err := s.deps.Extractor.Extract(ctx, source, framesDir, job.FPS)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, permanent(ErrInvalidVideo, "não foi possível extrair os frames do vídeo", err)
	}
	if len(frames) == 0 {
		return Result{}, permanent(ErrNoFramesExtracted, "nenhum frame foi extraído do vídeo", nil)
	}

	metadata := map[string]string{
		MetaFrameCount:     strconv.Itoa(len(frames)),
		MetaSourceDuration: strconv.FormatFloat(info.DurationSeconds, 'f', 3, 64),
	}
	size, err := s.deps.Storage.UploadStream(ctx, key, "application/zip", metadata, func(w io.Writer) error {
		return s.deps.Archiver.Write(w, frames)
	})
	if err != nil {
		return Result{}, fmt.Errorf("uploading result: %w", err)
	}

	return Result{
		FrameCount:      len(frames),
		DurationSeconds: info.DurationSeconds,
		ResultKey:       key,
		SizeBytes:       size,
		ProcessingTime:  s.now().Sub(start),
	}, nil
}

func (s *Service) reportFailure(ctx context.Context, log *slog.Logger, job Job, failure *ProcessingError) error {
	log.Info("video failed permanently", "error_code", failure.Code, "error", failure)
	if err := s.deps.Publisher.ProcessingFailed(ctx, job, failure); err != nil {
		return fmt.Errorf("publishing processing-failed: %w", err)
	}
	return nil
}

func reusedResult(key string, info ObjectInfo) Result {
	frames, _ := strconv.Atoi(info.Metadata[MetaFrameCount])
	duration, _ := strconv.ParseFloat(info.Metadata[MetaSourceDuration], 64)
	return Result{FrameCount: frames, DurationSeconds: duration, ResultKey: key, SizeBytes: info.SizeBytes, Reused: true}
}

// safeExt keeps the extension as a hint for ffmpeg, but never lets user input shape the path.
func safeExt(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if len(ext) < 2 || len(ext) > 6 {
		return ""
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}
