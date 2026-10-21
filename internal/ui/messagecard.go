package ui

import (
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

// actions returns the reply, forward and browser buttons, building them once.
// The callbacks look the message up when they run: the reader replaces the
// message objects on every refresh, so a closure over the pointer seen during
// the first frame would act on an older copy.
func (c *messageCard) actions(a *App) (*pillButton, *pillButton, *pillButton) {
	if c.reply == nil {
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
		c.browser = newPillButton(i18n.T("reader.openInBrowser"), iconOpenInNew, false, func() {
			if m := c.message(a); m != nil {
				a.openHTMLInBrowser(m)
			}
		})
	}
	return c.reply, c.fwd, c.browser
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
	reader  *readerView
	id      string
	header  widget.Clickable
	reply   *pillButton
	fwd     *pillButton
	browser *pillButton

	// sel is the selection state of the plain-text part. It belongs to one
	// message, so it is replaced when the card shows a different one.
	sel    *widget.Selectable
	selFor string
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

	if c.header.Clicked(gtx) {
		a.expanded[msg.ID] = !open
		open = !open
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
				return th.text(gtx, senderName(msg.From), 14, colText, font.Bold, 1)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, snippetText(msg.Snippet), 13, colMuted, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, formatListTime(msg.Date), 12, colMuted, font.Normal, 1)
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
								return th.text(gtx, senderName(msg.From), 14, colText, font.Bold, 1)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return th.text(gtx, msg.From.Email, 12, colMuted, font.Normal, 1)
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.text(gtx, formatFullTime(msg.Date), 12, colMuted, font.Normal, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.glyph(gtx, iconExpandLess, 18)
						})
					}),
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
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.reply.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.fwd.Layout(gtx, th) }),
			}
			if strings.TrimSpace(msg.HTMLBody) != "" {
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.browser.Layout(gtx, th) }),
				)
			}
			return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
			})
		}),
	)
}

// body shows the text/plain part. HTML bodies are never rendered in-app: they
// are written to a temp file and handed to the user's browser instead.
func (c *messageCard) body(gtx layout.Context, msg *mail.Message) layout.Dimensions {
	a := c.reader.app
	body := strings.TrimSpace(msg.TextBody)
	sel := c.bodySelection(msg)
	if body == "" {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.th.selectableText(gtx, sel, i18n.T("reader.noPlainText"), 14, colMuted, font.Normal)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if strings.TrimSpace(msg.HTMLBody) == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return c.browser.Layout(gtx, a.th)
				})
			}),
		)
	}
	return a.th.selectableText(gtx, sel, body, 14, colText, font.Normal)
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
