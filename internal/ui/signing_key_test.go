package ui

import (
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// signingKeyTestApp seeds one stored mailbox so a key row has an entry to write
// the key to.
func signingKeyTestApp(t *testing.T) (*App, *accounts.Store) {
	t.Helper()
	a, store := smtpTestApp(t)
	entry := accounts.Entry{ID: "gmail:ada@example.com", Kind: mail.KindGmail, Email: "ada@example.com"}
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return a, store
}

func TestSigningKeyRowStoresNormalizedKey(t *testing.T) {
	a, store := signingKeyTestApp(t)
	f := newSigningKeyRow(a, "gmail:ada@example.com", "ada@example.com", "")
	f.edit.SetText(" 0123 4567 89ab cdef ")
	f.save.disabled = false
	f.store()

	if f.err != "" {
		t.Fatalf("store reported %q", f.err)
	}
	entry, ok := store.Entry("gmail:ada@example.com")
	if !ok {
		t.Fatal("account vanished")
	}
	if entry.SigningKey != "0123456789ABCDEF" {
		t.Fatalf("SigningKey = %q, want the normalized long key ID", entry.SigningKey)
	}
	if got := f.edit.Text(); got != "0123456789ABCDEF" {
		t.Fatalf("field shows %q, want the normalized key", got)
	}
	if f.saved != entry.SigningKey {
		t.Fatalf("saved = %q, want %q", f.saved, entry.SigningKey)
	}
}

func TestSigningKeyRowRejectsInvalidKey(t *testing.T) {
	a, store := signingKeyTestApp(t)
	f := newSigningKeyRow(a, "gmail:ada@example.com", "ada@example.com", "")
	f.edit.SetText("ada@example.com")
	f.store()

	if want := i18n.T("settings.signingKeyInvalid"); f.err != want {
		t.Fatalf("err = %q, want %q", f.err, want)
	}
	entry, _ := store.Entry("gmail:ada@example.com")
	if entry.SigningKey != "" {
		t.Fatalf("SigningKey = %q, want nothing written", entry.SigningKey)
	}
}

func TestSigningKeyRowClearsKey(t *testing.T) {
	a, store := signingKeyTestApp(t)
	f := newSigningKeyRow(a, "gmail:ada@example.com", "ada@example.com", "0123456789ABCDEF")
	// A stored key has to be visible when the page opens.
	if got := f.edit.Text(); got != "0123456789ABCDEF" {
		t.Fatalf("field = %q, want the stored key", got)
	}
	f.edit.SetText("")
	f.store()

	if f.err != "" {
		t.Fatalf("store reported %q", f.err)
	}
	entry, _ := store.Entry("gmail:ada@example.com")
	if entry.SigningKey != "" {
		t.Fatalf("SigningKey = %q, want it cleared", entry.SigningKey)
	}
}

// TestSettingsSigningRowsListEveryIdentity pins the shape of the signing page:
// one row per mailbox and per sending account, each carrying the key stored for
// that identity instead of a shared field.
func TestSettingsSigningRowsListEveryIdentity(t *testing.T) {
	a, _, store := composeTestApp(t)
	// The test app's mailbox is not in the store on its own; storing it is what
	// gives the page a key row for a mailbox.
	if err := store.Upsert(accounts.Entry{ID: "demo", Kind: mail.KindGmail, Email: "ada@example.com"}); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	entry, ok := store.Entry("smtp:alt@example.com")
	if !ok {
		t.Fatal("seeded sending account missing")
	}
	entry.SigningKey = "0123456789ABCDEF"
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("store key: %v", err)
	}

	s := newSettingsView(a)
	if got, want := len(s.signRows), 3; got != want {
		t.Fatalf("rows = %d, want the mailbox and two sending accounts", got)
	}
	if got := s.signRows[0].accID; got != "demo" {
		t.Fatalf("first row = %q, want the mailbox", got)
	}
	if got := s.signRows[0].edit.Text(); got != "" {
		t.Fatalf("mailbox key = %q, want it empty", got)
	}
	if got := s.signRows[1].accID; got != "smtp:alt@example.com" {
		t.Fatalf("second row = %q, want the first sending account", got)
	}
	if got := s.signRows[1].edit.Text(); got != "0123456789ABCDEF" {
		t.Fatalf("sending account key = %q, want the stored one", got)
	}
	// The rows are labelled by address alone: no display name stands in for
	// the identity the key belongs to.
	if got := s.signRows[0].title; got != "ada@example.com" {
		t.Fatalf("mailbox title = %q, want the address", got)
	}
	if got := s.signRows[1].title; got != "alt@example.com" {
		t.Fatalf("sending account title = %q, want the address", got)
	}
	if got := s.signRows[2].edit.Text(); got != "" {
		t.Fatalf("second sending account key = %q, want it empty", got)
	}
}

// TestSettingsSigningRowsSkipUnstoredMailbox keeps the page free of rows that
// cannot save: the dev-mode demo mailbox is not in accounts.json, so it has no
// place to keep a key.
func TestSettingsSigningRowsSkipUnstoredMailbox(t *testing.T) {
	a, _, _ := composeTestApp(t)
	s := newSettingsView(a)
	for _, row := range s.signRows {
		if row.accID == "demo" {
			t.Fatal("a mailbox without a stored entry got a key row")
		}
	}
	if got := len(s.signRows); got != 2 {
		t.Fatalf("rows = %d, want only the two sending accounts", got)
	}
}

// TestSettingsSigningPageFrames lays out the signing tab with rows whose hint
// and error wrap, then scrolls it: the page must neither panic nor drop the
// rows off the panel.
func TestSettingsSigningPageFrames(t *testing.T) {
	a, _, store := composeTestApp(t)
	entry, _ := store.Entry("smtp:alt@example.com")
	entry.SigningKey = "0123456789ABCDEF"
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("store key: %v", err)
	}
	s := newSettingsView(a)
	s.page = settingsTabSigning
	s.signRows[0].err = i18n.T("settings.signingKeyInvalid")

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.list.ScrollBy(100)
	s.Layout(gtx, a)
	ops.Reset()
}

// TestSettingsSigningPageWithoutIdentities keeps the empty state from drawing a
// bare card: with nothing to configure the page is only the heading and hint.
func TestSettingsSigningPageWithoutIdentities(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{})
	s := newSettingsView(a)
	s.page = settingsTabSigning

	if len(s.signRows) != 0 {
		t.Fatalf("rows = %d, want none without identities", len(s.signRows))
	}
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()
}

func TestSMTPDialogAddsWithoutSigningKey(t *testing.T) {
	a, store := smtpTestApp(t)
	d := newSMTPDialog(a, nil)
	d.host.SetText("smtp.example.com")
	d.port.SetText("587")
	d.email.SetText("me@example.com")
	d.submit()

	if d.err != "" {
		t.Fatalf("submit failed: %s", d.err)
	}
	entry, ok := store.Entry("smtp:me@example.com")
	if !ok {
		t.Fatal("sending account missing")
	}
	if entry.SigningKey != "" {
		t.Fatalf("SigningKey = %q, want a new account to start unsigned", entry.SigningKey)
	}
}

// TestSMTPDialogKeepsStoredSigningKey guards the move of the key out of the
// form: the dialog no longer carries the field, so it must carry the stored
// value over or editing a sender would silently turn its signing off.
func TestSMTPDialogKeepsStoredSigningKey(t *testing.T) {
	a, store := smtpTestApp(t)
	entry := accounts.Entry{
		ID:         "smtp:me@example.com",
		Kind:       mail.KindSMTP,
		Email:      "me@example.com",
		SigningKey: "0123456789ABCDEF",
		SMTP:       &accounts.SMTPConfig{Host: "smtp.example.com", Port: 587, Security: "starttls"},
	}
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	d := newSMTPDialog(a, &entry)
	d.name.SetText("Me")
	d.submit()

	if d.err != "" {
		t.Fatalf("submit failed: %s", d.err)
	}
	saved, ok := store.Entry("smtp:me@example.com")
	if !ok {
		t.Fatal("sending account vanished")
	}
	if saved.SigningKey != "0123456789ABCDEF" {
		t.Fatalf("SigningKey = %q, want the stored key kept", saved.SigningKey)
	}
	if saved.Name != "Me" {
		t.Fatalf("Name = %q, want the edit to stick", saved.Name)
	}
}
