package ui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/smtp"
)

// composeTestApp returns an App whose mailbox has a stored sending account that
// asks for a blind copy and one that does not.
func composeTestApp(t *testing.T) (*App, *fakeService, *accounts.Store) {
	t.Helper()
	a, store := smtpTestApp(t)
	svc := &fakeService{}
	a.services[a.activeAcc] = svc
	seed := []accounts.Entry{
		{
			ID:    "smtp:alt@example.com",
			Kind:  mail.KindSMTP,
			Email: "alt@example.com",
			Name:  "Alt Sender",
			SMTP: &accounts.SMTPConfig{
				Host:         "smtp.example.com",
				Port:         587,
				Username:     "alt",
				Security:     "starttls",
				BccToAccount: true,
			},
		},
		{
			ID:    "smtp:quiet@example.com",
			Kind:  mail.KindSMTP,
			Email: "quiet@example.com",
			SMTP:  &accounts.SMTPConfig{Host: "smtp.example.com", Port: 465, Security: "tls"},
		},
	}
	for _, entry := range seed {
		if err := store.Upsert(entry); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	return a, svc, store
}

// TestComposeSendersListTheMailboxFirst pins the order of the From row: the
// mailbox the draft belongs to, then every stored sending account.
func TestComposeSendersListTheMailboxFirst(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	if len(c.senders) != 3 {
		t.Fatalf("senders = %+v, want the mailbox and two sending accounts", c.senders)
	}
	if c.fromID != a.activeAcc {
		t.Fatalf("from = %q, want the mailbox %q", c.fromID, a.activeAcc)
	}
	if c.senders[0].entry != nil || c.senders[0].email != "ada@example.com" {
		t.Fatalf("first sender = %+v, want the mailbox", c.senders[0])
	}
	if !c.senders[1].bccCopy || c.senders[2].bccCopy {
		t.Fatalf("blind copy flags = %v/%v, want them to follow the stored choice", c.senders[1].bccCopy, c.senders[2].bccCopy)
	}
	if len(c.fromRows) != len(c.senders) || !c.fromRows[0].active {
		t.Fatalf("from rows = %+v, want the mailbox checked", c.fromRows)
	}
}

// TestComposeSenderFillsAndClearsBcc covers the blind copy: a sending account
// that asks for one prefills the mailbox address, switching away clears only
// that automatic value, and a value the user typed survives.
func TestComposeSenderFillsAndClearsBcc(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	c.setSender("smtp:alt@example.com")
	if got := c.bcc.Text(); got != "ada@example.com" {
		t.Fatalf("Bcc = %q, want the mailbox address", got)
	}
	if !c.fromRows[1].active || c.fromRows[0].active {
		t.Fatalf("from rows = %+v, want the sending account checked", c.fromRows)
	}

	c.setSender("smtp:quiet@example.com")
	if got := c.bcc.Text(); got != "" {
		t.Fatalf("Bcc = %q, want it cleared after leaving the copying sender", got)
	}

	// A value the user typed is theirs: switching senders must not touch it.
	c.bcc.SetText("typed@example.com")
	c.setSender("demo")
	if got := c.bcc.Text(); got != "typed@example.com" {
		t.Fatalf("Bcc = %q, want the typed address kept", got)
	}

	// The automatic value is replaced when another copying sender is picked.
	c.bcc.SetText("")
	c.setSender("smtp:alt@example.com")
	if got := c.bcc.Text(); got != "ada@example.com" {
		t.Fatalf("Bcc = %q, want the mailbox address again", got)
	}
}

// TestComposeSendThroughTheMailbox keeps the default path: the draft goes to the
// backend of the account it was written in, with the From the row shows.
func TestComposeSendThroughTheMailbox(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText("friend@example.com")
	c.subject.SetText("hello")
	c.body.SetText("body")

	c.send()
	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 1 })

	draft := svc.sentDrafts()[0]
	if draft.From.Email != "ada@example.com" {
		t.Fatalf("From = %+v, want the mailbox address", draft.From)
	}
	if len(draft.To) != 1 || draft.To[0].Email != "friend@example.com" {
		t.Fatalf("To = %+v", draft.To)
	}
	if draft.MessageID != c.messageID {
		t.Fatalf("Message-ID = %q, want the one the composer shows %q", draft.MessageID, c.messageID)
	}
	if draft.Date.IsZero() || !draft.Date.Equal(c.date) {
		t.Fatalf("Date = %v, want the one the composer shows %v", draft.Date, c.date)
	}
}

// TestComposeSendThroughASendingAccount covers the second transport: picking a
// sending account hands the entry and the draft to the SMTP sender instead of
// the mailbox backend.
func TestComposeSendThroughASendingAccount(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	type delivered struct {
		entry accounts.Entry
		draft mail.Draft
	}
	got := make(chan delivered, 1)
	a.sendSMTP = func(_ context.Context, entry accounts.Entry, draft mail.Draft) error {
		got <- delivered{entry: entry, draft: draft}
		return nil
	}

	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText("friend@example.com")
	c.body.SetText("body")
	c.setSender("smtp:alt@example.com")
	c.send()

	select {
	case result := <-got:
		if result.entry.ID != "smtp:alt@example.com" {
			t.Fatalf("entry = %+v, want the chosen sending account", result.entry)
		}
		if result.draft.From.Email != "alt@example.com" || result.draft.From.Name != "Alt Sender" {
			t.Fatalf("From = %+v, want the sending identity", result.draft.From)
		}
		if len(result.draft.Bcc) != 1 || result.draft.Bcc[0].Email != "ada@example.com" {
			t.Fatalf("Bcc = %+v, want the prefill that was on screen", result.draft.Bcc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the draft never reached the SMTP sender")
	}

	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 0 && !c.sending })
	if sent := svc.sentDrafts(); len(sent) != 0 {
		t.Fatalf("the mailbox backend received %+v", sent)
	}
}

// TestComposeSendReportsARejectedLogin turns the SMTP failure into the sentence
// the user can act on instead of the protocol answer.
func TestComposeSendReportsARejectedLogin(t *testing.T) {
	a, _, _ := composeTestApp(t)
	a.sendSMTP = func(context.Context, accounts.Entry, mail.Draft) error {
		return fmt.Errorf("%w: 535 5.7.8 credentials rejected", smtp.ErrAuth)
	}

	c := newComposeView(a, composeNew, nil, "")
	// The composer has to be the open layer for the error to stay in its
	// footer instead of going to the notification bar.
	a.overlay = c
	c.to.SetText("friend@example.com")
	c.setSender("smtp:alt@example.com")
	c.send()
	drainUntil(t, a, func() bool { return c.errMsg != "" })

	if want := i18n.T("error.smtpAuth"); !strings.Contains(c.errMsg, want) {
		t.Fatalf("error = %q, want it to contain %q", c.errMsg, want)
	}
}

// TestComposeFromRowStaysHidden walks the two states of the From row: it only
// appears once there is a second identity to pick.
func TestComposeFromRowStaysHidden(t *testing.T) {
	a, _, store := composeTestApp(t)
	for _, entry := range store.SMTPEntries() {
		if err := store.Remove(entry.ID); err != nil {
			t.Fatalf("clear store: %v", err)
		}
	}
	c := newComposeView(a, composeNew, nil, "")
	if len(c.senders) != 1 {
		t.Fatalf("senders = %+v, want only the mailbox", c.senders)
	}
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	if dims := c.senderRow(gtx); dims.Size != (image.Point{}) {
		t.Fatalf("the From row took %v without a sending account", dims.Size)
	}

	if err := store.Upsert(accounts.Entry{
		ID:    "smtp:alt@example.com",
		Kind:  mail.KindSMTP,
		Email: "alt@example.com",
		SMTP:  &accounts.SMTPConfig{Host: "smtp.example.com", Port: 587, Security: "starttls"},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	c = newComposeView(a, composeNew, nil, "")
	if dims := c.senderRow(gtx); dims.Size.Y == 0 {
		t.Fatal("the From row is still hidden with a sending account stored")
	}
}

// drainUntil runs the queued UI callbacks until cond holds. It bounds the wait
// so a send that never completes fails the test instead of hanging it.
func drainUntil(t *testing.T, a *App, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.drain()
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the queued callback never ran")
}
