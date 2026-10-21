package ui

import (
	"image"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// The frame sizes the layout tests use. testWindow is the size the app opens
// with; smallWindow is below the point where the panels, the toolbar and the
// sidebar run out of room.
var (
	testWindow  = image.Pt(1200, 840)
	smallWindow = image.Pt(560, 420)
)

// testGtx returns a headless layout context capping the frame at size, with a
// 1:1 pixel density so dp and sp assertions compare against pixels directly.
func testGtx(ops *op.Ops, size image.Point) layout.Context {
	return layout.Context{
		Ops:         ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: size},
		Now:         time.Now(),
	}
}

// testCell is testGtx with exact constraints, for the cases that pin the size
// of a single widget instead of the whole window.
func testCell(ops *op.Ops, size image.Point) layout.Context {
	gtx := testGtx(ops, size)
	gtx.Constraints = layout.Exact(size)
	return gtx
}

// newRouterFrame returns a headless frame backed by a real input router. The
// router has to outlive the frame: the hit areas and the keyboard focus the
// widgets declare in one frame are what route the events of the next one.
func newRouterFrame(size image.Point) (*input.Router, *op.Ops, layout.Context) {
	ops := new(op.Ops)
	router := new(input.Router)
	gtx := testGtx(ops, size)
	gtx.Source = router.Source()
	return router, ops, gtx
}

// runFrame draws one frame of a the way eventLoop does it: the keyboard is
// routed before anything is laid out, and the router collects the hit areas
// afterwards. Events queued between two calls reach the widgets through that
// router.
func runFrame(a *App, router *input.Router, ops *op.Ops, gtx layout.Context) {
	ops.Reset()
	a.handleKeys(gtx)
	a.layout(gtx)
	router.Frame(ops)
}
