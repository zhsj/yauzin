package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestDownloadFileName pins the default name of a downloaded message: the
// subject survives, separators and controls do not, and an empty subject gets
// a readable fallback.
func TestDownloadFileName(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		want    string
	}{
		{name: "plain", subject: "Quarterly report", want: "Quarterly report.eml"},
		{name: "unicode subject", subject: "会议纪要", want: "会议纪要.eml"},
		{name: "separators and controls", subject: "a/b\\c\x00", want: "a_b_c_.eml"},
		{name: "surrounding dots and spaces", subject: "  .hidden. ", want: "hidden.eml"},
		{name: "empty subject", subject: " \t ", want: "message.eml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := downloadFileName(tc.subject); got != tc.want {
				t.Fatalf("downloadFileName(%q) = %q, want %q", tc.subject, got, tc.want)
			}
		})
	}
}

// TestDownloadFileNameTruncates keeps a long subject within the file name
// limit without splitting a multi-byte character.
func TestDownloadFileNameTruncates(t *testing.T) {
	got := downloadFileName(strings.Repeat("会", downloadNameMaxBytes))
	if !utf8.ValidString(got) {
		t.Fatalf("truncated name %q is not valid UTF-8", got)
	}
	if !strings.HasSuffix(got, ".eml") {
		t.Fatalf("truncated name %q lost its extension", got)
	}
	if len(got) > downloadNameMaxBytes+len(".eml") {
		t.Fatalf("truncated name is %d bytes, want at most %d", len(got), downloadNameMaxBytes+len(".eml"))
	}
}
