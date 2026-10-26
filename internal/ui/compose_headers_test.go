package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/mail"
)

// replyMessage is the parent a reply threads onto: a message the backend read a
// Message-ID and one ancestor for.
func replyMessage() *mail.Message {
	return &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:      "m2",
			From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
			Subject: "hello",
		},
		MessageID:  "<m2@example.com>",
		References: []string{"<m1@example.com>"},
		TextBody:   "hello",
	}
}

// TestComposeReplyThreadsOntoTheMessage pins the whole path of the thread
// headers: the reply inherits the parent's chain, and the draft that goes out
// carries the same IDs the header details show.
func TestComposeReplyThreadsOntoTheMessage(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	msg := replyMessage()
	a.msgs = []*mail.Message{msg}

	c := newComposeView(a, composeReply, msg, "")
	if got, want := c.inReplyTo, "<m2@example.com>"; got != want {
		t.Fatalf("In-Reply-To = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.references, " "), "<m1@example.com> <m2@example.com>"; got != want {
		t.Fatalf("References = %q, want %q", got, want)
	}
	if !strings.Contains(c.referencesText(), "<m2@example.com>") {
		t.Fatalf("the details show %q, missing the parent", c.referencesText())
	}

	c.send()
	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 1 })

	draft := svc.sentDrafts()[0]
	if draft.InReplyTo != c.inReplyTo || draft.InReplyTo == "" {
		t.Fatalf("sent In-Reply-To = %q, want %q", draft.InReplyTo, c.inReplyTo)
	}
	if got, want := strings.Join(draft.References, " "), strings.Join(c.references, " "); got != want {
		t.Fatalf("sent References = %q, want %q", got, want)
	}
	if draft.MessageID != c.messageID || draft.MessageID == "" {
		t.Fatalf("sent Message-ID = %q, want the one on screen %q", draft.MessageID, c.messageID)
	}
	if !draft.Date.Equal(c.date) || draft.Date.IsZero() {
		t.Fatalf("sent Date = %v, want the one on screen %v", draft.Date, c.date)
	}
}

// TestComposeThreadsOnlyAReply keeps the other two modes clean: a new message
// and a forward start a conversation of their own.
func TestComposeThreadsOnlyAReply(t *testing.T) {
	a, _, _ := composeTestApp(t)
	msg := replyMessage()

	for _, mode := range []composeMode{composeNew, composeForward} {
		c := newComposeView(a, mode, msg, "")
		if c.inReplyTo != "" || len(c.references) != 0 {
			t.Fatalf("mode %v carries %q/%q", mode, c.inReplyTo, c.references)
		}
	}
	// A message with no Message-ID leaves nothing to point at, so the reply goes
	// out as a message of its own rather than guessing an ID.
	plain := &mail.Message{MessageSummary: mail.MessageSummary{From: mail.Address{Email: "bob@example.com"}}}
	if c := newComposeView(a, composeReply, plain, ""); c.inReplyTo != "" || len(c.references) != 0 {
		t.Fatalf("a parent without a Message-ID produced %q/%q", c.inReplyTo, c.references)
	}
}

// TestComposeMessageIDFollowsTheSender pins the domain of the generated ID: it
// has to name the identity the message goes out as, and change when that
// identity changes.
func TestComposeMessageIDFollowsTheSender(t *testing.T) {
	a, _, store := composeTestApp(t)
	if err := store.Upsert(accounts.Entry{
		ID:    "smtp:writer@other.example",
		Kind:  mail.KindSMTP,
		Email: "writer@other.example",
		SMTP:  &accounts.SMTPConfig{Host: "smtp.other.example", Port: 587, Security: "starttls"},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	c := newComposeView(a, composeNew, nil, "")
	if !strings.HasSuffix(c.messageID, "@example.com>") {
		t.Fatalf("Message-ID = %q, want the mailbox domain", c.messageID)
	}
	mailboxID := c.messageID

	c.setSender("smtp:writer@other.example")
	if c.messageID == mailboxID {
		t.Fatal("the Message-ID survived a sender change")
	}
	if !strings.HasSuffix(c.messageID, "@other.example>") {
		t.Fatalf("Message-ID = %q, want the sending account's domain", c.messageID)
	}
	if _, ok := mail.NormalizeMessageID(c.messageID); !ok {
		t.Fatalf("Message-ID = %q is not a valid ID", c.messageID)
	}
}

// TestComposeReferencesTextCutsLongChains keeps the details readable: a long
// conversation shows its newest entries, while the message keeps every ID.
func TestComposeReferencesTextCutsLongChains(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")
	for i := 0; i < 10; i++ {
		c.references = append(c.references, "<id"+string(rune('a'+i))+"@example.com>")
	}
	text := c.referencesText()
	if !strings.HasPrefix(text, "… ") {
		t.Fatalf("long chain = %q, want the cut marker", text)
	}
	for _, ref := range c.references[8:] {
		if !strings.Contains(text, ref) {
			t.Fatalf("chain %q misses the newest %q", text, ref)
		}
	}
	if strings.Contains(text, c.references[0]) {
		t.Fatalf("chain %q still shows the oldest entry", text)
	}
	if len(c.references) != 10 {
		t.Fatalf("the draft lost entries: %d", len(c.references))
	}
}

// TestComposeDateIsStable pins the Date the details show: it is decided once,
// when the composer opens, so the value on screen never drifts from the one the
// message carries.
func TestComposeDateIsStable(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")
	opened := c.date
	if opened.IsZero() {
		t.Fatal("the composer opened without a Date")
	}
	if again := time.Now(); !opened.Before(again.Add(time.Second)) {
		t.Fatalf("Date %v is not the opening time", opened)
	}
	c.setSender("smtp:alt@example.com")
	if !c.date.Equal(opened) {
		t.Fatalf("Date moved from %v to %v after a sender change", opened, c.date)
	}
}
