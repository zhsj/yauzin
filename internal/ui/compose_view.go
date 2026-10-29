package ui

import (
	"image"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mailto"
)

// composeView is Gmail's floating compose window, docked bottom right.
type composeView struct {
	app  *App
	mode composeMode
	msg  *mail.Message

	to      widget.Editor
	cc      widget.Editor
	bcc     widget.Editor
	subject widget.Editor
	body    widget.Editor

	// fromID is the identity the draft goes out as: the mailbox the composer
	// belongs to, or one of the stored sending accounts. senders and fromRows
	// are snapshotted when the composer opens, because the clickables behind
	// them must stay stable while the layer is laid out.
	fromID    string
	senders   []composeSender
	fromRows  []*settingsRow
	fromOpen  bool
	fromClick widget.Clickable
	// date and messageID are fixed when the composer opens, so the header
	// details show exactly what the message carries. The ID is rebuilt when
	// another sender puts another domain on the wire.
	date      time.Time
	messageID string
	// inReplyTo and references thread a reply onto the message it answers. A
	// new message and a forward carry neither.
	inReplyTo  string
	references []string
	// headersOpen folds out the read-only header details below the subject.
	headersOpen  bool
	headersClick widget.Clickable
	// mailboxID is the account the draft belongs to, fixed when the composer
	// opens: switching mailboxes mid-draft must not redirect the send.
	mailboxID string
	// bccAuto is the address the From switch put into the Bcc field, so
	// switching back clears only what the switch itself wrote.
	bccAuto string

	attachBtn *iconButton
	maxBtn    *iconButton
	minBtn    *iconButton
	closeBtn  *iconButton
	sendBtn   *pillButton

	attachments []mail.DraftAttachment
	chips       []*attachmentChip

	// maximized fills the window up to composeMargin; it and minimized are
	// mutually exclusive, so the header never offers a state it cannot leave.
	maximized bool
	minimized bool
	sending   bool
	errMsg    string
	focusInit bool

	// card consumes clicks on the compose window itself so they never reach the
	// mailbox behind it. Only the card area is registered, so the rest of the
	// window stays interactive.
	card widget.Clickable
}

func newComposeView(a *App, mode composeMode, msg *mail.Message, replySelection string) *composeView {
	c := &composeView{app: a, mode: mode, msg: msg}

	c.to.SingleLine = true
	c.cc.SingleLine = true
	c.bcc.SingleLine = true
	c.subject.SingleLine = true

	c.mailboxID = a.activeAcc
	c.fromID = a.activeAcc
	c.senders = a.composeSenders()
	for _, sender := range c.senders {
		item := sender
		row := &settingsRow{
			title:  item.name,
			active: item.id == c.fromID,
			indent: true,
			onTap:  func() { c.setSender(item.id) },
		}
		// The address is the subtitle only when a name already fills the title.
		if row.title == "" {
			row.title = item.email
		} else {
			row.subtitle = item.email
		}
		c.fromRows = append(c.fromRows, row)
	}

	c.date = time.Now()
	c.refreshIdentity()

	if msg != nil && (mode == composeReply || mode == composeReplyAll) {
		// The identity rows were built above, so the caller's own addresses are
		// known here: a reply-all must not put the user back on the list.
		self := make([]mail.Address, len(c.senders))
		for i, sender := range c.senders {
			self[i] = mail.Address{Email: sender.email}
		}
		to, cc := mail.ReplyRecipients(msg, self, mode == composeReplyAll)
		c.to.SetText(mail.FormatAddressList(to))
		c.cc.SetText(mail.FormatAddressList(cc))
		c.subject.SetText(replySubject(msg.Subject))
		quote := replyQuote(msg, replySelection)
		c.body.SetText(quote)
		// SetText leaves the caret at the beginning; a reply is written below
		// the quote, so move it past the trailing blank line.
		end := utf8.RuneCountInString(quote)
		c.body.SetCaret(end, end)
		// Without the parent's Message-ID there is nothing to thread onto, so
		// the reply goes out as a message of its own instead of pointing at a
		// guessed ID.
		if inReplyTo, ok := mail.NormalizeMessageID(msg.MessageID); ok {
			c.inReplyTo = inReplyTo
			c.references = mail.ReplyReferences(msg)
		}
	}
	if msg != nil && mode == composeForward {
		c.subject.SetText(forwardSubject(msg.Subject))
		c.body.SetText(forwardQuote(msg))
	}

	c.attachBtn = newIconButton(iconAttach, 36, func() { a.pickAttachment(c) })
	c.maxBtn = newIconButton(iconOpenInFull, 32, c.toggleMaximized)
	c.minBtn = newIconButton(iconMinimize, 32, c.toggleMinimized)
	c.closeBtn = newIconButton(iconClose, 32, a.hideOverlay)
	c.sendBtn = newPillButton(i18n.T("compose.send"), iconSendWhite, true, c.send)
	c.sendBtn.minW = 96
	return c
}

// newComposeFromMailto fills a new-message composer from a mailto URI. It is a
// constructor of its own so newComposeView keeps its shape for the reply,
// forward and plain new-message callers.
func newComposeFromMailto(a *App, m mailto.Message) *composeView {
	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText(m.To)
	c.cc.SetText(m.Cc)
	c.bcc.SetText(m.Bcc)
	c.subject.SetText(m.Subject)
	c.body.SetText(m.Body)
	if m.Body != "" {
		// SetText leaves the caret at the beginning; the link's text is a
		// starting point, so the caret goes past it.
		end := utf8.RuneCountInString(m.Body)
		c.body.SetCaret(end, end)
	}
	// in-reply-to becomes the threading headers the composer owns, and nothing
	// else. A value NormalizeMessageID rejects leaves the draft a message of
	// its own, the way a reply to a parent without a Message-ID does.
	if id, ok := mail.NormalizeMessageID(m.InReplyTo); ok {
		c.inReplyTo = id
		c.references = []string{id}
	}
	return c
}

// toggleMaximized expands the composer over the window, or drops back to the
// floating window. Expanding and minimizing are opposite ends of one axis, so
// leaving the collapsed state clears it.
func (c *composeView) toggleMaximized() {
	c.maximized = !c.maximized
	if c.maximized {
		c.minimized = false
	}
}

// toggleMinimized collapses the composer to its header row, or restores the
// previous size; it wins over the maximized state in the same way.
func (c *composeView) toggleMinimized() {
	c.minimized = !c.minimized
	if c.minimized {
		c.maximized = false
	}
}

func (c *composeView) title() string {
	switch c.mode {
	case composeReply:
		return i18n.T("compose.reply")
	case composeReplyAll:
		return i18n.T("compose.replyAll")
	case composeForward:
		return i18n.T("compose.forward")
	default:
		return i18n.T("compose.new")
	}
}

const (
	// composeMargin is the gap between the composer and the window edge, in
	// both the floating and the maximized state.
	composeMargin = unit.Dp(24)
	// composeLabelWidth is the width of the fixed label column of the header
	// fields; it fits the longest translated label at 14sp.
	composeLabelWidth = unit.Dp(56)
)

// composeArea is the region an expanded composer may fill: the window below the
// header and right of the sidebar. Overlays are laid out against the whole
// window while the header is drawn after them, so an expansion that ignored
// either edge would slide under the title bar and hide its own buttons.
func composeArea(gtx layout.Context, a *App) image.Rectangle {
	left := 0
	if !a.sidebarCollapsed {
		left = gtx.Dp(sidebarWidth)
	}
	topLeft := image.Pt(
		min(left, gtx.Constraints.Max.X),
		min(gtx.Dp(a.headerHeight()), gtx.Constraints.Max.Y),
	)
	return image.Rectangle{Min: topLeft, Max: gtx.Constraints.Max}
}

// panel reports the size and origin of the composer card for the current
// state. The floating and minimized windows hug the bottom right corner of the
// window; the expanded one fills composeArea up to composeMargin on every side.
func (c *composeView) panel(gtx layout.Context, a *App) (size, origin image.Point) {
	margin := gtx.Dp(composeMargin)
	window := gtx.Constraints.Max
	if c.maximized && !c.minimized {
		space := composeArea(gtx, a).Inset(margin)
		size = image.Pt(max(space.Dx(), 1), max(space.Dy(), 1))
		// A window too small for the area still has to keep the card on screen.
		return size, image.Pt(
			max(min(space.Min.X, window.X-size.X), 0),
			max(min(space.Min.Y, window.Y-size.Y), 0),
		)
	}
	var want, least image.Point
	if c.minimized {
		// A minimized window keeps its width but shrinks to the header row, so
		// the minimum collapses with it.
		want = image.Pt(gtx.Dp(480), gtx.Dp(48))
		least = image.Pt(gtx.Dp(320), gtx.Dp(48))
	} else {
		want = image.Pt(gtx.Dp(480), gtx.Dp(540))
		least = image.Pt(gtx.Dp(320), gtx.Dp(160))
	}
	size = panelSize(gtx, want, least)
	origin = image.Pt(
		max(window.X-size.X-margin, 0),
		max(window.Y-size.Y-margin, 0),
	)
	return size, origin
}

func (c *composeView) Layout(gtx layout.Context, a *App) layout.Dimensions {
	if !c.focusInit {
		c.focusInit = true
		// A draft whose recipient is already set — a reply, or a message filled
		// from a mailto URI — is written in the body, so the body takes the
		// keyboard right away; an empty composer starts with the recipient.
		target := &c.to
		if strings.TrimSpace(c.to.Text()) != "" {
			target = &c.body
			// The focus command lands after this frame, so the caret only shows
			// once one more frame draws the now focused body.
			gtx.Execute(op.InvalidateCmd{})
		}
		gtx.Execute(key.FocusCmd{Tag: target})
	}

	size, origin := c.panel(gtx, a)

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
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.senderRow(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldRow(gtx, &c.to, i18n.T("compose.to"), "")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldRow(gtx, &c.cc, i18n.T("compose.cc"), "")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldRow(gtx, &c.bcc, i18n.T("compose.bcc"), "")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldRow(gtx, &c.subject, i18n.T("compose.subject"), "")
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.headersToggleRow(gtx) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !c.headersOpen {
					return layout.Dimensions{}
				}
				return c.headerDetails(gtx)
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

// composeSender is one candidate of the From row: the mailbox the draft belongs
// to, or a stored sending account.
type composeSender struct {
	id    string
	name  string
	email string
	// entry is the stored sending account, nil for the mailbox itself.
	entry *accounts.Entry
	// bccCopy asks for the mailbox address in the Bcc field when this sender is
	// picked, so a message sent through another server still leaves a copy
	// behind.
	bccCopy bool
}

// composeSenders returns the identities a draft can go out as: the mailbox that
// owns it first, then every stored sending account.
func (a *App) composeSenders() []composeSender {
	var out []composeSender
	if acc, ok := a.accountByID(a.activeAcc); ok {
		out = append(out, composeSender{id: acc.ID, name: acc.DisplayName(), email: acc.Email})
	}
	for _, entry := range a.smtpEntries() {
		item := entry
		out = append(out, composeSender{
			id:      item.ID,
			name:    item.Name,
			email:   item.Email,
			entry:   &item,
			bccCopy: item.SMTP != nil && item.SMTP.BccToAccount,
		})
	}
	return out
}

// accountByID looks up a mailbox by its ID.
func (a *App) accountByID(id string) (mail.Account, bool) {
	for _, acc := range a.accounts {
		if acc.ID == id {
			return acc, true
		}
	}
	return mail.Account{}, false
}

// currentSender returns the identity the draft is set to go out as.
func (c *composeView) currentSender() *composeSender {
	for i := range c.senders {
		if c.senders[i].id == c.fromID {
			return &c.senders[i]
		}
	}
	return nil
}

// refreshIdentity rebuilds the Message-ID after the sender changed, so the
// right hand side of the ID stays in the domain the message is sent from.
func (c *composeView) refreshIdentity() {
	email := ""
	if sender := c.currentSender(); sender != nil {
		email = sender.email
	}
	c.messageID = mail.NewMessageID(email)
}

// senderLabel is the collapsed From line: the name next to the address, or just
// the address when there is no name to add.
func (c *composeView) senderLabel() string {
	sender := c.currentSender()
	if sender == nil {
		return ""
	}
	if sender.name == "" || sender.name == sender.email {
		return sender.email
	}
	return sender.name + " <" + sender.email + ">"
}

// mailboxEmail is the address of the mailbox the draft belongs to: the default
// blind-copy target of a sending account.
func (c *composeView) mailboxEmail() string {
	if acc, ok := c.app.accountByID(c.mailboxID); ok {
		return acc.Email
	}
	return ""
}

// setSender switches the identity and keeps the blind copy in step: a sending
// account that asks for a copy fills the Bcc field with the mailbox address,
// and switching away clears only what was filled in automatically.
func (c *composeView) setSender(id string) {
	c.fromOpen = false
	if id == c.fromID {
		return
	}
	c.fromID = id
	c.refreshIdentity()
	for i, sender := range c.senders {
		if i < len(c.fromRows) {
			c.fromRows[i].active = sender.id == id
		}
	}

	want := ""
	if sender := c.currentSender(); sender != nil && sender.bccCopy {
		want = c.mailboxEmail()
	}
	current := strings.TrimSpace(c.bcc.Text())
	switch {
	case want != "" && (current == "" || current == c.bccAuto):
		c.bcc.SetText(want)
		c.bccAuto = want
	case want == "" && c.bccAuto != "" && current == c.bccAuto:
		c.bcc.SetText("")
		c.bccAuto = ""
	}
}

// senderRow draws the From line, the one header field that is a choice instead
// of an editor. It stays hidden until a sending account exists, so a plain
// single-account setup keeps its familiar shape. The candidates expand inside
// the window: a menu would replace this layer and throw the draft away.
func (c *composeView) senderRow(gtx layout.Context) layout.Dimensions {
	if len(c.senders) < 2 {
		return layout.Dimensions{}
	}
	th := c.app.th
	if c.fromClick.Clicked(gtx) {
		c.fromOpen = !c.fromOpen
		invalidateAfterTap(gtx)
	}
	return column(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return c.fromClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				// The whole line is a control, so it has to take the hand
				// cursor. Its height is only known once the label and the
				// address are shaped, hence the record.
				measure := gtx
				measure.Constraints.Min.Y = 0
				measure.Constraints.Max.Y = 1 << 16
				macro := op.Record(gtx.Ops)
				content := c.fromLine(measure, th)
				call := macro.Stop()
				size := image.Pt(max(gtx.Constraints.Max.X, content.Size.X), content.Size.Y)
				th.cursor(gtx, pointer.CursorPointer, size)
				call.Add(gtx.Ops)
				return layout.Dimensions{Size: size}
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !c.fromOpen {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: 8, Right: 8, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.senderList(gtx, c.fromRows, th)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.separator(gtx) }),
	)
}

// fromLine is the collapsed From line: its label, the chosen identity and the
// arrow that opens the candidate list.
func (c *composeView) fromLine(gtx layout.Context, th *theme) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 10, Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.fieldLabel(gtx, i18n.T("compose.from"))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, c.senderLabel(), textMain, colText, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.fromOpen {
					return th.glyph(gtx, iconExpandLess, 16)
				}
				return th.glyph(gtx, iconExpandMore, 16)
			}),
		)
	})
}

// senderList lays out the From candidates in a tinted, rounded panel with a
// hairline between entries. The list expands inside the composer rather than in
// a menu, so the panel is drawn by hand: the rows are recorded first because
// the background has to be painted behind them.
func (c *composeView) senderList(gtx layout.Context, list []*settingsRow, th *theme) layout.Dimensions {
	const pad = unit.Dp(4)
	measure := gtx
	measure.Constraints.Min.Y = 0
	measure.Constraints.Max.Y = 1 << 16
	macro := op.Record(gtx.Ops)
	content := layout.Inset{Top: pad, Bottom: pad}.Layout(measure, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(list)*2)
		for i, row := range list {
			item := row
			if i > 0 {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 12, Right: 12}.Layout(gtx, hline)
				}))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return item.Layout(gtx, th)
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	call := macro.Stop()

	size := content.Size
	r := gtx.Dp(8)
	rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: r, NE: r, SE: r, SW: r}
	stack := rr.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface2)
	stack.Pop()
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}

// fieldRow lays out one header field as a fixed label column and the editor
// beside it. The label stays visible once the field has text, which is how a
// filled To or Cc line stays readable; hint is the placeholder inside the
// editor and is empty whenever the label already names the field.
func (c *composeView) fieldRow(gtx layout.Context, ed *widget.Editor, label, hint string) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 10, Top: 2, Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.fieldLabel(gtx, label) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return c.app.th.textField(gtx, ed, hint, textMain)
			}),
		)
	})
}

// fieldLabel draws one cell of the label column. The column has a fixed width
// so From, To, Cc, Bcc and Subject line up, and a long translation truncates
// instead of pushing its editor off a narrow window.
func (c *composeView) fieldLabel(gtx layout.Context, label string) layout.Dimensions {
	w := gtx.Dp(composeLabelWidth)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, 1<<16)}
	return c.app.th.text(gtx, label, textMain, colMuted, font.Normal, 1)
}

// headersLabelWidth is the label column of the header details. It is wider than
// composeLabelWidth because names such as References do not fit there.
const headersLabelWidth = unit.Dp(96)

// headersToggleRow folds the header details out below the subject. The whole row
// is the control, so it advertises itself.
func (c *composeView) headersToggleRow(gtx layout.Context) layout.Dimensions {
	th := c.app.th
	if c.headersClick.Clicked(gtx) {
		c.headersOpen = !c.headersOpen
		invalidateAfterTap(gtx)
	}
	return c.headersClick.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := layout.Inset{Left: 16, Right: 10, Top: 2, Bottom: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, i18n.T("compose.headers"), textMeta, colMuted, font.Normal, 1)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if c.headersOpen {
						return th.glyph(gtx, iconExpandLess, 14)
					}
					return th.glyph(gtx, iconExpandMore, 14)
				}),
			)
		})
		th.cursor(gtx, pointer.CursorPointer, dims.Size)
		return dims
	})
}

// headerDetails lists the headers the draft carries, read-only. A row without a
// value is left out: a new message and a forward have no thread headers, and an
// empty row would suggest otherwise.
func (c *composeView) headerDetails(gtx layout.Context) layout.Dimensions {
	rows := []struct{ label, value string }{
		{i18n.T("compose.headerDate"), c.date.Format(time.RFC1123Z)},
		{i18n.T("compose.headerMessageID"), c.messageID},
		{i18n.T("compose.headerInReplyTo"), c.inReplyTo},
		{i18n.T("compose.headerReferences"), c.referencesText()},
	}
	children := make([]layout.FlexChild, 0, len(rows))
	for _, row := range rows {
		if row.value == "" {
			continue
		}
		item := row
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 16, Right: 10, Top: 1, Bottom: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.headerLabel(gtx, item.label) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return c.app.th.wrappedText(gtx, item.value, textMeta, colMuted, font.Normal)
					}),
				)
			})
		}))
	}
	return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return column(gtx, children...)
	})
}

// headerLabel draws one label of the details block in its own column, so the
// values line up the way the editable fields do.
func (c *composeView) headerLabel(gtx layout.Context, label string) layout.Dimensions {
	w := gtx.Dp(headersLabelWidth)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, 1<<16)}
	return c.app.th.text(gtx, label, textMeta, colMuted, font.Normal, 1)
}

// referencesText is the visible form of the References chain. A long chain is
// cut to its last entries so the details stay a few lines; the message itself
// keeps every ID.
func (c *composeView) referencesText() string {
	const shown = 8
	refs := c.references
	if len(refs) == 0 {
		return ""
	}
	prefix := ""
	if len(refs) > shown {
		refs = refs[len(refs)-shown:]
		prefix = "… "
	}
	return prefix + strings.Join(refs, " ")
}

// separator draws the hairline between two header fields.
func (c *composeView) separator(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 16}.Layout(gtx, hline)
}

// bodyRow lays out the message body, the one row that takes the leftover height.
func (c *composeView) bodyRow(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 8, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return c.app.th.textField(gtx, &c.body, i18n.T("compose.body"), textMain)
	})
}

func (c *composeView) header(gtx layout.Context) layout.Dimensions {
	a := c.app
	th := a.th
	// The glyph alone says where a click leads: out of the corner while
	// floating, back into it while expanded. The button carries no hover label.
	if c.maximized {
		c.maxBtn.setIcon(iconCloseFull)
	} else {
		c.maxBtn.setIcon(iconOpenInFull)
	}
	return layout.Inset{Left: 16, Right: 8, Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, c.title(), textMain, colText, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.maxBtn.Layout(gtx, th) }),
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
					return th.text(gtx, c.errMsg, textMeta, colDanger, font.Normal, 1)
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
				return th.text(gtx, c.name, textMeta, colMuted, font.Normal, 1)
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
