package ui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestWebURLKeepsOnlyWholeHTTPAddresses pins the URL rule of the selection
// menu: the fragment is handed to the platform opener, so only a whole http(s)
// address may pass and every other scheme stays a plain copy.
func TestWebURLKeepsOnlyWholeHTTPAddresses(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"https://example.com/a?b=1", "https://example.com/a?b=1"},
		{"  http://example.com  ", "http://example.com"},
		{"HTTPS://Example.COM/Path", "HTTPS://Example.COM/Path"},
		{"www.example.com", ""},
		{"example.com", ""},
		{"file:///etc/passwd", ""},
		{"javascript:alert(1)", ""},
		{"mailto:ada@example.com", ""},
		{"https://example.com/a b", ""},
		{"see https://example.com", ""},
		{"https://", ""},
		{"", ""},
		{"   ", ""},
	} {
		if got := webURL(tc.in); got != tc.want {
			t.Errorf("webURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSelectionMenuItemsFollowTheFragment checks the entries offered for a
// selection: copy is always there, opening a browser only for a whole URL.
func TestSelectionMenuItemsFollowTheFragment(t *testing.T) {
	a := testApp(t)

	items := a.selectionMenuItems("just words")
	if len(items) != 1 || items[0].Label != i18n.T("reader.menu.copy") {
		t.Fatalf("plain selection menu = %+v, want the copy entry alone", items)
	}
	if items[0].OnTap == nil {
		t.Fatal("the copy entry is not actionable")
	}

	items = a.selectionMenuItems("  https://example.com/a  ")
	if len(items) != 2 {
		t.Fatalf("link selection menu has %d entries, want copy and open", len(items))
	}
	if items[1].Label != i18n.T("reader.menu.openLink") || items[1].OnTap == nil {
		t.Fatalf("second entry = %+v, want the browser entry", items[1])
	}

	if items := a.selectionMenuItems("   "); items != nil {
		t.Fatalf("empty selection menu = %+v, want none", items)
	}
}

// TestCopyTextWaitsForTheNextFrame pins the clipboard hand-off: the menu
// callback only stages the text, and the next frame consumes the staging once
// it has a layout context to execute the write with.
func TestCopyTextWaitsForTheNextFrame(t *testing.T) {
	a := testApp(t)
	a.copyText("")
	if a.clipPending != "" {
		t.Fatalf("copying nothing staged %q", a.clipPending)
	}

	a.copyText("ada@example.com")
	if got := a.clipPending; got != "ada@example.com" {
		t.Fatalf("clipPending = %q, want the copied text", got)
	}

	var ops op.Ops
	a.flushClipboard(testGtx(&ops, testWindow))
	if a.clipPending != "" {
		t.Fatalf("clipPending = %q after the frame, want it consumed", a.clipPending)
	}
}

// TestTextSelectionRightClickOnly covers the right-click rule of one run of
// text: a secondary press on a selection is reported, a press without a
// selection or with the primary button is not.
func TestTextSelectionRightClickOnly(t *testing.T) {
	a := testApp(t)
	router, ops, gtx := newRouterFrame(image.Pt(400, 120))
	gtx.Constraints = layout.Exact(image.Pt(300, 60))

	var s textSelection
	txt := "see https://example.com now"
	at := f32.Pt(20, 12)
	frame := func() {
		ops.Reset()
		s.Wrap(gtx, a.reader, "m1", txt, textMain, 0, colText, font.Normal)
		router.Frame(ops)
	}
	press := func(buttons pointer.Buttons) {
		// Queued events are routed to the areas declared by the previous frame
		// and drained by the frame below, which is where Take sees them.
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: buttons})
		frame()
	}

	frame() // register the area
	press(pointer.ButtonPrimary)
	if _, ok := s.Take(); ok {
		t.Fatal("a primary press opened the selection menu")
	}

	// A right click without a selection has nothing to act on.
	press(pointer.ButtonSecondary)
	if _, ok := s.Take(); ok {
		t.Fatal("a right click without a selection opened the menu")
	}

	s.Select("m1").SetCaret(4, 23) // the URL
	frame()
	press(pointer.ButtonSecondary)
	if text, ok := s.Take(); !ok || text != "https://example.com" {
		t.Fatalf("right click reported %q, %v; want the selected URL", text, ok)
	}
	if _, ok := s.Take(); ok {
		t.Fatal("the press was reported twice")
	}
}

// selectionCard builds an open card for one message on a frame backed by a
// real router, so presses travel through the hit areas the card declared. The
// card is laid out once before returning, which registers those areas.
func selectionCard(t *testing.T) (*App, *messageCard, *mail.Message, *input.Router, *op.Ops, layout.Context) {
	t.Helper()
	a := testApp(t)
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:   "m1",
			From: mail.Address{Name: "Ada Lovelace", Email: "ada@example.com"},
		},
		TextBody: "the body",
	}
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{msg.ID: true}
	card := a.reader.cardFor(msg.ID)

	router, ops, gtx := newRouterFrame(image.Pt(600, 200))
	gtx.Constraints = layout.Exact(image.Pt(600, 200))
	ops.Reset()
	card.Layout(gtx, msg)
	router.Frame(ops)
	return a, card, msg, router, ops, gtx
}

// TestRightClickOnTheSenderOpensTheSelectionMenu drives a right click through
// the card: the press lands on the sender name, which the card must not treat
// as a fold request, and opens the menu for what the fragment allows.
func TestRightClickOnTheSenderOpensTheSelectionMenu(t *testing.T) {
	a, card, msg, router, ops, gtx := selectionCard(t)
	draw := func() {
		ops.Reset()
		card.Layout(gtx, msg)
		router.Frame(ops)
	}
	draw()
	card.name.sel.SetCaret(0, 12) // "Ada Lovelace"

	// Inside the sender name: the content inset starts at 14dp, the avatar is
	// 32dp wide and the gap after it 12dp.
	at := f32.Pt(64, 18)
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonSecondary})
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	draw()
	draw()

	if !a.expanded[msg.ID] {
		t.Fatal("a click on the sender name folded the card")
	}
	m, ok := a.overlay.(*menuOverlay)
	if !ok {
		t.Fatalf("overlay = %T after a right click, want a menu", a.overlay)
	}
	if len(m.items) != 1 || m.items[0].Label != i18n.T("reader.menu.copy") {
		t.Fatalf("menu = %+v, want the copy entry alone for a name", m.items)
	}

	// The entry stages the fragment for the frame that writes the clipboard.
	m.rows[0].onTap()
	if got := a.clipPending; got != "Ada Lovelace" {
		t.Fatalf("clipPending = %q after the copy entry, want the sender name", got)
	}
	if a.overlay != nil {
		t.Fatalf("overlay = %T after choosing an entry, want it closed", a.overlay)
	}
}

// TestRightClickOnTheBodyReportsTheFragment checks the body's own area: the
// body text is the third run the card consults, and its right clicks must
// report the body selection.
func TestRightClickOnTheBodyReportsTheFragment(t *testing.T) {
	_, card, msg, router, ops, gtx := selectionCard(t)
	msg.TextBody = "see https://example.com/a"
	draw := func() {
		ops.Reset()
		card.body(gtx, msg)
		router.Frame(ops)
	}
	draw()
	card.bodyText.sel.SetCaret(4, 27) // the URL

	at := f32.Pt(20, 10)
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonSecondary})
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	draw()

	text, ok := card.selection()
	if !ok || text != "https://example.com/a" {
		t.Fatalf("body right click reported %q, %v; want the selected URL", text, ok)
	}
}

// readerSelections builds a reader with a subject and two messages. Both cards
// exist, so a test can give a fragment to a card that this frame does not lay
// out.
func readerSelections(t *testing.T) (*App, *messageCard, *messageCard) {
	t.Helper()
	a := testApp(t)
	a.thread = mail.ThreadSummary{ID: "t1", Subject: "the subject"}
	a.msgs = []*mail.Message{
		{
			MessageSummary: mail.MessageSummary{ID: "m1", From: mail.Address{Name: "Ada Lovelace", Email: "ada@example.com"}},
			TextBody:       "first body",
		},
		{
			MessageSummary: mail.MessageSummary{ID: "m2", From: mail.Address{Name: "Grace Hopper", Email: "grace@example.com"}},
			TextBody:       "second body",
		},
	}
	return a, a.reader.cardFor("m1"), a.reader.cardFor("m2")
}

// TestReaderKeepsOneSelection pins exclusivity across the conversation: a
// fragment selected in the open card retires the one a collapsed card held,
// which is never laid out and would otherwise stay hidden.
func TestReaderKeepsOneSelection(t *testing.T) {
	a, first, second := readerSelections(t)
	a.expanded = map[string]bool{"m1": true}

	sel := second.bodyText.Select("m2")
	sel.SetText("second body")
	sel.SetCaret(0, 6)
	sel = first.name.Select("m1")
	sel.SetText("Ada Lovelace")
	sel.SetCaret(0, 3)

	var ops op.Ops
	a.reader.Layout(testGtx(&ops, image.Pt(900, 600)))

	if got := first.name.Selected("m1"); got != "Ada" {
		t.Fatalf("sender selection = %q, want the fragment selected last", got)
	}
	if got := second.bodyText.Selected("m2"); got != "" {
		t.Fatalf("collapsed card kept %q, want its selection retired", got)
	}
}

// TestSelectionInOneMessageRetiresTheOther covers two bodies in one frame:
// whichever the frame lays out last wins, and only one survives.
func TestSelectionInOneMessageRetiresTheOther(t *testing.T) {
	a, first, second := readerSelections(t)
	a.expanded = map[string]bool{"m1": true, "m2": true}

	for _, c := range []struct {
		card *messageCard
		id   string
		txt  string
		end  int
	}{
		{first, "m1", "first body", 5},
		{second, "m2", "second body", 6},
	} {
		sel := c.card.bodyText.Select(c.id)
		sel.SetText(c.txt)
		sel.SetCaret(0, c.end)
	}

	var ops op.Ops
	a.reader.Layout(testGtx(&ops, image.Pt(900, 600)))

	held := 0
	for _, id := range []string{"m1", "m2"} {
		card := a.reader.cards[id]
		if card.bodyText.Selected(id) != "" {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("%d body selections survived the frame, want exactly one", held)
	}
}

// TestSubjectSelectionRetiresTheBody covers the headline: selecting it drops
// the fragment held by a message, and the other way round.
func TestSubjectSelectionRetiresTheBody(t *testing.T) {
	a, card, _ := readerSelections(t)
	body := card.bodyText.Select("m1")
	body.SetText("first body")
	body.SetCaret(0, 5)

	var ops op.Ops
	gtx := testGtx(&ops, image.Pt(900, 600))
	a.reader.Layout(gtx)
	if got := card.bodyText.Selected("m1"); got != "first" {
		t.Fatalf("body selection = %q, want it kept while the subject holds none", got)
	}

	subject := a.reader.subject.Select("t1")
	subject.SetText("the subject")
	subject.SetCaret(0, 3)
	a.reader.Layout(gtx)

	if got := a.reader.subject.Selected("t1"); got != "the" {
		t.Fatalf("subject selection = %q, want the fragment selected last", got)
	}
	if got := card.bodyText.Selected("m1"); got != "" {
		t.Fatalf("body kept %q after the subject was selected, want it retired", got)
	}
}

// TestClickOnAnotherRunRetiresTheSelection pins the click rule: a plain left
// click on a run without dragging still drops the selection held elsewhere.
func TestClickOnAnotherRunRetiresTheSelection(t *testing.T) {
	_, card, msg, router, ops, gtx := selectionCard(t)
	draw := func() {
		ops.Reset()
		card.Layout(gtx, msg)
		router.Frame(ops)
	}

	card.bodyText.sel.SetCaret(0, 8) // "the body"
	draw()
	if got := card.bodyText.Selected(msg.ID); got != "the body" {
		t.Fatalf("body selection = %q, want the fragment before the click", got)
	}

	at := f32.Pt(64, 18) // the sender name
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonPrimary})
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	draw()

	if got := card.bodyText.Selected(msg.ID); got != "" {
		t.Fatalf("body kept %q after a click on the sender, want it retired", got)
	}
}

// TestOpenCardFoldsFromNonTextParts pins which parts of the open head fold the
// card: the avatar, the timestamp and the arrow, never the sender text.
func TestOpenCardFoldsFromNonTextParts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		click func(*messageCard)
	}{
		{"arrow", func(c *messageCard) { c.fold.Click() }},
		{"avatar", func(c *messageCard) { c.avatar.Click() }},
		{"timestamp", func(c *messageCard) { c.stamp.Click() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			msg := &mail.Message{MessageSummary: mail.MessageSummary{ID: "m1", From: mail.Address{Name: "Ada", Email: "ada@example.com"}}}
			a.msgs = []*mail.Message{msg}
			a.expanded = map[string]bool{msg.ID: true}
			card := a.reader.cardFor(msg.ID)

			var ops op.Ops
			gtx := testGtx(&ops, image.Pt(600, 200))
			card.Layout(gtx, msg)
			tc.click(card)
			card.Layout(gtx, msg)
			if a.expanded[msg.ID] {
				t.Fatalf("the card stayed open after a click on its %s", tc.name)
			}
		})
	}
}
