package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestZip_WritesAllFilesFlatAndStored(t *testing.T) {
	dir := t.TempDir()
	want := map[string]string{"frame_0001.png": "first", "frame_0002.png": "second"}
	var paths []string
	for name, content := range want {
		p := filepath.Join(dir, "nested", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	var buf bytes.Buffer
	if err := (Zip{}).Write(&buf, paths); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	r, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	if len(r.File) != len(want) {
		t.Fatalf("zip has %d entries, want %d", len(r.File), len(want))
	}
	for _, f := range r.File {
		if f.Method != zip.Store {
			t.Errorf("%s method = %d, want Store", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want[f.Name] {
			t.Errorf("%s content = %q, want %q", f.Name, got, want[f.Name])
		}
	}
}

func TestZip_MissingFile(t *testing.T) {
	err := (Zip{}).Write(io.Discard, []string{filepath.Join(t.TempDir(), "missing.png")})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Write() error = %v, want not-exist", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestZip_PropagatesWriterErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "frame_0001.png")
	if err := os.WriteFile(p, bytes.Repeat([]byte("x"), 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Zip{}).Write(failingWriter{}, []string{p}); err == nil {
		t.Fatal("Write() want error when the destination fails")
	}
}
