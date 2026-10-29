package ui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestFindRangesKeepsRuneOffsets pins the offsets the Selectable is asked
// about: they are rune offsets, so a non-ASCII prefix may not shift them the
// way a byte scan would.
func TestFindRangesKeepsRuneOffsets(t *testing.T) {
	body := "Ärger und ärger"
	got := findRanges(body, "ÄRGER", 10)
	want := [][2]int{{0, 5}, {10, 15}}
	if len(got) != len(want) {
		t.Fatalf("ranges = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranges = %v, want %v", got, want)
		}
	}

	// The ASCII case is the same rule: a match advances by its own length, so
	// two hits never overlap.
	got = findRanges("aaaa", "aa", 10)
	if len(got) != 2 || got[0] != [2]int{0, 2} || got[1] != [2]int{2, 4} {
		t.Fatalf("ranges of a repeating pattern = %v, want the non-overlapping pair", got)
	}

	if got := findRanges("body", "", 10); got != nil {
		t.Fatalf("ranges of an empty query = %v, want none", got)
	}
}

// TestFindRangesRespectsTheLimit pins the cap: the caller reads a full result
// as "there may be more", so the scan may not keep collecting.
func TestFindRangesRespectsTheLimit(t *testing.T) {
	if got := findRanges(strings.Repeat("a", 10), "a", 3); len(got) != 3 {
		t.Fatalf("ranges = %v, want 3", got)
	}
}

// TestFindBarHitsFollowTheConversationOrder checks the order the jumps rely on:
// the messages in conversation order, each of them in body order.
func TestFindBarHitsFollowTheConversationOrder(t *testing.T) {
	a := testApp(t)
	a.reader.find.show()
	a.msgs = []*mail.Message{
		{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "needle one and needle two"},
		{MessageSummary: mail.MessageSummary{ID: "m1"}, TextBody: "a body without the term"},
		{MessageSummary: mail.MessageSummary{ID: "m2"}, TextBody: "needle three"},
	}
	a.reader.find.ed.SetText("needle")
	if !a.reader.find.sync(a.msgs) {
		t.Fatal("sync reported no change for a new query")
	}

	f := a.reader.find
	want := []findHit{
		{msgID: "m0", start: 0, end: 6},
		{msgID: "m0", start: 15, end: 21},
		{msgID: "m2", start: 0, end: 6},
	}
	if len(f.hits) != len(want) {
		t.Fatalf("hits = %+v, want %d", f.hits, len(want))
	}
	for i := range want {
		if f.hits[i] != want[i] {
			t.Fatalf("hit %d = %+v, want %+v", i, f.hits[i], want[i])
		}
	}

	// The hits of one message are contiguous, which is what lets a card look
	// them up as a sub-slice.
	base, hits := f.hitsFor("m0")
	if base != 0 || len(hits) != 2 {
		t.Fatalf("hits of m0 = %d/%v, want the first two", base, hits)
	}
	if base, hits := f.hitsFor("m2"); base != 2 || len(hits) != 1 {
		t.Fatalf("hits of m2 = %d/%v, want the last one", base, hits)
	}

	// Calling sync again without changing anything keeps the hits.
	if a.reader.find.sync(a.msgs) {
		t.Fatal("sync recomputed an unchanged query")
	}
}

// TestFindBarCapsTheHitList keeps a pathological query from collecting
// thousands of ranges; the counter says the list was cut.
func TestFindBarCapsTheHitList(t *testing.T) {
	a := testApp(t)
	a.reader.find.show()
	a.msgs = []*mail.Message{{
		MessageSummary: mail.MessageSummary{ID: "m0"},
		TextBody:       strings.Repeat("a", findHitLimit+5),
	}}
	a.reader.find.ed.SetText("a")
	a.reader.find.sync(a.msgs)

	f := a.reader.find
	if len(f.hits) != findHitLimit || !f.capped {
		t.Fatalf("hits = %d, capped = %v; want %d and the cap", len(f.hits), f.capped, findHitLimit)
	}
	if got, want := f.counter(), i18n.T("reader.find.countCapped", map[string]int{"Current": 1, "Total": findHitLimit}); got != want {
		t.Fatalf("counter = %q, want %q", got, want)
	}
}

// TestFindBarStepsWrapAround checks the two directions and the wrap, and that
// every step asks for the hit to be shown.
func TestFindBarStepsWrapAround(t *testing.T) {
	a := testApp(t)
	a.reader.find.show()
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "x x x"}}
	a.reader.find.ed.SetText("x")
	a.reader.find.sync(a.msgs)

	f := a.reader.find
	if len(f.hits) != 3 {
		t.Fatalf("hits = %d, want 3", len(f.hits))
	}
	f.step(1)
	if f.cur != 1 {
		t.Fatalf("cursor = %d after next, want 1", f.cur)
	}
	f.step(-2)
	if f.cur != 2 {
		t.Fatalf("cursor = %d after wrapping back past the start, want 2", f.cur)
	}
	f.step(1)
	if f.cur != 0 {
		t.Fatalf("cursor = %d after wrapping past the end, want 0", f.cur)
	}

	f.clear()
	f.step(1)
	if f.cur != 0 {
		t.Fatalf("cursor = %d with no hits, want it untouched", f.cur)
	}
}

// TestFindJumpsToTheMessage checks the first half of a jump: the message is
// expanded, the list starts at its row, and the offset is reset so the second
// half can measure from the top of the card.
func TestFindJumpsToTheMessage(t *testing.T) {
	a := testApp(t)
	a.reader.find.show()
	a.msgs = []*mail.Message{
		{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "the first message"},
		{MessageSummary: mail.MessageSummary{ID: "m1"}, TextBody: "the second message with needle"},
	}
	a.expanded = map[string]bool{"m0": true}
	a.reader.list.Position = layout.Position{First: 0, Offset: 100}

	a.reader.find.ed.SetText("needle")
	a.reader.find.sync(a.msgs)

	if !a.expanded["m1"] {
		t.Fatal("the message with the hit stayed folded")
	}
	pos := a.reader.list.Position
	if pos.First != 1 || pos.Offset != 0 {
		t.Fatalf("position = %+v after the jump, want the second message at the top", pos)
	}
	if !a.reader.find.jump {
		t.Fatal("the jump did not ask for the in-card correction")
	}

	// A hit in the card that is already being scrolled keeps the offset: the
	// correction measures from the card itself, and resetting would flash the
	// top of a long message first.
	a.reader.list.Position = layout.Position{First: 1, Offset: 250}
	a.reader.find.reveal()
	if got := a.reader.list.Position; got.First != 1 || got.Offset != 250 {
		t.Fatalf("position = %+v for a hit of the first visible card, want it untouched", got)
	}
}

// TestFindHighlightsTheBody drives the card: the first layout collects the
// rectangles of the hits, the next one paints them from the cache.
func TestFindHighlightsTheBody(t *testing.T) {
	a := testApp(t)
	a.reader.find.show()
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{ID: "m0", From: mail.Address{Email: "ada@example.com"}},
		TextBody:       "Hello world, hello again",
	}
	a.msgs = []*mail.Message{msg}
	a.expanded = map[string]bool{"m0": true}
	a.reader.find.ed.SetText("hello")
	a.reader.find.sync(a.msgs)

	card := a.reader.cardFor("m0")
	var ops op.Ops
	gtx := testCell(&ops, image.Pt(600, 400))
	card.Layout(gtx, msg)

	marks := card.marks
	if marks.gen != a.reader.find.gen {
		t.Fatalf("marks generation = %d, want %d", marks.gen, a.reader.find.gen)
	}
	if len(marks.rects) != 2 {
		t.Fatalf("rects = %d, want one per hit", len(marks.rects))
	}
	if len(marks.hitOf) != 2 || marks.hitOf[0] != 0 || marks.hitOf[1] != 1 {
		t.Fatalf("hitOf = %v, want one owner per rectangle", marks.hitOf)
	}
	if r, ok := marks.current(0); !ok || r != marks.rects[0] {
		t.Fatalf("current(0) = %v, %v; want the first rectangle", r, ok)
	}
	if _, ok := marks.current(1); !ok {
		t.Fatal("current(1) found no rectangle for the second hit")
	}

	// Moving the cursor re-owns the emphasis without re-measuring: the
	// geometry stays valid and points at the other rectangle.
	a.reader.find.cur = 1
	if r, ok := marks.current(1); !ok || r != marks.rects[1] {
		t.Fatalf("current(1) = %v, %v; want the second rectangle", r, ok)
	}
	if !marks.valid(a.reader.find.gen, marks.width, msg.TextBody) {
		t.Fatal("changing the cursor invalidated the geometry")
	}
	a.reader.find.cur = 0

	// The second layout paints from the cache: the geometry must survive and
	// still describe the same two hits.
	ops.Reset()
	card.Layout(gtx, msg)
	if len(card.marks.rects) != 2 || card.marks.gen != marks.gen {
		t.Fatalf("the second layout dropped the cached geometry: %d rects", len(card.marks.rects))
	}

	// Closing the row drops the highlights.
	a.reader.find.hide()
	ops.Reset()
	card.Layout(gtx, msg)
	if len(card.marks.rects) != 0 {
		t.Fatalf("marks survived the closed row: %v", card.marks.rects)
	}
}

// TestFindEscClosesTheRowFirst pins the Esc order: the row is above the
// conversation, so the first Esc closes it and keeps the conversation open.
func TestFindEscClosesTheRowFirst(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(1))
	a.thread = a.list.threads[0]
	a.selectedThread = a.thread.ID
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "the body"}}

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)
	a.reader.find.show()
	runFrame(a, router, ops, gtx)

	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	a.handleKeys(gtx)
	if a.reader.find.open {
		t.Fatal("Esc left the find row open")
	}
	if a.selectedThread == "" {
		t.Fatal("Esc closed the conversation instead of the find row")
	}

	// The next Esc is the conversation's.
	runFrame(a, router, ops, gtx)
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	a.handleKeys(gtx)
	if a.selectedThread != "" {
		t.Fatalf("the second Esc left the conversation %q open", a.selectedThread)
	}
}

// TestFindShortcutNeedsAnOpenConversation checks the Ctrl+F gate: no reader, or
// a panel on top of it, leaves the row closed.
func TestFindShortcutNeedsAnOpenConversation(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(1))
	a.cursorThread = 0

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)

	press := func() {
		router.Queue(key.Event{Name: "F", Modifiers: key.ModShortcut, State: key.Press})
		a.handleKeys(gtx)
	}
	press()
	if a.reader.find.open {
		t.Fatal("Ctrl+F opened the row with no conversation")
	}

	a.thread = a.list.threads[0]
	a.selectedThread = a.thread.ID
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "a body"}}
	runFrame(a, router, ops, gtx)
	press()
	if !a.reader.find.open {
		t.Fatal("Ctrl+F did not open the row in the reader")
	}

	a.showSettings()
	a.reader.find.hide()
	press()
	if a.reader.find.open {
		t.Fatal("Ctrl+F opened the row over a panel")
	}
}

// TestFindRevealCentresTheHitInTheBody is the second half of a jump: a hit far
// down a body taller than the viewport is scrolled to the upper third, and the
// correction stops instead of keeping the window redrawing.
func TestFindRevealCentresTheHitInTheBody(t *testing.T) {
	a := testApp(t)
	a.selectedThread = "t0"
	a.thread = mail.ThreadSummary{ID: "t0", MessageCount: 1}
	body := strings.Repeat("a filler line of the body\n", 150) +
		"here is the needle\n" +
		strings.Repeat("a tail line of the body\n", 60)
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: body}}
	a.expanded = map[string]bool{"m0": true}

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)
	a.reader.find.show()
	a.reader.find.ed.SetText("needle")
	for i := 0; i < 6; i++ {
		runFrame(a, router, ops, gtx)
	}

	f := a.reader.find
	if len(f.hits) != 1 {
		t.Fatalf("hits = %d, want the one needle", len(f.hits))
	}
	if f.jump {
		t.Fatal("the reveal never converged")
	}
	pos := a.reader.list.Position
	if pos.First != 0 {
		t.Fatalf("first visible row = %d, want the only message", pos.First)
	}
	if pos.Offset <= 0 {
		t.Fatalf("offset = %d, want the body scrolled to the hit", pos.Offset)
	}

	screenY, ok := hitScreenYOf(gtx, a, f.cur)
	if !ok {
		t.Fatal("the card collected no geometry for the current hit")
	}
	want := a.reader.viewH / findRevealDivisor
	if diff := screenY - want; diff < -gtx.Dp(4) || diff > gtx.Dp(4) {
		t.Fatalf("hit at %dpx, want it near the upper third at %dpx", screenY, want)
	}
}

// TestFindStepMovesTheEmphasisAndTheScroll is the regression for the row's
// next/previous buttons: the emphasis and the scroll follow the cursor, not the
// hit the geometry was collected for.
func TestFindStepMovesTheEmphasisAndTheScroll(t *testing.T) {
	a := testApp(t)
	a.selectedThread = "t0"
	a.thread = mail.ThreadSummary{ID: "t0", MessageCount: 1}
	body := strings.Repeat("a filler line of the body\n", 10) +
		"the first needle\n" +
		strings.Repeat("a filler line of the body\n", 140) +
		"the second needle\n" +
		strings.Repeat("a tail line of the body\n", 30)
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: body}}
	a.expanded = map[string]bool{"m0": true}

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)
	a.reader.find.show()
	a.reader.find.ed.SetText("needle")
	for i := 0; i < 6; i++ {
		runFrame(a, router, ops, gtx)
	}

	f := a.reader.find
	if len(f.hits) != 2 || f.cur != 0 {
		t.Fatalf("hits = %d, cursor = %d; want the two needles, starting at the first", len(f.hits), f.cur)
	}
	firstOffset := a.reader.list.Position.Offset

	f.step(1)
	for i := 0; i < 6; i++ {
		runFrame(a, router, ops, gtx)
	}

	if f.cur != 1 {
		t.Fatalf("cursor = %d after next, want the second hit", f.cur)
	}
	if f.jump {
		t.Fatal("the second hit's reveal never converged")
	}
	pos := a.reader.list.Position
	if pos.Offset <= firstOffset {
		t.Fatalf("offset = %d after next, want it past the first hit's %d", pos.Offset, firstOffset)
	}
	want := a.reader.viewH / findRevealDivisor
	secondY, ok := hitScreenYOf(gtx, a, 1)
	if !ok {
		t.Fatal("no geometry for the second hit")
	}
	if diff := secondY - want; diff < -gtx.Dp(4) || diff > gtx.Dp(4) {
		t.Fatalf("the second hit sits at %dpx, want it near the upper third at %dpx", secondY, want)
	}
	// Both hits are centred at the same screen line, so the difference shows
	// in the offset and in where the first one ended up.
	if firstNow, ok := hitScreenYOf(gtx, a, 0); !ok || firstNow >= 0 {
		t.Fatalf("the first hit is still at %dpx, want it above the viewport", firstNow)
	}

	// The previous button brings the first hit back.
	f.step(-1)
	for i := 0; i < 6; i++ {
		runFrame(a, router, ops, gtx)
	}
	if f.cur != 0 || f.jump {
		t.Fatalf("cursor = %d, jumping = %v after previous", f.cur, f.jump)
	}
	backY, ok := hitScreenYOf(gtx, a, 0)
	if !ok {
		t.Fatal("no geometry for the first hit after going back")
	}
	if diff := backY - want; diff < -gtx.Dp(4) || diff > gtx.Dp(4) {
		t.Fatalf("the first hit sits at %dpx after going back, want %dpx", backY, want)
	}
}

// hitScreenYOf returns where a hit sits inside the reader viewport, using the
// same geometry the reveal correction measures; false when the card holds no
// rectangle for it.
func hitScreenYOf(gtx layout.Context, a *App, cur int) (int, bool) {
	f := a.reader.find
	if cur < 0 || cur >= len(f.hits) {
		return 0, false
	}
	card, ok := a.reader.cards[f.hits[cur].msgID]
	if !ok {
		return 0, false
	}
	r, ok := card.marks.current(cur)
	if !ok {
		return 0, false
	}
	return -a.reader.list.Position.Offset + gtx.Dp(cardInsetTop) + card.headH + r.Min.Y, true
}

// TestFindResetsWithTheConversation keeps the query out of the next thread and
// out of the list view.
func TestFindResetsWithTheConversation(t *testing.T) {
	a := testApp(t)
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "the body"}}
	a.reader.find.show()
	a.reader.find.ed.SetText("body")
	a.reader.find.sync(a.msgs)
	if !a.reader.find.active() {
		t.Fatal("the row is not active with a query and a body")
	}

	a.reader.reset()
	if a.reader.find.open || a.reader.find.query != "" || a.reader.find.ed.Text() != "" {
		t.Fatalf("reset left the row open=%v query=%q text=%q",
			a.reader.find.open, a.reader.find.query, a.reader.find.ed.Text())
	}
	if len(a.reader.find.hits) != 0 {
		t.Fatalf("reset left %d hits behind", len(a.reader.find.hits))
	}

	// Leaving the reader for another folder or a search does the same, even
	// though it does not rebuild the reader.
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m0"}, TextBody: "the body"}}
	a.selectedThread = "t0"
	a.reader.find.show()
	a.reader.find.ed.SetText("body")
	a.reader.find.sync(a.msgs)
	a.resetSelection()
	if a.reader.find.open || a.reader.find.query != "" {
		t.Fatalf("leaving the reader left the row open=%v query=%q",
			a.reader.find.open, a.reader.find.query)
	}
}
