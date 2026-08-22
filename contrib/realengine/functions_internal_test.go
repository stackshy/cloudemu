package realengine

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func zipWith(t *testing.T, name string, size int) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}

	if _, err := w.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		t.Fatalf("zip write: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	return buf.Bytes()
}

// TestUnzipRejectsOversizeEntry proves an entry larger than the per-entry cap is
// rejected outright, not silently truncated to the cap.
func TestUnzipRejectsOversizeEntry(t *testing.T) {
	dir := t.TempDir()

	err := unzip(zipWith(t, "big.py", maxUnzipBytes+1), dir)
	if !errors.Is(err, errEntryTooLarge) {
		t.Fatalf("want errEntryTooLarge, got %v", err)
	}
}

// TestUnzipRejectsZipSlip proves a path-traversal entry is rejected.
func TestUnzipRejectsZipSlip(t *testing.T) {
	dir := t.TempDir()

	err := unzip(zipWith(t, "../escape.py", 4), dir)
	if !errors.Is(err, errZipSlip) {
		t.Fatalf("want errZipSlip, got %v", err)
	}
}

// TestUnzipWritesEntry confirms a normal entry lands under dir intact.
func TestUnzipWritesEntry(t *testing.T) {
	dir := t.TempDir()

	if err := unzip(zipWith(t, "handler.py", 10), dir); err != nil {
		t.Fatalf("unzip: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "handler.py"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(got) != 10 {
		t.Fatalf("want 10 bytes, got %d", len(got))
	}
}
