package ui

import (
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
)

// invalidateAfterTap schedules one more frame so state changed while handling
// a click shows up immediately. Clicks are consumed during layout, so widgets
// laid out earlier in the same frame (sidebar, overlays) have already been
// drawn with the old state; without this the repaint waits for the next
// pointer event and the effect of the tap appears late.
func invalidateAfterTap(gtx layout.Context) {
	gtx.Execute(op.InvalidateCmd{})
}

// cursor sets shape for area only. A Gio cursor operation applies to the
// current clip, so the clip is pushed and popped around it; without that the
// shape would spill over everything the caller was clipped to.
func (t *theme) cursor(gtx layout.Context, shape pointer.Cursor, size image.Point) {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	shape.Add(gtx.Ops)
	area.Pop()
}
