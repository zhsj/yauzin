package gmail

import (
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/zhsj/yauzin/internal/mail"
)

// readDataField scans body and collects what the returned reader yields.
func readDataField(t *testing.T, body io.Reader) (string, error) {
	t.Helper()
	tail, err := scanDataField(body)
	if err != nil {
		return "", err
	}
	raw, err := io.ReadAll(tail)
	return string(raw), err
}

func TestScanDataField(t *testing.T) {
	longID := `{"attachmentId":"` + strings.Repeat("A", 3*jsonScanBuffer) +
		`","size":5,"data":"aGVsbG8"}`

	tests := []struct {
		name string
		body string
		want string
		err  error
	}{
		{
			name: "size before data",
			body: `{"size":5,"data":"aGVsbG8"}`,
			want: "aGVsbG8",
		},
		{
			name: "attachment id before data",
			body: `{"attachmentId":"ANGjdJ-data-99","size":5,"data":"aGVsbG8"}`,
			want: "aGVsbG8",
		},
		{
			name: "attachment id longer than the scan buffer",
			body: longID,
			want: "aGVsbG8",
		},
		{
			name: "whitespace around the colon",
			body: "{ \"data\" :\n\t\"aGVsbG8\" }",
			want: "aGVsbG8",
		},
		{
			name: "value that reads data is not the key",
			body: `{"mimeType":"data","data":"aGVsbG8"}`,
			want: "aGVsbG8",
		},
		{
			name: "longer key that starts like the field",
			body: `{"database":"x","data":"aGVsbG8"}`,
			want: "aGVsbG8",
		},
		{
			name: "content after the field stays unread",
			body: `{"data":"aGVsbG8","size":5}`,
			want: "aGVsbG8",
		},
		{
			name: "empty field",
			body: `{"size":0,"data":""}`,
			want: "",
		},
		{
			name: "field missing",
			body: `{"size":0}`,
			err:  mail.ErrNotFound,
		},
		{
			name: "empty document",
			body: ``,
			err:  mail.ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readDataField(t, strings.NewReader(tt.body))
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("scan returned (%q, %v), want %v", got, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got != tt.want {
				t.Fatalf("field = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestScanDataFieldChunked pins that the scan does not depend on how the body
// happens to be chunked: a reader that hands out one byte at a time is the
// worst case a socket can produce.
func TestScanDataFieldChunked(t *testing.T) {
	body := `{"attachmentId":"a1","size":5,"data":"aGVsbG8"}`
	got, err := readDataField(t, iotest.OneByteReader(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != "aGVsbG8" {
		t.Fatalf("field = %q, want %q", got, "aGVsbG8")
	}
}

// TestScanDataFieldTruncated pins that a document cut off inside the field is
// not reported as a complete one: a save must not store half an attachment.
func TestScanDataFieldTruncated(t *testing.T) {
	got, err := readDataField(t, strings.NewReader(`{"size":5,"data":"aGVsbG8`))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated field returned %v, want io.ErrUnexpectedEOF", err)
	}
	if got != "aGVsbG8" {
		t.Fatalf("field = %q, want the bytes that did arrive", got)
	}
}

// decodePayload runs text through the same decoder stack the save uses.
func decodePayload(t *testing.T, chars string) (string, error) {
	t.Helper()
	src := newPaddingReader(strings.NewReader(chars))
	raw, err := io.ReadAll(base64.NewDecoder(base64.URLEncoding, src))
	return string(raw), err
}

func TestPaddingReader(t *testing.T) {
	tests := []struct {
		name  string
		chars string
		want  string
		err   bool
	}{
		{name: "padded", chars: "aGVsbG8=", want: "hello"},
		{name: "unpadded", chars: "aGVsbG8", want: "hello"},
		{name: "one character missing", chars: "aGk", want: "hi"},
		{name: "empty", chars: "", want: ""},
		{name: "invalid length", chars: "a", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodePayload(t, tt.chars)
			if tt.err {
				if err == nil {
					t.Fatalf("decoded %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got != tt.want {
				t.Fatalf("decoded %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPaddingReaderOneByteReader keeps the padding path honest when the
// underlying reader arrives in single byte chunks.
func TestPaddingReaderOneByteReader(t *testing.T) {
	src := newPaddingReader(iotest.OneByteReader(strings.NewReader("aGVsbG8")))
	raw, err := io.ReadAll(base64.NewDecoder(base64.URLEncoding, src))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw) != "hello" {
		t.Fatalf("decoded %q, want %q", raw, "hello")
	}
}
