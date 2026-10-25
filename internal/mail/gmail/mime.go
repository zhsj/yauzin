package gmail

import (
	"encoding/base64"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// mimeBodies is what one message contributes to the reader.
type mimeBodies struct {
	text        string
	html        string
	attachments []mail.Attachment
}

// message converts one Gmail message into the domain model.
func (s *Service) message(m *Message) *mail.Message {
	h := m.headers()
	out := &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:        m.ID,
			AccountID: s.acc.ID,
			From:      firstAddress(h["from"]),
			To:        parseAddressList(h["to"]),
			Subject:   h["subject"],
			Snippet:   m.Snippet,
			Date:      m.date(),
			Unread:    m.hasFlag("UNREAD"),
			Starred:   m.hasFlag("STARRED"),
			Labels:    append([]string(nil), m.LabelIDs...),
		},
		Cc: parseAddressList(h["cc"]),
	}
	var bodies mimeBodies
	collectParts(m.Payload, &bodies)
	out.HTMLBody = bodies.html
	out.TextBody = bodies.text
	out.Attachments = bodies.attachments
	out.HasAttachments = len(bodies.attachments) > 0
	return out
}

// collectParts walks the MIME tree, keeping the first text and HTML bodies and
// listing every file part. A part with a filename is a file even when it also
// carries a text media type (an attached .txt is not the body).
func collectParts(p *MessagePart, out *mimeBodies) {
	if p == nil {
		return
	}
	if p.Filename != "" {
		out.attachments = append(out.attachments, attachment(p))
		return
	}
	switch strings.ToLower(p.MIMEType) {
	case "text/plain":
		if out.text == "" {
			out.text = decodeBody(p.Body)
		}
		return
	case "text/html":
		if out.html == "" {
			out.html = decodeBody(p.Body)
		}
		return
	}
	for _, child := range p.Parts {
		collectParts(child, out)
	}
}

// attachment converts one file part into its metadata. The content itself is
// out of scope: the backend does not download attachments yet.
func attachment(p *MessagePart) mail.Attachment {
	a := mail.Attachment{Filename: p.Filename, MIMEType: p.MIMEType}
	if p.Body != nil {
		a.ID = p.Body.AttachmentID
		a.Size = int64(p.Body.Size)
	}
	return a
}

// decodeBody turns one part's body into text. Gmail has already undone the
// transfer encoding and the charset conversion: the bytes it returns are UTF-8
// even when the part declares charset=iso-8859-1 (a quoted-printable "Gr=FC=DFe"
// arrives as the UTF-8 Grüße), while the Content-Type header keeps the original
// label. Reading by that label would decode the UTF-8 bytes as latin-1 and turn
// "Grüße" into "GrÃ¼ÃŸe", so the bytes are taken as they are.
func decodeBody(b *MessagePartBody) string {
	if b == nil || b.Data == "" {
		return ""
	}
	raw, err := decodeBase64URL(b.Data)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeBase64URL accepts the padded and unpadded base64url Gmail uses.
func decodeBase64URL(data string) ([]byte, error) {
	if raw, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return raw, nil
	}
	return base64.URLEncoding.DecodeString(data)
}

// parseAddressList parses a header value, falling back to the raw text when it
// is malformed: the address list is display data, and dropping a sender is
// worse than showing an unparsed string.
func parseAddressList(raw string) []mail.Address {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if addrs, err := netmail.ParseAddressList(raw); err == nil && len(addrs) > 0 {
		out := make([]mail.Address, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, mail.Address{Name: a.Name, Email: a.Address})
		}
		return out
	}
	out := mail.ParseAddressList(raw)
	if len(out) > 0 {
		return out
	}
	return []mail.Address{{Name: raw}}
}

// firstAddress returns the sender of a message, if any.
func firstAddress(raw string) mail.Address {
	if addrs := parseAddressList(raw); len(addrs) > 0 {
		return addrs[0]
	}
	return mail.Address{}
}

// parseMailDate parses an RFC 5322 date header.
func parseMailDate(raw string) (time.Time, error) {
	return netmail.ParseDate(strings.TrimSpace(raw))
}
