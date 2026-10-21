package ui

import (
	"testing"

	"yauzin/internal/mail"
)

func TestReaderCardResolvesTheCurrentMessage(t *testing.T) {
	a := testApp(t)
	a.msgs = []*mail.Message{
		{MessageSummary: mail.MessageSummary{ID: "m1", Subject: "first"}},
		{MessageSummary: mail.MessageSummary{ID: "m2", Subject: "second"}},
	}
	card := a.reader.cardFor("m1")
	if got := card.message(a); got == nil || got.Subject != "first" {
		t.Fatalf("card resolved %+v, want m1", got)
	}

	// A refresh replaces the message objects; the card has to follow the new
	// one instead of keeping the pointer it was built with.
	a.msgs[0] = &mail.Message{MessageSummary: mail.MessageSummary{ID: "m1", Subject: "refreshed"}}
	if got := card.message(a); got == nil || got.Subject != "refreshed" {
		t.Fatalf("card resolved %+v, want the refreshed message", got)
	}

	a.msgs = a.msgs[1:]
	if got := card.message(a); got != nil {
		t.Fatalf("card still resolves %q for a conversation it is not part of", got.ID)
	}
}

func TestReaderReplyUsesTheCurrentMessage(t *testing.T) {
	a := testApp(t)
	a.msgs = []*mail.Message{{
		MessageSummary: mail.MessageSummary{
			ID:      "m1",
			Subject: "old subject",
			From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
		},
	}}
	card := a.reader.cardFor("m1")
	reply, _, _ := card.actions(a)

	a.msgs[0] = &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:      "m1",
			Subject: "new subject",
			From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
		},
	}
	reply.onTap()

	if a.compose == nil {
		t.Fatal("the reply button opened no composer")
	}
	if a.compose.msg == nil || a.compose.msg.Subject != "new subject" {
		t.Fatalf("reply quoted %+v, want the refreshed message", a.compose.msg)
	}
}

func TestReaderCachesArePerMessageAndResettable(t *testing.T) {
	a := testApp(t)
	first := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m1"}}
	second := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m2"}}
	att := mail.Attachment{ID: "a1", Filename: "report.txt"}

	one := a.reader.attButton(first, att)
	two := a.reader.attButton(second, att)
	if one == two {
		t.Fatal("two messages share one attachment chip, so a download would use the wrong message")
	}
	if again := a.reader.attButton(first, att); again != one {
		t.Fatal("the chip of one message is not reused")
	}

	a.reader.cardFor("m1")
	a.reader.reset()
	if len(a.reader.cards) != 0 || len(a.reader.atts) != 0 {
		t.Fatalf("reset left %d cards and %d chips behind", len(a.reader.cards), len(a.reader.atts))
	}
}
