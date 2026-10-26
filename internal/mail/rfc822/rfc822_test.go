package rfc822

import (
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestBuildSimpleMessage(t *testing.T) {
	draft := mail.Draft{
		From:    mail.Address{Name: "Ada Chen", Email: "ada@example.com"},
		To:      []mail.Address{{Name: "Doe, John", Email: "john@example.com"}},
		Cc:      []mail.Address{{Email: "cc@example.com"}},
		Subject: "报告：Grüße",
		Body:    "第一行\nsecond line\n",
	}
	raw, err := Build(draft, draft.From, Options{Date: time.Date(2026, 10, 26, 9, 30, 0, 0, time.FixedZone("CST", 8*3600))})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireCRLF(t, raw)

	msg := parse(t, raw)
	if got := msg.Header.Get("From"); !strings.Contains(got, "ada@example.com") {
		t.Fatalf("From = %q", got)
	}
	if got := decodeHeader(t, msg.Header.Get("Subject")); got != draft.Subject {
		t.Fatalf("Subject = %q, want %q", got, draft.Subject)
	}
	if got := msg.Header.Get("Date"); !strings.Contains(got, "+0800") {
		t.Fatalf("Date = %q, want the supplied zone", got)
	}
	if id := msg.Header.Get("Message-ID"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Fatalf("Message-ID = %q", id)
	}
	if got := msg.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := msg.Header.Get("Content-Transfer-Encoding"); got != "base64" {
		t.Fatalf("Content-Transfer-Encoding = %q", got)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("Bcc header written without asking for it")
	}
	if got := readBody(t, msg.Body); got != draft.Body {
		t.Fatalf("body = %q, want %q", got, draft.Body)
	}

	cc, err := netmail.ParseAddressList(msg.Header.Get("Cc"))
	if err != nil || len(cc) != 1 || cc[0].Address != "cc@example.com" {
		t.Fatalf("Cc = %v/%v", cc, err)
	}
	to, err := netmail.ParseAddressList(msg.Header.Get("To"))
	if err != nil || len(to) != 1 || to[0].Name != "Doe, John" {
		t.Fatalf("To = %v/%v, want the quoted display name kept", to, err)
	}
}

func TestBuildBccHeader(t *testing.T) {
	draft := mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Bcc:     []mail.Address{{Name: "Blind", Email: "bcc@example.com"}},
		Subject: "copies",
		Body:    "hello",
	}
	from := mail.Address{Email: "from@example.com"}

	without, err := Build(draft, from, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(without), "bcc@example.com") {
		t.Fatal("Bcc address leaked into the message without a Bcc header")
	}

	with, err := Build(draft, from, Options{BccHeader: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, with)
	bcc, err := netmail.ParseAddressList(msg.Header.Get("Bcc"))
	if err != nil || len(bcc) != 1 || bcc[0].Address != "bcc@example.com" {
		t.Fatalf("Bcc = %v/%v", bcc, err)
	}
}

// TestBuildThreadsReply pins the two headers that make a receiving client file
// the reply under the conversation it answers.
func TestBuildThreadsReply(t *testing.T) {
	draft := mail.Draft{
		To:         []mail.Address{{Email: "to@example.com"}},
		Subject:    "Re: hello",
		Body:       "answer",
		InReplyTo:  "<b@example.com>",
		References: []string{"<a@example.com>", "<b@example.com>"},
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, raw)
	if got := msg.Header.Get("In-Reply-To"); got != "<b@example.com>" {
		t.Fatalf("In-Reply-To = %q", got)
	}
	if got := msg.Header.Get("References"); got != "<a@example.com> <b@example.com>" {
		t.Fatalf("References = %q", got)
	}
}

// TestBuildOmitsThreadHeaders pins that a new message and a forward carry no
// threading headers at all: an empty In-Reply-To would still create a thread.
func TestBuildOmitsThreadHeaders(t *testing.T) {
	draft := mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "Fwd: hello",
		Body:    "forwarded",
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, raw)
	if got := msg.Header.Get("In-Reply-To"); got != "" {
		t.Fatalf("In-Reply-To = %q, want none", got)
	}
	if got := msg.Header.Get("References"); got != "" {
		t.Fatalf("References = %q, want none", got)
	}
}

// TestBuildDropsMalformedThreadHeaders keeps a back-end header from injecting a
// header of its own or writing an ID the wire cannot carry.
func TestBuildDropsMalformedThreadHeaders(t *testing.T) {
	draft := mail.Draft{
		To:         []mail.Address{{Email: "to@example.com"}},
		Body:       "x",
		InReplyTo:  "<a@example.com>\r\nBcc: victim@example.com",
		References: []string{"", "junk", "<ok@example.com>"},
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, raw)
	if got := msg.Header.Get("In-Reply-To"); got != "" {
		t.Fatalf("In-Reply-To = %q, want the malformed value dropped", got)
	}
	if got := msg.Header.Get("References"); got != "<ok@example.com>" {
		t.Fatalf("References = %q", got)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatalf("injected header survived:\n%s", raw)
	}
}

// TestBuildFoldsReferences checks that a long ancestor chain is folded like any
// other header and still reads back as one list.
func TestBuildFoldsReferences(t *testing.T) {
	refs := []string{
		"<0000000000000000000001@mail.example.com>",
		"<0000000000000000000002@mail.example.com>",
		"<0000000000000000000003@mail.example.com>",
		"<0000000000000000000004@mail.example.com>",
	}
	draft := mail.Draft{
		To:         []mail.Address{{Email: "to@example.com"}},
		Body:       "x",
		InReplyTo:  refs[len(refs)-1],
		References: refs,
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireCRLF(t, raw)
	head := string(raw)
	if i := strings.Index(head, "\r\n\r\n"); i >= 0 {
		head = head[:i]
	}
	folded := false
	for _, line := range strings.Split(head, "\r\n") {
		if len(line) > foldWidth {
			t.Fatalf("header line of %d octets: %q", len(line), line)
		}
		if strings.HasPrefix(line, " ") {
			folded = true
		}
	}
	if !folded {
		t.Fatalf("References was not folded:\n%s", head)
	}
	if got, want := parse(t, raw).Header.Get("References"), strings.Join(refs, " "); got != want {
		t.Fatalf("References = %q, want %q", got, want)
	}
}

// TestBuildPrefersDraftIdentity pins that the composer's Date and Message-ID are
// the ones written: they are what its header panel shows.
func TestBuildPrefersDraftIdentity(t *testing.T) {
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.FixedZone("CST", 8*3600))
	draft := mail.Draft{
		To:        []mail.Address{{Email: "to@example.com"}},
		Body:      "x",
		Date:      when,
		MessageID: "<fixed@example.com>",
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{
		Date:      time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		MessageID: "<option@example.com>",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, raw)
	if got := msg.Header.Get("Message-ID"); got != "<fixed@example.com>" {
		t.Fatalf("Message-ID = %q, want the draft one", got)
	}
	if got := msg.Header.Get("Date"); got != when.Format(time.RFC1123Z) {
		t.Fatalf("Date = %q, want %q", got, when.Format(time.RFC1123Z))
	}
}

func TestBuildWithAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	content := strings.Repeat("attachment body line\n", 100)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	draft := mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "with a file",
		Body:    "see attached",
		Attachments: []mail.DraftAttachment{
			{Name: "notes.txt", Path: path},
			{Name: "名字.bin", Path: path},
		},
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireCRLF(t, raw)

	msg := parse(t, raw)
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("Content-Type = %q/%v, want multipart/mixed", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	first, err := mr.NextPart()
	if err != nil {
		t.Fatalf("first part: %v", err)
	}
	if got := readBody(t, first); got != draft.Body {
		t.Fatalf("text part = %q, want %q", got, draft.Body)
	}

	for i, want := range draft.Attachments {
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if got := part.FileName(); got != want.Name {
			t.Fatalf("part %d filename = %q, want %q", i, got, want.Name)
		}
		data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
		if err != nil {
			t.Fatalf("part %d decode: %v", i, err)
		}
		if string(data) != content {
			t.Fatalf("part %d content = %d bytes, want %d", i, len(data), len(content))
		}
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Fatalf("trailing part: %v", err)
	}
}

func TestBuildFoldsLongAddressList(t *testing.T) {
	draft := mail.Draft{
		To: []mail.Address{
			{Name: "Doe, John", Email: "john.doe@a-very-long-domain-name.example.com"},
			{Name: "张三", Email: "zhangsan@a-very-long-domain-name.example.com"},
			{Name: "Third Person", Email: "third@a-very-long-domain-name.example.com"},
			{Name: "Fourth Person", Email: "fourth@a-very-long-domain-name.example.com"},
			{Name: "Fifth Person", Email: "fifth@a-very-long-domain-name.example.com"},
		},
		Subject: strings.Repeat("很长的主题 ", 20),
		Body:    "hello",
	}
	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireCRLF(t, raw)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), crlf), crlf) {
		if len(line) > foldWidth && !strings.HasPrefix(line, " ") {
			t.Fatalf("header line %d octets long: %q", len(line), line)
		}
	}

	msg := parse(t, raw)
	to, err := netmail.ParseAddressList(msg.Header.Get("To"))
	if err != nil {
		t.Fatalf("parse To: %v", err)
	}
	if len(to) != len(draft.To) {
		t.Fatalf("To has %d addresses, want %d", len(to), len(draft.To))
	}
	if to[0].Name != "Doe, John" {
		t.Fatalf("first display name = %q, want the comma kept", to[0].Name)
	}
	if got := decodeHeader(t, msg.Header.Get("Subject")); got != draft.Subject {
		t.Fatalf("Subject = %q, want %q", got, draft.Subject)
	}
}

func TestBuildRejectsUnsendableDrafts(t *testing.T) {
	if _, err := Build(mail.Draft{}, mail.Address{Email: "from@example.com"}, Options{}); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("Build(no recipients) = %v, want ErrInvalidArgument", err)
	}
	if _, err := Build(mail.Draft{To: []mail.Address{{Email: "to@example.com"}}}, mail.Address{}, Options{}); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("Build(no sender) = %v, want ErrInvalidArgument", err)
	}
}

func TestBuildFallsBackToDraftFrom(t *testing.T) {
	draft := mail.Draft{
		From:    mail.Address{Name: "Ada", Email: "ada@example.com"},
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "fallback",
		Body:    "x",
	}
	raw, err := Build(draft, mail.Address{}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := parse(t, raw).Header.Get("From"); !strings.Contains(got, "ada@example.com") {
		t.Fatalf("From = %q", got)
	}
}

func TestBuildStripsHeaderInjection(t *testing.T) {
	draft := mail.Draft{
		To:      []mail.Address{{Name: "Evil\r\nBcc: victim@example.com", Email: "to@example.com"}},
		Subject: "hello\r\nBcc: victim@example.com",
		Body:    "x",
	}
	raw, err := Build(draft, mail.Address{Name: "Sender\nX: y", Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	msg := parse(t, raw)
	if msg.Header.Get("Bcc") != "" || msg.Header.Get("X") != "" {
		t.Fatalf("injected header survived:\n%s", raw)
	}
	if got := decodeHeader(t, msg.Header.Get("Subject")); strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("Subject = %q, want the line breaks removed", got)
	}
}

func TestBuildReportsMissingAttachment(t *testing.T) {
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Attachments: []mail.DraftAttachment{{Name: "gone.bin", Path: filepath.Join(t.TempDir(), "gone.bin")}},
	}
	if _, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{}); err == nil {
		t.Fatal("Build accepted a missing attachment")
	}
}

// parse reads the generated bytes back the way a receiving client would.
func parse(t *testing.T, raw []byte) *netmail.Message {
	t.Helper()
	msg, err := netmail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return msg
}

// readBody decodes a base64 transfer encoded body.
func readBody(t *testing.T, r io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return string(data)
}

func decodeHeader(t *testing.T, raw string) string {
	t.Helper()
	dec := new(mime.WordDecoder)
	out, err := dec.DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decode header %q: %v", raw, err)
	}
	return out
}

// requireCRLF checks that no line ending is a bare LF, which some relays reject.
func requireCRLF(t *testing.T, raw []byte) {
	t.Helper()
	for i, b := range raw {
		if b == '\n' && (i == 0 || raw[i-1] != '\r') {
			t.Fatalf("bare LF at offset %d", i)
		}
	}
}
