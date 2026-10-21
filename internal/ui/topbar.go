package ui

import (
	"image"
	"time"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"image/color"
	"yauzin/internal/i18n"
)

// topBar holds the global controls: sidebar toggle, brand, search, settings
// and the account avatar, mirroring Gmail's 64px header. There is deliberately
// no refresh button; refreshing lives in the list toolbar.
//
// With a client-side title bar the same row doubles as the window title bar: it
// gains the minimize, maximize and close buttons, and the brand block becomes
// the grip that moves the window.
type topBar struct {
	app      *App
	menu     *iconButton
	settings *iconButton
	controls *windowControls

	search     widget.Editor
	clear      *iconButton
	avatarBtn  widget.Clickable
	avatarRect image.Rectangle
	// menuAnchor is avatarRect with its bottom pushed below the title bar, so
	// the account dropdown never opens underneath the bar.
	menuAnchor image.Rectangle
	brandClick widget.Clickable
	lastClick  time.Time
}

func newTopBar(a *App) *topBar {
	t := &topBar{app: a, controls: newWindowControls()}
	t.menu = newIconButton(iconMenu, 40, a.toggleSidebar)
	t.settings = newIconButton(iconSettings, 40, a.showSettings)
	t.search.SingleLine = true
	t.search.Submit = true
	t.clear = newIconButton(iconClose, 28, a.clearSearchInput)
	return t
}

const (
	// topBarInset is the horizontal padding of the header. The dropdown anchors
	// below use it to convert content coordinates into window coordinates.
	topBarInset = unit.Dp(18)
	// titleBarInset is the smaller padding of the combined title bar, which has
	// to fit the window buttons as well.
	titleBarInset = unit.Dp(10)
	// titleControlsGap separates the account area from the window buttons.
	titleControlsGap = unit.Dp(6)
	// brandNameMinWidth hides the wordmark once the window gets narrow.
	brandNameMinWidth = unit.Dp(820)
	// searchMinWidth keeps the search pill usable in the smallest window.
	searchMinWidth = unit.Dp(160)
	// brandNameEms fixes the width of the brand wordmark cell as a multiple of
	// the label's em size. The cell must not depend on the UI language (the
	// natural widths of "Yauzin" and "邮笺" differ a lot), otherwise the
	// centred search pill shifts when the language changes. Four ems fits the
	// widest translation with margin to spare.
	brandNameEms = 4
	// brandNameSize and brandNameWeight are the wordmark style. Both title bar
	// modes share them: the combined bar has the same 40dp content row, and a
	// 15sp label read as too small beside the 36dp mark.
	brandNameSize   = unit.Sp(22)
	brandNameWeight = font.Bold
	// searchGripMin is the thinnest blank strip kept beside the pill, so the
	// title bar stays draggable once the window gets narrow.
	searchGripMin = unit.Dp(12)
	// avatarSize is the diameter of the account avatar in the header.
	avatarSize = unit.Dp(30)
	// brandMarkSize is the cell of the brand mark on the title bar. 36dp fits
	// the 40dp content row with 2dp of breathing room above and below, and its
	// device pixels land on the embedded bitmap sizes at 1x / 1.25x / 1.5x /
	// 2x / 3x (36 / 45 / 54 / 72 / 108px). Those exact hits are why the mark
	// has its own variants: the bitmap is drawn 1:1 instead of being filtered
	// down, which is what made it look muddy at 24dp.
	brandMarkSize = unit.Dp(36)
	// brandMarkGap separates the brand mark from the wordmark. It stays small
	// because the wordmark itself leaves its cell's right side blank: see
	// brandName.
	brandMarkGap = unit.Dp(4)
	// doubleClickGap is the interval that turns two clicks on the brand block
	// into a maximize/restore toggle.
	doubleClickGap = 400 * time.Millisecond
)

func (t *topBar) Layout(gtx layout.Context) layout.Dimensions {
	a := t.app
	th := a.th

	csd := a.clientSide()
	width, height := gtx.Constraints.Max.X, gtx.Constraints.Max.Y

	// The bar is white like the content cards: a hairline separates it from the
	// page instead of the colour block the old header used.
	area := clip.Rect{Max: image.Pt(width, height)}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface)
	area.Pop()
	if height > 1 {
		line := clip.Rect{Min: image.Pt(0, height-1), Max: image.Pt(width, height)}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, colBorder)
		line.Pop()
	}

	inset := layout.Inset{Top: 8, Right: topBarInset, Bottom: 8, Left: topBarInset}
	searchMax := unit.Dp(720)
	if csd {
		// Four dp leaves room for the 40dp buttons inside the 48dp bar.
		inset = layout.Inset{Top: 4, Right: titleBarInset, Bottom: 4, Left: titleBarInset}
		searchMax = 560
	}
	t.avatarRect = t.anchorAvatar(gtx, csd, inset.Right)
	t.menuAnchor = t.avatarRect
	if csd {
		t.menuAnchor.Max.Y = gtx.Dp(a.headerHeight())
	}

	children := t.leftControls(gtx, csd, th)
	children = append(children, t.searchControl(gtx, csd, searchMax))
	children = append(children, t.rightControls(gtx, csd, th)...)
	return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// leftControls builds the leading group of the header: the sidebar toggle and,
// under a client-side title bar, the draggable brand block (otherwise the logo
// and wordmark).
func (t *topBar) leftControls(gtx layout.Context, csd bool, th *theme) []layout.FlexChild {
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return t.menu.layout(gtx, th, t.menu.ic, colHover)
		}),
	}
	if csd {
		showName := gtx.Constraints.Max.X >= gtx.Dp(brandNameMinWidth)
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return t.brand(gtx, th, showName)
			}),
		)
	} else {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 4) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.appLogo(gtx, brandMarkSize)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return hgap(gtx, brandMarkGap)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return t.brandName(gtx, th, brandNameSize, colPrimary, brandNameWeight)
			}),
		)
	}
	// Mirror the width of the performance readout on the right with an
	// invisible spacer, so toggling the readout keeps both side groups
	// balanced and the centred search pill does not shift.
	if t.app.perf.isVisible() {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			w := t.app.perf.phantomWidth(gtx, th)
			return layout.Dimensions{Size: image.Pt(w, 0)}
		}))
	}
	return children
}

// rightControls builds the trailing group: the settings button, the account
// avatar and, under a client-side title bar, the window controls.
func (t *topBar) rightControls(gtx layout.Context, csd bool, th *theme) []layout.FlexChild {
	a := t.app
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
	}
	// The performance readout sits left of the settings button and only
	// occupies space while it is enabled.
	if a.perf.isVisible() {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.perf.layout(gtx, th)
		}))
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, perfSettingsGap) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return t.settings.layout(gtx, th, t.settings.ic, colHover)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return t.avatar(gtx)
		}),
	)
	if csd {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, titleControlsGap) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.vline(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, titleControlsGap) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return t.controls.Layout(gtx, th, a)
			}),
		)
	}
	return children
}

// dragHandle marks a blank rectangle of the title bar as a window grip: the
// platform moves the window when the pointer is pressed on it.
func (t *topBar) dragHandle(gtx layout.Context, size image.Point) layout.Dimensions {
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	area.Pop()
	return layout.Dimensions{Size: size}
}

// brandName draws the brand wordmark inside a fixed-width cell so the header
// layout is identical in every UI language and the search pill never shifts
// when the language changes. See brandNameEms.
//
// The label sits at the start of the cell instead of being centred in it: the
// translations differ a lot in width, so centring turned the slack of the wide
// cell into a visible gap between the mark and the wordmark (the two-character
// Chinese label at 22sp left about 22dp blank on either side). Left aligning
// keeps the cell, and with it the main-axis arithmetic of the row, unchanged
// while the text starts right after brandMarkGap.
func (t *topBar) brandName(gtx layout.Context, th *theme, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	w := gtx.Sp(size) * brandNameEms
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Constraints.Max.Y)}
	dims := th.text(cgtx, i18n.T("app.brand"), size, col, weight, 1)
	return layout.Dimensions{Size: image.Pt(w, dims.Size.Y), Baseline: dims.Baseline}
}

// brand is the draggable identity block of the title bar. Dragging moves the
// window, a double click toggles maximize.
func (t *topBar) brand(gtx layout.Context, th *theme, showName bool) layout.Dimensions {
	a := t.app
	dims := t.brandClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.appLogo(gtx, brandMarkSize)
			}),
		}
		if showName {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, brandMarkGap) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return t.brandName(gtx, th, brandNameSize, colPrimary, brandNameWeight)
				}),
			)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
	if t.brandClick.Clicked(gtx) {
		now := time.Now()
		if now.Sub(t.lastClick) < doubleClickGap {
			t.lastClick = time.Time{}
			a.windowActions |= toggleMaximize(a.maximized)
			invalidateAfterTap(gtx)
		} else {
			t.lastClick = now
		}
	}
	// The brand block is a drag handle like the blank strips beside the search
	// pill; a double click toggles maximize.
	t.dragHandle(gtx, dims.Size)
	return dims
}

// anchorAvatar computes the avatar rectangle in window coordinates, which the
// account dropdown uses as its anchor.
func (t *topBar) anchorAvatar(gtx layout.Context, csd bool, inset unit.Dp) image.Rectangle {
	size := gtx.Dp(avatarSize)
	right := gtx.Dp(inset)
	if csd {
		right += 2*gtx.Dp(titleControlsGap) + gtx.Dp(unit.Dp(1)) + t.controls.Width(gtx)
	}
	left := gtx.Constraints.Max.X - right - size
	top := (gtx.Dp(t.app.headerHeight()) - size) / 2
	return image.Rect(left, top, left+size, top+size)
}

func (t *topBar) avatar(gtx layout.Context) layout.Dimensions {
	a := t.app
	diameter := gtx.Dp(avatarSize)
	size := image.Pt(diameter, diameter)

	if t.avatarBtn.Clicked(gtx) {
		a.showAccountMenu()
		invalidateAfterTap(gtx)
	}
	return t.avatarBtn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		a.th.cursor(gtx, pointer.CursorPointer, size)
		return a.th.avatar(gtx, a.accountName(), avatarSize)
	})
}

// vline separates the account area from the window buttons.
func (t *topBar) vline(gtx layout.Context) layout.Dimensions {
	h := gtx.Dp(unit.Dp(16))
	w := gtx.Dp(unit.Dp(1))
	area := clip.Rect{Max: image.Pt(w, h)}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	area.Pop()
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// maxWidth clamps the widget width, keeping it centred by the caller.
func maxWidth(gtx layout.Context, max unit.Dp, w layout.Widget) layout.Dimensions {
	limit := gtx.Dp(max)
	if gtx.Constraints.Max.X > limit {
		gtx.Constraints.Min.X = limit
		gtx.Constraints.Max.X = limit
	}
	return w(gtx)
}
