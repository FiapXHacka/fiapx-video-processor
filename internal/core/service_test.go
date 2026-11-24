package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	videoID = "01936f5e-7f00-7aaa-8bbb-ccccdddd0001"
	userID  = "01936f50-0000-7000-8000-000000000042"
)

func validJob() Job {
	return Job{
		MessageID: "msg-1", VideoID: videoID, UserID: userID,
		FileName: "pitch.MP4", SizeBytes: 1024, SourceKey: "uploads/u/v/original.mp4", FPS: 1, Attempt: 1,
	}
}

// --- fakes -------------------------------------------------------------------------------

type fakeStorage struct {
	existing    map[string]ObjectInfo
	statErr     error
	downloadErr error
	uploadErr   error
	downloaded  []string
	uploaded    map[string][]byte
	uploadMeta  map[string]map[string]string
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{existing: map[string]ObjectInfo{}, uploaded: map[string][]byte{}, uploadMeta: map[string]map[string]string{}}
}

func (f *fakeStorage) Download(_ context.Context, key, dst string) error {
	if f.downloadErr != nil {
		return f.downloadErr
	}
	f.downloaded = append(f.downloaded, dst)
	return os.WriteFile(dst, []byte("video bytes of "+key), 0o600)
}

func (f *fakeStorage) Stat(_ context.Context, key string) (ObjectInfo, bool, error) {
	if f.statErr != nil {
		return ObjectInfo{}, false, f.statErr
	}
	info, ok := f.existing[key]
	return info, ok, nil
}

func (f *fakeStorage) UploadStream(_ context.Context, key, _ string, meta map[string]string, write func(io.Writer) error) (int64, error) {
	if f.uploadErr != nil {
		return 0, f.uploadErr
	}
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		return 0, err
	}
	f.uploaded[key] = buf.Bytes()
	f.uploadMeta[key] = meta
	return int64(buf.Len()), nil
}

type fakeProbe struct {
	info VideoInfo
	err  error
}

func (f fakeProbe) Probe(context.Context, string) (VideoInfo, error) { return f.info, f.err }

type fakeExtractor struct {
	frames int
	err    error
	onRun  func(ctx context.Context)
	gotFPS float64
}

func (f *fakeExtractor) Extract(ctx context.Context, _ string, outDir string, fps float64) ([]string, error) {
	f.gotFPS = fps
	if f.onRun != nil {
		f.onRun(ctx)
	}
	if f.err != nil {
		return nil, f.err
	}
	var paths []string
	for i := 1; i <= f.frames; i++ {
		p := filepath.Join(outDir, fmt.Sprintf("frame_%04d.png", i))
		if err := os.WriteFile(p, []byte("png"), 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

type namesArchiver struct{}

func (namesArchiver) Write(w io.Writer, files []string) error {
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, filepath.Base(f)); err != nil {
			return err
		}
	}
	return nil
}

type fakePublisher struct {
	events    []string
	failures  []*ProcessingError
	processed []Result
	failOn    string
}

func (f *fakePublisher) record(name string) error {
	if f.failOn == name {
		return errors.New("broker unavailable")
	}
	f.events = append(f.events, name)
	return nil
}

func (f *fakePublisher) ProcessingStarted(context.Context, Job) error { return f.record("started") }

func (f *fakePublisher) Processed(_ context.Context, _ Job, r Result) error {
	if err := f.record("processed"); err != nil {
		return err
	}
	f.processed = append(f.processed, r)
	return nil
}

func (f *fakePublisher) ProcessingFailed(_ context.Context, _ Job, e *ProcessingError) error {
	if err := f.record("failed"); err != nil {
		return err
	}
	f.failures = append(f.failures, e)
	return nil
}

type fixture struct {
	storage   *fakeStorage
	probe     fakeProbe
	extractor *fakeExtractor
	publisher *fakePublisher
	workDir   string
	limits    Limits
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{
		storage:   newFakeStorage(),
		probe:     fakeProbe{info: VideoInfo{DurationSeconds: 3}},
		extractor: &fakeExtractor{frames: 3},
		publisher: &fakePublisher{},
		workDir:   t.TempDir(),
		limits:    Limits{MaxDurationSeconds: 600, MaxSizeBytes: 500 << 20},
	}
}

func (f *fixture) service() *Service {
	deps := Deps{Storage: f.storage, Probe: f.probe, Extractor: f.extractor, Archiver: namesArchiver{}, Publisher: f.publisher}
	svc := NewService(deps, f.workDir, f.limits, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tick := time.Date(2026, 11, 23, 18, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { tick = tick.Add(250 * time.Millisecond); return tick }
	return svc
}

func assertEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func assertWorkDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("work dir not cleaned: %v", entries)
	}
}

// --- tests -------------------------------------------------------------------------------

func TestProcess_Success(t *testing.T) {
	f := newFixture(t)

	if err := f.service().Process(context.Background(), validJob()); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	assertEvents(t, f.publisher.events, "started", "processed")
	key := ResultKey(userID, videoID)
	if got := string(f.storage.uploaded[key]); got != "frame_0001.png\nframe_0002.png\nframe_0003.png\n" {
		t.Fatalf("zip content = %q", got)
	}
	if meta := f.storage.uploadMeta[key]; meta[MetaFrameCount] != "3" || meta[MetaSourceDuration] != "3.000" {
		t.Fatalf("metadata = %v", meta)
	}
	r := f.publisher.processed[0]
	if r.FrameCount != 3 || r.ResultKey != key || r.Reused || r.ProcessingTime <= 0 {
		t.Fatalf("result = %+v", r)
	}
	if f.extractor.gotFPS != 1 {
		t.Fatalf("fps = %v", f.extractor.gotFPS)
	}
	if got := filepath.Base(f.storage.downloaded[0]); got != "source.mp4" {
		t.Fatalf("source file = %q, want lower-cased extension", got)
	}
	assertWorkDirEmpty(t, f.workDir)
}

func TestProcess_RedeliveryReusesExistingResult(t *testing.T) {
	f := newFixture(t)
	key := ResultKey(userID, videoID)
	f.storage.existing[key] = ObjectInfo{SizeBytes: 42, Metadata: map[string]string{MetaFrameCount: "7", MetaSourceDuration: "7.000"}}

	if err := f.service().Process(context.Background(), validJob()); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	assertEvents(t, f.publisher.events, "processed")
	if r := f.publisher.processed[0]; !r.Reused || r.FrameCount != 7 || r.SizeBytes != 42 || r.DurationSeconds != 7 {
		t.Fatalf("result = %+v", r)
	}
	if len(f.storage.downloaded) != 0 {
		t.Fatal("video must not be downloaded again")
	}
}

func TestProcess_PermanentFailuresArePublishedAndAcked(t *testing.T) {
	tests := []struct {
		name     string
		arrange  func(*fixture)
		job      func(Job) Job
		wantCode ErrorCode
		started  bool
	}{
		{name: "probe rejects content", arrange: func(f *fixture) { f.probe.err = errors.New("moov atom not found") }, wantCode: ErrInvalidVideo, started: true},
		{name: "too long", arrange: func(f *fixture) { f.probe.info.DurationSeconds = 601 }, wantCode: ErrVideoTooLong, started: true},
		{name: "ffmpeg fails", arrange: func(f *fixture) { f.extractor.err = errors.New("exit status 1") }, wantCode: ErrInvalidVideo, started: true},
		{name: "no frames", arrange: func(f *fixture) { f.extractor.frames = 0 }, wantCode: ErrNoFramesExtracted, started: true},
		{name: "too large", job: func(j Job) Job { j.SizeBytes = 501 << 20; return j }, wantCode: ErrVideoTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.arrange != nil {
				tt.arrange(f)
			}
			job := validJob()
			if tt.job != nil {
				job = tt.job(job)
			}

			if err := f.service().Process(context.Background(), job); err != nil {
				t.Fatalf("Process() error = %v, want nil (ack)", err)
			}

			if tt.started {
				assertEvents(t, f.publisher.events, "started", "failed")
			} else {
				assertEvents(t, f.publisher.events, "failed")
			}
			if got := f.publisher.failures[0]; got.Code != tt.wantCode || got.Message == "" {
				t.Fatalf("failure = %+v, want code %s with a user message", got, tt.wantCode)
			}
			if len(f.storage.uploaded) != 0 {
				t.Fatal("nothing must be uploaded on failure")
			}
			assertWorkDirEmpty(t, f.workDir)
		})
	}
}

func TestProcess_TransientFailuresAreNotAcked(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*fixture)
	}{
		{name: "stat fails", arrange: func(f *fixture) { f.storage.statErr = errors.New("s3 timeout") }},
		{name: "download fails", arrange: func(f *fixture) { f.storage.downloadErr = errors.New("s3 timeout") }},
		{name: "upload fails", arrange: func(f *fixture) { f.storage.uploadErr = errors.New("s3 timeout") }},
		{name: "started not confirmed", arrange: func(f *fixture) { f.publisher.failOn = "started" }},
		{name: "processed not confirmed", arrange: func(f *fixture) { f.publisher.failOn = "processed" }},
		{name: "failed not confirmed", arrange: func(f *fixture) { f.probe.err = errors.New("bad"); f.publisher.failOn = "failed" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.arrange(f)

			err := f.service().Process(context.Background(), validJob())

			if err == nil || errors.Is(err, ErrMalformedJob) {
				t.Fatalf("Process() error = %v, want transient error (no ack)", err)
			}
			for _, e := range f.publisher.events {
				if e == "failed" {
					t.Fatal("transient errors must not be reported to the user as failures")
				}
			}
			assertWorkDirEmpty(t, f.workDir)
		})
	}
}

func TestProcess_CancellationDuringExtractionIsTransient(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.extractor.onRun = func(context.Context) { cancel() }
	f.extractor.err = errors.New("signal: killed")

	err := f.service().Process(ctx, validJob())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Process() error = %v, want context.Canceled", err)
	}
	assertEvents(t, f.publisher.events, "started")
	assertWorkDirEmpty(t, f.workDir)
}

func TestProcess_MalformedJob(t *testing.T) {
	tests := map[string]func(Job) Job{
		"video id is a path": func(j Job) Job { j.VideoID = "../../etc"; return j },
		"user id empty":      func(j Job) Job { j.UserID = ""; return j },
		"no source key":      func(j Job) Job { j.SourceKey = ""; return j },
		"zero fps":           func(j Job) Job { j.FPS = 0; return j },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)

			err := f.service().Process(context.Background(), mutate(validJob()))

			if !errors.Is(err, ErrMalformedJob) {
				t.Fatalf("Process() error = %v, want ErrMalformedJob", err)
			}
			assertEvents(t, f.publisher.events)
		})
	}
}

func TestSafeExt(t *testing.T) {
	tests := map[string]string{
		"video.MP4":       ".mp4",
		"a.b.mkv":         ".mkv",
		"noext":           "",
		"evil.mp4/../x":   "",
		"weird.m p4":      "",
		"long.extension1": "",
		"dot.":            "",
	}
	for in, want := range tests {
		if got := safeExt(in); got != want {
			t.Errorf("safeExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProcessingErrorFormatting(t *testing.T) {
	cause := errors.New("ffprobe exit 1")
	e := permanent(ErrInvalidVideo, "inválido", cause)
	if !errors.Is(e, cause) {
		t.Fatal("cause must be unwrappable")
	}
	if e.Error() != "INVALID_VIDEO: inválido: ffprobe exit 1" {
		t.Fatalf("Error() = %q", e.Error())
	}
	if permanent(ErrNoFramesExtracted, "x", nil).Error() != "NO_FRAMES_EXTRACTED: x" {
		t.Fatal("unexpected format without cause")
	}
}
