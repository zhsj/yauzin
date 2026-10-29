package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestComposeSendKeepsOriginalAttachmentPath pins that the draft carries the
// user's own file: nothing is copied to the scratch directory on the way.
func TestComposeSendKeepsOriginalAttachmentPath(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	path := writeAttachmentFile(t, "report.txt", "hello")
	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText("friend@example.com")
	c.addAttachment(mail.DraftAttachment{Name: "report.txt", Path: path})

	c.send()
	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 1 })

	draft := svc.sentDrafts()[0]
	if len(draft.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want one", draft.Attachments)
	}
	if got := draft.Attachments[0].Path; got != path {
		t.Fatalf("attachment path = %q, want the original %q", got, path)
	}
	requireFileExists(t, path)
}

// TestComposeRemoveAttachmentKeepsFile keeps a removed attachment from taking
// the user's file with it.
func TestComposeRemoveAttachmentKeepsFile(t *testing.T) {
	a, _, _ := composeTestApp(t)
	path := writeAttachmentFile(t, "notes.txt", "content")
	c := newComposeView(a, composeNew, nil, "")
	c.addAttachment(mail.DraftAttachment{Name: "notes.txt", Path: path})

	c.removeAttachment(0)
	if len(c.attachments) != 0 || len(c.chips) != 0 {
		t.Fatalf("attachments/chips = %d/%d, want none", len(c.attachments), len(c.chips))
	}
	requireFileExists(t, path)
}

// TestComposeDiscardKeepsAttachmentFiles covers closing the composer: the draft
// forgets the file, the file system does not.
func TestComposeDiscardKeepsAttachmentFiles(t *testing.T) {
	a, _, _ := composeTestApp(t)
	path := writeAttachmentFile(t, "notes.txt", "content")
	c := newComposeView(a, composeNew, nil, "")
	c.addAttachment(mail.DraftAttachment{Name: "notes.txt", Path: path})

	c.discard()
	if len(c.attachments) != 0 || len(c.chips) != 0 {
		t.Fatalf("attachments/chips = %d/%d, want none", len(c.attachments), len(c.chips))
	}
	requireFileExists(t, path)
}

// TestComposeDuplicateAttachmentKeepsFile keeps the duplicate check from
// deleting the file the first chip still points at.
func TestComposeDuplicateAttachmentKeepsFile(t *testing.T) {
	a, _, _ := composeTestApp(t)
	path := writeAttachmentFile(t, "notes.txt", "content")
	c := newComposeView(a, composeNew, nil, "")
	att := mail.DraftAttachment{Name: "notes.txt", Path: path}

	c.addAttachment(att)
	c.addAttachment(att)
	if got, want := len(c.attachments), 1; got != want {
		t.Fatalf("attachments = %d, want %d", got, want)
	}
	if c.errMsg != i18n.T("compose.duplicateAttachment") {
		t.Fatalf("errMsg = %q", c.errMsg)
	}
	requireFileExists(t, path)
}

func writeAttachmentFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	return path
}

func requireFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("attachment file %q: %v", path, err)
	}
}
