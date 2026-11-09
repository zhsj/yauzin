package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// headerMessage returns a message that carries every header the details block
// knows about.
func headerMessage() *mail.Message {
	return &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:      "m1",
			From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
			To:      []mail.Address{{Name: "Ada", Email: "ada@example.com"}, {Email: "second@example.com"}},
			Subject: "hello",
			Date:    time.Date(2026, time.November, 10, 9, 30, 0, 0, time.Local),
		},
		Cc:         []mail.Address{{Email: "cc@example.com"}},
		MessageID:  "<m1@example.com>",
		References: []string{"<m0@example.com>"},
		ReplyTo:    []mail.Address{{Email: "list@example.com"}},
		TextBody:   "the body",
	}
}

// TestDetailsRowsListTheMessageHeaders pins the fields, their order and their
// rendering: the block is the only place the reader shows recipients at all.
func TestDetailsRowsListTheMessageHeaders(t *testing.T) {
	msg := headerMessage()
	got := detailsRows(msg)
	want := []headerRow{
		{headerFieldFrom, i18n.T("compose.from"), "Bob <bob@example.com>"},
		{headerFieldTo, i18n.T("compose.to"), "Ada <ada@example.com>, second@example.com"},
		{headerFieldCc, i18n.T("compose.cc"), "cc@example.com"},
		{headerFieldReplyTo, i18n.T("reader.headerReplyTo"), "list@example.com"},
		{headerFieldSubject, i18n.T("compose.subject"), "hello"},
		{headerFieldDate, i18n.T("compose.headerDate"), formatFullTime(msg.Date)},
		{headerFieldMessageID, i18n.T("compose.headerMessageID"), "<m1@example.com>"},
		{headerFieldReferences, i18n.T("compose.headerReferences"), "<m0@example.com>"},
	}
	if len(got) != len(want) {
		t.Fatalf("details rows = %+v, want %d rows", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDetailsRowsSkipEmptyHeaders keeps the block from listing headers the
// message does not carry: most messages have no Reply-To and no Cc.
func TestDetailsRowsSkipEmptyHeaders(t *testing.T) {
	msg := &mail.Message{MessageSummary: mail.MessageSummary{
		From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
		Subject: "hello",
	}}
	rows := detailsRows(msg)
	want := []string{i18n.T("compose.from"), i18n.T("compose.subject")}
	if len(rows) != len(want) {
		t.Fatalf("details rows = %+v, want only %v", rows, want)
	}
	for i, label := range want {
		if rows[i].label != label {
			t.Fatalf("row %d label = %q, want %q", i, rows[i].label, label)
		}
	}
}

// TestDetailsRowsCutLongReferences keeps a pathological chain from filling the
// card: the block shows the newest entries, with a marker for the rest.
func TestDetailsRowsCutLongReferences(t *testing.T) {
	refs := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		refs = append(refs, "<m"+string(rune('0'+i))+"@example.com>")
	}
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{From: mail.Address{Email: "bob@example.com"}},
		References:     refs,
	}
	var value string
	for _, row := range detailsRows(msg) {
		if row.label == i18n.T("compose.headerReferences") {
			value = row.value
		}
	}
	if !strings.HasPrefix(value, "… ") {
		t.Fatalf("references = %q, want the cut marker", value)
	}
	if strings.Contains(value, refs[0]) {
		t.Fatalf("references = %q, want the oldest IDs dropped", value)
	}
	if !strings.Contains(value, refs[len(refs)-1]) {
		t.Fatalf("references = %q, want the newest ID kept", value)
	}
}

// TestDetailsBlockIsCountedInTheHead pins the find bar's measurement: the block
// sits above the divider, so its height has to reach headH, or every hit in the
// card is scrolled to a line that is off by the height of the block.
func TestDetailsBlockIsCountedInTheHead(t *testing.T) {
	a := testApp(t)
	msg := headerMessage()
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{msg.ID: true}
	card := a.reader.cardFor(msg.ID)

	var ops op.Ops
	gtx := testCell(&ops, image.Pt(600, 1<<20))
	card.Layout(gtx, msg)
	closed := card.headH

	// The card insets its content by 14dp on each side; at the 1:1 density of
	// the tests the block is measured at the width it is drawn in.
	var detailOps op.Ops
	detailGtx := testCell(&detailOps, image.Pt(600-2*14, 1<<20))
	want := card.details(detailGtx, msg).Size.Y
	if want <= 0 {
		t.Fatal("the details block reports no height")
	}

	card.detailsOpen = true
	ops.Reset()
	card.Layout(gtx, msg)
	if got := card.headH - closed; got != want {
		t.Fatalf("head grew by %dpx, want the %dpx details block", got, want)
	}
}

// TestDetailsToggleClicksThrough drives the switch the way the pointer does: a
// click on the control folds the details out and a second click folds them back,
// without the card itself collapsing.
func TestDetailsToggleClicksThrough(t *testing.T) {
	a := testApp(t)
	msg := headerMessage()
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{msg.ID: true}
	card := a.reader.cardFor(msg.ID)

	router, ops, gtx := newRouterFrame(image.Pt(600, 200))
	gtx.Constraints = layout.Exact(image.Pt(600, 200))
	frame := func() {
		ops.Reset()
		card.detailsToggle(gtx, a.th)
		router.Frame(ops)
	}
	click := func() {
		// The click lands on the label, which starts at the left edge of the
		// control. Queued events reach the areas of the previous frame and are
		// drained by the frame after that.
		at := f32.Pt(2, 2)
		router.Queue(mouseEvent(pointer.Press, at), mouseEvent(pointer.Release, at))
		frame()
		frame()
	}

	frame() // register the control's area
	click()
	if !card.detailsOpen {
		t.Fatal("clicking the switch left the details closed")
	}
	if !a.expanded[msg.ID] {
		t.Fatal("the details switch folded the card")
	}

	// A fragment selected in the block goes away with it: the runs are not
	// drawn while the block is closed.
	sel := card.detailsSel[headerFieldFrom].Select(msg.ID)
	sel.SetText("Bob <bob@example.com>")
	sel.SetCaret(0, 3)
	click()
	if card.detailsOpen {
		t.Fatal("clicking the switch again left the details open")
	}
	if got := card.detailsSel[headerFieldFrom].Selected(msg.ID); got != "" {
		t.Fatalf("closing the details kept %q selected", got)
	}
}

// TestRightClickOnAHeaderDetailCopiesIt drives a right click through the card:
// the press lands on the value of the first detail row, which has to report the
// selection so the copy menu can act on it.
func TestRightClickOnAHeaderDetailCopiesIt(t *testing.T) {
	a := testApp(t)
	msg := headerMessage()
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{msg.ID: true}
	card := a.reader.cardFor(msg.ID)

	router, ops, gtx := newRouterFrame(image.Pt(600, 400))
	gtx.Constraints = layout.Exact(image.Pt(600, 400))
	draw := func() {
		ops.Reset()
		card.Layout(gtx, msg)
		router.Frame(ops)
	}

	// The first detail row sits below the head and its divider, and its value
	// column begins after the 96dp label and its 8dp gap. Both measurements
	// come from the card itself instead of a guessed pixel.
	draw()
	head := card.headH - 2*gtx.Dp(8) - gtx.Dp(1)
	card.detailsOpen = true
	draw()
	line := a.th.text(testGtx(new(op.Ops), image.Pt(400, 400)), "x", textMeta, colText, font.Normal, 1).Size.Y
	sel := card.detailsSel[headerFieldFrom].Select(msg.ID)
	sel.SetCaret(0, 3) // "Bob"
	at := f32.Pt(float32(gtx.Dp(14+96+8+4)), float32(gtx.Dp(cardInsetTop+6+1)+head+line/2))
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonSecondary})
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	draw()
	draw()

	m, ok := a.overlay.(*menuOverlay)
	if !ok {
		t.Fatalf("overlay = %T after a right click, want a menu", a.overlay)
	}
	if len(m.items) == 0 || m.items[0].Label != i18n.T("reader.menu.copy") {
		t.Fatalf("menu = %+v, want the copy entry first", m.items)
	}
	m.rows[0].onTap()
	if got := a.clipPending; got != "Bob" {
		t.Fatalf("clipPending = %q after the copy entry, want the selected fragment", got)
	}
}

// TestHeaderDetailSelectionRetiresTheBody keeps the reader's one-selection rule:
// a fragment selected in the header details drops the one in the body, the way
// the sender name and the subject already do.
func TestHeaderDetailSelectionRetiresTheBody(t *testing.T) {
	a := testApp(t)
	msg := headerMessage()
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{msg.ID: true}
	card := a.reader.cardFor(msg.ID)
	card.detailsOpen = true

	var ops op.Ops
	gtx := testGtx(&ops, image.Pt(900, 600))
	a.reader.Layout(gtx)

	body := card.bodySelection(msg)
	body.SetCaret(0, 3) // "the"
	a.reader.Layout(gtx)
	if got := card.bodyText.Selected(msg.ID); got != "the" {
		t.Fatalf("body selection = %q, want the fragment before the header was selected", got)
	}

	sel := card.detailsSel[headerFieldSubject].Select(msg.ID)
	sel.SetCaret(0, 2) // "he"
	a.reader.Layout(gtx)
	if got := card.detailsSel[headerFieldSubject].Selected(msg.ID); got != "he" {
		t.Fatalf("header selection = %q, want the fragment selected last", got)
	}
	if got := card.bodyText.Selected(msg.ID); got != "" {
		t.Fatalf("body kept %q after the header was selected, want it retired", got)
	}
}
