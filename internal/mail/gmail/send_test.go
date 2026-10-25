package gmail

import (
	"bytes"
	"context"
	"errors"
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
	var (
		gotPath  string
		gotQuery string
		gotType  string
		gotAuth  string
		body     []byte
	)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("uploadType")
		gotType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		writeJSON(t, w, `{"id":"sent-1","threadId":"thread-1"}`)
	}))

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

	if want := "/upload/users/me/messages/send"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotQuery != "media" {
		t.Fatalf("uploadType = %q, want media", gotQuery)
	}
	if gotType != "message/rfc822" {
		t.Fatalf("Content-Type = %q, want message/rfc822", gotType)
	}
	if gotAuth != "Bearer access-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}

	msg, err := netmail.ReadMessage(bytes.NewReader(body))
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
	var hit int
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		writeJSON(t, w, `{"id":"sent-1"}`)
	}))
	if err := svc.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hit != 1 {
		t.Fatalf("API calls = %d, want one", hit)
	}
}

// TestSendRetriesRejectedToken covers the shared 401 path for the media upload:
// the request is replayed with a refreshed token.
func TestSendRetriesRejectedToken(t *testing.T) {
	var tokens []string
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"access_token":"fresh-token","token_type":"Bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/upload/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(tokens) == 1 {
			writeErrorJSON(t, w, http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)
			return
		}
		writeJSON(t, w, `{"id":"sent-1"}`)
	})
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
		t.Fatalf("requests = %d, want a retry", len(tokens))
	}
	if tokens[1] != "Bearer fresh-token" {
		t.Fatalf("retry Authorization = %q, want the refreshed token", tokens[1])
	}
}
