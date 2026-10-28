package ui

import (
	"image"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"

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

// TestReplyQuotesTheSelectedBody pins the selection rule at the card: the
// composer quotes the fragment the reader selected, while forwarding keeps
// quoting the whole message.
func TestReplyQuotesTheSelectedBody(t *testing.T) {
	a := testApp(t)
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:   "m1",
			From: mail.Address{Name: "Bob", Email: "bob@example.com"},
		},
		TextBody: "first\nsecond\nthird",
	}
	a.msgs = []*mail.Message{msg}
	card := a.reader.cardFor("m1")
	card.actions(a)
	sel := card.bodySelection(msg)
	sel.SetText(msg.TextBody)
	sel.SetCaret(6, 12) // "second"

	card.reply.onTap()
	if a.compose == nil {
		t.Fatal("the reply button opened no composer")
	}
	got := a.compose.body.Text()
	if want := replyQuote(msg, "second"); got != want {
		t.Fatalf("reply body = %q, want %q", got, want)
	}
	if strings.Contains(got, "> third") {
		t.Fatalf("reply quoted text outside the selection: %q", got)
	}
	// The reply is written below the quote, so the caret has to wait there.
	if start, end := a.compose.body.Selection(); start != end || start != utf8.RuneCountInString(got) {
		t.Fatalf("reply caret = %d..%d of %d runes, want it at the end of the quote",
			start, end, utf8.RuneCountInString(got))
	}

	card.fwd.onTap()
	if got, want := a.compose.body.Text(), forwardQuote(msg); got != want {
		t.Fatalf("forward body = %q, want %q", got, want)
	}
}

// TestReplyIgnoresAnotherMessagesSelection keeps a selection made in one
// message out of a reply to another, and out of a card whose body was never
// laid out.
func TestReplyIgnoresAnotherMessagesSelection(t *testing.T) {
	a := testApp(t)
	older := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m1"}, TextBody: "older text"}
	newer := &mail.Message{
		MessageSummary: mail.MessageSummary{ID: "m2", From: mail.Address{Email: "bob@example.com"}},
		TextBody:       "newer text",
	}
	a.msgs = []*mail.Message{older, newer}

	card := a.reader.cardFor("m1")
	sel := card.bodySelection(older)
	sel.SetText(older.TextBody)
	sel.SetCaret(0, 5)

	if got := a.reader.replySelection("m2"); got != "" {
		t.Fatalf("replySelection(m2) = %q, want nothing from another message", got)
	}
	if got := a.reader.replySelection("m3"); got != "" {
		t.Fatalf("replySelection(m3) = %q, want nothing from a card that does not exist", got)
	}

	a.openCompose(composeReply, newer)
	if got, want := a.compose.body.Text(), replyQuote(newer, ""); got != want {
		t.Fatalf("reply body = %q, want the whole message %q", got, want)
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

// openReaderTest puts the app in the reader of the first conversation of a
// three-row page and returns a frame with the key filters registered.
func openReaderTest(t *testing.T) (*App, *input.Router, layout.Context) {
	t.Helper()
	a := testApp(t)
	a.list.setThreads(cursorThreads(3))
	a.thread = a.list.threads[0]
	a.selectedThread = a.thread.ID
	for i := 0; i < 3; i++ {
		a.msgs = append(a.msgs, &mail.Message{
			MessageSummary: mail.MessageSummary{ID: "m" + strconv.Itoa(i)},
			TextBody:       "a body long enough to stand in for real mail",
		})
	}
	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)
	return a, router, gtx
}

// TestReaderScrollKeys pins the scroll layer: the arrows move a small step, the
// paging keys a screenful less the overlap, and Home/End go to the edges. Each
// key is read before the next frame lays the list out, which is exactly the
// window in which the offset is still observable.
func TestReaderScrollKeys(t *testing.T) {
	a, router, gtx := openReaderTest(t)

	line := gtx.Dp(readerScrollStep)
	page := a.reader.viewH - gtx.Dp(readerPageOverlap)
	if page <= line {
		t.Fatalf("page step = %dpx, want more than the %dpx line step", page, line)
	}

	router.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	a.handleKeys(gtx)
	if got := a.reader.list.Position.Offset; got != line {
		t.Fatalf("offset = %d after Down, want %d", got, line)
	}

	router.Queue(key.Event{Name: key.NameUpArrow, State: key.Press})
	a.handleKeys(gtx)
	if got := a.reader.list.Position.Offset; got != 0 {
		t.Fatalf("offset = %d after Up, want the top", got)
	}

	router.Queue(key.Event{Name: key.NamePageDown, State: key.Press})
	a.handleKeys(gtx)
	if got := a.reader.list.Position.Offset; got != page {
		t.Fatalf("offset = %d after PgDn, want %d", got, page)
	}

	router.Queue(key.Event{Name: key.NamePageUp, State: key.Press})
	a.handleKeys(gtx)
	if got := a.reader.list.Position.Offset; got != 0 {
		t.Fatalf("offset = %d after PgUp, want the top", got)
	}

	router.Queue(key.Event{Name: key.NameEnd, State: key.Press})
	a.handleKeys(gtx)
	if pos := a.reader.list.Position; pos.First != len(a.msgs)-1 || pos.Offset != 0 {
		t.Fatalf("position = %+v after End, want the last card at the top", pos)
	}

	router.Queue(key.Event{Name: key.NameHome, State: key.Press})
	a.handleKeys(gtx)
	if pos := a.reader.list.Position; pos.First != 0 || pos.Offset != 0 {
		t.Fatalf("position = %+v after Home, want the first card", pos)
	}
}

// TestReaderJKNavigatesConversations checks the other half of the split: J and
// K open the neighbouring conversation of the page and move the list cursor
// with it, stopping at the edges instead of wrapping.
func TestReaderJKNavigatesConversations(t *testing.T) {
	a := testApp(t)
	useService(a, &fakeService{})
	a.list.setThreads(cursorThreads(3))
	a.thread = a.list.threads[0]
	a.selectedThread = a.thread.ID
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}}}

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)

	router.Queue(key.Event{Name: "J", State: key.Press})
	a.handleKeys(gtx)
	if a.selectedThread != "t1" || a.cursorThread != 1 {
		t.Fatalf("after J: thread = %q, cursor = %d, want t1 at row 1",
			a.selectedThread, a.cursorThread)
	}

	router.Queue(key.Event{Name: "K", State: key.Press})
	a.handleKeys(gtx)
	if a.selectedThread != "t0" || a.cursorThread != 0 {
		t.Fatalf("after K: thread = %q, cursor = %d, want t0 at row 0",
			a.selectedThread, a.cursorThread)
	}

	// The top of the page has no previous conversation to open.
	router.Queue(key.Event{Name: "K", State: key.Press})
	a.handleKeys(gtx)
	if a.selectedThread != "t0" {
		t.Fatalf("K at the top opened %q, want to stay on t0", a.selectedThread)
	}

	a.thread = a.list.threads[len(a.list.threads)-1]
	a.selectedThread = a.thread.ID
	router.Queue(key.Event{Name: "J", State: key.Press})
	a.handleKeys(gtx)
	if a.selectedThread != a.thread.ID {
		t.Fatalf("J at the bottom opened %q, want to stay on %q", a.selectedThread, a.thread.ID)
	}
}
