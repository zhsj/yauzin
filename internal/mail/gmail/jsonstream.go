package gmail

import (
	"bufio"
	"errors"
	"io"

	"github.com/zhsj/yauzin/internal/mail"
)

// dataField is the only member of MessagePartBody that carries the payload.
const dataField = "data"

// jsonScanBuffer bounds what the field scanner holds while it walks a response,
// and is the largest chunk a streamed field is read in. The memory one save
// needs is therefore flat however large the attachment is.
const jsonScanBuffer = 4 << 10

// scanDataField returns a reader over the base64url text of the "data" member
// of a MessagePartBody document, leaving the rest of the document unread.
//
// messages.attachments.get answers {"size": N, "data": "<base64url>"}, and the
// data member is the attachment. Decoding that document into a struct would
// hold the whole attachment in memory twice - once as the JSON string, once as
// the decoded bytes - so the save scans to the member instead and decodes what
// it gets while the response is still arriving.
//
// The scan is a minimal state machine over JSON string tokens rather than a
// parser: keys are strings followed by ':', values are strings, and the
// base64url alphabet holds no quote or backslash, so the value needs no
// unescaping. A document without the member (a part whose content lives in the
// payload) is reported as mail.ErrNotFound.
func scanDataField(r io.Reader) (io.Reader, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, jsonScanBuffer)
	}
	for {
		if err := skipToQuote(br); err != nil {
			return nil, fieldErr(err)
		}
		key, size, err := readString(br, len(dataField))
		if err != nil {
			return nil, fieldErr(err)
		}
		if string(key) != dataField || size != len(dataField) {
			continue
		}
		b, err := nextByte(br)
		if err != nil {
			return nil, fieldErr(err)
		}
		if b != ':' {
			// A value that happens to read "data", not the key.
			continue
		}
		if b, err = nextByte(br); err != nil {
			return nil, fieldErr(err)
		}
		if b != '"' {
			return nil, mail.ErrNotFound
		}
		return &jsonString{br: br}, nil
	}
}

// fieldErr reports a body that ends before the field as a missing field: there
// is no payload left to save.
func fieldErr(err error) error {
	if errors.Is(err, io.EOF) {
		return mail.ErrNotFound
	}
	return err
}

// skipToQuote consumes the document up to and including the next quote, which
// opens a JSON string.
func skipToQuote(br *bufio.Reader) error {
	for {
		_, err := br.ReadSlice('"')
		if err == nil {
			return nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return err
		}
	}
}

// readString consumes one JSON string whose opening quote has already been read
// and returns at most keep bytes of its content together with the length of the
// whole content, which tells a key from a longer value that starts the same.
func readString(br *bufio.Reader, keep int) ([]byte, int, error) {
	var head []byte
	size := 0
	for {
		chunk, err := br.ReadSlice('"')
		if err == nil {
			// The slice ends with the closing quote.
			body := chunk[:len(chunk)-1]
			size += len(body)
			return appendHead(head, body, keep), size, nil
		}
		head = appendHead(head, chunk, keep)
		size += len(chunk)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return head, size, err
		}
	}
}

// appendHead appends chunk to head without growing head past keep bytes.
func appendHead(head, chunk []byte, keep int) []byte {
	room := keep - len(head)
	if room <= 0 {
		return head
	}
	if len(chunk) > room {
		chunk = chunk[:room]
	}
	return append(head, chunk...)
}

// nextByte returns the next byte that is not JSON whitespace.
func nextByte(br *bufio.Reader) (byte, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return b, nil
	}
}

// jsonString yields the content of one JSON string once its opening quote has
// been read, and stops at the closing quote so the rest of the document stays
// unread. base64url text carries no escape sequences, so the first quote that
// shows up ends the string.
type jsonString struct {
	br    *bufio.Reader
	buf   []byte
	done  bool
	final error
}

func (s *jsonString) Read(b []byte) (int, error) {
	for len(s.buf) == 0 && !s.done {
		chunk, err := s.br.ReadSlice('"')
		switch {
		case err == nil:
			s.buf = chunk[:len(chunk)-1]
			s.done = true
		case errors.Is(err, bufio.ErrBufferFull):
			// The slice points into the scanner's buffer and is invalidated by
			// the next read, so the content is copied out.
			s.buf = append(s.buf[:0], chunk...)
		default:
			// EOF here means the document ended inside the string: what did
			// arrive is not the whole payload, and the caller must not store it
			// as if it were.
			s.buf = append(s.buf[:0], chunk...)
			s.done = true
			s.final = err
			if errors.Is(err, io.EOF) {
				s.final = io.ErrUnexpectedEOF
			}
		}
	}
	if len(s.buf) > 0 {
		n := copy(b, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}
	if s.final != nil {
		return 0, s.final
	}
	return 0, io.EOF
}

// paddingReader re-adds the '=' padding a base64url field may have dropped, so
// the standard padded decoder accepts both spellings of the same content.
type paddingReader struct {
	r       io.Reader
	n       int64
	pending []byte
	done    bool
}

func newPaddingReader(r io.Reader) *paddingReader { return &paddingReader{r: r} }

func (p *paddingReader) Read(b []byte) (int, error) {
	if len(p.pending) > 0 {
		n := copy(b, p.pending)
		p.pending = p.pending[n:]
		return n, nil
	}
	if p.done {
		return 0, io.EOF
	}
	n, err := p.r.Read(b)
	p.n += int64(n)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, io.EOF) {
		return n, err
	}
	p.done = true
	p.pending = base64Padding(p.n)
	if len(p.pending) == 0 {
		if n > 0 {
			return n, io.EOF
		}
		return 0, io.EOF
	}
	if n > 0 {
		// The padding follows on the next read; the content itself is already
		// decoded and must not be held back.
		return n, nil
	}
	m := copy(b, p.pending)
	p.pending = p.pending[m:]
	return m, nil
}

// base64Padding is the '=' a base64url payload of n characters is missing. A
// length of 4n+1 cannot be valid base64, so no padding is added and the decoder
// reports the payload.
func base64Padding(n int64) []byte {
	switch n % 4 {
	case 2:
		return []byte("==")
	case 3:
		return []byte("=")
	}
	return nil
}
