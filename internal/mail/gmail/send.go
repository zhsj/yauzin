package gmail

import (
	"context"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/rfc822"
)

// Send delivers the draft with users.messages.send.
//
// The message is uploaded through a resumable session as its raw RFC 822
// source, streamed from disk in fixed size chunks: the attachments never sit in
// memory as a whole, and an interrupted transfer can continue from the offset
// the server confirms instead of starting over.
//
// Gmail only accepts a From that belongs to the account, so the account address
// wins over Draft.From. Sending as another identity goes through that
// identity's own SMTP server, not through this backend.
func (s *Service) Send(ctx context.Context, draft mail.Draft) error {
	if err := draft.Validate(); err != nil {
		return err
	}
	// The Bcc header has to be in the message: the API sees nothing but the raw
	// source, and Gmail strips the header when it delivers.
	msg, err := rfc822.Prepare(draft, mail.Address{Name: s.acc.Name, Email: s.acc.Email}, rfc822.Options{BccHeader: true})
	if err != nil {
		return err
	}
	return s.c.doResumableUpload(ctx, "messages.send", "/users/me/messages/send", "message/rfc822", msg, nil)
}
