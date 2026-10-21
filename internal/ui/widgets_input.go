package ui

import (
	"image"
	"math"

	"image/color"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// tick paints Gmail's checkbox mark in the square at the current origin. The
// icon font only ships weight 400, whose tick is markedly thinner than the one
// Gmail draws, so two round-capped strokes are painted at the wanted thickness.
func tick(gtx layout.Context, size, w int, col color.NRGBA) {
	pt := func(fx, fy float32) f32.Point {
		return f32.Pt(fx*float32(size), fy*float32(size))
	}
	stroke := func(from, to f32.Point) {
		d := to.Sub(from)
		l := float32(math.Hypot(float64(d.X), float64(d.Y)))
		if l <= 0 {
			return
		}
		angle := float32(math.Atan2(float64(d.Y), float64(d.X)))
		tr := f32.Affine2D{}.Rotate(f32.Point{}, angle).Offset(from)
		defer op.Affine(tr).Push(gtx.Ops).Pop()
		// The rect reaches w/2 past both ends, which rounds into caps.
		rr := clip.RRect{
			Rect: image.Rect(-w/2, -w/2, int(l)+w/2, w/2),
			NW:   w / 2, NE: w / 2, SE: w / 2, SW: w / 2,
		}
		area := rr.Push(gtx.Ops)
		paint.Fill(gtx.Ops, col)
		area.Pop()
	}
	knee := pt(0.42, 0.70)
	stroke(pt(0.25, 0.52), knee)
	stroke(knee, pt(0.76, 0.30))
}

// checkBox is the square selection control used by the list.
type checkBox struct {
	click   widget.Clickable
	checked bool
	partial bool
	// hit mirrors iconButton.hit so a row clickable can tell a click on the
	// box from a click on the row itself.
	hit      bool
	onChange func(bool)
}

func newCheckBox(onChange func(bool)) *checkBox {
	return &checkBox{onChange: onChange}
}

func (c *checkBox) setState(v int) {
	c.checked = v == 1
	c.partial = v == 2
}

func (c *checkBox) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	c.hit = c.click.Clicked(gtx)
	if c.hit {
		c.checked = !c.checked
		c.partial = false
		if c.onChange != nil {
			c.onChange(c.checked)
		}
		invalidateAfterTap(gtx)
	}
	return c.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		s := gtx.Dp(32)
		gtx.Constraints = layout.Exact(image.Pt(s, s))
		th.cursor(gtx, pointer.CursorPointer, image.Pt(s, s))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			box := gtx.Dp(18)
			gtx.Constraints = layout.Exact(image.Pt(box, box))
			// Gmail keeps the box white in every state and lets a hairline
			// outline plus a dark mark carry it.
			strokeRect(gtx, colSurface, colFaint, 2, 1)
			if c.checked {
				tick(gtx, box, max(2, box/9), colMuted)
			} else if c.partial {
				line := clip.Rect{Min: image.Pt(box/4, box/2-1), Max: image.Pt(box-box/4, box/2+1)}.Push(gtx.Ops)
				paint.Fill(gtx.Ops, colMuted)
				line.Pop()
			}
			return layout.Dimensions{Size: image.Pt(box, box)}
		})
	})
}

// toggleSwitch is the Material-style on/off switch used by boolean settings.
// When onChange is set the switch handles its own clicks; otherwise it only
// displays the state and the surrounding row is the hit target, so a click
// anywhere on the row toggles it exactly once instead of twice (a parent
// Clickable receives the same pointer event).
type toggleSwitch struct {
	click    widget.Clickable
	on       bool
	onChange func(bool)
}

func newToggleSwitch(on bool, onChange func(bool)) *toggleSwitch {
	return &toggleSwitch{on: on, onChange: onChange}
}

func (s *toggleSwitch) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	clicked := s.click.Clicked(gtx)
	if clicked && s.onChange != nil {
		s.on = !s.on
		s.onChange(s.on)
		invalidateAfterTap(gtx)
	}
	w := gtx.Dp(36)
	h := gtx.Dp(20)
	size := image.Pt(w, h)
	if s.onChange != nil {
		th.cursor(gtx, pointer.CursorPointer, size)
	}
	gtx.Constraints = layout.Exact(size)
	// Track: a full-round capsule, primary while on, hairline gray while off.
	track := colBorder
	if s.on {
		track = colPrimary
	}
	fillRect(gtx, track, 10)
	// Knob: a 16dp circle sitting 2dp inside the track, dark gray while off
	// and white while on.
	knob := gtx.Dp(16)
	pad := gtx.Dp(2)
	x := pad
	col := colFaint
	if s.on {
		x = w - pad - knob
		col = colSurface
	}
	kn := gtx
	kn.Constraints = layout.Exact(image.Pt(knob, knob))
	off := op.Offset(image.Pt(x, (h-knob)/2)).Push(kn.Ops)
	fillRect(kn, col, 8)
	off.Pop()
	return layout.Dimensions{Size: size}
}

// tapBlocker consumes clicks over its whole area and optionally paints a scrim.
// It is placed beneath modal cards so clicks on empty space never reach the
// mailbox behind the overlay. A pass-through blocker still reports the click
// (useful to dismiss a dropdown) but lets the widgets below handle it too.
type tapBlocker struct {
	click widget.Clickable
	col   color.NRGBA
	pass  bool
	onTap func()
}

func newTapBlocker(col color.NRGBA, onTap func()) *tapBlocker {
	return &tapBlocker{col: col, onTap: onTap}
}

// newPassThroughTapBlocker builds a blocker that does not stop the event, so
// the widget underneath reacts to the same click.
func newPassThroughTapBlocker(col color.NRGBA, onTap func()) *tapBlocker {
	return &tapBlocker{col: col, pass: true, onTap: onTap}
}

func (b *tapBlocker) Layout(gtx layout.Context) layout.Dimensions {
	if b.click.Clicked(gtx) && b.onTap != nil {
		b.onTap()
		invalidateAfterTap(gtx)
	}
	if b.pass {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	}
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := gtx.Constraints.Max
		if size.X <= 0 || size.Y <= 0 {
			size = gtx.Constraints.Min
		}
		if b.col.A != 0 {
			rect := clip.Rect{Max: size}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, b.col)
			rect.Pop()
		}
		return layout.Dimensions{Size: size}
	})
}

// textField is a single-line editor styled for the compose and search fields.
func (t *theme) textField(gtx layout.Context, ed *widget.Editor, hint string, size unit.Sp) layout.Dimensions {
	es := t.editor(ed, hint, size)
	es.Color = colText
	es.HintColor = colFaint
	es.SelectionColor = colSelection
	return es.Layout(gtx)
}

// editor prepares a single-line editor with the shared font. Callers set the
// colors that fit the background they draw it on.
func (t *theme) editor(ed *widget.Editor, hint string, size unit.Sp) material.EditorStyle {
	es := material.Editor(t.mat, ed, hint)
	es.Font = font.Font{Typeface: t.face}
	es.TextSize = size
	return es
}

// handleEditorEvents drains editor events, returning true when Enter was
// pressed (Submit must be enabled on the editor).
func handleEditorEvents(gtx layout.Context, ed *widget.Editor) bool {
	submitted := false
	for {
		ev, ok := ed.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok {
			submitted = true
		}
	}
	return submitted
}
