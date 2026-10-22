package ui

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/zhsj/yauzin/internal/i18n"
)

func (t *topBar) clearSearch() {
	t.search.SetText("")
}

// searchControl fills the middle of the header. Without a client-side title bar
// it is a centred pill; with one, the pill plus the blank drag handles on
// either side.
func (t *topBar) searchControl(gtx layout.Context, csd bool, searchMax unit.Dp) layout.FlexChild {
	if csd {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return t.searchCell(gtx, searchMax)
		})
	}
	return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return maxWidth(gtx, searchMax, func(gtx layout.Context) layout.Dimensions {
				return t.searchField(gtx, csd)
			})
		})
	})
}

// searchCell lays out the middle of the client-side title bar: the search pill
// in the centre and a blank drag handle on either side. The handles are the
// empty parts of the bar, so the pill, the buttons and the avatar keep the
// pointer to themselves.
func (t *topBar) searchCell(gtx layout.Context, searchMax unit.Dp) layout.Dimensions {
	w, h := gtx.Constraints.Max.X, gtx.Constraints.Max.Y

	// Keep a strip of blank bar beside the pill, but never shrink the pill
	// below its usable width.
	pill := gtx.Dp(searchMax)
	if avail := w - 2*gtx.Dp(searchGripMin); pill > avail {
		pill = avail
	}
	if minW := gtx.Dp(searchMinWidth); pill < minW {
		pill = minW
	}
	if pill > w {
		pill = w
	}
	if pill < 0 {
		pill = 0
	}
	left := (w - pill) / 2
	right := w - pill - left

	t.dragHandle(gtx, image.Pt(left, h))
	if pill > 0 {
		gtx.Constraints = layout.Constraints{
			Min: image.Pt(pill, 0),
			Max: image.Pt(pill, h),
		}
		// Record first: the pill reports its own height and has to be centred
		// vertically in the taller title bar row.
		macro := op.Record(gtx.Ops)
		dims := t.searchField(gtx, true)
		call := macro.Stop()
		off := op.Offset(image.Pt(left, (h-dims.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		off.Pop()
	}
	off := op.Offset(image.Pt(left+pill, 0)).Push(gtx.Ops)
	t.dragHandle(gtx, image.Pt(right, h))
	off.Pop()

	return layout.Dimensions{Size: image.Pt(w, h)}
}

func (t *topBar) searchField(gtx layout.Context, csd bool) layout.Dimensions {
	a := t.app
	th := a.th
	h := gtx.Dp(44)
	if csd {
		h = gtx.Dp(34)
	}
	if max := gtx.Constraints.Max.Y; max > 0 && max < h {
		h = max
	}
	minW := gtx.Dp(searchMinWidth)
	if gtx.Constraints.Max.X < minW {
		minW = gtx.Constraints.Max.X
	}
	if gtx.Constraints.Min.X < minW {
		gtx.Constraints.Min.X = minW
	}
	w := gtx.Constraints.Max.X
	if w < minW {
		w = minW
	}
	size := image.Pt(w, h)

	rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: h / 2, NE: h / 2, SE: h / 2, SW: h / 2}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSearchBG)
	stack.Pop()

	iconSize := 20
	lead := 16
	if csd {
		iconSize, lead = 18, 12
	}
	gtx.Constraints = layout.Exact(size)
	layout.Inset{Left: unit.Dp(lead), Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.glyphIn(gtx, iconSearch, unit.Sp(iconSize), unit.Dp(iconSize))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 10) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				submitted := handleEditorEvents(gtx, &t.search)
				// The editor must span the whole cell: centring it would let
				// its natural (hint sized) box float in the middle, which
				// starts the caret and the placeholder in the middle of the
				// pill. Vertical centring is left to the Flex alignment.
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = 0
				es := th.editor(&t.search, i18n.T("topbar.search"), 14)
				es.Color, es.HintColor = colText, colFaint
				es.SelectionColor = colSelection
				dims := es.Layout(gtx)
				// An empty box is not a search: submitting it would leave the
				// list showing results no query asks for.
				if submitted {
					if q := strings.TrimSpace(t.search.Text()); q != "" {
						a.setQuery(q)
					}
				}
				return dims
			}),
		}
		// The clear button only exists while there is something to clear, so
		// the empty pill keeps its full width for the hint.
		if t.search.Text() != "" {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 4) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.clear.Layout(gtx, th) }),
			)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})

	return layout.Dimensions{Size: size}
}
