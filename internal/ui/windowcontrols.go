package ui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// windowButtonSize is the square click target of a title bar window button.
const windowButtonSize = unit.Dp(36)

// windowButton paints one window action with a round hover highlight.
type windowButton struct {
	click  widget.Clickable
	danger bool
	draw   func(gtx layout.Context, size int, col color.NRGBA)
}

// Layout draws the button and reports whether it was clicked this frame.
func (b *windowButton) Layout(gtx layout.Context, th *theme) (layout.Dimensions, bool) {
	size := gtx.Dp(windowButtonSize)
	box := image.Pt(size, size)
	clicked := b.click.Clicked(gtx)
	if clicked {
		invalidateAfterTap(gtx)
	}
	dims := b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(box)
		th.cursor(gtx, pointer.CursorPointer, box)
		col := colMuted
		if b.click.Hovered() {
			fill := colHover
			if b.danger {
				fill = colDanger
				col = colOnDeco
			}
			circle := clip.Ellipse{Max: box}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, fill)
			circle.Pop()
		}
		b.draw(gtx, size, col)
		return layout.Dimensions{Size: box}
	})
	return dims, clicked
}

// windowControls are the minimize, maximize/restore and close buttons of the
// client-side title bar. They are drawn by the app instead of Gio's decoration
// widgets so the bar matches the rest of the interface.
type windowControls struct {
	minimize *windowButton
	maximize *windowButton
	close    *windowButton
}

func newWindowControls() *windowControls {
	return &windowControls{
		minimize: &windowButton{draw: drawMinimizeIcon},
		maximize: &windowButton{draw: drawMaximizeIcon},
		close:    &windowButton{draw: drawCloseIcon, danger: true},
	}
}

// Width reports the pixel width of the button group.
func (c *windowControls) Width(gtx layout.Context) int {
	return 3 * gtx.Dp(windowButtonSize)
}

// Layout draws the buttons and records the requested actions on the app. The
// actions run after the frame, so closing the window cannot tear it down in the
// middle of the layout.
func (c *windowControls) Layout(gtx layout.Context, th *theme, a *App) layout.Dimensions {
	if a.maximized {
		c.maximize.draw = drawRestoreIcon
	} else {
		c.maximize.draw = drawMaximizeIcon
	}

	var acts system.Action
	buttons := []*windowButton{c.minimize, c.maximize, c.close}
	children := make([]layout.FlexChild, 0, len(buttons))
	for _, button := range buttons {
		b := button
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims, clicked := b.Layout(gtx, th)
			if !clicked {
				return dims
			}
			switch b {
			case c.minimize:
				acts |= system.ActionMinimize
			case c.maximize:
				acts |= toggleMaximize(a.maximized)
			case c.close:
				acts |= system.ActionClose
			}
			return dims
		}))
	}
	dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	a.windowActions |= acts
	return dims
}

// windowSteps splits the title bar requests from the mode reset that has to
// follow a minimize. Gio's Wayland backend latches its minimized mode: only a
// surface enter clears it, which a compositor restoring a window onto the same
// output never sends, and its Configure then drops every later mode change.
// Without the reset the maximize button would stay dead for the rest of the
// session once the window was minimized. The reset only updates that internal
// record: the protocol still sees a minimized window, no request follows.
//
// The cost is one extra ConfigEvent and, while the window is hidden, one extra
// frame. The one visible consequence: when the window was maximized before the
// minimize, the first click on the maximize button only re-syncs the state the
// compositor kept anyway, and the second click restores.
func windowSteps(acts system.Action) (perform system.Action, resetMode bool) {
	return acts, acts&system.ActionMinimize != 0
}

// toggleMaximize returns the window action that flips the current maximize
// state.
func toggleMaximize(maximized bool) system.Action {
	if maximized {
		return system.ActionUnmaximize
	}
	return system.ActionMaximize
}

// strokePath paints a stroked path.
func strokePath(gtx layout.Context, p clip.Path, width float32, col color.NRGBA) {
	st := clip.Stroke{Path: p.End(), Width: width}.Op().Push(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	st.Pop()
}

// strokeOutline paints the outline of a rounded rectangle.
func strokeOutline(gtx layout.Context, r image.Rectangle, radius, width float32, col color.NRGBA) {
	rr := clip.RRect{Rect: r, NW: int(radius), NE: int(radius), SE: int(radius), SW: int(radius)}
	st := clip.Stroke{Path: rr.Path(gtx.Ops), Width: width}.Op().Push(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	st.Pop()
}

func drawMinimizeIcon(gtx layout.Context, size int, col color.NRGBA) {
	center := float32(size) / 2
	arm := float32(gtx.Dp(unit.Dp(5)))
	width := float32(gtx.Dp(unit.Dp(1.2)))
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(center-arm, center))
	p.LineTo(f32.Pt(center+arm, center))
	strokePath(gtx, p, width, col)
}

func drawMaximizeIcon(gtx layout.Context, size int, col color.NRGBA) {
	center := float32(size) / 2
	half := float32(gtx.Dp(unit.Dp(5)))
	width := float32(gtx.Dp(unit.Dp(1.2)))
	radius := float32(gtx.Dp(unit.Dp(1.5)))
	r := image.Rect(int(center-half), int(center-half), int(center+half), int(center+half))
	strokeOutline(gtx, r, radius, width, col)
}

// drawRestoreIcon interleaves two outlines, the usual glyph for the
// un-maximize action.
func drawRestoreIcon(gtx layout.Context, size int, col color.NRGBA) {
	center := float32(size) / 2
	half := float32(gtx.Dp(unit.Dp(4.5)))
	offset := float32(gtx.Dp(unit.Dp(2)))
	width := float32(gtx.Dp(unit.Dp(1.2)))
	radius := float32(gtx.Dp(unit.Dp(1.5)))
	back := image.Rect(int(center-half+offset), int(center-half-offset), int(center+half+offset), int(center+half-offset))
	front := image.Rect(int(center-half-offset), int(center-half+offset), int(center+half-offset), int(center+half+offset))
	strokeOutline(gtx, back, radius, width, col)
	strokeOutline(gtx, front, radius, width, col)
}

func drawCloseIcon(gtx layout.Context, size int, col color.NRGBA) {
	center := float32(size) / 2
	arm := float32(gtx.Dp(unit.Dp(4.5)))
	width := float32(gtx.Dp(unit.Dp(1.2)))
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(center-arm, center-arm))
	p.LineTo(f32.Pt(center+arm, center+arm))
	p.MoveTo(f32.Pt(center+arm, center-arm))
	p.LineTo(f32.Pt(center-arm, center+arm))
	strokePath(gtx, p, width, col)
}
