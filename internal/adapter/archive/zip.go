// Package archive packs the extracted frames into the zip the user downloads.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/FiapXHacka/fiapx-video-processor/internal/core"
)

// Zip implements core.Archiver. It streams entries to w, so the caller can pipe it straight
// into a multipart upload without writing the zip to disk (ADR-0008).
//
// Entries are stored, not deflated: PNG is already compressed, so deflate (used by the
// prototype) cost CPU on the bottleneck of the system for almost no size gain.
type Zip struct{}

var _ core.Archiver = Zip{}

func (Zip) Write(w io.Writer, files []string) error {
	zw := zip.NewWriter(w)
	for _, f := range files {
		if err := addFile(zw, f); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("finishing zip: %w", err)
	}
	return nil
}

func addFile(zw *zip.Writer, path string) error {
	file, err := os.Open(path) // #nosec G304 -- paths come from the extractor's output dir
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer file.Close() //nolint:errcheck // read-only file: Close cannot lose data

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(path)
	header.Method = zip.Store

	entry, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("adding %s: %w", header.Name, err)
	}
	if _, err := io.Copy(entry, file); err != nil {
		return fmt.Errorf("writing %s: %w", header.Name, err)
	}
	return nil
}
