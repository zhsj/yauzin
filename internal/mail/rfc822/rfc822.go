// Package rfc822 renders a draft into the RFC 5322 message both senders hand
// to their transport: the Gmail API takes the raw message, and an SMTP session
// writes it out after DATA. Keeping one renderer means the two paths cannot
// drift in how they encode headers or attachments.
package rfc822

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

const (
	crlf = "\r\n"
	// foldWidth is the column at which a header line is wrapped. RFC 5322
	// recommends 78 characters per line and never allows more than 998 octets.
	foldWidth = 78
	// base64Width is the line length of an encoded body or attachment.
	base64Width = 76
)

// Options tunes one rendering. The zero value produces a dated, uniquely
// numbered message without a Bcc header.
type Options struct {
	// BccHeader writes the blind recipients into the message. The Gmail API
	// only sees the raw message, so it needs the header; a direct SMTP session
	// delivers them through RCPT TO and must leave the header out, or every
	// other recipient learns who was blind-copied.
	BccHeader bool
	// Date replaces the current time in the Date header.
	Date time.Time
	// MessageID replaces the generated Message-ID, which tests rely on.
	MessageID string
}

// Build renders draft as a complete message. from is the sender written into
// the From header and is where the Message-ID domain comes from; an empty
// address falls back to draft.From.
func Build(draft mail.Draft, from mail.Address, opts Options) ([]byte, error) {
	if from.Email == "" {
		from = draft.From
	}
	if from.Email == "" {
		return nil, fmt.Errorf("%w: no sender", mail.ErrInvalidArgument)
	}
	if err := draft.Validate(); err != nil {
		return nil, err
	}

	date := opts.Date
	if date.IsZero() {
		date = time.Now()
	}
	messageID := opts.MessageID
	if messageID == "" {
		messageID = newMessageID(from.Email)
	}

	var b bytes.Buffer
	writeAddresses(&b, "From", []mail.Address{from})
	writeAddresses(&b, "To", draft.To)
	writeAddresses(&b, "Cc", draft.Cc)
	if opts.BccHeader {
		writeAddresses(&b, "Bcc", draft.Bcc)
	}
	writeFolded(&b, "Subject", mime.QEncoding.Encode("utf-8", stripNewlines(draft.Subject)))
	writeLine(&b, "Date", date.Format(time.RFC1123Z))
	writeLine(&b, "Message-ID", messageID)
	writeLine(&b, "MIME-Version", "1.0")

	if len(draft.Attachments) == 0 {
		writeLine(&b, "Content-Type", "text/plain; charset=utf-8")
		writeLine(&b, "Content-Transfer-Encoding", "base64")
		b.WriteString(crlf)
		if err := writeBase64(&b, bytes.NewReader([]byte(draft.Body))); err != nil {
			return nil, fmt.Errorf("rfc822: encode body: %w", err)
		}
		return b.Bytes(), nil
	}

	boundary := newBoundary()
	writeLine(&b, "Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString(crlf)

	b.WriteString("--" + boundary + crlf)
	writeLine(&b, "Content-Type", "text/plain; charset=utf-8")
	writeLine(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString(crlf)
	if err := writeBase64(&b, bytes.NewReader([]byte(draft.Body))); err != nil {
		return nil, fmt.Errorf("rfc822: encode body: %w", err)
	}

	for _, att := range draft.Attachments {
		if err := writeAttachment(&b, boundary, att); err != nil {
			return nil, err
		}
	}
	b.WriteString("--" + boundary + "--" + crlf)
	return b.Bytes(), nil
}

// writeAttachment copies one file into the buffer as a base64 part. The file is
// streamed so a large attachment does not have to sit in memory twice.
func writeAttachment(b *bytes.Buffer, boundary string, att mail.DraftAttachment) error {
	f, err := os.Open(att.Path)
	if err != nil {
		return fmt.Errorf("rfc822: open attachment %q: %w", att.Name, err)
	}
	defer func() { _ = f.Close() }()

	b.WriteString("--" + boundary + crlf)
	writeLine(b, "Content-Type", attachmentType(att.Name))
	writeLine(b, "Content-Transfer-Encoding", "base64")
	writeLine(b, "Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": stripNewlines(filepath.Base(att.Name))}))
	b.WriteString(crlf)
	if err := writeBase64(b, f); err != nil {
		return fmt.Errorf("rfc822: read attachment %q: %w", att.Name, err)
	}
	return nil
}

// attachmentType guesses the media type from the file name; unknown extensions
// are sent as an opaque stream, which every client still saves as a file.
func attachmentType(name string) string {
	if ctype := mime.TypeByExtension(filepath.Ext(name)); ctype != "" {
		return ctype
	}
	return "application/octet-stream"
}

// writeLine writes a single header that is never folded.
func writeLine(b *bytes.Buffer, key, value string) {
	b.WriteString(key)
	b.WriteString(":")
	if value != "" {
		b.WriteString(" ")
		b.WriteString(value)
	}
	b.WriteString(crlf)
}

// writeFolded writes a header whose value may be long, folding it at the spaces
// between words. An encoded subject arrives as several space separated encoded
// words, so the fold never cuts one in half.
func writeFolded(b *bytes.Buffer, key, value string) {
	b.WriteString(key)
	b.WriteString(":")
	if value == "" {
		b.WriteString(crlf)
		return
	}
	width := len(key) + 1
	for _, word := range splitWords(value) {
		if width+len(word)+1 > foldWidth {
			b.WriteString(crlf + " ")
			width = 1
		} else {
			b.WriteString(" ")
			width++
		}
		b.WriteString(word)
		width += len(word)
	}
	b.WriteString(crlf)
}

// writeAddresses writes an address list, folding between addresses. A display
// name may itself contain a comma, so the list is folded per rendered address
// instead of on the separators of the raw text.
func writeAddresses(b *bytes.Buffer, key string, addrs []mail.Address) {
	if len(addrs) == 0 {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	width := len(key) + 2
	for i, addr := range addrs {
		rendered := renderAddress(addr)
		if i > 0 {
			if width+len(rendered)+2 > foldWidth {
				b.WriteString("," + crlf + " ")
				width = 1
			} else {
				b.WriteString(", ")
				width += 2
			}
		}
		b.WriteString(rendered)
		width += len(rendered)
	}
	b.WriteString(crlf)
}

// renderAddress formats one address with the display name quoted and, when it
// is not ASCII, RFC 2047 encoded.
func renderAddress(a mail.Address) string {
	addr := &netmail.Address{Name: stripNewlines(a.Name), Address: stripNewlines(a.Email)}
	return addr.String()
}

// stripNewlines removes CR and LF from header text, so a display name or a
// subject typed by the user cannot inject a header of its own.
func stripNewlines(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// splitWords splits on spaces, keeping the words themselves untouched.
func splitWords(s string) []string {
	var out []string
	field := -1
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' {
			if field < 0 {
				field = i
			}
			continue
		}
		if field >= 0 {
			out = append(out, s[field:i])
			field = -1
		}
	}
	if field >= 0 {
		out = append(out, s[field:])
	}
	return out
}

// writeBase64 streams r as a base64 body, breaking the output into lines. The
// encoder emits whole 4 byte groups, so a width that is a multiple of four
// never splits an encoded group.
func writeBase64(b *bytes.Buffer, r io.Reader) error {
	lw := &lineWriter{w: b}
	enc := base64.NewEncoder(base64.StdEncoding, lw)
	if _, err := io.Copy(enc, r); err != nil {
		_ = enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if lw.n > 0 {
		b.WriteString(crlf)
	}
	return nil
}

// lineWriter inserts a CRLF after every base64Width bytes.
type lineWriter struct {
	w io.Writer
	n int
}

func (l *lineWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if l.n == base64Width {
			if _, err := io.WriteString(l.w, crlf); err != nil {
				return written, err
			}
			l.n = 0
		}
		chunk := len(p)
		if rest := base64Width - l.n; chunk > rest {
			chunk = rest
		}
		n, err := l.w.Write(p[:chunk])
		written += n
		l.n += n
		if err != nil {
			return written, err
		}
		p = p[chunk:]
	}
	return written, nil
}

// newBoundary returns a boundary that cannot collide with the content.
func newBoundary() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "yauzin-boundary"
	}
	return "yauzin-" + hex.EncodeToString(raw[:])
}

// newMessageID builds a message ID in the sender's domain, so relays that check
// the right hand side accept it.
func newMessageID(email string) string {
	domain := "yauzin"
	if at := strings.LastIndexByte(email, '@'); at >= 0 && at+1 < len(email) {
		domain = email[at+1:]
	}
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domain)
	}
	return "<" + hex.EncodeToString(raw[:]) + "@" + domain + ">"
}
