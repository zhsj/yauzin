package ui

import (
	"image"
	"testing"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
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
	card.actions(a)
	reply := card.reply

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

// TestMessageCardMenuFollowsTheMessage pins which entries a card offers: the
// previews need an HTML body, the source view is always there.
func TestMessageCardMenuFollowsTheMessage(t *testing.T) {
	a := testApp(t)
	withHTML := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m1"}, HTMLBody: "<p>hi</p>"}
	plain := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m2"}, TextBody: "hi"}

	card := a.reader.cardFor("m1")
	card.actions(a)

	items := card.menuItems(a, withHTML)
	want := []string{
		i18n.T("reader.menu.safePreview"),
		i18n.T("reader.menu.fullPreview"),
		i18n.T("reader.menu.viewSource"),
	}
	if len(items) != len(want) {
		t.Fatalf("menu has %d entries, want %d", len(items), len(want))
	}
	for i, item := range items {
		if item.Label != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, item.Label, want[i])
		}
		if item.Icon.glyph == 0 || item.OnTap == nil {
			t.Fatalf("entry %d is not actionable: %+v", i, item)
		}
	}

	items = card.menuItems(a, plain)
	if len(items) != 1 || items[0].Label != i18n.T("reader.menu.viewSource") {
		t.Fatalf("text-only menu = %+v, want the source view alone", items)
	}
}

// TestMessageCardMoreOpensTheMenu drives the button without a pointer: the
// click callback is what the menu anchor is built from.
func TestMessageCardMoreOpensTheMenu(t *testing.T) {
	a := testApp(t)
	a.msgs = []*mail.Message{{
		MessageSummary: mail.MessageSummary{ID: "m1"},
		HTMLBody:       "<p>hi</p>",
	}}
	card := a.reader.cardFor("m1")
	card.actions(a)
	card.more.onTap()

	m, ok := a.overlay.(*menuOverlay)
	if !ok {
		t.Fatalf("overlay = %T after the more button, want *menuOverlay", a.overlay)
	}
	if len(m.items) != 3 {
		t.Fatalf("menu has %d entries, want 3", len(m.items))
	}
}

func TestMessageCardMoreAnchorFallsBackToThePointer(t *testing.T) {
	a := testApp(t)
	card := a.reader.cardFor("m1")
	card.actions(a)

	a.pointer = image.Pt(120, 240)
	if got, want := card.moreAnchor(a), image.Rect(120, 240, 121, 241); got != want {
		t.Fatalf("anchor = %v, want the pointer %v", got, want)
	}

	card.more.rect = image.Rect(10, 20, 42, 52)
	if got := card.moreAnchor(a); got != card.more.rect {
		t.Fatalf("anchor = %v, want the button %v", got, card.more.rect)
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
