package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// plainWriter is an io.WriteCloser without a Name, like the portal writers on
// platforms that do not hand back an *os.File.
type plainWriter struct{}

func (plainWriter) Write(p []byte) (int, error) { return len(p), nil }
func (plainWriter) Close() error                { return nil }

// namedWriter reports an empty name, which must fall back to the attachment's
// own name instead of showing nothing.
type namedWriter struct{ name string }

func (w namedWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w namedWriter) Close() error                { return nil }
func (w namedWriter) Name() string                { return w.name }

func TestSavedName(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "chosen-*.pdf")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer func() { _ = f.Close() }()

	if got := savedName(f, "fallback.pdf"); got != filepath.Base(f.Name()) {
		t.Fatalf("savedName(*os.File) = %q, want %q", got, filepath.Base(f.Name()))
	}
	if got := savedName(plainWriter{}, "fallback.pdf"); got != "fallback.pdf" {
		t.Fatalf("savedName(no name) = %q, want fallback.pdf", got)
	}
	if got := savedName(namedWriter{}, "fallback.pdf"); got != "fallback.pdf" {
		t.Fatalf("savedName(empty name) = %q, want fallback.pdf", got)
	}
}
