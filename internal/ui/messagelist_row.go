package ui

import (
	"image"
	"strconv"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// The label pill of a conversation row follows Gmail's 40dp row: a small
// solid chip around the label name.
const (
	labelChipPadX   unit.Dp = 6
	labelChipPadY   unit.Dp = 2
	labelChipGap    unit.Dp = 4
	labelChipRadius unit.Dp = 4
	// labelChipMin is the narrowest chip worth drawing: a narrower one would
	// show an ellipsis instead of a readable name.
	labelChipMin unit.Dp = 24
	// labelStripMin is the narrowest subject cell that still gets chips. Below
	// it the strip would squeeze the subject it annotates.
	labelStripMin unit.Dp = 48
)

type messageRow struct {
	list *messageListView
	app  *App

	click     widget.Clickable
	check     *checkBox
	star      *iconButton
	currentID string

	archive *iconButton
	del     *iconButton
	mark    *iconButton
}

func newMessageRow(l *messageListView) *messageRow {
	r := &messageRow{list: l, app: l.app}
	r.check = newCheckBox(func(checked bool) {
		r.list.toggleSelection(r.currentID, checked)
	})
	r.star = newListIcon(iconStarOutline, 28, func() {
		r.app.toggleThreadStar(r.currentID)
	})
	r.archive = newListIcon(iconArchive, 28, func() {
		r.app.archiveThreadByID(r.currentID)
	})
	r.del = newListIcon(iconDelete, 28, func() {
		r.app.deleteThreadByID(r.currentID)
	})
	r.mark = newListIcon(iconMarkRead, 28, func() {
		r.app.toggleThreadReadByID(r.currentID)
	})
	return r
}

func (r *messageRow) Layout(gtx layout.Context, sum mail.ThreadSummary, selected, cursor bool) layout.Dimensions {
	r.currentID = sum.ID
	// Pointer events reach every handler whose area contains them, so a click
	// on the checkbox or on one of the hover buttons is delivered to the row
	// clickable as well. The open action is therefore deferred until the
	// controls have reported whether one of them consumed the click.
	rowClicked := r.click.Clicked(gtx)
	if rowClicked {
		invalidateAfterTap(gtx)
	}
	r.clearHits()

	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(listRowHeight))

	fill := colSurface
	if !sum.Unread() {
		fill = colRowRead
	}
	if r.click.Hovered() {
		fill = colRowHover
	}
	// The keyboard cursor keeps the row visible even while the pointer rests
	// somewhere else, so it wins over the hover fill. The fill alone cannot
	// separate the cursor from a checked row, so the cursor also gets the
	// accent bar drawn below.
	if selected || cursor {
		fill = colRowSelected
	}

	dims := r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		r.app.th.cursor(gtx, pointer.CursorPointer, size)
		area := clip.Rect{Max: size}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, fill)
		area.Pop()

		if sum.ID == "" {
			return layout.Dimensions{Size: size}
		}
		// The Gmail-style cursor marker overlays the left inset instead of
		// shifting the controls, so j/k movement never nudges the row content.
		if cursor {
			bar := clip.Rect{Max: image.Pt(gtx.Dp(listCursorWidth), size.Y)}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, colPrimary)
			bar.Pop()
		}
		r.syncControls(sum, selected)
		return r.controls(gtx, sum, r.click.Hovered())
	})
	if rowClicked && sum.ID != "" && !r.controlHit() {
		r.app.openThread(sum)
	}
	return dims
}

// syncControls updates the icon buttons so a recycled row shows the state of
// the conversation it now holds.
func (r *messageRow) syncControls(sum mail.ThreadSummary, selected bool) {
	if sum.Unread() {
		r.mark.setIcon(iconMarkRead)
		r.mark.setTip(i18n.T("tooltip.markRead"))
	} else {
		r.mark.setIcon(iconMarkUnread)
		r.mark.setTip(i18n.T("tooltip.markUnread"))
	}
	// The hover labels are re-translated every frame, so a language switch
	// needs no rebuild.
	r.archive.setTip(i18n.T("tooltip.archive"))
	r.del.setTip(i18n.T("tooltip.delete"))
	if sum.Starred {
		r.star.setIcon(iconStarFilled)
	} else {
		r.star.setIcon(iconStarOutline)
	}
	checked := 0
	if selected {
		checked = 1
	}
	r.check.setState(checked)
}

// controls lays out the checkbox, star, participants, subject and snippet, plus
// either the hover actions or the date/attachment column.
func (r *messageRow) controls(gtx layout.Context, sum mail.ThreadSummary, hovered bool) layout.Dimensions {
	return layout.Inset{Left: 12, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		weight := font.Normal
		if sum.Unread() {
			weight = font.Bold
		}
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.check.Layout(gtx, r.app.th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.star.Layout(gtx, r.app.th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.senderCell(gtx, sum, weight) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return r.subjectSnippetCell(gtx, sum, weight) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.tailCell(gtx, sum, hovered) }),
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// senderCell shows the participants of the conversation, followed by the
// message count when the thread holds more than one.
func (r *messageRow) senderCell(gtx layout.Context, sum mail.ThreadSummary, weight font.Weight) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Dp(168)
	gtx.Constraints.Max.X = gtx.Dp(168)
	// The conversation count follows the names directly, so the sender cell
	// keeps a left-to-right flow; when a count is shown, the names give up its
	// width and truncate instead of pushing the count out of the cell.
	names := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if sum.MessageCount > 1 {
				gtx.Constraints.Max.X = gtx.Dp(128)
			}
			return r.app.th.text(gtx, threadParticipants(sum), textMain, colText, weight, 1)
		}),
	}
	if sum.MessageCount > 1 {
		names = append(names,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return r.app.th.text(gtx, strconv.Itoa(sum.MessageCount), textSecondary, colMuted, font.Normal, 1)
			}),
		)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, names...)
}

// subjectSnippetCell shows the labels of the conversation in front of the
// subject, then splits the remaining width evenly between the subject and the
// snippet.
func (r *messageRow) subjectSnippetCell(gtx layout.Context, sum mail.ThreadSummary, weight font.Weight) layout.Dimensions {
	children := make([]layout.FlexChild, 0, 5)
	if chips := r.app.rowLabels(sum); len(chips) > 0 {
		if w := labelStripWidth(gtx, gtx.Constraints.Max.X); w > 0 {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return r.labelStrip(gtx, chips, w)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			)
		}
	}
	children = append(children,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return r.app.th.text(gtx, subjectOrPlaceholder(sum.Subject), textMain, colText, weight, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return r.app.th.text(gtx, snippetText(sum.Snippet), textMain, colMuted, font.Normal, 1)
		}),
	)
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

// labelStripWidth caps the label strip at half of the subject cell, so the
// subject and the snippet keep the rest. A cell too narrow for chips reports 0
// and the row draws no labels at all.
func labelStripWidth(gtx layout.Context, cell int) int {
	w := cell / 2
	if w < gtx.Dp(labelStripMin) {
		return 0
	}
	return w
}

// labelStrip draws the chips of one row left to right, stopping when the
// remaining width cannot hold another readable chip.
func (r *messageRow) labelStrip(gtx layout.Context, chips []labelChip, maxWidth int) layout.Dimensions {
	th := r.app.th
	padX, padY := gtx.Dp(labelChipPadX), gtx.Dp(labelChipPadY)
	gap := gtx.Dp(labelChipGap)
	radius := gtx.Dp(labelChipRadius)
	minWidth := gtx.Dp(labelChipMin)
	// The chips share one line box, so the tallest one fixes the pill height.
	// Measuring it first would shape the same text twice; instead the first
	// chip's height is reused for the rest.
	height := 0
	x := 0
	for _, chip := range chips {
		remain := maxWidth - x
		if remain < minWidth {
			break
		}
		textMax := remain - 2*padX
		if textMax <= 0 {
			break
		}
		// Recording the text measures it and keeps the draw operations, so the
		// name is shaped once and replayed inside the pill.
		macro := op.Record(gtx.Ops)
		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(textMax, 1<<16)}
		text := th.text(measure, chip.name, textTiny, chipTextColor(chip.bg), font.Normal, 1)
		call := macro.Stop()

		w := min(text.Size.X+2*padX, remain)
		if height == 0 {
			height = text.Size.Y + 2*padY
		}
		area := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
		rr := clip.RRect{Rect: image.Rectangle{Max: image.Pt(w, height)}, NW: radius, NE: radius, SE: radius, SW: radius}.Push(gtx.Ops)
		paint.Fill(gtx.Ops, chip.bg)
		rr.Pop()
		label := op.Offset(image.Pt(padX, (height-text.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		label.Pop()
		area.Pop()
		x += w + gap
	}
	if x == 0 {
		return layout.Dimensions{}
	}
	// The loop leaves a trailing gap after the last chip.
	return layout.Dimensions{Size: image.Pt(x-gap, height)}
}

// tailCell reserves the trailing column: the attachment slot and the date, or
// the hover actions on top of that same slot.
func (r *messageRow) tailCell(gtx layout.Context, sum mail.ThreadSummary, hovered bool) layout.Dimensions {
	// The tail always reserves the same width — the idle row of the attachment
	// slot and the date cell — so the subject/snippet body keeps its equal split
	// no matter whether the pointer hovers a row or the conversation has an
	// attachment. The hover actions overlay the same slot, right-aligned.
	const tailWidth = 8 + 16 + 8 + 76
	return layout.Stack{Alignment: layout.E}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if hovered {
				return layout.Dimensions{Size: image.Pt(gtx.Dp(tailWidth), 0)}
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !sum.HasAttachments {
						return layout.Dimensions{Size: image.Pt(gtx.Dp(16), gtx.Dp(16))}
					}
					return r.app.th.glyphIn(gtx, iconAttach, 15, 16)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Dp(76)
					gtx.Constraints.Max.X = gtx.Dp(76)
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return r.app.th.text(gtx, formatListTime(sum.LastDate), textSecondary, colMuted, font.Normal, 1)
					})
				}),
			)
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if !hovered {
				return layout.Dimensions{}
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.archive.Layout(gtx, r.app.th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.del.Layout(gtx, r.app.th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.mark.Layout(gtx, r.app.th) }),
			)
		}),
	)
}

// clearHits forgets the control hits of the previous frame, so buttons that
// were not laid out this frame cannot suppress the row action.
func (r *messageRow) clearHits() {
	r.check.hit, r.star.hit = false, false
	r.archive.hit, r.del.hit, r.mark.hit = false, false, false
}

// controlHit reports whether one of the row's controls consumed the click.
func (r *messageRow) controlHit() bool {
	return r.check.hit || r.star.hit || r.archive.hit || r.del.hit || r.mark.hit
}
