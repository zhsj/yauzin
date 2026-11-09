package ui

import (
	"image"
	"strings"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// actions returns the reply and forward buttons, building them once. The
// callbacks look the message up when they run: the reader replaces the message
// objects on every refresh, so a closure over the pointer seen during the first
// frame would act on an older copy.
func (c *messageCard) actions(a *App) {
	if c.reply != nil {
		return
	}
	c.reply = newPillButton(i18n.T("reader.reply"), iconReply, false, func() {
		if m := c.message(a); m != nil {
			a.openCompose(composeReply, m)
		}
	})
	c.fwd = newPillButton(i18n.T("reader.forward"), iconForward, false, func() {
		if m := c.message(a); m != nil {
			a.openCompose(composeForward, m)
		}
	})
	// The overflow menu is built once as well: its entries are assembled per
	// click, because which ones apply depends on the message being shown.
	c.more = newIconButton(iconMoreVert, 32, func() {
		if m := c.message(a); m != nil {
			a.showMenu(c.moreAnchor(a), menuAlignEnd, c.menuItems(a, m))
		}
	})
	c.more.setGlyph(20)
	c.more.setTip(i18n.T("reader.moreActions"))
	c.more.track = true
}

// menuItems lists the actions of one message: reply-all is always offered, the
// two previews need an HTML body, and the download works for every message.
// The composer takes the message from the reader when the item is clicked, so
// a refresh that replaced the message object is still quoted correctly.
func (c *messageCard) menuItems(a *App, m *mail.Message) []menuItem {
	items := []menuItem{{
		Label: i18n.T("reader.replyAll"),
		Icon:  iconReply,
		OnTap: func() {
			if msg := c.message(a); msg != nil {
				a.openCompose(composeReplyAll, msg)
			}
		},
	}}
	if strings.TrimSpace(m.HTMLBody) != "" {
		items = append(items,
			menuItem{Label: i18n.T("reader.menu.safePreview"), Icon: iconOpenInNew, OnTap: func() {
				if msg := c.message(a); msg != nil {
					a.openHTMLInBrowser(msg, previewSafe)
				}
			}},
			menuItem{Label: i18n.T("reader.menu.fullPreview"), Icon: iconWarning, OnTap: func() {
				if msg := c.message(a); msg != nil {
					a.openHTMLInBrowser(msg, previewFull)
				}
			}},
		)
	}
	return append(items, menuItem{Label: i18n.T("reader.menu.downloadMessage"), Icon: iconFile, OnTap: func() {
		if msg := c.message(a); msg != nil {
			a.saveMessage(msg)
		}
	}})
}

// moreAnchor is where the dropdown opens: the button itself. A tap on a touch
// screen has no hover frame before it, so the pointer position is the fallback.
func (c *messageCard) moreAnchor(a *App) image.Rectangle {
	if r := c.more.rect; !r.Empty() {
		return r
	}
	p := a.pointer
	return image.Rect(p.X, p.Y, p.X+1, p.Y+1)
}

// message returns the message the card currently shows. Cards are keyed by
// message ID, so the ID identifies it across refreshes.
func (c *messageCard) message(a *App) *mail.Message {
	for _, m := range a.msgs {
		if m.ID == c.id {
			return m
		}
	}
	return nil
}

// cardInsetTop is the padding above the content of a card. The find bar adds it
// to the measured head height to locate the body inside the card.
const cardInsetTop unit.Dp = 10

// truncateName keeps attachment chips from stretching the card.
func truncateName(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	return string(runes[:limit]) + "…"
}

type messageCard struct {
	reader *readerView
	id     string
	// header toggles the card while it is collapsed. The expanded card is
	// folded from avatar, stamp and fold instead: the sender name and address
	// are selectable, so a click on them must not toggle anything.
	header widget.Clickable
	avatar widget.Clickable
	stamp  widget.Clickable
	fold   widget.Clickable
	// detailsClick folds the header details out below the head of the open
	// card. The state lives on the card rather than on the App because the
	// reader drops the cards with the conversation: a newly opened thread
	// starts with its headers folded.
	detailsClick widget.Clickable
	detailsOpen  bool

	reply *pillButton
	fwd   *pillButton
	more  *iconButton

	// name, addr and bodyText are the selectable runs of this card. Each
	// belongs to one message, so it is rebuilt when the card shows a different
	// one.
	name     textSelection
	addr     textSelection
	bodyText textSelection
	// detailsSel holds one selectable run per header field, indexed by the
	// field rather than by row position: an empty Cc removes its row, and a
	// position would then hand Reply-To the selection Cc was carrying.
	detailsSel [headerFieldCount]textSelection

	// headH is the height of the open head and its divider on the last layout,
	// height the height of the whole card. The find bar needs both to scroll a
	// hit inside a card taller than the viewport.
	headH  int
	height int
	// marks are the highlight rectangles of the find hits in this body,
	// measured from the layout of an earlier frame.
	marks findMarks
}

// selectedText returns the text selected in the body of this card's message, or
// "" when nothing is selected. A selection belonging to another message is
// ignored: the card drops it when it shows a different one.
func (c *messageCard) selectedText() string {
	if c == nil {
		return ""
	}
	return c.bodyText.Selected(c.id)
}

// bodySelection returns the Selectable for msg, dropping any selection left
// over from the previous message shown in this card.
func (c *messageCard) bodySelection(msg *mail.Message) *widget.Selectable {
	return c.bodyText.Select(msg.ID)
}

// textRuns lists every selectable run of this card, the header details
// included. The menu and the reader's one-selection rule both read this list,
// so a run added here is wired into both at once. The array is returned by
// value: it is small and fixed, and a slice would allocate once per frame.
func (c *messageCard) textRuns() [3 + headerFieldCount]*textSelection {
	var runs [3 + headerFieldCount]*textSelection
	runs[0], runs[1], runs[2] = &c.name, &c.addr, &c.bodyText
	for f := headerField(0); f < headerFieldCount; f++ {
		runs[3+f] = &c.detailsSel[f]
	}
	return runs
}

// selection reports the fragment a right click landed on in this card, if any.
func (c *messageCard) selection() (string, bool) {
	for _, s := range c.textRuns() {
		if text, ok := s.Take(); ok {
			return text, true
		}
	}
	return "", false
}

func (c *messageCard) Layout(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	if a.expanded == nil {
		a.expanded = map[string]bool{}
	}
	open := a.expanded[msg.ID]

	c.actions(a)
	// Pointer events reach every handler under them, so a click on the ⋮ is
	// also delivered to the area that folds the card. The toggle therefore
	// waits until the button has reported whether it consumed the click.
	clicked := c.header.Clicked(gtx)
	folded := c.avatar.Clicked(gtx) || c.stamp.Clicked(gtx) || c.fold.Clicked(gtx)
	c.more.hit = false
	if clicked || folded {
		invalidateAfterTap(gtx)
	}

	fill := colRowRead
	if open {
		fill = colSurface
	}

	// Measure the content first: list children get unbounded height, so the
	// card background must follow the content instead of filling the axis.
	macro := op.Record(gtx.Ops)
	content := layout.Inset{Top: cardInsetTop, Bottom: 12, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !open {
			return c.collapsed(gtx, msg)
		}
		return c.expanded(gtx, msg)
	})
	call := macro.Stop()

	// The controls have had their say: a click the ⋮ consumed must not also
	// collapse or expand the card.
	if (clicked || folded) && !c.more.hit {
		a.expanded[msg.ID] = !open
	}

	w := gtx.Constraints.Max.X
	if w <= 0 || w > 1<<20 {
		w = content.Size.X
	}
	size := image.Pt(w, content.Size.Y)
	c.height = size.Y
	cardGtx := gtx
	cardGtx.Constraints = layout.Exact(size)
	strokeRect(cardGtx, fill, colBorder, 12, 1)
	call.Add(gtx.Ops)
	// A right click on one of the selectable runs opens the menu here: the menu
	// belongs to the App, which draws it below the card, and its operations
	// must not be recorded into the card's macro.
	if text, ok := c.selection(); ok {
		a.showTextMenu(text)
		invalidateAfterTap(gtx)
	}
	return layout.Dimensions{Size: size}
}

func (c *messageCard) collapsed(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	th := a.th
	return c.header.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return th.avatar(gtx, senderName(msg.From), 32) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(180)
				gtx.Constraints.Max.X = gtx.Dp(180)
				return th.text(gtx, senderName(msg.From), textMain, colText, font.Bold, 1)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, snippetText(msg.Snippet), textSecondary, colMuted, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, formatListTime(msg.Date), textMeta, colMuted, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.glyph(gtx, iconExpandMore, 18)
				})
			}),
		)
		// The whole header toggles the card, so it advertises itself.
		th.cursor(gtx, pointer.CursorPointer, dims.Size)
		return dims
	})
}

// expanded draws the open card. Its head is split on purpose: the sender name
// and address are selectable runs, and a click on them places a caret instead
// of folding the card. Folding therefore lives on the three non-text parts of
// the row, each with its own Clickable, so no click has to be classified after
// the fact.
func (c *messageCard) expanded(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	th := a.th
	// The head and its divider are measured as they are drawn: the find bar
	// locates the body below them, and the body itself is laid out after.
	c.headH = 0
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.headControl(gtx, th, &c.avatar, func(gtx layout.Context) layout.Dimensions {
						return th.avatar(gtx, senderName(msg.From), 32)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return column(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.name.Line(gtx, c.reader, msg.ID, senderName(msg.From), textMain, colText, font.Bold)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return c.addr.Line(gtx, c.reader, msg.ID, msg.From.Email, textMeta, colMuted, font.Normal)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.detailsToggle(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.headControl(gtx, th, &c.stamp, func(gtx layout.Context) layout.Dimensions {
						return th.text(gtx, formatFullTime(msg.Date), textMeta, colMuted, font.Normal, 1)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return c.headControl(gtx, th, &c.fold, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.glyph(gtx, iconExpandLess, 18)
						})
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 2) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.more.Layout(gtx, th) }),
			)
			c.headH += dims.Size.Y
			return dims
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !c.detailsOpen {
				return layout.Dimensions{}
			}
			// The details sit above the divider and belong to the head as far
			// as the find bar is concerned: its height has to reach headH, or
			// every hit in this card is scrolled to the wrong line.
			dims := c.details(gtx, msg)
			c.headH += dims.Size.Y
			return dims
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			dims := layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, hline)
			c.headH += dims.Size.Y
			return dims
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.body(gtx, msg)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(msg.Attachments) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.attachments(gtx, msg)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.reply.Layout(gtx, th) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.fwd.Layout(gtx, th) }),
				)
			})
		}),
	)
}

// headControl wraps one clickable part of the open head in its Clickable and
// advertises it with a hand cursor. The cursor belongs inside the clickable's
// own area, so it is declared after the content was recorded and replayed on
// top of it.
func (c *messageCard) headControl(gtx layout.Context, th *theme, click *widget.Clickable, content layout.Widget) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		macro := op.Record(gtx.Ops)
		dims := content(gtx)
		call := macro.Stop()
		th.cursor(gtx, pointer.CursorPointer, dims.Size)
		call.Add(gtx.Ops)
		return dims
	})
}

// detailsToggle folds the header details out. It is its own control, not one of
// the fold targets: a click here must reveal the headers, never collapse the
// card, and the label says which so the chevron is not the only hint.
func (c *messageCard) detailsToggle(gtx layout.Context, th *theme) layout.Dimensions {
	// The switch is read before its own layout, the way the composer's header
	// row does it: the frame that reports the click is the one that draws the
	// details, instead of showing the old state for another frame.
	if c.detailsClick.Clicked(gtx) {
		c.detailsOpen = !c.detailsOpen
		if !c.detailsOpen {
			// The runs go with the block. Keeping a selection in a run that is
			// no longer drawn would bring an unexplained highlight back the
			// next time the block opens.
			for i := range c.detailsSel {
				c.detailsSel[i].clear()
			}
		}
		invalidateAfterTap(gtx)
	}
	return c.headControl(gtx, th, &c.detailsClick, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, i18n.T("compose.headers"), textMeta, colMuted, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 2) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.detailsOpen {
					return th.glyph(gtx, iconExpandLess, 14)
				}
				return th.glyph(gtx, iconExpandMore, 14)
			}),
		)
	})
}

// headerField identifies one row of the header details. The rows are filtered
// by presence, so the selectable runs are keyed by this instead of by the row
// position they end up in.
type headerField int

const (
	headerFieldFrom headerField = iota
	headerFieldTo
	headerFieldCc
	headerFieldReplyTo
	headerFieldSubject
	headerFieldDate
	headerFieldMessageID
	headerFieldReferences
	headerFieldCount
)

// headerRow is one field of the card's header details.
type headerRow struct {
	field headerField
	label string
	value string
}

// detailsRows lists the headers a received message carries. A row without a
// value is left out: most messages have no Reply-To or Cc, and an empty row
// would suggest the header exists but could not be read.
func detailsRows(msg *mail.Message) []headerRow {
	rows := []headerRow{
		{headerFieldFrom, i18n.T("compose.from"), msg.From.String()},
		{headerFieldTo, i18n.T("compose.to"), mail.FormatAddressList(msg.To)},
		{headerFieldCc, i18n.T("compose.cc"), mail.FormatAddressList(msg.Cc)},
		{headerFieldReplyTo, i18n.T("reader.headerReplyTo"), mail.FormatAddressList(msg.ReplyTo)},
		{headerFieldSubject, i18n.T("compose.subject"), strings.TrimSpace(msg.Subject)},
		{headerFieldDate, i18n.T("compose.headerDate"), formatFullTime(msg.Date)},
		{headerFieldMessageID, i18n.T("compose.headerMessageID"), strings.TrimSpace(msg.MessageID)},
		{headerFieldReferences, i18n.T("compose.headerReferences"), formatReferences(msg.References)},
	}
	kept := rows[:0]
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		kept = append(kept, row)
	}
	return kept
}

// details draws the label/value block of the open card, the read-only
// counterpart of the composer's header details.
func (c *messageCard) details(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	th := c.reader.app.th
	rows := detailsRows(msg)
	children := make([]layout.FlexChild, 0, len(rows))
	for _, row := range rows {
		item := row
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 1, Bottom: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						w := gtx.Dp(headersLabelWidth)
						gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, 1<<16)}
						return th.text(gtx, item.label, textMeta, colMuted, font.Normal, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						// The values stay one step darker than the labels: a
						// Message-ID or a list of recipients has to stay
						// readable, and long values wrap instead of clipping.
						// They are the selectable half: a recipient or an ID is
						// what a reader copies out of the block.
						return c.detailsSel[item.field].Wrap(gtx, c.reader, msg.ID, item.value, textMeta, 0, colText, font.Normal)
					}),
				)
			})
		}))
	}
	return layout.Inset{Top: 6, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return column(gtx, children...)
	})
}

// body shows the text/plain part. HTML bodies are never rendered in-app: the
// card's "more" menu hands them to the browser instead. The whole body is
// drawn in the monospace face, the placeholder hint included: mail is laid out
// in columns, and there is no reason to switch faces inside the same block.
// The body is also the one surface that gets bodyLineHeight: the hint is a
// single line, so it keeps the default spacing.
//
// The find highlights are painted before the text, from the geometry measured
// when the text was laid out last; the fresh geometry is collected after the
// text is drawn, because Gio only reports glyph positions after layout.
func (c *messageCard) body(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	body := plainBody(msg)
	if body == "" {
		return c.bodyText.WrapMono(gtx, c.reader, msg.ID, i18n.T("reader.noPlainText"), textMain, 0, colMuted, font.Normal)
	}
	f := c.reader.find
	width := gtx.Constraints.Max.X
	if !f.active() {
		c.marks = findMarks{}
		return c.bodyText.WrapMono(gtx, c.reader, msg.ID, body, textMain, bodyLineHeight, colText, font.Normal)
	}
	if c.marks.valid(f.gen, width, body) {
		c.marks.paint(gtx, f.cur)
	}
	dims := c.bodyText.WrapMono(gtx, c.reader, msg.ID, body, textMain, bodyLineHeight, colText, font.Normal)
	if !c.marks.valid(f.gen, width, body) {
		c.collectFind(msg.ID, body, width)
	}
	return dims
}

// collectFind asks the laid-out Selectable where the hits of this message are.
// The rectangles are reused until the query, the text or the width changes.
func (c *messageCard) collectFind(msgID, body string, width int) {
	f := c.reader.find
	m := &c.marks
	m.gen, m.width, m.text = f.gen, width, body
	m.rects = m.rects[:0]
	// hitOf keeps every rectangle tied to the hit it came from; which one is
	// current is decided when painting and scrolling, not here.
	m.hitOf = m.hitOf[:0]
	base, hits := f.hitsFor(msgID)
	runeLen := utf8.RuneCountInString(body)
	var regions []widget.Region
	for i, hit := range hits {
		if hit.end > runeLen {
			// The hits predate a body that changed under them.
			continue
		}
		regions = c.bodyText.sel.Regions(hit.start, hit.end, regions)
		for _, region := range regions {
			if region.Bounds.Empty() {
				continue
			}
			m.rects = append(m.rects, region.Bounds)
			m.hitOf = append(m.hitOf, base+i)
		}
	}
}

func (c *messageCard) attachments(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	children := make([]layout.FlexChild, 0, len(msg.Attachments)*2)
	for _, att := range msg.Attachments {
		attachment := att
		btn := c.reader.attButton(msg, attachment)
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return btn.Layout(gtx, a.th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}
