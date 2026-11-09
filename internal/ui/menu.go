package ui

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
)

// The menu card sizes: a plain entry is one line tall, an entry with a detail
// line below its label needs the room for both. The card is narrow by default
// and widens when any entry carries a detail, so an address is not cut off.
const (
	menuRowHeight       unit.Dp = 40
	menuRowDetailHeight unit.Dp = 58
	menuCardWidth       unit.Dp = 200
	menuCardDetailWidth unit.Dp = 280
)

// menuItem is one entry of a dropdown menu. Detail is a second, smaller line
// under the label (the address under an account name), Icon draws a leading
// glyph, Avatar a leading initials circle instead, and Checked marks the active
// entry with a trailing check mark.
type menuItem struct {
	Label   string
	Detail  string
	Icon    icon
	Avatar  string
	Checked bool
	OnTap   func()
}

// menuAlign is the edge of the anchor the card lines up with: a control near
// the left of the window opens its card rightwards, one near the right edge
// opens it leftwards. Both directions are clamped into the window afterwards.
type menuAlign int

const (
	// menuAlignStart puts the card's left edge on the anchor's left edge.
	menuAlignStart menuAlign = iota
	// menuAlignEnd puts the card's right edge on the anchor's right edge.
	menuAlignEnd
)

// menuOverlay is a small dropdown anchored under a widget. The anchor and the
// alignment are chosen by the owning view; the card is clamped so it always
// stays inside the window.
type menuOverlay struct {
	items   []menuItem
	rows    []*menuRow
	anchor  image.Rectangle
	align   menuAlign
	blocker *tapBlocker
	// card swallows clicks on the padding of the menu itself, so they neither
	// reach the mailbox nor close the menu.
	card *tapBlocker
	// list scrolls the entries when the menu is taller than the window. It has
	// to live on the overlay: a layout.List built per frame forgets its offset.
	list layout.List
}

func newMenuOverlay(a *App, anchor image.Rectangle, align menuAlign, items []menuItem) *menuOverlay {
	m := &menuOverlay{anchor: anchor, align: align, items: items}
	// A dropdown is not modal: a click outside the card dismisses it and still
	// reaches the widget underneath. The guard keeps a panel that the click
	// opened from being closed again on the same frame.
	m.blocker = newPassThroughTapBlocker(color.NRGBA{}, func() {
		if a.menu == m {
			a.hideOverlay()
		}
	})
	m.card = newTapBlocker(color.NRGBA{}, nil)
	for _, raw := range items {
		item := raw
		m.rows = append(m.rows, newMenuRow(item, func() {
			// Close first so an entry that opens another panel starts clean.
			a.hideOverlay()
			if item.OnTap != nil {
				item.OnTap()
			}
		}))
	}
	return m
}

// cardWidth returns the width of the card. A detail line widens it: an address
// does not fit the narrow card a bare label is happy in.
func (m *menuOverlay) cardWidth(gtx layout.Context) int {
	w := gtx.Dp(menuCardWidth)
	for _, r := range m.rows {
		if r.detail != "" {
			w = gtx.Dp(menuCardDetailWidth)
			break
		}
	}
	return min(w, max(gtx.Constraints.Max.X-16, 1))
}

// cardHeight sums the rows, so a two-line entry is not clipped by the card.
func (m *menuOverlay) cardHeight(gtx layout.Context) int {
	h := 0
	for _, r := range m.rows {
		h += r.height(gtx)
	}
	return min(h+gtx.Dp(12), max(gtx.Constraints.Max.Y-16, 1))
}

// geometry returns the size of the card and where it goes. It is kept apart
// from Layout so the clamping can be checked without a window.
func (m *menuOverlay) geometry(gtx layout.Context) (size, origin image.Point) {
	maxX, maxY := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	// The card is capped to the window first: a menu with more entries than fit
	// scrolls instead of growing past the edge and being clamped into a
	// negative origin.
	cardW := m.cardWidth(gtx)
	cardH := m.cardHeight(gtx)
	// Line the card up with the edge the caller picked, then clamp it: an
	// anchor too close to the opposite edge (or a narrow window) still keeps
	// the whole card on screen.
	x := m.anchor.Min.X
	if m.align == menuAlignEnd {
		x = m.anchor.Max.X - cardW
	}
	x = min(max(x, 8), max(maxX-cardW-8, 8))
	y := m.anchor.Max.Y + 4
	if y+cardH > maxY-8 {
		// No room below the anchor: open upwards.
		y = m.anchor.Min.Y - cardH - 4
	}
	y = min(max(y, 8), max(maxY-cardH-8, 8))
	return image.Pt(cardW, cardH), image.Pt(x, y)
}

func (m *menuOverlay) Layout(gtx layout.Context, a *App) layout.Dimensions {
	_ = m.blocker.Layout(gtx)

	size, origin := m.geometry(gtx)
	cardGtx, pop := placeCard(gtx, size, origin)
	defer pop()
	drawCard(cardGtx, size, 8)

	// Clicks on the card padding stop here: the rows below are laid out after
	// this and therefore stay on top of it.
	m.card.Layout(cardGtx)

	layout.Inset{Top: 6, Bottom: 6}.Layout(cardGtx, func(gtx layout.Context) layout.Dimensions {
		m.list.Axis = layout.Vertical
		return m.list.Layout(gtx, len(m.rows), func(gtx layout.Context, i int) layout.Dimensions {
			return m.rows[i].Layout(gtx, a.th)
		})
	})
	return layout.Dimensions{Size: size}
}
