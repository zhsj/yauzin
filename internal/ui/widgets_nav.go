package ui

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

const (
	// navRowInset keeps a folder row where it has always been.
	navRowInset unit.Dp = 14
	// navLabelIndent shifts one nested level to the right.
	navLabelIndent unit.Dp = 18
	// navMarkerCell is the square holding a label's dot or its disclosure
	// chevron. Parent and leaf rows share it, so the marks line up and the
	// chevron target is as large as the dot it replaces.
	navMarkerCell unit.Dp = 20
)

// navItem is one row of the sidebar: folder icon or label dot, name and unread
// count.
type navItem struct {
	click  widget.Clickable
	toggle widget.Clickable // disclosure chevron of an expandable label row
	id     string
	label  string
	ic     icon
	hasDot bool
	dot    color.NRGBA
	count  int
	active bool
	// depth indents a nested label; expandable rows draw the chevron in the
	// dot's cell and report whether their subtree is currently open.
	depth      int
	expandable bool
	expanded   bool
	// foldLocked marks a row that is held open because the active label sits
	// below it. Its chevron stays visible but cannot be folded, so it must not
	// pretend to be clickable.
	foldLocked bool
	onTap      func(string)
	onToggle   func()
}

func newNavItem(id, label string, ic icon, onTap func(string)) *navItem {
	return &navItem{id: id, label: label, ic: ic, onTap: onTap}
}

func newNavItemBar(id, label string, dot color.NRGBA, onTap func(string)) *navItem {
	return &navItem{id: id, label: label, hasDot: true, dot: dot, onTap: onTap}
}

func (n *navItem) setCount(v int)   { n.count = v }
func (n *navItem) setActive(v bool) { n.active = v }

func (n *navItem) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	// The chevron sits inside the row, so the pointer press reaches both
	// clickables. The row action is deferred until the chevron has reported
	// whether it consumed the click, like the hover controls of a mail row.
	rowClicked := n.click.Clicked(gtx)
	toggled := n.expandable && !n.foldLocked && n.toggle.Clicked(gtx)
	dims := n.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(36)
		w := gtx.Constraints.Max.X
		if w <= 0 {
			w = gtx.Constraints.Min.X
		}
		if w < gtx.Dp(120) {
			w = gtx.Dp(120)
		}
		size := image.Pt(w, h)
		th.cursor(gtx, pointer.CursorPointer, size)
		rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: h / 2, NE: h / 2, SE: h / 2, SW: h / 2}
		stack := rr.Push(gtx.Ops)
		switch {
		case n.active:
			paint.Fill(gtx.Ops, colNavActive)
		case n.click.Hovered():
			paint.Fill(gtx.Ops, colHover)
		}
		stack.Pop()
		gtx.Constraints = layout.Exact(size)
		left := navRowInset + unit.Dp(n.depth)*navLabelIndent
		layout.Inset{Left: left, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{}
			if n.hasDot {
				children = append(children, layout.Rigid(n.labelMarker(th)))
			} else {
				ic := n.ic
				if n.active {
					ic.col = colPrimary
				}
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.glyphIn(gtx, ic, 20, 20)
				}))
			}
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 16) }),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, n.label, 14, colText, font.Medium, 1)
				}),
			)
			if n.count > 0 {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, strconv.Itoa(n.count), 12, colMuted, font.Medium, 1)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
		})
		return layout.Dimensions{Size: size}
	})
	switch {
	case toggled && n.onToggle != nil:
		n.onToggle()
		invalidateAfterTap(gtx)
	case rowClicked && n.onTap != nil:
		n.onTap(n.id)
		invalidateAfterTap(gtx)
	}
	return dims
}

// labelMarker draws what stands before a label name: the color dot of a leaf,
// or the disclosure chevron of a label with children, in the label's own color
// and with a tinted disc while hovered so the small target reads as clickable.
// The chevron owns a clickable of its own, so tapping it folds the subtree
// instead of selecting the label.
func (n *navItem) labelMarker(th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		cell := image.Pt(gtx.Dp(navMarkerCell), gtx.Dp(navMarkerCell))
		gtx.Constraints = layout.Exact(cell)
		if !n.expandable {
			return labelDot(gtx, n.dot)
		}
		ic := icon{glyph: glyphChevronRight, col: n.dot}
		if n.expanded {
			ic.glyph = glyphExpandMore
		}
		if n.foldLocked {
			// The active label sits below this row, so folding it is refused
			// anyway. Draw the chevron alone: a hit area, a hand cursor or a
			// hover disc would promise a click that cannot happen.
			return th.glyphIn(gtx, ic, 16, navMarkerCell)
		}
		hovered := n.toggle.Hovered()
		return n.toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(cell)
			// The cursor and the hover disc must be declared inside the
			// clickable's own area: a cursor op pushed outside it is shadowed
			// by that area and the pointer keeps the default shape.
			th.cursor(gtx, pointer.CursorPointer, cell)
			if hovered {
				disc := n.dot
				disc.A = 0x33
				c := clip.Ellipse{Max: cell}.Push(gtx.Ops)
				paint.Fill(gtx.Ops, disc)
				c.Pop()
			}
			return th.glyphIn(gtx, ic, 16, navMarkerCell)
		})
	}
}

// labelDot centres the 10dp color dot of a label in its cell.
func labelDot(gtx layout.Context, dot color.NRGBA) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		d := gtx.Dp(10)
		gtx.Constraints = layout.Exact(image.Pt(d, d))
		c := clip.Ellipse{Max: image.Pt(d, d)}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, dot)
		c.Pop()
		return layout.Dimensions{Size: image.Pt(d, d)}
	})
}

// menuRow is one entry of a dropdown menu: an optional leading avatar or
// glyph, the label and an optional trailing check mark for the active entry.
type menuRow struct {
	click   widget.Clickable
	label   string
	ic      icon
	avatar  string
	checked bool
	onTap   func()
}

func newMenuRow(item menuItem, onTap func()) *menuRow {
	return &menuRow{
		label:   item.Label,
		ic:      item.Icon,
		avatar:  item.Avatar,
		checked: item.Checked,
		onTap:   onTap,
	}
}

func (r *menuRow) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if r.click.Clicked(gtx) && r.onTap != nil {
		r.onTap()
		invalidateAfterTap(gtx)
	}
	return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		w := gtx.Constraints.Max.X
		if w <= 0 {
			w = gtx.Dp(220)
		}
		size := image.Pt(w, gtx.Dp(40))
		th.cursor(gtx, pointer.CursorPointer, size)
		if r.click.Hovered() {
			rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: 6, NE: 6, SE: 6, SW: 6}
			hs := rr.Push(gtx.Ops)
			paint.Fill(gtx.Ops, colHover)
			hs.Pop()
		}
		gtx.Constraints = layout.Exact(size)
		layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, 5)
			switch {
			case r.avatar != "":
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.avatar(gtx, r.avatar, 22)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				)
			case r.ic.glyph != 0:
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.glyphIn(gtx, r.ic, 18, 22)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				)
			}
			children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, r.label, 14, colText, font.Normal, 1)
			}))
			if r.checked {
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.glyphIn(gtx, iconCheckPrimary, 16, 18)
					}),
				)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
		})
		return layout.Dimensions{Size: size}
	})
}
