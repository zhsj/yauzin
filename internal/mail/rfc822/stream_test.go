package rfc822

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestPrepareSizeMatchesBuild pins the size arithmetic to the renderer: the
// resumable upload trusts Size as the total, so it has to be exactly the
// number of bytes WriteTo produces at every base64 line boundary.
func TestPrepareSizeMatchesBuild(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 55, 56, 57, 58, 75, 76, 77, 152, 1000}
	dir := t.TempDir()
	for _, size := range sizes {
		path := filepath.Join(dir, "part.bin")
		content := bytes.Repeat([]byte("x"), size)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write attachment: %v", err)
		}
		draft := mail.Draft{
			To:      []mail.Address{{Email: "to@example.com"}},
			Subject: "size",
			Body:    "body",
			Attachments: []mail.DraftAttachment{
				{Name: "part.bin", Path: path},
			},
		}
		msg, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
		if err != nil {
			t.Fatalf("size %d: Prepare: %v", size, err)
		}
		raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
		if err != nil {
			t.Fatalf("size %d: Build: %v", size, err)
		}
		if got, want := msg.Size(), int64(len(raw)); got != want {
			t.Fatalf("size %d: Size() = %d, want %d", size, got, want)
		}
	}
}

// TestWriteToStreamsAttachments checks that a prepared message writes exactly
// its declared size, can be written twice, and carries the attachment content.
func TestWriteToStreamsAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	content := strings.Repeat("attachment line\n", 500)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	draft := mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "stream",
		Body:    "see attached",
		Attachments: []mail.DraftAttachment{
			{Name: "notes.txt", Path: path},
		},
	}
	msg, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	var first bytes.Buffer
	n, err := msg.WriteTo(&first)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if n != msg.Size() || int64(first.Len()) != msg.Size() {
		t.Fatalf("wrote %d bytes (%d buffered), want %d", n, first.Len(), msg.Size())
	}

	var second bytes.Buffer
	if _, err := msg.WriteTo(&second); err != nil {
		t.Fatalf("second WriteTo: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("writing the same message twice produced different bytes")
	}

	parsed := parse(t, first.Bytes())
	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	mr := multipart.NewReader(parsed.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("first part: %v", err)
	}
	if got := readBody(t, part); got != draft.Body {
		t.Fatalf("text part = %q", got)
	}
	part, err = mr.NextPart()
	if err != nil {
		t.Fatalf("attachment part: %v", err)
	}
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
	if err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	if string(data) != content {
		t.Fatalf("attachment = %d bytes, want %d", len(data), len(content))
	}
}

// TestWriteToReportsChangedAttachment keeps a resumable upload from declaring
// one length and sending another when the file changes after Prepare.
func TestWriteToReportsChangedAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "part.bin")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Attachments: []mail.DraftAttachment{{Name: "part.bin", Path: path}},
	}
	msg, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(path, []byte("after the change"), 0o600); err != nil {
		t.Fatalf("rewrite attachment: %v", err)
	}
	if _, err := msg.WriteTo(io.Discard); err == nil {
		t.Fatal("WriteTo accepted an attachment that changed size")
	}
}
