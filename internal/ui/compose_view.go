package ui

import (
	"image"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// composeView is Gmail's floating compose window, docked bottom right.
type composeView struct {
	app  *App
	mode composeMode
	msg  *mail.Message

	to      widget.Editor
	cc      widget.Editor
	subject widget.Editor
	body    widget.Editor

	attachBtn *iconButton
	minBtn    *iconButton
	closeBtn  *iconButton
	sendBtn   *pillButton

	attachments []mail.DraftAttachment
	chips       []*attachmentChip

	minimized bool
	sending   bool
	errMsg    string
	focusInit bool

	// discardPending is set when the draft was dismissed while a send was in
	// flight, so the staged files outlive the goroutine that reads them.
	discardPending bool

	// card consumes clicks on the compose window itself so they never reach the
	// mailbox behind it. Only the card area is registered, so the rest of the
	// window stays interactive.
	card widget.Clickable
}

func newComposeView(a *App, mode composeMode, msg *mail.Message, replySelection string) *composeView {
	c := &composeView{app: a, mode: mode, msg: msg}

	c.to.SingleLine = true
	c.cc.SingleLine = true
	c.subject.SingleLine = true

	if msg != nil && mode == composeReply {
		c.to.SetText(msg.From.String())
		c.subject.SetText(replySubject(msg.Subject))
		c.body.SetText(replyQuote(msg, replySelection))
	}
	if msg != nil && mode == composeForward {
		c.subject.SetText(forwardSubject(msg.Subject))
		c.body.SetText(forwardQuote(msg))
	}

	c.attachBtn = newIconButton(iconAttach, 36, func() { a.pickAttachment(c) })
	c.minBtn = newIconButton(iconMinimize, 32, func() { c.minimized = !c.minimized })
	c.closeBtn = newIconButton(iconClose, 32, a.hideOverlay)
	c.sendBtn = newPillButton(i18n.T("compose.send"), iconSendWhite, true, c.send)
	c.sendBtn.minW = 96
	return c
}

func (c *composeView) title() string {
	switch c.mode {
	case composeReply:
		return i18n.T("compose.reply")
	case composeForward:
		return i18n.T("compose.forward")
	default:
		return i18n.T("compose.new")
	}
}

func (c *composeView) Layout(gtx layout.Context, a *App) layout.Dimensions {
	if !c.focusInit {
		c.focusInit = true
		gtx.Execute(key.FocusCmd{Tag: &c.to})
	}
	// Ctrl+Enter sends from any field. The filter requires the modifier, so a
	// plain Enter still inserts a newline in the body.
	if pressedWith(gtx, key.NameEnter, key.ModShortcut) || pressedWith(gtx, key.NameReturn, key.ModShortcut) {
		c.send()
	}

	// The minimum is applied inside the viewport, not after clamping to it:
	// otherwise a window smaller than the minimum produced a panel wider than
	// its constraints and a negative offset.
	want := image.Pt(gtx.Dp(480), gtx.Dp(540))
	least := image.Pt(320, 160)
	if c.minimized {
		// A minimized window keeps its width but shrinks to the header row, so
		// the minimum collapses with it.
		want.Y = gtx.Dp(48)
		least.Y = gtx.Dp(48)
	}
	size := panelSize(gtx, want, least)
	origin := image.Pt(
		max(gtx.Constraints.Max.X-size.X-24, 0),
		max(gtx.Constraints.Max.Y-size.Y-24, 0),
	)

	cardGtx, pop := placeCard(gtx, size, origin)
	defer pop()

	// Swallow clicks on the card itself; it is registered before the content so
	// the buttons and fields inside stay on top.
	c.card.Layout(cardGtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: size}
	})
	drawCard(cardGtx, size, 8)

	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.header(gtx) }),
	}
	if !c.minimized {
		rows = append(rows,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.fieldRow(gtx, &c.to, i18n.T("compose.to")) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.fieldRow(gtx, &c.cc, i18n.T("compose.cc")) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldRow(gtx, &c.subject, i18n.T("compose.subject"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return c.bodyRow(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.chipRow(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.footer(gtx) }),
		)
	}
	layout.Flex{Axis: layout.Vertical}.Layout(cardGtx, rows...)
	return layout.Dimensions{Size: gtx.Constraints.Max}
}

// fieldRow lays out one single-line header field (to, cc or subject).
func (c *composeView) fieldRow(gtx layout.Context, ed *widget.Editor, placeholder string) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 10, Top: 2, Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.app.th.textField(gtx, ed, placeholder, 14)
	})
}

// separator draws the hairline between two header fields.
func (c *composeView) separator(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 16}.Layout(gtx, hline)
}

// bodyRow lays out the message body, the one row that takes the leftover height.
func (c *composeView) bodyRow(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 8, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.app.th.textField(gtx, &c.body, i18n.T("compose.body"), 14)
	})
}

func (c *composeView) header(gtx layout.Context) layout.Dimensions {
	a := c.app
	th := a.th
	return layout.Inset{Left: 16, Right: 8, Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, c.title(), 14, colText, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.minBtn.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.closeBtn.Layout(gtx, th) }),
		)
	})
}

func (c *composeView) footer(gtx layout.Context) layout.Dimensions {
	a := c.app
	th := a.th
	if c.sending {
		c.sendBtn.setText(i18n.T("compose.sending"))
	} else {
		c.sendBtn.setText(i18n.T("compose.send"))
	}
	return layout.Inset{Left: 12, Right: 12, Top: 8, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.attachBtn.Layout(gtx, th) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if c.errMsg == "" {
						return layout.Dimensions{}
					}
					return th.text(gtx, c.errMsg, 12, colDanger, font.Normal, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.sendBtn.Layout(gtx, th) }),
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

func (c *composeView) chipRow(gtx layout.Context) layout.Dimensions {
	if len(c.chips) == 0 {
		return layout.Dimensions{}
	}
	rows := make([]layout.FlexChild, 0, (len(c.chips)+1)/2)
	for i := 0; i < len(c.chips); i += 2 {
		start := i
		end := i + 2
		if end > len(c.chips) {
			end = len(c.chips)
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, (end-start)*2)
			for _, chip := range c.chips[start:end] {
				ch := chip
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ch.Layout(gtx, c.app.th) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
				)
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
		}))
	}
	return layout.Inset{Left: 12, Right: 12, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

type attachmentChip struct {
	name   string
	remove *iconButton
}

func newAttachmentChip(name string, onRemove func()) *attachmentChip {
	return &attachmentChip{name: name, remove: newIconButton(iconClose, 22, onRemove)}
}

func (c *attachmentChip) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	macro := op.Record(gtx.Ops)
	content := layout.Inset{Top: 3, Bottom: 3, Left: 10, Right: 4}.Layout(measure, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.glyph(gtx, iconFile, 14)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = gtx.Dp(120)
				return th.text(gtx, c.name, 12, colMuted, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 4) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.remove.Layout(gtx, th) }),
		)
	})
	call := macro.Stop()

	size := content.Size
	r := size.Y / 2
	rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: r, NE: r, SE: r, SW: r}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface2)
	stack.Pop()
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}
