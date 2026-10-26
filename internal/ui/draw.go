package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// avatar paints a coloured circle with the initials of name.
func (t *theme) avatar(gtx layout.Context, name string, size unit.Dp) layout.Dimensions {
	s := gtx.Dp(size)
	area := clip.Ellipse{Max: image.Pt(s, s)}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, avatarColor(name))
	area.Pop()

	gtx.Constraints = layout.Exact(image.Pt(s, s))
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return t.text(gtx, initials(name), unit.Sp(float32(size)*0.4), color.NRGBA{A: 0xFF, R: 0xFF, G: 0xFF, B: 0xFF}, font.Medium, 1)
	})
	return layout.Dimensions{Size: image.Pt(s, s)}
}

// fillRect paints a rounded rectangle covering the incoming minimum
// constraints. The caller sizes the surrounding layout.
func fillRect(gtx layout.Context, col color.NRGBA, radius unit.Dp) layout.Dimensions {
	r := gtx.Dp(radius)
	size := gtx.Constraints.Min
	if size.X <= 0 || size.Y <= 0 {
		size = gtx.Constraints.Max
	}
	rect := image.Rectangle{Max: size}
	rr := clip.RRect{Rect: rect, NW: r, NE: r, SE: r, SW: r}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, col)
	stack.Pop()
	return layout.Dimensions{Size: size}
}

// drawCard paints a white rounded panel with a hairline border. It is the one
// place the compose panel, the dropdown menu and the settings panel share, so
// their radii stay consistent.
func drawCard(gtx layout.Context, size image.Point, radius int) {
	rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: radius, NE: radius, SE: radius, SW: radius}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface)
	stack.Pop()
	border := clip.RRect{Rect: image.Rect(0, 0, size.X, size.Y), NW: radius, NE: radius, SE: radius, SW: radius}
	bs := border.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	bs.Pop()
	inner := clip.RRect{Rect: image.Rect(1, 1, size.X-1, size.Y-1), NW: radius - 1, NE: radius - 1, SE: radius - 1, SW: radius - 1}
	is := inner.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface)
	is.Pop()
}

// strokeRect paints a rounded rectangle with a hairline border. Clips only
// intersect, so a ring cannot be painted directly: the whole shape takes the
// border colour and the inset interior is painted over it with the fill.
func strokeRect(gtx layout.Context, fill, border color.NRGBA, radius unit.Dp, width unit.Dp) layout.Dimensions {
	r := gtx.Dp(radius)
	size := gtx.Constraints.Min
	if size.X <= 0 || size.Y <= 0 {
		size = gtx.Constraints.Max
	}
	rect := image.Rectangle{Max: size}
	rr := clip.RRect{Rect: rect, NW: r, NE: r, SE: r, SW: r}
	stack := rr.Push(gtx.Ops)
	if border.A != 0 {
		paint.Fill(gtx.Ops, border)
	} else {
		paint.Fill(gtx.Ops, fill)
	}
	stack.Pop()
	if border.A != 0 {
		w := gtx.Dp(width)
		inside := image.Rect(rect.Min.X+w, rect.Min.Y+w, rect.Max.X-w, rect.Max.Y-w)
		if !inside.Empty() {
			ir := max(r-w, 0)
			inner := clip.RRect{Rect: inside, NW: ir, NE: ir, SE: ir, SW: ir}
			is := inner.Push(gtx.Ops)
			paint.Fill(gtx.Ops, fill)
			is.Pop()
		}
	}
	return layout.Dimensions{Size: size}
}

func hgap(gtx layout.Context, w unit.Dp) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(gtx.Dp(w), 0)}
}

func vgap(gtx layout.Context, h unit.Dp) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(0, gtx.Dp(h))}
}

// column stacks children vertically and reports only their natural height. A
// parent Flex hands its own row height down as the minimum constraint, and the
// default SpaceEnd spacing would then leave that leftover space below the
// children, which pins the text to the top of the row; clearing Min.Y lets the
// parent centre the block instead.
func column(gtx layout.Context, children ...layout.FlexChild) layout.Dimensions {
	gtx.Constraints.Min.Y = 0
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// hline draws a full-width one pixel separator.
func hline(gtx layout.Context) layout.Dimensions {
	h := gtx.Dp(1)
	size := image.Pt(gtx.Constraints.Max.X, h)
	if size.X <= 0 {
		size.X = gtx.Constraints.Min.X
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	area.Pop()
	return layout.Dimensions{Size: size}
}

// hexColor parses "#RRGGBB" values used by backends for label colors.
func hexColor(raw string) color.NRGBA {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(raw) != 6 {
		return colFaint
	}
	v, err := strconv.ParseUint(raw, 16, 32)
	if err != nil {
		return colFaint
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}
