package ui

import (
	"context"
	"errors"
	"strings"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// snapshot is the draft as the composer currently shows it. Both the send and
// the signing step work on one, so the signature covers exactly the content the
// message will carry.
func (c *composeView) snapshot() mail.Draft {
	sender := c.currentSender()
	draft := mail.Draft{
		To:          append([]mail.Address{}, mail.ParseAddressList(c.to.Text())...),
		Cc:          append([]mail.Address{}, mail.ParseAddressList(c.cc.Text())...),
		Bcc:         append([]mail.Address{}, mail.ParseAddressList(c.bcc.Text())...),
		Subject:     c.subject.Text(),
		Body:        c.body.Text(),
		Attachments: append([]mail.DraftAttachment{}, c.attachments...),
		// The identity and the thread headers are the ones the composer shows,
		// so what was read in the details is what goes out.
		MessageID:  c.messageID,
		Date:       c.date,
		InReplyTo:  c.inReplyTo,
		References: append([]string{}, c.references...),
		Signature:  c.signed,
	}
	if sender != nil {
		draft.From = mail.Address{Name: sender.name, Email: sender.email}
	}
	return draft
}

// sign is the composer's own step: it asks gpg for a signature and keeps it on
// the draft. The passphrase prompt belongs here, when the user asked for it, not
// in the middle of a send.
func (c *composeView) sign() {
	if c.signing || c.sending {
		return
	}
	sender := c.currentSender()
	if sender == nil {
		c.errMsg = i18n.T("compose.accountUnavailable")
		return
	}
	// The footer only draws the button for an identity with a key, so this is
	// a guard, not a message: no click can reach it, and a direct call must
	// still not ask gpg to sign with an empty key.
	if sender.key == "" {
		return
	}
	draft := c.snapshot()
	key := sender.key
	name := subjectOrPlaceholder(strings.Join(strings.Fields(draft.Subject), " "))

	c.signing = true
	c.errMsg = ""
	c.signGen++
	gen := c.signGen
	task := c.app.tasks.begin(taskSign, "", name)
	c.signTaskID = task.id
	taskID := task.id

	var sig *mail.Signature
	cancel := c.app.async("sign", timeoutSign, nil,
		func(ctx context.Context) error {
			var err error
			sig, err = c.app.signDraft(ctx, key, draft)
			return err
		},
		func() {
			c.signing = false
			c.signCancel = nil
			c.app.tasks.finish(taskID, "")
			// A draft that changed while gpg worked gets no badge: the result
			// does not cover what is on screen any more.
			if gen != c.signGen {
				return
			}
			c.signed = sig
		},
		func(err error) {
			c.signing = false
			c.signCancel = nil
			// A dismissed task means the user cancelled the signature.
			cancelled := c.app.tasks.find(taskID) == nil
			c.app.tasks.fail(taskID, err, false)
			if gen != c.signGen {
				return
			}
			if cancelled {
				c.errMsg = i18n.T("compose.signCancelled")
				return
			}
			c.errMsg = i18n.T("compose.signFailed", map[string]string{"Error": errorText(err)})
		})
	c.signCancel = cancel
	c.app.tasks.setCancel(taskID, func() {
		// Dismissing the row stops gpg: the draft stays unsigned and nothing was
		// sent.
		cancel()
		c.signing = false
		c.signCancel = nil
		if gen == c.signGen {
			c.errMsg = i18n.T("compose.signCancelled")
		}
	})
}

// clearSignature drops a signature that no longer describes the draft, and
// stops a signing step that was working on the old content.
func (c *composeView) clearSignature() {
	if c.signed == nil && !c.signing {
		return
	}
	// The generation moves first: the cancel below must not report itself as a
	// failure of the draft the user is now editing.
	c.signGen++
	if c.signing {
		if c.signTaskID != 0 {
			c.app.tasks.dismiss(c.signTaskID)
		}
		c.signing = false
		c.signCancel = nil
	}
	c.signed = nil
}

// abortSignature stops the signing step without touching the rest of the draft.
// Closing a composer must not leave gpg waiting for a passphrase nobody will
// answer.
func (c *composeView) abortSignature() {
	if c.signing {
		if c.signCancel != nil {
			c.signCancel()
		}
		if c.signTaskID != 0 {
			c.app.tasks.dismiss(c.signTaskID)
		}
		c.signing = false
		c.signCancel = nil
		c.signGen++
	}
	c.signed = nil
}

func (c *composeView) send() {
	if c.sending || c.signing {
		return
	}
	sender := c.currentSender()
	if sender == nil {
		c.errMsg = i18n.T("compose.accountUnavailable")
		return
	}
	// Snapshot the draft: the fields and the attachment list stay editable while
	// the send runs, and the backend must not see those edits.
	draft := c.snapshot()
	if len(draft.To) == 0 {
		c.errMsg = i18n.T("compose.needRecipient")
		return
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
	// A send is a background job like a download: the task row in the sidebar
	// reports it, and keeps reporting after the draft is gone.
	name := subjectOrPlaceholder(strings.Join(strings.Fields(draft.Subject), " "))
	task := c.app.tasks.begin(taskSend, "", name)
	taskID := task.id
	// The account the draft belongs to. Sending may outlive a switch, so the
	// completion still has to reset this composer, but only the original
	// mailbox is refreshed afterwards.
	accID := c.app.activeAcc
	finish := func() {
		c.sending = false
	}
	cancel := c.app.async("send", timeoutSend, nil,
		deliver,
		func() {
			c.app.tasks.finish(taskID, "")
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
			// A dismissed task means the user cancelled the send: the draft
			// keeps its content and the failure is not reported.
			cancelled := c.app.tasks.find(taskID) == nil
			c.app.tasks.fail(taskID, err, false)
			finish()
			if cancelled {
				return
			}
			// The renderer refused the signature because the draft changed
			// since it was made: drop the badge so the footer and the state
			// agree that this draft needs signing again.
			if errors.Is(err, mail.ErrSignatureStale) {
				c.signed = nil
			}
			msg := i18n.T("compose.sendFailed", map[string]string{"Error": errorText(err)})
			if c.app.overlay == c {
				c.errMsg = msg
				return
			}
			// The composer was closed while sending, so its footer can no
			// longer show the error.
			c.app.notify(msg, true)
		})
	c.app.tasks.setCancel(taskID, func() {
		// Cancelling the work also releases the composer: it stays open with
		// the draft untouched so the message can be sent again.
		cancel()
		c.sending = false
	})
}

// addAttachment is called on the UI goroutine after the file picker returns.
// The path is the user's own file: nothing is copied and nothing is deleted
// here, whatever happens to the draft.
func (c *composeView) addAttachment(att mail.DraftAttachment) {
	for _, existing := range c.attachments {
		if existing.Path == att.Path {
			c.errMsg = i18n.T("compose.duplicateAttachment")
			return
		}
	}
	c.attachments = append(c.attachments, att)
	c.refreshChips()
	c.clearSignature()
}

func (c *composeView) removeAttachment(index int) {
	if index < 0 || index >= len(c.attachments) {
		return
	}
	// Build a new slice instead of splicing in place: the send goroutine may
	// still hold the previous one.
	rest := make([]mail.DraftAttachment, 0, len(c.attachments)-1)
	rest = append(rest, c.attachments[:index]...)
	rest = append(rest, c.attachments[index+1:]...)
	c.attachments = rest
	c.refreshChips()
	c.clearSignature()
}

// discard drops the attachment list of a draft that is going away. The files
// belong to the user and are left untouched.
func (c *composeView) discard() {
	if c == nil {
		return
	}
	// A signing step still waiting for a passphrase has no draft to sign any
	// more, and the process it started must not outlive the window.
	c.abortSignature()
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
