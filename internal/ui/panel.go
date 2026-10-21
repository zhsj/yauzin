package ui

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
)

// panelSize clamps a floating panel to the window. The minimum is applied
// inside the viewport, not after clamping to it: otherwise a window smaller
// than the minimum would produce a panel wider than its constraints and a
// negative origin. want and minSize are in pixels.
func panelSize(gtx layout.Context, want, minSize image.Point) image.Point {
	maxW := max(gtx.Constraints.Max.X-48, 1)
	maxH := max(gtx.Constraints.Max.Y-48, 1)
	return image.Pt(
		max(min(want.X, maxW), min(minSize.X, maxW)),
		max(min(want.Y, maxH), min(minSize.Y, maxH)),
	)
}

// placeCard starts the card's offset and returns a context constrained to
// exactly size, plus the pop that must be deferred. Drawing stays with the
// caller: the tap blocker and drawCard have to keep their relative order, and
// a panel that registers its blocker after its content swallows the clicks of
// its own buttons.
func placeCard(gtx layout.Context, size, origin image.Point) (layout.Context, func()) {
	off := op.Offset(origin).Push(gtx.Ops)
	cardGtx := gtx
	cardGtx.Constraints = layout.Exact(size)
	return cardGtx, off.Pop
}
