package ui

import (
	"image"
	"image/color"
	"time"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

var colStatusBG = color.NRGBA{R: 0x20, G: 0x21, B: 0x24, A: 0xF2}

// statusBar is the lightweight replacement for a toast: a small dark rectangle
// in the bottom left corner of the window that reports the result of an action.
// It can be dismissed by clicking and is also cleared by the dismissal timer
// it owns; every entry point runs on the UI goroutine.
type statusBar struct {
	click widget.Clickable
	msg   string
	isErr bool
	gen   int
	// timer dismisses the current message. show and clear stop it, so an
	// older message never outlives its replacement.
	timer *time.Timer
}

func (s *statusBar) show(msg string, isErr bool) {
	s.stopTimer()
	s.gen++
	s.msg = msg
	s.isErr = isErr
}

func (s *statusBar) clear() {
	s.stopTimer()
	s.gen++
	s.msg = ""
	s.isErr = false
}

// arm replaces the dismissal timer with one that fires after d. Only the
// latest message is ever timed.
func (s *statusBar) arm(d time.Duration, dismiss func()) {
	s.stopTimer()
	s.timer = time.AfterFunc(d, dismiss)
}

func (s *statusBar) stopTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *statusBar) visible() bool { return s.msg != "" }

func (s *statusBar) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if s.msg == "" {
		return layout.Dimensions{}
	}
	if s.click.Clicked(gtx) {
		s.clear()
		invalidateAfterTap(gtx)
		return layout.Dimensions{}
	}
	// The bar hugs its content like Gmail's snackbar, up to a cap so long
	// messages wrap instead of stretching across the window.
	maxW := gtx.Constraints.Max.X - gtx.Dp(48)
	if maxW > gtx.Dp(520) {
		maxW = gtx.Dp(520)
	}
	if maxW < gtx.Dp(160) {
		maxW = gtx.Dp(160)
	}

	textCol := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	ic := iconCheck
	if s.isErr {
		ic = iconAlert
	}

	macro := op.Record(gtx.Ops)
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(maxW, 1<<20)}
	content := layout.Inset{Top: 11, Bottom: 11, Left: 16, Right: 12}.Layout(measure, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.glyphIn(gtx, ic, 18, 20)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, s.msg, 13, textCol, font.Normal, 2)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.glyph(gtx, iconClose, 16)
				})
			}),
		)
	})
	call := macro.Stop()

	size := content.Size
	return s.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		th.cursor(gtx, pointer.CursorPointer, size)
		rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(4))
		stack := rr.Push(gtx.Ops)
		paint.Fill(gtx.Ops, colStatusBG)
		stack.Pop()
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}
