package ui

import (
	"strings"
	"testing"

	"github.com/zhsj/yauzin/internal/mailto"
)

// TestOpenMailtoFillsTheComposer pins the mapping from the parsed URI onto the
// composer: the five text fields and the caret at the end of the body.
func TestOpenMailtoFillsTheComposer(t *testing.T) {
	a, _, _ := composeTestApp(t)
	a.openMailto(mailto.Message{
		To:      "bob@example.com, carol@example.com",
		Cc:      "cc@example.com",
		Bcc:     "bcc@example.com",
		Subject: "Hello",
		Body:    "Line 1\nLine 2",
	})

	c, ok := a.overlay.(*composeView)
	if !ok {
		t.Fatalf("overlay = %T, want a composer", a.overlay)
	}
	if c.mode != composeNew {
		t.Fatalf("mode = %v, want a new message", c.mode)
	}
	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{"To", c.to.Text(), "bob@example.com, carol@example.com"},
		{"Cc", c.cc.Text(), "cc@example.com"},
		{"Bcc", c.bcc.Text(), "bcc@example.com"},
		{"Subject", c.subject.Text(), "Hello"},
		{"Body", c.body.Text(), "Line 1\nLine 2"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	if start, end := c.body.Selection(); start != end || start != len([]rune("Line 1\nLine 2")) {
		t.Errorf("body caret = %d..%d, want it at the end of the text", start, end)
	}
}

// TestMailtoThreadHeadersReachTheDraft checks the legacy in-reply-to field end
// to end: the composer shows the normalized ID under References, and the draft
// it sends carries both headers without touching the subject.
func TestMailtoThreadHeadersReachTheDraft(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	a.openMailto(mailto.Message{
		To:        "bob@example.com",
		Subject:   "Re: lunch",
		InReplyTo: "parent@example.com",
	})

	c := a.compose
	if c == nil {
		t.Fatal("the mailto composer is not the open layer")
	}
	if got, want := c.inReplyTo, "<parent@example.com>"; got != want {
		t.Fatalf("In-Reply-To = %q, want %q", got, want)
	}
	if got, want := strings.Join(c.references, " "), "<parent@example.com>"; got != want {
		t.Fatalf("References = %q, want %q", got, want)
	}

	c.send()
	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 1 })

	draft := svc.sentDrafts()[0]
	if draft.InReplyTo != c.inReplyTo {
		t.Errorf("sent In-Reply-To = %q, want %q", draft.InReplyTo, c.inReplyTo)
	}
	if got, want := strings.Join(draft.References, " "), strings.Join(c.references, " "); got != want {
		t.Errorf("sent References = %q, want %q", got, want)
	}
	if got, want := draft.Subject, "Re: lunch"; got != want {
		t.Errorf("sent subject = %q, want the link's subject %q", got, want)
	}
}

// TestMailtoDropsAnInvalidThreadID keeps a value that is not a Message-ID out
// of the headers: the draft goes out as a message of its own instead.
func TestMailtoDropsAnInvalidThreadID(t *testing.T) {
	a, _, _ := composeTestApp(t)

	for _, raw := range []string{"", "not an id", "<a@example.com\r\nBcc: victim@example.com>"} {
		a.openMailto(mailto.Message{To: "bob@example.com", InReplyTo: raw})
		c := a.compose
		if c == nil {
			t.Fatalf("mailto with in-reply-to %q opened no composer", raw)
		}
		if c.inReplyTo != "" || len(c.references) != 0 {
			t.Errorf("in-reply-to %q produced %q/%q, want neither header",
				raw, c.inReplyTo, c.references)
		}
	}
}

// TestMailtoReplacesTheOpenComposer follows the agreed policy: an arriving
// mailto wins over an unsent draft.
func TestMailtoReplacesTheOpenComposer(t *testing.T) {
	a, _, _ := composeTestApp(t)
	a.openCompose(composeNew, nil)
	first := a.compose
	first.to.SetText("someone@example.com")
	first.body.SetText("unsent text")

	a.openMailto(mailto.Message{To: "bob@example.com", Body: "from the link"})

	if a.compose == first {
		t.Fatal("the mailto composer reused the open draft")
	}
	if got := a.compose.to.Text(); got != "bob@example.com" {
		t.Errorf("To = %q, want the link's recipient", got)
	}
	if got := a.compose.body.Text(); got != "from the link" {
		t.Errorf("Body = %q, want the link's text", got)
	}
}

// TestRequestMailtoAsksForAWindow checks the activation path: the request opens
// a composer through the queue and wakes the lifecycle loop for a window.
func TestRequestMailtoAsksForAWindow(t *testing.T) {
	a, _, _ := composeTestApp(t)
	a.requestMailto("mailto:bob@example.com?subject=Hi")

	select {
	case <-a.openCh:
	default:
		t.Fatal("the request did not ask for a window")
	}
	a.drain()
	c, ok := a.overlay.(*composeView)
	if !ok {
		t.Fatalf("overlay = %T, want a composer", a.overlay)
	}
	if got := c.subject.Text(); got != "Hi" {
		t.Errorf("Subject = %q, want %q", got, "Hi")
	}
}

// TestRequestMailtoIgnoresABrokenURI keeps an undecodable link from opening an
// empty composer; the window is still asked for above.
func TestRequestMailtoIgnoresABrokenURI(t *testing.T) {
	a, _, _ := composeTestApp(t)
	a.requestMailto("mailto:bob%zz@example.com")
	a.drain()

	if a.overlay != nil {
		t.Fatalf("overlay = %T, want none", a.overlay)
	}
}

// TestMailtoFocusFollowsTheRecipient pins the keyboard: a link that already
// names a recipient is written in the body, a bare one still starts in To.
func TestMailtoFocusFollowsTheRecipient(t *testing.T) {
	a, _, _ := composeTestApp(t)
	router, ops, gtx := newRouterFrame(testWindow)
	frame := func() { runFrame(a, router, ops, gtx) }

	a.openMailto(mailto.Message{To: "bob@example.com", Subject: "Hi"})
	c := a.compose
	frame()
	frame()
	if !gtx.Focused(&c.body) {
		t.Fatal("the mailto composer opened without the keyboard in the body")
	}
	if gtx.Focused(&c.to) {
		t.Fatal("the filled recipient field kept the keyboard")
	}

	a.openMailto(mailto.Message{Subject: "Hi"})
	c = a.compose
	frame()
	frame()
	if !gtx.Focused(&c.to) {
		t.Fatal("a link without a recipient opened without the keyboard in To")
	}
}
