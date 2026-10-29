package ui

import (
	"image"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/zhsj/yauzin/internal/i18n"
)

// loadingWatchdog bounds how long the refresh glyph may spin. Every loader
// clears the flag on its own paths; this only keeps a missed one from leaving
// the window at 60fps forever. It is longer than any load timeout.
const loadingWatchdog = 45 * time.Second

// toolbarMoreLeftPad is the distance from the list edge to the first control:
// the content card insets (4+8) plus the toolbar's own 12dp left inset.
const toolbarMoreLeftPad = 24

// toolbarGap separates the select-all box from the refresh button.
const toolbarGap = 2

// listToolbar is the Gmail style bar above the conversation list: bulk select
// on the left, page range and previous/next arrows on the right. Refreshing
// lives here (not in the top bar).
type listToolbar struct {
	app *App

	check   *checkBox
	refresh *iconButton
	more    *iconButton

	archive    *iconButton
	delete     *iconButton
	markRead   *iconButton
	markUnread *iconButton
	spam       *iconButton
	clear      *iconButton

	prev *iconButton
	next *iconButton

	page int
	// hasNext reports whether the backend offered a cursor for the next page.
	// The toolbar cannot derive it from a total any more: paging is by cursor.
	hasNext bool
	// rangeStart/rangeEnd/rangeTotal back the page label. The label itself is
	// translated while laying out, so a language change needs no reload.
	rangeStart, rangeEnd, rangeTotal int
	loading                          bool
	// loadingUntil is when the loading flag is given up on. See
	// loadingWatchdog.
	loadingUntil time.Time
	moreRect     image.Rectangle
}

func newListToolbar(a *App) *listToolbar {
	t := &listToolbar{app: a}
	t.check = newCheckBox(func(checked bool) {
		if checked {
			a.list.selectAll()
			return
		}
		a.list.clearSelection()
	})
	t.refresh = newListIcon(iconRefresh, 32, a.reloadCheckedNow)
	// Refreshing has its own button just left of this one, so the menu only
	// carries the actions that have no toolbar glyph.
	t.more = newListIcon(iconMoreVert, 32, func() {
		a.showMenu(t.moreRect, menuAlignStart, []menuItem{
			{Label: i18n.T("list.markAllRead"), Icon: iconMarkRead, OnTap: func() { a.markPageRead(true) }},
		})
	})
	t.archive = newListIcon(iconArchive, 32, a.bulkArchive)
	t.delete = newListIcon(iconDelete, 32, a.bulkDelete)
	t.markRead = newListIcon(iconMarkRead, 32, func() { a.bulkSetRead(true) })
	t.markUnread = newListIcon(iconMarkUnread, 32, func() { a.bulkSetRead(false) })
	t.spam = newListIcon(iconSpam, 32, a.bulkToggleSpam)
	t.clear = newListIcon(iconClose, 32, func() { a.list.clearSelection() })
	t.prev = newListIcon(iconChevronLeft, 32, func() { a.goToPage(t.page - 1) })
	t.next = newListIcon(iconChevronRight, 32, func() { a.goToPage(t.page + 1) })
	return t
}

// setLoading is the one place the loading flag changes: the refresh glyph
// spins exactly when the list is loading, and the watchdog window is armed
// here so no caller has to remember it.
func (t *listToolbar) setLoading(v bool) {
	t.loading = v
	t.refresh.setSpinning(v)
	if v {
		t.loadingUntil = time.Now().Add(loadingWatchdog)
	}
}

// setTips refreshes the hover labels in the active language; the labels are
// re-translated every frame, so a language switch needs no rebuild.
func (t *listToolbar) setTips() {
	t.archive.setTip(i18n.T("tooltip.archive"))
	t.delete.setTip(i18n.T("tooltip.delete"))
	t.markRead.setTip(i18n.T("tooltip.markRead"))
	t.markUnread.setTip(i18n.T("tooltip.markUnread"))
	// The selected rows come from the folder on screen, so the spam action
	// points at the other folder, like the reader's and the rows' buttons.
	spam := !t.app.viewingSpam()
	t.spam.setTip(spamTip(spam))
	if spam {
		t.spam.setIcon(iconSpam)
	} else {
		t.spam.setIcon(iconInbox)
	}
}

// setPage records the page the list is showing: its number, the total the
// backend reported, how many conversations it holds and whether another page
// was offered.
func (t *listToolbar) setPage(page, total, count int, hasNext bool) {
	t.page = page
	t.hasNext = hasNext
	t.rangeTotal = total
	// The page range depends only on how many rows arrived; the total is shown
	// separately and may be replaced by the folder's accurate count later (see
	// rangeText), so an unknown total must not blank the range.
	if count == 0 {
		t.rangeStart, t.rangeEnd = 0, 0
		return
	}
	start := (page-1)*perPage + 1
	t.rangeStart, t.rangeEnd = start, start+count-1
}

// rangeText renders the page label in the active language.
func (t *listToolbar) rangeText() string {
	total := t.rangeTotal
	// Browsing a folder or label, the accurate conversation count lives in the
	// sidebar's folder details, which arrive after the page (reloadAll loads
	// names, then rows, then counters). Gmail's resultSizeEstimate is only an
	// estimate that often stays the same across labels, so prefer the folder
	// total once it is known. A search has no active folder, so folderTotal
	// reports zero and the list's own estimate stays.
	if t.app != nil {
		if n := t.app.folderTotal(t.app.activeFolder); n > 0 {
			total = n
		}
	}
	if total <= 0 {
		return ""
	}
	return i18n.T("list.range", map[string]int{
		"Start": t.rangeStart,
		"End":   t.rangeEnd,
		"Total": total,
	})
}

func (t *listToolbar) Layout(gtx layout.Context) layout.Dimensions {
	th := t.app.th
	// The last line of defence for the spinner: a clear that some path forgot
	// costs one watchdog period of redraws, not a window that never idles.
	if t.loading && time.Now().After(t.loadingUntil) {
		t.setLoading(false)
	}
	h := gtx.Dp(48)
	w := gtx.Constraints.Max.X
	t.setTips()
	paintToolbarBackground(gtx, w, h)
	t.anchorMore(gtx, h)
	selected := t.selectionState()

	gtx.Constraints = layout.Exact(image.Pt(w, h))
	layout.Inset{Left: 12, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := append(t.leftControls(gtx, th, selected), t.rightControls(gtx, th)...)
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
	return layout.Dimensions{Size: image.Pt(w, h)}
}

// paintToolbarBackground fills the bar and draws its bottom hairline.
func paintToolbarBackground(gtx layout.Context, w, h int) {
	area := clip.Rect{Max: image.Pt(w, h)}
	stack := area.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface)
	stack.Pop()
	line := clip.Rect{Min: image.Pt(0, h-1), Max: image.Pt(w, h)}
	ls := line.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	ls.Pop()
}

// anchorMore records where the overflow menu should open (window
// coordinates). The anchor is the overflow button itself, so the menu opens
// just below it; the button is centred in the 48dp bar.
func (t *listToolbar) anchorMore(gtx layout.Context, h int) {
	baseX := 0
	if !t.app.sidebarCollapsed {
		baseX = gtx.Dp(sidebarWidth)
	}
	y := gtx.Dp(t.app.headerHeight()) + 2 + 4
	// The overflow button sits after the select-all box, a 2dp gap and the
	// refresh button. Its position comes from the widgets' actual sizes rather
	// than literals that drift when one of them is resized; the leading pad is
	// the content card insets (4+8) plus the toolbar's own 12dp left inset.
	btn := gtx.Dp(t.more.size)
	btnX := baseX + gtx.Dp(toolbarMoreLeftPad) + gtx.Dp(32) + gtx.Dp(toolbarGap) + gtx.Dp(t.refresh.size)
	btnY := y + (h-btn)/2
	t.moreRect = image.Rect(btnX, btnY, btnX+btn, btnY+btn)
}

// selectionState syncs the select-all box with the current selection and
// returns how many conversations are selected.
func (t *listToolbar) selectionState() int {
	selected := t.app.list.selectionCount()
	allSelected := selected > 0 && selected >= len(t.app.list.threads)
	switch {
	case selected == 0:
		t.check.setState(0)
	case allSelected:
		t.check.setState(1)
	default:
		t.check.setState(2)
	}
	return selected
}

// leftControls builds the select-all box and whichever action group matches
// the selection: refresh and overflow while idle, bulk actions while not.
func (t *listToolbar) leftControls(gtx layout.Context, th *theme, selected int) []layout.FlexChild {
	children := make([]layout.FlexChild, 0, 8)
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.check.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 2) }),
	)
	if selected == 0 {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.refresh.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.more.Layout(gtx, th) }),
		)
	} else {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.archive.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.delete.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.markRead.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.markUnread.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !t.app.showsSpamAction() {
					return layout.Dimensions{}
				}
				return t.spam.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, i18n.N("list.selected", selected), textSecondary, colText, font.Medium, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 4) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.clear.Layout(gtx, th) }),
		)
	}
	return append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Min}
	}))
}

// rightControls builds the page range and the paging arrows. The arrows are
// disabled while a page loads, so a second request cannot supersede the first.
func (t *listToolbar) rightControls(gtx layout.Context, th *theme) []layout.FlexChild {
	t.prev.setDisabled(t.loading || t.page <= 1)
	t.next.setDisabled(t.loading || !t.hasNext)
	return []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				text := t.rangeText()
				if text == "" {
					return layout.Dimensions{}
				}
				return th.text(gtx, text, textSecondary, colMuted, font.Normal, 1)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.prev.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.next.Layout(gtx, th) }),
	}
}
