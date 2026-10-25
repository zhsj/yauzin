package gmail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// captureLogs replaces the default logger with a text handler on a buffer at
// the given level and restores the previous logger when the test ends.
func captureLogs(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// failingTransport is a request that never reaches a server.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed")
}

func TestLogAccountDropsTheBackendPrefix(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"gmail:user@example.com", "user@example.com"},
		{"user@example.com", "user@example.com"},
		{"", "-"},
	} {
		if got := logAccount(tc.id); got != tc.want {
			t.Errorf("logAccount(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestSingleRequestsAreLogged pins a single REST call: the label is the
// address, the endpoint the API path without the host, and no status line is
// written when the round trip itself failed.
func TestSingleRequestsAreLogged(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[]}`)
	}))

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	line := buf.String()
	for _, want := range []string{
		"gmail request",
		"account=user@example.com",
		"method=GET",
		"endpoint=/users/me/labels",
		"status=200",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "gmail:") {
		t.Errorf("log kept the backend prefix: %q", line)
	}
}

// TestBatchRequestsAreLogged names what a batch does: the parent path of the
// embedded calls and how many there are, instead of the opaque /batch URL.
func TestBatchRequestsAreLogged(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body, "t2": t2Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"t2"}]}`)
	})

	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	line := buf.String()
	for _, want := range []string{
		"gmail batch",
		"account=user@example.com",
		"endpoint=/users/me/threads",
		"calls=2",
		"status=200",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "endpoint=/batch") {
		t.Errorf("batch logged its envelope endpoint: %q", line)
	}
}

// TestRequestTransportFailureIsLogged makes a failed round trip report the
// error and no status: the request never got an answer.
func TestRequestTransportFailureIsLogged(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	svc, _ := newTestService(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		WithHTTPClient(&http.Client{Transport: failingTransport{}}))

	if _, err := svc.Folders(context.Background()); err == nil {
		t.Fatal("Folders succeeded with a failing transport")
	}

	line := buf.String()
	for _, want := range []string{
		"gmail request",
		"account=user@example.com",
		"endpoint=/users/me/labels",
		"err=",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "status=") {
		t.Errorf("a failed request must not report a status: %q", line)
	}
}

func TestRequestsAreNotLoggedAtTheInfoLevel(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[]}`)
	}))

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("request logged at the info level: %q", buf.String())
	}
}
