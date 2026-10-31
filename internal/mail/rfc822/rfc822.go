// Package rfc822 renders a draft into the RFC 5322 message both senders hand
// to their transport: the Gmail API takes the raw message, and an SMTP session
// writes it out after DATA. Keeping one renderer means the two paths cannot
// drift in how they encode headers or attachments.
package rfc822

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	// Date replaces the current time in the Date header unless the draft
	// already carries one.
	Date time.Time
	// MessageID replaces the generated Message-ID, which tests rely on. A
	// Message-ID on the draft wins over it.
	MessageID string
}

// Message is a rendered draft that can be measured and written. It holds no
// attachment content: WriteTo opens every file and streams it to the target,
// so rendering a message costs the same memory however large its attachments
// are. A Message is immutable after Prepare and can be written repeatedly, as
// long as the files on disk do not change.
type Message struct {
	header  []byte
	parts   []part
	closing []byte
	size    int64
}

// entity is the MIME entity of a draft: its own header block, the parts that
// follow it and, when it is multipart, the closing boundary line. It is kept
// apart from Message because a PGP/MIME signature covers exactly these bytes and
// nothing else: the address and subject headers stay outside it.
type entity struct {
	header  []byte
	parts   []part
	closing []byte
}

// size is the exact number of bytes writeTo produces.
func (e entity) size() int64 {
	n := int64(len(e.header)) + int64(len(e.closing))
	for _, p := range e.parts {
		n += int64(len(p.prefix)) + p.size()
	}
	return n
}

// writeTo writes the entity: its header, every part and the closing line.
func (e entity) writeTo(w io.Writer) error {
	if len(e.header) > 0 {
		if _, err := w.Write(e.header); err != nil {
			return err
		}
	}
	for _, p := range e.parts {
		if len(p.prefix) > 0 {
			if _, err := w.Write(p.prefix); err != nil {
				return err
			}
		}
		if err := p.writeTo(w); err != nil {
			return err
		}
	}
	if len(e.closing) > 0 {
		if _, err := w.Write(e.closing); err != nil {
			return err
		}
	}
	return nil
}

// signedWriteTo writes the bytes a PGP/MIME signature covers.
//
// RFC 2046 §5.1.1 attaches the CRLF that precedes a boundary delimiter to the
// delimiter itself, so a part that should end with a line break needs two CRLFs
// before the next delimiter. The entity built here ends with one, which means the
// receiver reconstructs it up to, but not including, that CRLF; RFC 3156 §5 says
// the same from the signing side ("MUST NOT be included in the signature if
// absent"). Signing the extra CRLF would make every verifier report a bad
// signature, so it is dropped here and only here.
func (e entity) signedWriteTo(w io.Writer) error {
	tw := &crlfTailWriter{w: w}
	if err := e.writeTo(tw); err != nil {
		return err
	}
	return tw.Flush()
}

// digest is the SHA-256 of the signed bytes, the value a signature records so
// the renderer can prove the draft did not change after it was signed.
func (e entity) digest() ([32]byte, error) {
	h := sha256.New()
	if err := e.signedWriteTo(h); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// crlfTailWriter holds back the last two bytes it is given, so the caller can
// drop them when they are the CRLF that belongs to the following boundary. Every
// other byte passes straight through, and Write always reports the whole slice
// as consumed: the count is what io.Copy and the pipe rely on, and it describes
// the input rather than the output on purpose.
type crlfTailWriter struct {
	w    io.Writer
	tail []byte
}

func (t *crlfTailWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	pending := append(t.tail, p...)
	if len(pending) <= len(crlf) {
		t.tail = pending
		return len(p), nil
	}
	cut := len(pending) - len(crlf)
	if _, err := t.w.Write(pending[:cut]); err != nil {
		return len(p), err
	}
	// Keep the two held bytes without holding on to the caller's buffer.
	t.tail = append(t.tail[:0], pending[cut:]...)
	return len(p), nil
}

// Flush writes whatever is still held back, minus a trailing CRLF. The two held
// bytes are either that CRLF or the entity's real ending, so nothing else can be
// swallowed.
func (t *crlfTailWriter) Flush() error {
	tail := t.tail
	t.tail = nil
	if string(tail) == crlf {
		return nil
	}
	if len(tail) == 0 {
		return nil
	}
	_, err := t.w.Write(tail)
	return err
}

// partKind says how the content of a part reaches the message. The zero value
// is the common case: the text body and the attachments, base64 encoded on the
// way out.
type partKind uint8

const (
	// partRaw is written verbatim: the armored signature.
	partRaw partKind = iota + 1
	// partEntity is a nested MIME entity written verbatim: the part a
	// multipart/signed message signs.
	partEntity
)

// part is one section of the message.
type part struct {
	kind partKind
	// prefix is the boundary line and the part headers, including the blank
	// line before the content. It is empty for the body of a message without
	// attachments, where the entity header already describes the body.
	prefix []byte
	// name is the attachment name, used in errors only.
	name string
	// path is the attachment file; empty for the text body.
	path string
	// data is the text body when path is empty.
	data []byte
	// fileSize is the size of path when it was prepared, checked again when
	// the file is read.
	fileSize int64
	// raw is the verbatim content of a partRaw part.
	raw []byte
	// nested is the content of a partEntity part.
	nested *entity
}

// Build renders draft as a complete message. It is the buffered form of
// Prepare and WriteTo, kept for callers that need the bytes in memory.
func Build(draft mail.Draft, from mail.Address, opts Options) ([]byte, error) {
	msg, err := Prepare(draft, from, opts)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if _, err := msg.WriteTo(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Signer produces the detached signature of one MIME entity. The implementation
// lives outside this package, because signing means running gpg: keeping process
// execution out of the renderer is what lets tests inject a fake.
type Signer interface {
	// SignDetached signs the entity read from r and returns the armored
	// signature together with the RFC 3156 hash name it was made with.
	SignDetached(ctx context.Context, r io.Reader) (armored []byte, micalg string, err error)
	// Key names the signing key, for the signature record and the logs.
	Key() string
}

// Sign renders the entity of draft, signs it and returns the record the draft
// carries until it is sent. Attachments are read once, here; the send then reads
// them again to reproduce and check the signed bytes.
//
// The entity holds the body and the attachments only, so the identity, the
// subject, the date and the recipients do not affect the signature: PGP/MIME
// signs the MIME part and leaves the message headers outside it. What is signed
// is signedWriteTo, which stops before the CRLF that the following boundary
// takes with it.
func Sign(ctx context.Context, draft mail.Draft, s Signer) (*mail.Signature, error) {
	boundary := ""
	if len(draft.Attachments) > 0 {
		boundary = newBoundary()
	}
	ent, err := buildEntity(draft, boundary)
	if err != nil {
		return nil, err
	}

	// The entity is streamed into gpg through a pipe so an attachment never has
	// to sit in memory, and the same bytes go through a hash for the record.
	digest := sha256.New()
	pr, pw := io.Pipe()
	written := make(chan error, 1)
	go func() {
		err := ent.signedWriteTo(pw)
		_ = pw.CloseWithError(err)
		written <- err
	}()

	armored, micalg, signErr := s.SignDetached(ctx, io.TeeReader(pr, digest))
	// A signer that stopped reading leaves the writer blocked; closing the read
	// side releases it either way.
	_ = pr.Close()
	writeErr := <-written
	if signErr != nil {
		return nil, signErr
	}
	if writeErr != nil {
		return nil, writeErr
	}
	if len(armored) == 0 {
		return nil, fmt.Errorf("%w: signer returned no signature", mail.ErrInvalidArgument)
	}
	micalg = strings.TrimSpace(micalg)
	if micalg == "" {
		return nil, fmt.Errorf("%w: signer returned no hash name", mail.ErrInvalidArgument)
	}

	sig := &mail.Signature{
		Data:     crlfLines(armored),
		Micalg:   micalg,
		Boundary: boundary,
		Key:      s.Key(),
	}
	copy(sig.Digest[:], digest.Sum(nil))
	return sig, nil
}

// Prepare validates draft and decides everything about the rendered message
// except its bytes: the headers, the boundary, and the exact size. Attachments
// are opened once here, so a file that is missing or unreadable fails before a
// connection is dialled or an upload session is started.
func Prepare(draft mail.Draft, from mail.Address, opts Options) (*Message, error) {
	if from.Email == "" {
		from = draft.From
	}
	if from.Email == "" {
		return nil, fmt.Errorf("%w: no sender", mail.ErrInvalidArgument)
	}
	if err := draft.Validate(); err != nil {
		return nil, err
	}

	// The composer fills Date and Message-ID so the headers it shows are the
	// ones written here; Options stays the override tests use.
	date := draft.Date
	if date.IsZero() {
		date = opts.Date
	}
	if date.IsZero() {
		date = time.Now()
	}
	messageID := opts.MessageID
	if draft.MessageID != "" {
		messageID = draft.MessageID
	}
	if id, ok := mail.NormalizeMessageID(messageID); ok {
		messageID = id
	} else {
		messageID = mail.NewMessageID(from.Email)
	}

	if draft.Signature != nil {
		return prepareSigned(draft, from, date, messageID, opts)
	}

	boundary := ""
	if len(draft.Attachments) > 0 {
		boundary = newBoundary()
	}
	ent, err := buildEntity(draft, boundary)
	if err != nil {
		return nil, err
	}
	msg := &Message{
		header:  append(headerFields(draft, from, date, messageID, opts.BccHeader), ent.header...),
		parts:   ent.parts,
		closing: ent.closing,
	}
	msg.size = int64(len(msg.header)) + msg.partsSize() + int64(len(msg.closing))
	return msg, nil
}

// prepareSigned wraps the drafted entity in multipart/signed. The signature was
// made before the send, so the entity has to be reproduced byte for byte: the
// boundary the signer used is reused and the digest is checked before a single
// byte is written.
func prepareSigned(draft mail.Draft, from mail.Address, date time.Time, messageID string, opts Options) (*Message, error) {
	sig := draft.Signature
	if sig.Micalg == "" || len(sig.Data) == 0 {
		return nil, fmt.Errorf("%w: incomplete signature", mail.ErrSignatureStale)
	}
	if len(draft.Attachments) > 0 && sig.Boundary == "" {
		return nil, fmt.Errorf("%w: signed entity has no boundary", mail.ErrSignatureStale)
	}
	ent, err := buildEntity(draft, sig.Boundary)
	if err != nil {
		return nil, err
	}
	sum, err := ent.digest()
	if err != nil {
		return nil, err
	}
	if sum != sig.Digest {
		return nil, fmt.Errorf("%w: %s", mail.ErrSignatureStale, sig.Key)
	}

	boundary := newBoundary()
	for boundary == sig.Boundary {
		boundary = newBoundary()
	}
	var header bytes.Buffer
	header.Write(headerFields(draft, from, date, messageID, opts.BccHeader))
	writeLine(&header, "Content-Type",
		`multipart/signed; micalg=`+sig.Micalg+`; protocol="application/pgp-signature"; boundary="`+boundary+`"`)
	header.WriteString(crlf)

	msg := &Message{
		header: header.Bytes(),
		parts: []part{
			{kind: partEntity, prefix: []byte("--" + boundary + crlf), nested: &ent},
			{kind: partRaw, prefix: signaturePartPrefix(boundary), raw: sig.Data},
		},
		closing: []byte("--" + boundary + "--" + crlf),
	}
	msg.size = int64(len(msg.header)) + msg.partsSize() + int64(len(msg.closing))
	return msg, nil
}

// partsSize is the length of every part once written.
func (m *Message) partsSize() int64 {
	var n int64
	for _, p := range m.parts {
		n += int64(len(p.prefix)) + p.size()
	}
	return n
}

// buildEntity renders the MIME entity of a draft. boundary is empty for a
// message without attachments, whose body is the whole entity.
func buildEntity(draft mail.Draft, boundary string) (entity, error) {
	if len(draft.Attachments) == 0 {
		var b bytes.Buffer
		writeLine(&b, "Content-Type", "text/plain; charset=utf-8")
		writeLine(&b, "Content-Transfer-Encoding", "base64")
		b.WriteString(crlf)
		return entity{
			header: b.Bytes(),
			parts:  []part{{data: []byte(draft.Body)}},
		}, nil
	}

	var b bytes.Buffer
	writeLine(&b, "Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString(crlf)
	ent := entity{
		header: b.Bytes(),
		parts: []part{{
			prefix: bodyPartPrefix(boundary),
			data:   []byte(draft.Body),
		}},
	}
	for _, att := range draft.Attachments {
		size, err := attachmentSize(att)
		if err != nil {
			return entity{}, err
		}
		ent.parts = append(ent.parts, part{
			prefix:   attachmentPartPrefix(boundary, att),
			name:     att.Name,
			path:     att.Path,
			fileSize: size,
		})
	}
	ent.closing = []byte("--" + boundary + "--" + crlf)
	return ent, nil
}

// Size is the exact number of bytes WriteTo produces. A resumable upload
// declares it when the session starts, so the server knows when the message is
// complete.
func (m *Message) Size() int64 { return m.size }

// WriteTo writes the message to w, streaming every attachment from disk. The
// returned count always equals Size; a mismatch means a file changed after
// Prepare and the message would not be what the transport declared.
func (m *Message) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	if _, err := cw.Write(m.header); err != nil {
		return cw.n, err
	}
	for _, p := range m.parts {
		if len(p.prefix) > 0 {
			if _, err := cw.Write(p.prefix); err != nil {
				return cw.n, err
			}
		}
		if err := p.writeTo(cw); err != nil {
			return cw.n, err
		}
	}
	if len(m.closing) > 0 {
		if _, err := cw.Write(m.closing); err != nil {
			return cw.n, err
		}
	}
	if cw.n != m.size {
		return cw.n, fmt.Errorf("rfc822: wrote %d bytes, want %d", cw.n, m.size)
	}
	return cw.n, nil
}

// size is the exact number of bytes the part's content occupies once written.
func (p part) size() int64 {
	switch p.kind {
	case partRaw:
		return int64(len(p.raw))
	case partEntity:
		return p.nested.size()
	default:
		return encodedSize(p.rawSize())
	}
}

// rawSize is the number of bytes before transfer encoding.
func (p part) rawSize() int64 {
	if p.path == "" {
		return int64(len(p.data))
	}
	return p.fileSize
}

// writeTo writes one part's content: verbatim for the two kinds that carry
// already rendered bytes, base64 encoded otherwise.
func (p part) writeTo(w io.Writer) error {
	switch p.kind {
	case partRaw:
		_, err := w.Write(p.raw)
		return err
	case partEntity:
		return p.nested.writeTo(w)
	}
	if p.path == "" {
		if err := writeBase64(w, bytes.NewReader(p.data)); err != nil {
			return fmt.Errorf("rfc822: encode body: %w", err)
		}
		return nil
	}
	f, err := os.Open(p.path)
	if err != nil {
		return fmt.Errorf("rfc822: open attachment %q: %w", p.name, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("rfc822: stat attachment %q: %w", p.name, err)
	}
	if info.Size() != p.fileSize {
		_ = f.Close()
		return fmt.Errorf("rfc822: attachment %q changed size: %d, want %d", p.name, info.Size(), p.fileSize)
	}
	writeErr := writeBase64(w, f)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("rfc822: read attachment %q: %w", p.name, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("rfc822: read attachment %q: %w", p.name, closeErr)
	}
	return nil
}

// headerFields renders the message headers that stay outside the MIME entity:
// addresses, subject, date, message ID, the thread headers and MIME-Version.
// The Content-Type of the entity follows them without a blank line between, so
// an unsigned message is still the same bytes it always was.
func headerFields(draft mail.Draft, from mail.Address, date time.Time, messageID string, bcc bool) []byte {
	var b bytes.Buffer
	writeAddresses(&b, "From", []mail.Address{from})
	writeAddresses(&b, "To", draft.To)
	writeAddresses(&b, "Cc", draft.Cc)
	if bcc {
		writeAddresses(&b, "Bcc", draft.Bcc)
	}
	writeFolded(&b, "Subject", mime.QEncoding.Encode("utf-8", stripNewlines(draft.Subject)))
	writeLine(&b, "Date", date.Format(time.RFC1123Z))
	writeLine(&b, "Message-ID", messageID)
	writeThreadHeaders(&b, draft)
	writeLine(&b, "MIME-Version", "1.0")
	return b.Bytes()
}

// signaturePartPrefix is the second part of a multipart/signed message: the
// armored detached signature.
func signaturePartPrefix(boundary string) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + crlf)
	writeLine(&b, "Content-Type", "application/pgp-signature")
	writeLine(&b, "Content-Description", "OpenPGP digital signature")
	b.WriteString(crlf)
	return b.Bytes()
}

// crlfLines returns data with every line ending as CRLF, which is the only form
// allowed inside a MIME message. gpg prints the armored text with plain LF, so
// the signature has to be converted once, here, where its length also comes
// from.
func crlfLines(data []byte) []byte {
	text := strings.ReplaceAll(string(data), crlf, "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\n", crlf)
	return []byte(text)
}

// bodyPartPrefix is the boundary line and headers of the text part of a
// multipart message.
func bodyPartPrefix(boundary string) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + crlf)
	writeLine(&b, "Content-Type", "text/plain; charset=utf-8")
	writeLine(&b, "Content-Transfer-Encoding", "base64")
	b.WriteString(crlf)
	return b.Bytes()
}

// attachmentPartPrefix is the boundary line and headers of one file part.
func attachmentPartPrefix(boundary string, att mail.DraftAttachment) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + crlf)
	writeLine(&b, "Content-Type", attachmentType(att.Name))
	writeLine(&b, "Content-Transfer-Encoding", "base64")
	writeLine(&b, "Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": stripNewlines(filepath.Base(att.Name))}))
	b.WriteString(crlf)
	return b.Bytes()
}

// attachmentSize opens the file once so a missing or unreadable attachment is
// reported before anything is sent, and returns the size the renderer expects.
func attachmentSize(att mail.DraftAttachment) (int64, error) {
	f, err := os.Open(att.Path)
	if err != nil {
		return 0, fmt.Errorf("rfc822: open attachment %q: %w", att.Name, err)
	}
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return 0, fmt.Errorf("rfc822: stat attachment %q: %w", att.Name, statErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("rfc822: read attachment %q: %w", att.Name, closeErr)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("rfc822: attachment %q is not a regular file", att.Name)
	}
	return info.Size(), nil
}

// encodedSize is the length of n bytes once base64 encoded and wrapped at
// base64Width. It mirrors writeBase64 exactly: every complete line is followed
// by CRLF, and a non-empty payload ends with one.
func encodedSize(n int64) int64 {
	if n <= 0 {
		return 0
	}
	raw := (n + 2) / 3 * 4
	return raw + 2*((raw-1)/base64Width) + 2
}

// countWriter counts what reaches the target and treats a short write as an
// error, so a truncated attachment cannot pass as a complete message.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

// attachmentType guesses the media type from the file name; unknown extensions
// are sent as an opaque stream, which every client still saves as a file.
func attachmentType(name string) string {
	if ctype := mime.TypeByExtension(filepath.Ext(name)); ctype != "" {
		return ctype
	}
	return "application/octet-stream"
}

// writeThreadHeaders writes the headers that file a reply under the message it
// answers. A new message and a forward carry neither, so they write nothing.
func writeThreadHeaders(b *bytes.Buffer, draft mail.Draft) {
	if id, ok := mail.NormalizeMessageID(draft.InReplyTo); ok {
		writeLine(b, "In-Reply-To", id)
	}
	refs := make([]string, 0, len(draft.References))
	for _, raw := range draft.References {
		if id, ok := mail.NormalizeMessageID(raw); ok {
			refs = append(refs, id)
		}
	}
	if len(refs) == 0 {
		return
	}
	writeFolded(b, "References", strings.Join(refs, " "))
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
func writeBase64(w io.Writer, r io.Reader) error {
	lw := &lineWriter{w: w}
	enc := base64.NewEncoder(base64.StdEncoding, lw)
	if _, err := io.Copy(enc, r); err != nil {
		_ = enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if lw.n > 0 {
		_, err := io.WriteString(w, crlf)
		return err
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
