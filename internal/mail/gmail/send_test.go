package gmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"strings"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestSendUploadsRawMessage(t *testing.T) {
	rec := &uploadRecorder{t: t}
	svc, _ := newTestService(t, rec.handler())

	draft := mail.Draft{
		From:    mail.Address{Name: "Ignored", Email: "other@example.com"},
		To:      []mail.Address{{Email: "to@example.com"}},
		Bcc:     []mail.Address{{Email: "bcc@example.com"}},
		Subject: "发送测试",
		Body:    "hello there",
	}
	if err := svc.Send(context.Background(), draft); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if rec.initCalls != 1 || rec.putCalls != 1 {
		t.Fatalf("init/put calls = %d/%d, want 1/1", rec.initCalls, rec.putCalls)
	}
	if rec.contentType != "message/rfc822" {
		t.Fatalf("X-Upload-Content-Type = %q, want message/rfc822", rec.contentType)
	}
	if got, want := int64(rec.received.Len()), rec.total; got != want {
		t.Fatalf("uploaded %d bytes, want the declared %d", got, want)
	}

	msg, err := netmail.ReadMessage(bytes.NewReader(rec.received.Bytes()))
	if err != nil {
		t.Fatalf("parse uploaded message: %v", err)
	}
	from, err := netmail.ParseAddressList(msg.Header.Get("From"))
	if err != nil || len(from) != 1 || from[0].Address != "user@example.com" {
		t.Fatalf("From = %v/%v, want the account address", from, err)
	}
	if bcc := msg.Header.Get("Bcc"); !strings.Contains(bcc, "bcc@example.com") {
		t.Fatalf("Bcc = %q, want the blind recipient in the raw message", bcc)
	}
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != draft.Subject {
		t.Fatalf("Subject = %q/%v, want %q", subject, err, draft.Subject)
	}
}

func TestSendRejectsDraftWithoutRecipients(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Send reached the API with nobody to send to")
	}))
	err := svc.Send(context.Background(), mail.Draft{Subject: "nobody"})
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("Send = %v, want ErrInvalidArgument", err)
	}
}

func TestSendMapsMissingScopeToUnauthorized(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusForbidden,
			`{"error":{"code":403,"message":"Insufficient Permission","errors":[{"reason":"insufficientPermissions"}]}}`)
	}))
	err := svc.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	})
	if !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("Send = %v, want ErrUnauthorized", err)
	}
}

// TestSendIsNotUnsupported guards the capability the composer relies on: the
// backend used to answer ErrUnsupported for every send.
func TestSendIsNotUnsupported(t *testing.T) {
	rec := &uploadRecorder{t: t}
	svc, _ := newTestService(t, rec.handler())
	if err := svc.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.initCalls != 1 || rec.putCalls != 1 {
		t.Fatalf("API calls = %d/%d, want a session and one chunk", rec.initCalls, rec.putCalls)
	}
}

// TestSendRetriesRejectedToken covers the shared 401 path: the session request
// is replayed with a refreshed token.
func TestSendRetriesRejectedToken(t *testing.T) {
	var tokens []string
	rec := &uploadRecorder{t: t}
	rec.initFunc = func(w http.ResponseWriter, r *http.Request) bool {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
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
		func(o *options) { o.tokenURL = srv.URL + "/token" },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if err := svc.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("session requests = %d, want a retry", len(tokens))
	}
	if tokens[1] != "Bearer fresh-token" {
		t.Fatalf("retry Authorization = %q, want the refreshed token", tokens[1])
	}
}

// TestSendMapsOversizeToInvalidArgument keeps the server's size rejection a
// categorised failure instead of an opaque protocol error.
func TestSendMapsOversizeToInvalidArgument(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusRequestEntityTooLarge,
			`{"error":{"code":413,"message":"Request payload size exceeds the limit"}}`)
	}))
	err := svc.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	})
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("Send = %v, want ErrInvalidArgument", err)
	}
}

// uploadRecorder answers a resumable session, recording what arrives so tests
// can reconstruct the message.
type uploadRecorder struct {
	t           *testing.T
	initCalls   int
	putCalls    int
	statusCalls int
	total       int64
	contentType string
	received    bytes.Buffer

	// initFunc may answer the session request itself; true means it did.
	initFunc func(w http.ResponseWriter, r *http.Request) bool
	// putFunc may answer a data request itself; true means it did.
	putFunc func(w http.ResponseWriter, r *http.Request, data []byte, start, end int64) bool
}

func (u *uploadRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadType") == "resumable":
			u.initCalls++
			u.contentType = r.Header.Get("X-Upload-Content-Type")
			u.total = parseInt64(u.t, r.Header.Get("X-Upload-Content-Length"))
			if u.initFunc != nil && u.initFunc(w, r) {
				return
			}
			w.Header().Set("Location", "/session/1")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/session/1":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				u.t.Errorf("read chunk: %v", err)
			}
			if strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */") {
				u.statusCalls++
				u.reportRange(w)
				return
			}
			start, end := parseContentRange(u.t, r.Header.Get("Content-Range"))
			u.putCalls++
			if u.putFunc != nil && u.putFunc(w, r, data, start, end) {
				return
			}
			_, _ = u.received.Write(data)
			if end+1 < u.total {
				u.reportRange(w)
				return
			}
			writeJSON(u.t, w, `{"id":"sent-1","threadId":"thread-1"}`)
		default:
			u.t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// reportRange answers a 308 with everything stored so far.
func (u *uploadRecorder) reportRange(w http.ResponseWriter) {
	if u.received.Len() > 0 {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", u.received.Len()-1))
	}
	w.WriteHeader(statusResumeIncomplete)
}

// parseContentRange reads "bytes start-end/total".
func parseContentRange(t *testing.T, value string) (start, end int64) {
	t.Helper()
	var total int64
	if _, err := fmt.Sscanf(value, "bytes %d-%d/%d", &start, &end, &total); err != nil {
		t.Fatalf("Content-Range %q: %v", value, err)
	}
	return start, end
}

func parseInt64(t *testing.T, value string) int64 {
	t.Helper()
	var n int64
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		t.Fatalf("integer %q: %v", value, err)
	}
	return n
}
