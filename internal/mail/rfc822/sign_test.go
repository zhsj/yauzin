package rfc822

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"github.com/zhsj/yauzin/internal/mail"
)

// fakeSigner stands in for gpg: it keeps the entity it was handed so a test can
// compare it with what Prepare reproduces at send time.
type fakeSigner struct {
	micalg string
	entity []byte
	err    error
	// empty makes the signer succeed with no signature, the way a broken gpg
	// wrapper would.
	empty bool
}

func (f *fakeSigner) SignDetached(_ context.Context, r io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	f.entity = data
	if f.err != nil {
		return nil, "", f.err
	}
	if f.empty {
		return nil, f.micalg, nil
	}
	return []byte("-----BEGIN PGP SIGNATURE-----\n\nAAAA\n-----END PGP SIGNATURE-----\n"), f.micalg, nil
}

func (f *fakeSigner) Key() string { return "0123456789ABCDEF" }

func newFakeSigner() *fakeSigner { return &fakeSigner{micalg: "pgp-sha256"} }

// signDraft signs draft with the fake and returns the draft carrying the
// signature, the way the composer does before a send.
func signDraft(t *testing.T, draft mail.Draft, s *fakeSigner) mail.Draft {
	t.Helper()
	sig, err := Sign(context.Background(), draft, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	draft.Signature = sig
	return draft
}

// signedMessage is a multipart/signed message taken apart.
type signedMessage struct {
	header netmail.Header
	params map[string]string
	body   partData
	sig    partData
}

// partData is one MIME part: its headers and its raw content. The content has to
// be read before the next part is requested, because advancing a multipart
// reader closes the previous part.
type partData struct {
	header netmail.Header
	raw    []byte
}

// signedParts parses a multipart/signed message back into its two parts.
func signedParts(t *testing.T, raw []byte) signedMessage {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	if mediaType != "multipart/signed" {
		t.Fatalf("Content-Type = %q, want multipart/signed", mediaType)
	}
	if params["protocol"] != "application/pgp-signature" {
		t.Fatalf("protocol = %q", params["protocol"])
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	read := func(what string) partData {
		p, err := mr.NextPart()
		if err != nil {
			t.Fatalf("%s part: %v", what, err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("read %s part: %v", what, err)
		}
		return partData{header: netmail.Header(p.Header), raw: data}
	}
	out := signedMessage{header: msg.Header, params: params}
	out.body = read("first")
	out.sig = read("second")
	if _, err := mr.NextPart(); err != io.EOF {
		t.Fatalf("third part: %v, want io.EOF", err)
	}
	return out
}

func TestSignRecordsTheSignedEntity(t *testing.T) {
	s := newFakeSigner()
	draft := mail.Draft{To: []mail.Address{{Email: "to@example.com"}}, Body: "hello"}
	sig, err := Sign(context.Background(), draft, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sum := sha256.Sum256(s.entity)
	if sig.Digest != sum {
		t.Fatal("Digest does not cover the entity the signer read")
	}
	// The CRLF after the base64 line belongs to the boundary that follows it,
	// so the signed bytes stop before it.
	want := "Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"aGVsbG8="
	if string(s.entity) != want {
		t.Fatalf("signed entity = %q, want %q", s.entity, want)
	}
	if sig.Micalg != "pgp-sha256" || sig.Key != "0123456789ABCDEF" {
		t.Fatalf("signature record = %+v", sig)
	}
	if sig.Boundary != "" {
		t.Fatalf("Boundary = %q, want none without attachments", sig.Boundary)
	}
	if bytes.Contains(sig.Data, []byte("\n\n")) || !bytes.Contains(sig.Data, []byte("\r\n\r\n")) {
		t.Fatalf("armored signature = %q, want CRLF line endings", sig.Data)
	}
	if !bytes.HasSuffix(sig.Data, []byte("\r\n")) {
		t.Fatalf("armored signature = %q, want a final CRLF", sig.Data)
	}
}

func TestPrepareWrapsSignedMessage(t *testing.T) {
	draft := mail.Draft{To: []mail.Address{{Email: "to@example.com"}}, Subject: "hi", Body: "hello"}
	s := newFakeSigner()
	draft = signDraft(t, draft, s)

	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{MessageID: "<m1@example.com>"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := signedParts(t, raw)

	if got.header.Get("Subject") != "hi" {
		t.Fatalf("Subject = %q, want it outside the signed part", got.header.Get("Subject"))
	}
	if got.params["micalg"] != "pgp-sha256" {
		t.Fatalf("micalg = %q", got.params["micalg"])
	}
	if ct := got.body.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("first part Content-Type = %q", ct)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(string(got.body.raw)), "\r\n", ""))
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("body = %q", decoded)
	}
	if ct := got.sig.header.Get("Content-Type"); ct != "application/pgp-signature" {
		t.Fatalf("second part Content-Type = %q", ct)
	}
	if !strings.HasPrefix(string(got.sig.raw), "-----BEGIN PGP SIGNATURE-----") {
		t.Fatalf("signature part = %q", got.sig.raw)
	}
}

func TestSignedSizeMatchesBuild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("attached"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Body:        "body",
		Attachments: []mail.DraftAttachment{{Name: "note.txt", Path: path}},
	}
	draft = signDraft(t, draft, newFakeSigner())

	msg, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var b bytes.Buffer
	if _, err := msg.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if int64(b.Len()) != msg.Size() {
		t.Fatalf("Size = %d, wrote %d", msg.Size(), b.Len())
	}
	got := signedParts(t, b.Bytes())
	mediaType, params, err := mime.ParseMediaType(got.body.header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("inner Content-Type: %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("inner media type = %q", mediaType)
	}
	// The inner boundary has to be the one the signature was made with, or the
	// signed bytes would not be reproducible.
	if params["boundary"] != draft.Signature.Boundary {
		t.Fatalf("inner boundary = %q, want %q", params["boundary"], draft.Signature.Boundary)
	}
	if got.params["boundary"] == params["boundary"] {
		t.Fatalf("outer boundary = %q, want one of its own", got.params["boundary"])
	}
}

// TestSignedBytesAreWhatAParserRecovers is the regression the wire format needs:
// a verifier takes the message apart and checks the signature against the bytes
// of the first part. The CRLF before the boundary belongs to the boundary
// (RFC 2046 §5.1.1), so the signed bytes must not include it.
func TestSignedBytesAreWhatAParserRecovers(t *testing.T) {
	s := newFakeSigner()
	draft := mail.Draft{To: []mail.Address{{Email: "to@example.com"}}, Body: "hello"}
	sig, err := Sign(context.Background(), draft, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	draft.Signature = sig

	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := signedParts(t, raw)
	part := firstPartBytes(t, raw)

	if !bytes.Equal(part, s.entity) {
		t.Fatalf("the receiver sees %q, the signer was given %q", part, s.entity)
	}
	if sum := sha256.Sum256(part); sum != sig.Digest {
		t.Fatal("Digest does not cover the bytes a verifier recovers")
	}
	if !bytes.HasPrefix(part, []byte("Content-Type: text/plain")) {
		t.Fatalf("first part = %q, want the entity header included", part)
	}
	if bytes.HasSuffix(part, []byte("\r\n")) {
		t.Fatalf("signed bytes end with the CRLF that belongs to the boundary: %q", part)
	}

	// Guard against the mistake the old code made: signing the body only, or
	// signing the entity with the boundary's CRLF, both look plausible here.
	if !bytes.Contains(part, []byte("Content-Transfer-Encoding: base64")) {
		t.Fatalf("signed bytes lost the entity headers: %q", part)
	}
	if got.body.header.Get("Content-Transfer-Encoding") != "base64" {
		t.Fatalf("part headers = %v", got.body.header)
	}
}

// firstPartBytes cuts the first part out of a multipart/signed message the way a
// receiver does: everything between the boundary lines, without the CRLF that
// precedes the next boundary delimiter.
func firstPartBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type: %v", err)
	}
	body, err := io.ReadAll(msg.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	opening := []byte("--" + params["boundary"] + "\r\n")
	start := bytes.Index(body, opening)
	if start < 0 {
		t.Fatalf("boundary line not found in %q", body)
	}
	rest := body[start+len(opening):]
	end := bytes.Index(rest, []byte("\r\n--"+params["boundary"]))
	if end < 0 {
		t.Fatalf("closing boundary line not found after the first part")
	}
	return rest[:end]
}

// TestSignedBytesWithAttachments repeats the round trip for the nested case: the
// signed entity is a multipart/mixed whose own closing line ends the signed data.
func TestSignedBytesWithAttachments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("attached"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	s := newFakeSigner()
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Body:        "body",
		Attachments: []mail.DraftAttachment{{Name: "note.txt", Path: path}},
	}
	sig, err := Sign(context.Background(), draft, s)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	draft.Signature = sig

	raw, err := Build(draft, mail.Address{Email: "from@example.com"}, Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	part := firstPartBytes(t, raw)

	if !bytes.Equal(part, s.entity) {
		t.Fatalf("the receiver sees %q, the signer was given %q", part, s.entity)
	}
	if sum := sha256.Sum256(part); sum != sig.Digest {
		t.Fatal("Digest does not cover the bytes a verifier recovers")
	}
	if !bytes.HasPrefix(part, []byte(`Content-Type: multipart/mixed; boundary="`)) {
		t.Fatalf("signed bytes = %q, want the inner entity header first", tailOf(part, 60))
	}
	wantTail := "--" + sig.Boundary + "--"
	if !strings.HasSuffix(string(part), wantTail) {
		t.Fatalf("signed bytes end with %q, want the inner closing line %q", tailOf(part, 40), wantTail)
	}
	if strings.HasSuffix(string(part), wantTail+"\r\n") {
		t.Fatal("the CRLF after the inner closing line belongs to the outer boundary, not the entity")
	}
}

// tailOf is the last n bytes of b, for failure messages.
func tailOf(b []byte, n int) string {
	if len(b) < n {
		return string(b)
	}
	return string(b[len(b)-n:])
}

func TestPrepareRejectsStaleSignature(t *testing.T) {
	draft := mail.Draft{To: []mail.Address{{Email: "to@example.com"}}, Body: "hello"}
	draft = signDraft(t, draft, newFakeSigner())
	// The body changed after signing: the composer clears the signature, but a
	// renderer that is handed one anyway must refuse rather than send it.
	draft.Body = "hello again"

	_, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
	if !errors.Is(err, mail.ErrSignatureStale) {
		t.Fatalf("Prepare = %v, want ErrSignatureStale", err)
	}
}

func TestPrepareRejectsRewrittenAttachment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("aaaa"), 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Body:        "body",
		Attachments: []mail.DraftAttachment{{Name: "note.txt", Path: path}},
	}
	draft = signDraft(t, draft, newFakeSigner())
	// Same size, different content: only the digest can notice this.
	if err := os.WriteFile(path, []byte("bbbb"), 0o600); err != nil {
		t.Fatalf("rewrite attachment: %v", err)
	}

	_, err := Prepare(draft, mail.Address{Email: "from@example.com"}, Options{})
	if !errors.Is(err, mail.ErrSignatureStale) {
		t.Fatalf("Prepare = %v, want ErrSignatureStale", err)
	}
}

func TestSignReportsSignerFailure(t *testing.T) {
	failure := errors.New("pinentry closed")
	s := newFakeSigner()
	s.err = failure
	if _, err := Sign(context.Background(), mail.Draft{Body: "x"}, s); !errors.Is(err, failure) {
		t.Fatalf("Sign = %v, want the signer's error", err)
	}
}

func TestSignRejectsEmptySignature(t *testing.T) {
	s := &fakeSigner{micalg: "pgp-sha256", empty: true}
	if _, err := Sign(context.Background(), mail.Draft{Body: "x"}, s); err == nil {
		t.Fatal("Sign accepted an empty signature")
	}
}
