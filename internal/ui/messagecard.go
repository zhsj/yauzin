package ui

import (
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
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

// menuItems lists the viewing actions of one message. The two previews need an
// HTML body, the source view works for every message.
func (c *messageCard) menuItems(a *App, m *mail.Message) []menuItem {
	items := make([]menuItem, 0, 3)
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
	return append(items, menuItem{Label: i18n.T("reader.menu.viewSource"), Icon: iconSource, OnTap: func() {
		if msg := c.message(a); msg != nil {
			a.openMessageSource(msg)
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
	header widget.Clickable
	reply  *pillButton
	fwd    *pillButton
	more   *iconButton

	// sel is the selection state of the plain-text part. It belongs to one
	// message, so it is replaced when the card shows a different one.
	sel    *widget.Selectable
	selFor string
}

// selectedText returns the text selected in the body of this card's message, or
// "" when nothing is selected. A selection belonging to another message is
// ignored: the card drops it when it shows a different one.
func (c *messageCard) selectedText() string {
	if c == nil || c.sel == nil || c.selFor != c.id || c.sel.SelectionLen() == 0 {
		return ""
	}
	return c.sel.SelectedText()
}

// bodySelection returns the Selectable for msg, dropping any selection left
// over from the previous message shown in this card.
func (c *messageCard) bodySelection(msg *mail.Message) *widget.Selectable {
	if c.sel == nil || c.selFor != msg.ID {
		c.sel = &widget.Selectable{}
		c.selFor = msg.ID
	}
	return c.sel
}

func (c *messageCard) Layout(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	if a.expanded == nil {
		a.expanded = map[string]bool{}
	}
	open := a.expanded[msg.ID]

	c.actions(a)
	// Pointer events reach every handler under them, so a click on the ⋮ is
	// also delivered to the header. The toggle therefore waits until the button
	// has reported whether it consumed the click.
	clicked := c.header.Clicked(gtx)
	c.more.hit = false
	if clicked {
		invalidateAfterTap(gtx)
	}

	fill := colRowRead
	if open {
		fill = colSurface
	}

	// Measure the content first: list children get unbounded height, so the
	// card background must follow the content instead of filling the axis.
	macro := op.Record(gtx.Ops)
	content := layout.Inset{Top: 10, Bottom: 12, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if !open {
			return c.collapsed(gtx, msg)
		}
		return c.expanded(gtx, msg)
	})
	call := macro.Stop()

	// The controls have had their say: a click the ⋮ consumed must not also
	// collapse or expand the card.
	if clicked && !c.more.hit {
		a.expanded[msg.ID] = !open
	}

	w := gtx.Constraints.Max.X
	if w <= 0 || w > 1<<20 {
		w = content.Size.X
	}
	size := image.Pt(w, content.Size.Y)
	cardGtx := gtx
	cardGtx.Constraints = layout.Exact(size)
	strokeRect(cardGtx, fill, colBorder, 12, 1)
	call.Add(gtx.Ops)
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

func (c *messageCard) expanded(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	th := a.th
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.header.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				dims := layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return th.avatar(gtx, senderName(msg.From), 32) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return column(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return th.text(gtx, senderName(msg.From), textMain, colText, font.Bold, 1)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return th.text(gtx, msg.From.Email, textMeta, colMuted, font.Normal, 1)
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.text(gtx, formatFullTime(msg.Date), textMeta, colMuted, font.Normal, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.glyph(gtx, iconExpandLess, 18)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 2) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.more.Layout(gtx, th) }),
				)
				// The whole header toggles the card, so it advertises itself.
				th.cursor(gtx, pointer.CursorPointer, dims.Size)
				return dims
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 8, Bottom: 8}.Layout(gtx, hline)
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

// body shows the text/plain part. HTML bodies are never rendered in-app: the
// card's "more" menu hands them to the browser instead. The body is the one
// surface that gets bodyLineHeight: a placeholder hint is a single line, so it
// keeps the default spacing.
func (c *messageCard) body(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	th := c.reader.app.th
	body := strings.TrimSpace(msg.TextBody)
	sel := c.bodySelection(msg)
	if body == "" {
		return th.selectableText(gtx, sel, i18n.T("reader.noPlainText"), textMain, 0, colMuted, font.Normal)
	}
	return th.selectableText(gtx, sel, body, textMain, bodyLineHeight, colText, font.Normal)
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
