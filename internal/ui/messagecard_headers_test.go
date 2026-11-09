package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
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
		{i18n.T("compose.from"), "Bob <bob@example.com>"},
		{i18n.T("compose.to"), "Ada <ada@example.com>, second@example.com"},
		{i18n.T("compose.cc"), "cc@example.com"},
		{i18n.T("reader.headerReplyTo"), "list@example.com"},
		{i18n.T("compose.subject"), "hello"},
		{i18n.T("compose.headerDate"), formatFullTime(msg.Date)},
		{i18n.T("compose.headerMessageID"), "<m1@example.com>"},
		{i18n.T("compose.headerReferences"), "<m0@example.com>"},
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
	click()
	if card.detailsOpen {
		t.Fatal("clicking the switch again left the details open")
	}
}
