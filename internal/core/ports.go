package core

import (
	"context"
	"io"
)

// ObjectInfo is what the use case needs to know about an object that already exists.
type ObjectInfo struct {
	SizeBytes int64
	Metadata  map[string]string
}

// Storage is the object store holding uploaded videos and generated zips (S3 / MinIO).
type Storage interface {
	// Download copies the object at key to the local file dst.
	Download(ctx context.Context, key, dst string) error
	// Stat returns found=false (and no error) when the object does not exist.
	Stat(ctx context.Context, key string) (info ObjectInfo, found bool, err error)
	// UploadStream uploads whatever write produces, without buffering the whole object,
	// and returns the number of bytes stored.
	UploadStream(ctx context.Context, key, contentType string, metadata map[string]string, write func(io.Writer) error) (int64, error)
}

// VideoInfo is what probing a file tells us before spending CPU on extraction.
type VideoInfo struct {
	DurationSeconds float64
}

// VideoProbe inspects the content (not the file name) of a video.
type VideoProbe interface {
	Probe(ctx context.Context, videoPath string) (VideoInfo, error)
}

// FrameExtractor writes one image per 1/fps seconds into outDir and returns the paths in order.
type FrameExtractor interface {
	Extract(ctx context.Context, videoPath, outDir string, fps float64) ([]string, error)
}

// Archiver packs files into a single archive written to w.
type Archiver interface {
	Write(w io.Writer, files []string) error
}

// EventPublisher publishes the outcome events. Implementations must only return nil once the
// broker confirmed the message (publisher confirms), because the input message is acked afterwards.
type EventPublisher interface {
	ProcessingStarted(ctx context.Context, job Job) error
	Processed(ctx context.Context, job Job, result Result) error
	ProcessingFailed(ctx context.Context, job Job, failure *ProcessingError) error
}
