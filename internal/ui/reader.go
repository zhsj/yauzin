package ui

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

const (
	// readerScrollStep is the distance of one Up/Down press: about three lines
	// of body text, so a key reads as a small nudge through a long message
	// instead of a jump between cards.
	readerScrollStep unit.Dp = 60
	// readerPageOverlap stays on screen after a PageUp/PageDown, so the line
	// the reader was at is still there to pick the reading back up.
	readerPageOverlap unit.Dp = 40
)

// readerView renders one conversation: a toolbar, the subject headline and one
// bordered card per message with the newest one expanded. Only text/plain
// bodies are shown; HTML bodies and the original source open from the card's
// "more" menu.
type readerView struct {
	app   *App
	list  layout.List
	cards map[string]*messageCard
	atts  map[string]*pillButton

	// viewH is the height of the scrolling area on the last frame. The keys are
	// read before layout, so a PageUp/PageDown step comes from the frame that
	// was just drawn.
	viewH int

	back    *iconButton
	archive *iconButton
	del     *iconButton
	read    *iconButton
	star    *iconButton
	reply   *iconButton
	forward *iconButton

	newestForReply *mail.Message
}

func newReader(a *App) *readerView {
	r := &readerView{
		app:   a,
		cards: map[string]*messageCard{},
		atts:  map[string]*pillButton{},
	}
	r.list.Axis = layout.Vertical
	r.back = newListIcon(iconBack, 36, a.closeThread)
	r.archive = newListIcon(iconArchive, 36, a.archiveThread)
	r.del = newListIcon(iconDelete, 36, a.deleteThread)
	r.read = newListIcon(iconMarkRead, 36, a.toggleThreadRead)
	r.star = newListIcon(iconStarOutline, 36, func() { a.toggleThreadStar(a.thread.ID) })
	r.reply = newListIcon(iconReply, 36, func() {
		if r.newestForReply != nil {
			a.openCompose(composeReply, r.newestForReply)
		}
	})
	r.forward = newListIcon(iconForward, 36, func() {
		if r.newestForReply != nil {
			a.openCompose(composeForward, r.newestForReply)
		}
	})
	return r
}

func (r *readerView) Layout(gtx layout.Context) layout.Dimensions {
	a := r.app
	if a.loadingThread {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return a.th.text(gtx, i18n.T("reader.loading"), 14, colMuted, font.Normal, 1)
		})
	}
	if len(a.msgs) == 0 {
		return a.th.emptyState(gtx, iconMail, i18n.T("reader.emptyTitle"), i18n.T("reader.emptyHint"))
	}

	r.newestForReply = a.msgs[len(a.msgs)-1]
	if a.thread.Starred {
		r.star.setIcon(iconStarFilled)
	} else {
		r.star.setIcon(iconStarOutline)
	}
	if a.thread.Unread() {
		r.read.setIcon(iconMarkRead)
		r.read.setTip(i18n.T("tooltip.markRead"))
	} else {
		r.read.setIcon(iconMarkUnread)
		r.read.setTip(i18n.T("tooltip.markUnread"))
	}
	// The toolbar icons that need explaining carry hover labels, re-translated
	// every frame so a language switch needs no rebuild. Back, reply and
	// forward are unambiguous and go without.
	r.archive.setTip(i18n.T("tooltip.archive"))
	r.del.setTip(i18n.T("tooltip.delete"))

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return r.toolbar(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 6, Bottom: 10, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.th.wrappedText(gtx, subjectOrPlaceholder(a.thread.Subject), 22, colText, font.Bold)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 4) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return a.th.text(gtx, i18n.N("reader.messageCount", len(a.msgs)), 12, colMuted, font.Normal, 1)
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			r.viewH = gtx.Constraints.Max.Y
			return r.list.Layout(gtx, len(a.msgs), func(gtx layout.Context, i int) layout.Dimensions {
				msg := a.msgs[i]
				card := r.cardFor(msg.ID)
				return layout.Inset{Bottom: 10, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return card.Layout(gtx, msg)
				})
			})
		}),
	)
}

func (r *readerView) toolbar(gtx layout.Context) layout.Dimensions {
	a := r.app
	th := a.th
	return layout.Inset{Top: 4, Bottom: 4, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.back.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.archive.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.del.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.read.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.star.Layout(gtx, th) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.reply.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.forward.Layout(gtx, th) }),
		)
	})
}

// relabel forgets the cached action buttons of every card, so the next frame
// builds them in the active language. The open cards, the scroll position and
// the attachment chips survive: the chips show file names, not UI copy.
func (r *readerView) relabel() {
	for _, card := range r.cards {
		card.reply, card.fwd, card.more = nil, nil, nil
	}
}

func (r *readerView) cardFor(id string) *messageCard {
	if c, ok := r.cards[id]; ok {
		return c
	}
	c := &messageCard{reader: r, id: id}
	r.cards[id] = c
	return c
}

// replySelection returns the text selected in the body of the message with the
// given ID, or "" when nothing is selected there or the card is not built.
func (r *readerView) replySelection(id string) string {
	return r.cards[id].selectedText()
}

// reset forgets the per-message state of the previous conversation, so the
// caches cannot grow with every thread that was ever opened.
func (r *readerView) reset() {
	r.cards = map[string]*messageCard{}
	r.atts = map[string]*pillButton{}
	r.list.Position = layout.Position{}
}

// scrollBy moves the conversation by dy pixels, positive scrolling down. The
// offset is edited in place because layout.List only listens to the pointer: the
// next Layout turns an offset past a card into a new First, walks back over the
// previous card when the offset goes negative, and clamps both ends.
func (r *readerView) scrollBy(dy int) {
	if dy == 0 || len(r.app.msgs) == 0 {
		return
	}
	r.list.Position.Offset += dy
	r.app.invalidate()
}

// scrollPage moves one screenful less the overlap, so the text that was at the
// bottom edge stays visible after the jump.
func (r *readerView) scrollPage(gtx layout.Context, dir int) {
	h := r.viewH
	if h <= 0 {
		// The keys run before the first layout of a freshly opened conversation,
		// so no viewport has been measured yet: the window height is the closest
		// estimate available.
		h = gtx.Constraints.Max.Y
	}
	if step := h - gtx.Dp(readerPageOverlap); step > 0 {
		r.scrollBy(dir * step)
	}
}

// scrollToEdge jumps to the first or the last card. The last one is put at the
// top and the list pulls the offset back to the bottom edge on the next frame.
func (r *readerView) scrollToEdge(end bool) {
	if len(r.app.msgs) == 0 {
		return
	}
	r.list.Position.First, r.list.Position.Offset = 0, 0
	if end {
		r.list.Position.First = len(r.app.msgs) - 1
	}
	r.app.invalidate()
}

// attButton returns the download chip of one attachment. The cache key carries
// the message ID too: attachment IDs are backend data and need not be unique
// across messages.
func (r *readerView) attButton(msg *mail.Message, att mail.Attachment) *pillButton {
	key := msg.ID + "\x00" + att.ID
	if b, ok := r.atts[key]; ok {
		return b
	}
	messageID := msg.ID
	b := newPillButton(truncateName(att.Filename, 24), iconFile, false, func() {
		r.app.saveAttachment(messageID, att)
	})
	b.minW = 120
	r.atts[key] = b
	return b
}
