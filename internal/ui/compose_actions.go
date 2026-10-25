package ui

import (
	"context"
	"errors"
	"os"

	"io/fs"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func (c *composeView) send() {
	if c.sending {
		return
	}
	to := mail.ParseAddressList(c.to.Text())
	if len(to) == 0 {
		c.errMsg = i18n.T("compose.needRecipient")
		return
	}
	sender := c.currentSender()
	if sender == nil {
		c.errMsg = i18n.T("compose.accountUnavailable")
		return
	}
	// Snapshot the draft: the fields and the attachment list stay editable while
	// the send runs, and the backend must not see those edits.
	draft := mail.Draft{
		From:        mail.Address{Name: sender.name, Email: sender.email},
		To:          append([]mail.Address{}, to...),
		Cc:          append([]mail.Address{}, mail.ParseAddressList(c.cc.Text())...),
		Bcc:         append([]mail.Address{}, mail.ParseAddressList(c.bcc.Text())...),
		Subject:     c.subject.Text(),
		Body:        c.body.Text(),
		Attachments: append([]mail.DraftAttachment{}, c.attachments...),
	}

	// The identity decides the transport: the mailbox sends through its own
	// backend, a sending account through its SMTP server.
	var deliver func(ctx context.Context) error
	if sender.entry == nil {
		svc := c.app.services[c.mailboxID]
		if svc == nil {
			c.errMsg = i18n.T("compose.accountUnavailable")
			return
		}
		deliver = func(ctx context.Context) error { return svc.Send(ctx, draft) }
	} else {
		entry := *sender.entry
		deliver = func(ctx context.Context) error { return c.app.sendSMTP(ctx, entry, draft) }
	}

	c.sending = true
	c.errMsg = ""
	// The account the draft belongs to. Sending may outlive a switch, so the
	// completion still has to reset this composer, but only the original
	// mailbox is refreshed afterwards.
	accID := c.app.activeAcc
	finish := func() {
		c.sending = false
		if c.discardPending {
			c.discardPending = false
			c.removeStaged()
		}
	}
	c.app.async("send", timeoutSend, nil,
		deliver,
		func() {
			finish()
			// Only close the layer this window owns: the user may have opened
			// settings or another draft in the meantime.
			if c.app.overlay == c {
				c.app.hideOverlay()
			}
			c.app.notify(i18n.T("toast.sent"), false)
			if accID != c.app.activeAcc {
				return
			}
			c.app.reloadFolders()
			if c.app.activeFolder == "sent" {
				c.app.goToPage(1)
			}
		},
		func(err error) {
			finish()
			msg := i18n.T("compose.sendFailed", map[string]string{"Error": errorText(err)})
			if c.app.overlay == c {
				c.errMsg = msg
				return
			}
			// The composer was closed while sending, so its footer can no
			// longer show the error.
			c.app.notify(msg, true)
		})
}

// addAttachment is called on the UI goroutine after the file picker returns.
func (c *composeView) addAttachment(att mail.DraftAttachment) {
	for _, existing := range c.attachments {
		if existing.Path == att.Path {
			// The caller has already staged a copy; it will never be used.
			_ = os.Remove(att.Path)
			c.errMsg = i18n.T("compose.duplicateAttachment")
			return
		}
	}
	c.attachments = append(c.attachments, att)
	c.refreshChips()
}

func (c *composeView) removeAttachment(index int) {
	if index < 0 || index >= len(c.attachments) {
		return
	}
	att := c.attachments[index]
	// Build a new slice instead of splicing in place: the send goroutine may
	// still hold the previous one.
	rest := make([]mail.DraftAttachment, 0, len(c.attachments)-1)
	rest = append(rest, c.attachments[:index]...)
	rest = append(rest, c.attachments[index+1:]...)
	c.attachments = rest
	if att.Path != "" {
		if err := os.Remove(att.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logErr("remove staged attachment", err)
		}
	}
	c.refreshChips()
}

// discard drops the staged attachment files of a draft that is going away. It
// is safe to call twice: the slice is cleared, so removed paths are not
// removed again.
func (c *composeView) discard() {
	if c == nil {
		return
	}
	if c.sending {
		// The send goroutine may still be reading the files; the completion
		// callback removes them instead.
		c.discardPending = true
		return
	}
	c.removeStaged()
}

// removeStaged deletes the staged files of every attachment the draft still
// holds.
func (c *composeView) removeStaged() {
	for _, att := range c.attachments {
		if att.Path == "" {
			continue
		}
		if err := os.Remove(att.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logErr("remove staged attachment", err)
		}
	}
	c.attachments = nil
	c.chips = nil
}

func (c *composeView) refreshChips() {
	c.chips = c.chips[:0]
	for i, att := range c.attachments {
		index := i
		c.chips = append(c.chips, newAttachmentChip(att.Name, func() { c.removeAttachment(index) }))
	}
}
