package ui

import (
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// mouseEvent is the platform's shape for a mouse button event at pos.
func mouseEvent(kind pointer.Kind, pos f32.Point) pointer.Event {
	return pointer.Event{
		Kind:     kind,
		Source:   pointer.Mouse,
		Buttons:  pointer.ButtonPrimary,
		Position: pos,
	}
}

// TestSearchFieldTakesTypedText pins the whole input path of the search pill: a
// press gives the editor the keyboard focus, the typed characters land in it,
// and Enter submits the query.
//
// The field used to take the focus (the caret blinked) and then reject every
// character: the shortcut router sampled the focus by draining key.FocusFilter
// events, and that filter also matches the target's EditEvent, which carries
// the text. The editor never saw one.
func TestSearchFieldTakesTypedText(t *testing.T) {
	a := testApp(t)
	a.decorated = false // the client-side title bar: the pill sits between drag handles

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)

	// The title bar centres the pill, so the middle of the bar is inside it.
	pos := f32.Pt(float32(gtx.Constraints.Max.X/2), float32(gtx.Dp(a.headerHeight())/2))
	router.Queue(mouseEvent(pointer.Press, pos), mouseEvent(pointer.Release, pos))
	// The press is handled while the frame is laid out, which is where the
	// editor asks for the focus; the request is executed at the end of it.
	runFrame(a, router, ops, gtx)
	runFrame(a, router, ops, gtx)

	if !a.searchFocused {
		t.Fatal("pressing the search pill left the field without the keyboard focus")
	}
	if !router.Source().Focused(&a.top.search) {
		t.Fatal("the search editor is not the focused input handler after the press")
	}

	router.Queue(key.EditEvent{Text: "yauzin"})
	runFrame(a, router, ops, gtx)
	if got := a.top.search.Text(); got != "yauzin" {
		t.Fatalf("search field text = %q after typing, want %q", got, "yauzin")
	}

	// Enter has to reach the editor's Submit handling instead of being read as
	// the list's "open" shortcut.
	router.Queue(key.Event{Name: key.NameEnter, State: key.Press})
	runFrame(a, router, ops, gtx)
	if a.query != "yauzin" {
		t.Fatalf("query = %q after Enter, want %q", a.query, "yauzin")
	}
}

// TestSearchEscBlursTheField covers Esc in the search pill: it only hands the
// keyboard back. The typed text, the active query and the rows behind the
// field stay, and the next Esc belongs to the conversation again.
func TestSearchEscBlursTheField(t *testing.T) {
	a := testApp(t)
	a.decorated = false
	a.query = "invoice"
	a.activeFolder = ""
	a.top.search.SetText("invoic")
	seedList(a)

	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)

	// "/" gives the pill the keyboard; its focus request runs at the end of
	// the frame, so the state is sampled one frame later.
	router.Queue(key.Event{Name: "/", State: key.Press})
	runFrame(a, router, ops, gtx)
	runFrame(a, router, ops, gtx)
	if !a.searchFocused || !router.Source().Focused(&a.top.search) {
		t.Fatal("the search pill did not take the keyboard focus")
	}

	// The Esc frame only queues the blur; the next frame executes the
	// FocusCmd at its end, and the focus state is sampled again after that.
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	runFrame(a, router, ops, gtx)
	runFrame(a, router, ops, gtx)
	runFrame(a, router, ops, gtx)

	if a.searchFocused || router.Source().Focused(&a.top.search) {
		t.Fatal("Esc left the search field focused")
	}
	if got := a.top.search.Text(); got != "invoic" {
		t.Fatalf("search box = %q after Esc, want the typed text kept", got)
	}
	if a.query != "invoice" {
		t.Fatalf("query = %q after Esc, want the active query kept", a.query)
	}
	if len(a.list.threads) != 3 {
		t.Fatalf("list rows = %d after Esc, want the results to stay", len(a.list.threads))
	}
	if a.selectedThread != "t0" {
		t.Fatalf("Esc closed the conversation %q while the search field had the focus", a.selectedThread)
	}

	// The next Esc is no longer the field's: it closes the conversation.
	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	runFrame(a, router, ops, gtx)
	if a.selectedThread != "" {
		t.Fatalf("the second Esc left the conversation %q open", a.selectedThread)
	}
}
