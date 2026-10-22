package gmail

import (
	"encoding/base64"
	"mime"
	netmail "net/mail"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

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
			out.text = decodeBody(p.Body, partHeader(p, "Content-Type"))
		}
		return
	case "text/html":
		if out.html == "" {
			out.html = decodeBody(p.Body, partHeader(p, "Content-Type"))
		}
		return
	}
	for _, child := range p.Parts {
		collectParts(child, out)
	}
}

// attachment converts one file part into its metadata. The content itself is
// out of scope: the backend is read-only and does not download attachments.
func attachment(p *MessagePart) mail.Attachment {
	a := mail.Attachment{Filename: p.Filename, MIMEType: p.MIMEType}
	if p.Body != nil {
		a.ID = p.Body.AttachmentID
		a.Size = int64(p.Body.Size)
	}
	return a
}

// decodeBody decodes a body part: Gmail already undid the transfer encoding,
// so the data is base64url of the raw bytes, and only the charset is left to
// convert.
func decodeBody(b *MessagePartBody, contentType string) string {
	if b == nil || b.Data == "" {
		return ""
	}
	raw, err := decodeBase64URL(b.Data)
	if err != nil {
		return ""
	}
	switch charsetOf(contentType) {
	case "iso-8859-1", "latin1", "latin-1":
		if converted, err := charmap.ISO8859_1.NewDecoder().Bytes(raw); err == nil {
			return string(converted)
		}
	case "windows-1252", "cp1252":
		if converted, err := charmap.Windows1252.NewDecoder().Bytes(raw); err == nil {
			return string(converted)
		}
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

// charsetOf returns the lower-cased charset parameter of a Content-Type value.
func charsetOf(contentType string) string {
	if contentType == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.ToLower(params["charset"])
}

// partHeader returns one header of a MIME part by case-insensitive name.
func partHeader(p *MessagePart, name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
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
