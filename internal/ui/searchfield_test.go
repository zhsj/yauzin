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
