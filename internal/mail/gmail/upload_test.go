package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// testPayload is an in-memory payload for the uploader tests, which do not
// need a real message.
type testPayload struct {
	data      []byte
	failAfter int
}

func (p testPayload) Size() int64 { return int64(len(p.data)) }

func (p testPayload) WriteTo(w io.Writer) (int64, error) {
	if p.failAfter <= 0 || p.failAfter >= len(p.data) {
		n, err := w.Write(p.data)
		return int64(n), err
	}
	n, err := w.Write(p.data[:p.failAfter])
	if err != nil {
		return int64(n), err
	}
	return int64(n), errors.New("render failed")
}

func TestResumableUploadChunks(t *testing.T) {
	shrinkUploadChunk(t)
	rec := &uploadRecorder{t: t}
	svc, _ := newTestService(t, rec.handler())

	payload := testPayload{data: bytes.Repeat([]byte("0123456789abcdef"), 600<<10/16)}
	if err := svc.c.doResumableUpload(context.Background(), "test", "/users/me/messages/send", "message/rfc822", payload, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if rec.putCalls != 3 {
		t.Fatalf("chunks = %d, want 3", rec.putCalls)
	}
	if rec.total != payload.Size() {
		t.Fatalf("declared size = %d, want %d", rec.total, payload.Size())
	}
	if !bytes.Equal(rec.received.Bytes(), payload.data) {
		t.Fatalf("received %d bytes, want %d", rec.received.Len(), len(payload.data))
	}
}

func TestResumableUploadResumesPartialChunk(t *testing.T) {
	shrinkUploadChunk(t)
	rec := &uploadRecorder{t: t}
	var calls int
	rec.putFunc = func(w http.ResponseWriter, r *http.Request, data []byte, start, end int64) bool {
		calls++
		if calls > 1 {
			return false
		}
		half := len(data) / 2
		_, _ = rec.received.Write(data[:half])
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", start+int64(half)-1))
		w.WriteHeader(statusResumeIncomplete)
		return true
	}
	svc, _ := newTestService(t, rec.handler())

	payload := testPayload{data: bytes.Repeat([]byte("x"), 400<<10)}
	if err := svc.c.doResumableUpload(context.Background(), "test", "/users/me/messages/send", "message/rfc822", payload, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if rec.statusCalls != 0 {
		t.Fatalf("status queries = %d, want none", rec.statusCalls)
	}
	if !bytes.Equal(rec.received.Bytes(), payload.data) {
		t.Fatalf("received %d bytes, want %d", rec.received.Len(), len(payload.data))
	}
}

func TestResumableUploadResumesAfterServerError(t *testing.T) {
	shrinkUploadChunk(t)
	setUploadRetryDelay(t, 0)
	rec := &uploadRecorder{t: t}
	var calls int
	rec.putFunc = func(w http.ResponseWriter, r *http.Request, data []byte, start, end int64) bool {
		calls++
		if calls > 1 {
			return false
		}
		half := len(data) / 2
		_, _ = rec.received.Write(data[:half])
		writeErrorJSON(t, w, http.StatusServiceUnavailable, `{"error":{"code":503,"message":"backend error"}}`)
		return true
	}
	svc, _ := newTestService(t, rec.handler())

	payload := testPayload{data: bytes.Repeat([]byte("y"), 400<<10)}
	if err := svc.c.doResumableUpload(context.Background(), "test", "/users/me/messages/send", "message/rfc822", payload, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if rec.statusCalls != 1 {
		t.Fatalf("status queries = %d, want 1", rec.statusCalls)
	}
	if !bytes.Equal(rec.received.Bytes(), payload.data) {
		t.Fatalf("received %d bytes, want %d", rec.received.Len(), len(payload.data))
	}
}

func TestResumableUploadRenewsTokenOnChunk(t *testing.T) {
	shrinkUploadChunk(t)
	var tokens []string
	rec := &uploadRecorder{t: t}
	var calls int
	rec.putFunc = func(w http.ResponseWriter, r *http.Request, data []byte, start, end int64) bool {
		tokens = append(tokens, r.Header.Get("Authorization"))
		calls++
		if calls == 1 {
			writeErrorJSON(t, w, http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)
			return true
		}
		return false
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"access_token":"fresh-token","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/", rec.handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	store := newFakeStore(testAccountID, "access-token")
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail, Email: "user@example.com"}, store,
		WithBaseURL(srv.URL),
		withTokenURL(srv.URL+"/token"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if err := svc.c.doResumableUpload(context.Background(), "test", "/users/me/messages/send", "message/rfc822", testPayload{data: []byte("message")}, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("chunk requests = %d, want a retry", len(tokens))
	}
	if tokens[1] != "Bearer fresh-token" {
		t.Fatalf("retry Authorization = %q, want the refreshed token", tokens[1])
	}
}

func TestResumableUploadReportsRendererError(t *testing.T) {
	shrinkUploadChunk(t)
	rec := &uploadRecorder{t: t}
	svc, _ := newTestService(t, rec.handler())

	payload := testPayload{data: bytes.Repeat([]byte("z"), 400<<10), failAfter: 200 << 10}
	err := svc.c.doResumableUpload(context.Background(), "test", "/users/me/messages/send", "message/rfc822", payload, nil)
	if err == nil {
		t.Fatal("upload succeeded although rendering failed")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("render")) && !bytes.Contains([]byte(err.Error()), []byte("read message")) {
		t.Fatalf("error = %v, want the render failure", err)
	}
}

// TestSendStreamsAttachment covers the whole path: a prepared message with a
// file is uploaded in chunks and reassembles into the same MIME structure.
func TestSendStreamsAttachment(t *testing.T) {
	shrinkUploadChunk(t)
	rec := &uploadRecorder{t: t}
	svc, _ := newTestService(t, rec.handler())

	dir := t.TempDir()
	path := filepath.Join(dir, "report.bin")
	content := bytes.Repeat([]byte("attachment-"), 30<<10)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	draft := mail.Draft{
		To:          []mail.Address{{Email: "to@example.com"}},
		Subject:     "with a file",
		Body:        "see attached",
		Attachments: []mail.DraftAttachment{{Name: "report.bin", Path: path}},
	}
	if err := svc.Send(context.Background(), draft); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.putCalls < 2 {
		t.Fatalf("chunks = %d, want more than one", rec.putCalls)
	}
	msg := parseTestMessage(t, rec.received.Bytes())
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type: %v", err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	if _, err := mr.NextPart(); err != nil {
		t.Fatalf("text part: %v", err)
	}
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("attachment part: %v", err)
	}
	got, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
	if err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("attachment = %d bytes, want %d", len(got), len(content))
	}
}

func shrinkUploadChunk(t *testing.T) {
	t.Helper()
	old := uploadChunkSize
	uploadChunkSize = 256 << 10
	t.Cleanup(func() { uploadChunkSize = old })
}

func setUploadRetryDelay(t *testing.T, d time.Duration) {
	t.Helper()
	old := uploadRetryDelay
	uploadRetryDelay = d
	t.Cleanup(func() { uploadRetryDelay = old })
}

// parseTestMessage reads the uploaded bytes back the way a receiving client
// would.
func parseTestMessage(t *testing.T, raw []byte) *netmail.Message {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	return msg
}
