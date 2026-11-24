// Package core contains the video processing use case and the ports it depends on.
// It has no knowledge of RabbitMQ, S3 or ffmpeg: those are adapters (ADR-0003).
package core

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"time"
)

// Job is the work requested by a video.uploaded event (see docs/contracts/asyncapi.yaml in the fiapx repo).
type Job struct {
	MessageID   string
	VideoID     string
	UserID      string
	FileName    string
	ContentType string
	SizeBytes   int64
	SourceKey   string
	FPS         float64
	Attempt     int
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ErrMalformedJob means the message cannot be processed nor reported: it has no valid ids.
// The consumer must ack and log it instead of requeueing (it would never succeed).
var ErrMalformedJob = errors.New("malformed job")

// Validate guards the ids because they become file paths and object keys (no path traversal).
func (j Job) Validate() error {
	var errs []error
	if !uuidPattern.MatchString(j.VideoID) {
		errs = append(errs, fmt.Errorf("videoId %q is not a uuid", j.VideoID))
	}
	if !uuidPattern.MatchString(j.UserID) {
		errs = append(errs, fmt.Errorf("userId %q is not a uuid", j.UserID))
	}
	if j.SourceKey == "" {
		errs = append(errs, errors.New("source key is empty"))
	}
	if j.FPS <= 0 {
		errs = append(errs, fmt.Errorf("fps must be > 0, got %v", j.FPS))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrMalformedJob, errors.Join(errs...))
	}
	return nil
}

// ResultKey is deterministic so a redelivered job can detect it was already processed (ADR-0007).
func ResultKey(userID, videoID string) string {
	return path.Join("results", userID, videoID, "frames.zip")
}

// Object metadata written with the zip, read back when a redelivery finds an existing result.
const (
	MetaFrameCount     = "frame-count"
	MetaSourceDuration = "source-duration"
)

// ErrorCode values are part of the public event contract (video.processing-failed).
type ErrorCode string

// Error codes published in video.processing-failed (ADR-0018).
const (
	ErrInvalidVideo      ErrorCode = "INVALID_VIDEO"
	ErrVideoTooLong      ErrorCode = "VIDEO_TOO_LONG"
	ErrVideoTooLarge     ErrorCode = "VIDEO_TOO_LARGE"
	ErrNoFramesExtracted ErrorCode = "NO_FRAMES_EXTRACTED"
	ErrProcessingTimeout ErrorCode = "PROCESSING_TIMEOUT"
	ErrMaxAttempts       ErrorCode = "MAX_ATTEMPTS_EXCEEDED"
)

// ProcessingError is a permanent failure: processing the same video again gives the same result,
// so it is reported to the user and the message is acked (ADR-0018).
type ProcessingError struct {
	Code    ErrorCode
	Message string // safe to show to the user
	Err     error  // internal cause, only for logs
}

func (e *ProcessingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ProcessingError) Unwrap() error { return e.Err }

func permanent(code ErrorCode, msg string, cause error) *ProcessingError {
	return &ProcessingError{Code: code, Message: msg, Err: cause}
}

// Result describes a successfully produced zip.
type Result struct {
	FrameCount      int
	DurationSeconds float64
	ResultKey       string
	SizeBytes       int64
	ProcessingTime  time.Duration
	Reused          bool // true when a redelivery found the zip already in storage
}
