package ui

import (
	"image"

	"image/color"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// iconButton is a square, circular-hover button drawing one glyph.
type iconButton struct {
	click    widget.Clickable
	ic       icon
	size     unit.Dp
	glyph    unit.Sp
	disabled bool
	// hit records whether the button consumed a click in the last layout. A
	// parent Clickable receives the same pointer event, so containers that
	// must not react to clicks on their controls consult this flag.
	hit     bool
	onTap   func()
	onHover func(bool)
	hovered bool
	// tip is the hover label shown while the pointer rests on the button.
	tip string
	// localPtr is the pointer position in the button's own coordinates,
	// captured by the private pointer area declared for tipped buttons. With
	// the window coordinates from theme.pointer it locates the button in the
	// window, so its label can be anchored to the button instead of to the
	// moving cursor.
	localPtr f32.Point
	// anchor is the window position the label hangs from. It is computed once
	// per hover session and frozen: deriving it every frame made the label
	// jitter by a pixel while the pointer moved inside the button.
	anchor   image.Point
	anchored bool

	// track asks the button to publish its rectangle in window coordinates, for
	// views that anchor a dropdown to it. The rectangle is refreshed only while
	// the pointer rests on the button: locating it needs a pointer position
	// inside its own area.
	track bool
	rect  image.Rectangle
}

func newIconButton(ic icon, size unit.Dp, onTap func()) *iconButton {
	return &iconButton{ic: ic, size: size, onTap: onTap}
}

// listIconGlyph is the glyph size of the icons that sit next to the list's
// 18dp selection box. Material Symbols ink covers roughly 0.7 of the em, so
// the default half-cell glyph only spans about 11dp and reads as a mismatch.
const listIconGlyph = unit.Sp(24)

func (b *iconButton) setIcon(ic icon)    { b.ic = ic }
func (b *iconButton) setDisabled(v bool) { b.disabled = v }
func (b *iconButton) setTip(text string) { b.tip = text }

// newListIcon is newIconButton with the glyph raised to listIconGlyph. Message
// rows and the list and reader toolbars share it so their icons match.
func newListIcon(ic icon, size unit.Dp, onTap func()) *iconButton {
	b := newIconButton(ic, size, onTap)
	b.setGlyph(listIconGlyph)
	return b
}

// setGlyph overrides the glyph size. The default is half the cell, whose ink
// lands around 11dp: too small next to the 18dp selection box the list draws.
func (b *iconButton) setGlyph(size unit.Sp) { b.glyph = size }

func (b *iconButton) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	return b.layout(gtx, th, b.ic, colHover)
}

// layout draws the button with an explicit icon and hover fill, so a button
// built for the light header can be reused on the indigo CSD header without
// rebuilding it.
func (b *iconButton) layout(gtx layout.Context, th *theme, ic icon, hoverCol color.NRGBA) layout.Dimensions {
	b.hit = b.click.Clicked(gtx) && !b.disabled
	if b.hit && b.onTap != nil {
		b.onTap()
		invalidateAfterTap(gtx)
	}
	hovered := b.click.Hovered() && !b.disabled
	if hovered != b.hovered {
		b.hovered = hovered
		if b.onHover != nil {
			b.onHover(hovered)
		}
	}
	s := gtx.Dp(b.size)
	glyph := b.glyph
	if glyph <= 0 {
		glyph = unit.Sp(float32(b.size) * 0.5)
	}
	dims := b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(s, s))
		if b.tip != "" || b.track {
			// A private pointer area tracks the pointer inside the button. It
			// is declared before the cursor op below: hit testing walks the
			// newest nodes first and leaves the area chain at the first
			// non-pass handler, so a cursor op on a later sibling area would
			// never be reached and the pointer would turn into an arrow.
			area := clip.Rect{Max: image.Pt(s, s)}.Push(gtx.Ops)
			event.Op(gtx.Ops, b)
			area.Pop()
		}
		if !b.disabled {
			th.cursor(gtx, pointer.CursorPointer, image.Pt(s, s))
		}
		if hovered {
			circle := clip.Ellipse{Max: image.Pt(s, s)}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, hoverCol)
			circle.Pop()
		}
		if b.disabled {
			ic.col.A = uint8(float32(ic.col.A) * 0.35)
		}
		th.glyphIn(gtx, ic, glyph, b.size)
		return layout.Dimensions{Size: image.Pt(s, s)}
	})
	// The hover label is queued after the clickable returns: Clickable clips
	// its own content to the button square, but the clip is popped by now, so
	// the recorded label is not clipped to anything. The App replays it next
	// to the button at the end of the frame. Disabled buttons keep quiet,
	// matching their dimmed look.
	//
	// The private pointer area is read every frame while the button has a
	// label: filters route events by what the previous frame declared, so
	// declaring only while hovered would miss the very Enter that starts a
	// hover and anchor from a stale position.
	if b.tip != "" || b.track {
		b.readPointer(gtx)
	}
	if b.track && hovered {
		// The pointer area reports the pointer in the button's own coordinates
		// while theme.pointer carries it in window coordinates; their difference
		// is the button's origin.
		origin := image.Pt(th.pointer.X-int(b.localPtr.X+.5), th.pointer.Y-int(b.localPtr.Y+.5))
		b.rect = image.Rectangle{Min: origin, Max: origin.Add(dims.Size)}
	}
	if hovered && b.tip != "" {
		if !b.anchored {
			origin := image.Pt(th.pointer.X-int(b.localPtr.X+.5), th.pointer.Y-int(b.localPtr.Y+.5))
			b.anchor = origin.Add(image.Pt(0, dims.Size.Y+gtx.Dp(4)))
			b.anchored = true
		}
		th.recordTip(gtx, b.tip, b.anchor)
	} else {
		b.anchored = false
	}
	return dims
}

// readPointer consumes the pointer events of the button's private area.
func (b *iconButton) readPointer(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: b, Kinds: pointer.Move | pointer.Enter})
		if !ok {
			return
		}
		if pe, ok := ev.(pointer.Event); ok {
			b.localPtr = pe.Position
		}
	}
}

// linkButton is an inline text action with an optional leading glyph, used for
// navigation inside a card such as the account wizard's back step. It has no
// background of its own: the label turns to the darker primary on hover.
type linkButton struct {
	click    widget.Clickable
	label    string
	ic       icon
	disabled bool
	onTap    func()
}

func newLinkButton(text string, ic icon, onTap func()) *linkButton {
	return &linkButton{label: text, ic: ic, onTap: onTap}
}

func (b *linkButton) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if b.click.Clicked(gtx) && !b.disabled && b.onTap != nil {
		b.onTap()
		invalidateAfterTap(gtx)
	}
	hovered := b.click.Hovered() && !b.disabled
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		col := colPrimary
		switch {
		case b.disabled:
			col = colFaint
		case hovered:
			col = colPrimaryDark
		}
		ic := b.ic
		ic.col = col
		children := make([]layout.FlexChild, 0, 3)
		if ic.glyph != 0 {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.glyphIn(gtx, ic, 16, 16)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			)
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return th.text(gtx, b.label, 13, col, font.Medium, 1)
		}))
		// The padding is inside the clickable, so the label keeps a hit area
		// taller than its line box without shifting the text right.
		return layout.Inset{Top: 4, Bottom: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
			if !b.disabled {
				th.cursor(gtx, pointer.CursorPointer, dims.Size)
			}
			return dims
		})
	})
}

// outlineButton is a compact bordered action for the details of a card. Unlike
// a link it carries a hairline border, which keeps secondary actions visible on
// a tinted background without the weight of a filled pill.
type outlineButton struct {
	click    widget.Clickable
	label    string
	danger   bool
	disabled bool
	onTap    func()
}

func newOutlineButton(text string, onTap func()) *outlineButton {
	return &outlineButton{label: text, onTap: onTap}
}

func (b *outlineButton) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if b.click.Clicked(gtx) && !b.disabled && b.onTap != nil {
		b.onTap()
		invalidateAfterTap(gtx)
	}
	hovered := b.click.Hovered() && !b.disabled
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		textCol, border, fill := colText, colBorder, colSurface
		switch {
		case b.disabled:
			textCol = colFaint
		case b.danger:
			textCol, border = colDanger, colDanger
			if hovered {
				fill = colDangerSoft
			}
		case hovered:
			fill = colSurface2
		}

		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
		macro := op.Record(gtx.Ops)
		label := th.text(measure, b.label, 13, textCol, font.Medium, 1)
		call := macro.Stop()

		// The height is fixed so the action line keeps its rhythm no matter
		// which labels the row carries, while the width hugs the text.
		size := image.Pt(label.Size.X+2*gtx.Dp(14), gtx.Dp(32))
		gtx.Constraints = layout.Exact(size)
		if !b.disabled {
			th.cursor(gtx, pointer.CursorPointer, size)
		}
		strokeRect(gtx, fill, border, 8, 1)
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return label
		})
		return layout.Dimensions{Size: size}
	})
}

// pillButton is Gmail's rounded action button, optionally filled with the
// primary colour.
type pillButton struct {
	click    widget.Clickable
	label    string
	ic       icon
	primary  bool
	floating bool
	disabled bool
	minW     unit.Dp
	onTap    func()
}

func newPillButton(text string, ic icon, primary bool, onTap func()) *pillButton {
	return &pillButton{label: text, ic: ic, primary: primary, minW: 96, onTap: onTap}
}

func newComposePill(text string, ic icon, onTap func()) *pillButton {
	b := newPillButton(text, ic, false, onTap)
	b.floating = true
	b.minW = 132
	return b
}

func (b *pillButton) setText(text string) { b.label = text }

func (b *pillButton) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if b.click.Clicked(gtx) && !b.disabled && b.onTap != nil {
		b.onTap()
		invalidateAfterTap(gtx)
	}
	return b.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
		macro := op.Record(gtx.Ops)
		content := b.content(measure, th)
		call := macro.Stop()

		minH := gtx.Dp(36)
		if b.floating {
			minH = gtx.Dp(48)
		}
		w := content.Size.X + gtx.Dp(32)
		if minW := gtx.Dp(b.minW); w < minW {
			w = minW
		}
		h := content.Size.Y + gtx.Dp(14)
		if h < minH {
			h = minH
		}
		size := image.Pt(w, h)
		if !b.disabled {
			th.cursor(gtx, pointer.CursorPointer, size)
		}
		b.paint(gtx, size, b.fillColor())

		off := op.Offset(image.Pt((w-content.Size.X)/2, (h-content.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		off.Pop()
		return layout.Dimensions{Size: size}
	})
}

// fillColor is the background of the current state: disabled and plain pills
// share the grey, a floating one sits on the surface and the primary one on
// the accent colour.
func (b *pillButton) fillColor() color.NRGBA {
	hovered := b.click.Hovered()
	switch {
	case b.disabled:
		return colSurface2
	case b.floating:
		if hovered {
			return colSearchBG
		}
		return colSurface
	case b.primary:
		if hovered {
			return colPrimaryDark
		}
		return colPrimary
	default:
		if hovered {
			return colPrimarySoft
		}
		return colSurface2
	}
}

// paint fills the rounded background. A floating pill also gets the hairline
// border that stands in for Gmail's elevation shadow.
func (b *pillButton) paint(gtx layout.Context, size image.Point, fill color.NRGBA) {
	r := size.Y / 2
	rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: r, NE: r, SE: r, SW: r}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, fill)
	stack.Pop()
	if !b.floating {
		return
	}
	border := clip.RRect{
		Rect: image.Rect(0, 0, size.X, size.Y),
		NW:   r, NE: r, SE: r, SW: r,
	}
	bs := border.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	bs.Pop()
	inner := clip.RRect{
		Rect: image.Rect(1, 1, size.X-1, size.Y-1),
		NW:   r - 1, NE: r - 1, SE: r - 1, SW: r - 1,
	}
	is := inner.Push(gtx.Ops)
	// The interior takes the button's fill, not the constant surface: painting
	// colSurface here covered the hover colour drawn above.
	paint.Fill(gtx.Ops, fill)
	is.Pop()
}

func (b *pillButton) content(gtx layout.Context, th *theme) layout.Dimensions {
	txtCol := colText
	if b.primary {
		txtCol = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	}
	if b.disabled {
		txtCol = colFaint
	}
	children := make([]layout.FlexChild, 0, 3)
	if b.ic.glyph != 0 {
		ic := b.ic
		if b.primary {
			ic.col = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
		}
		if b.disabled {
			ic.col = colFaint
		}
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.glyph(gtx, ic, 18)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		)
	}
	children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return th.text(gtx, b.label, 14, txtCol, font.Medium, 1)
	}))
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}
