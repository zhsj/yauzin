package gmail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestLoggingTransportReportsEndpointAccountAndStatus(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()

	client := withRequestLogging(srv.Client(), testAccountID)
	resp, err := client.Get(srv.URL + "/gmail/v1/users/me/threads?q=secret-token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()

	line := buf.String()
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, want := range []string{
		"level=DEBUG",
		"gmail request",
		"account=" + testAccountID,
		"method=GET",
		"endpoint=" + host + "/gmail/v1/users/me/threads",
		"status=418",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "secret-token") {
		t.Errorf("log leaked the query string: %q", line)
	}
}

func TestLoggingTransportReportsTransportFailure(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	client := withRequestLogging(&http.Client{Transport: failingTransport{}}, "")

	if _, err := client.Get("https://gmail.googleapis.com/gmail/v1/users/me/profile"); err == nil {
		t.Fatal("Get succeeded with a failing transport")
	}

	line := buf.String()
	for _, want := range []string{
		"account=-",
		"endpoint=gmail.googleapis.com/gmail/v1/users/me/profile",
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

func TestLoggingTransportHonorsTheLevel(t *testing.T) {
	buf := captureLogs(t, slog.LevelInfo)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := withRequestLogging(srv.Client(), testAccountID).Get(srv.URL + "/users/me/labels")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()

	if buf.Len() != 0 {
		t.Fatalf("request logged at the info level: %q", buf.String())
	}
}

// TestWithRequestLoggingKeepsTheClient checks that the wrapper only ever
// returns a copy: the shared default client and an injected client keep their
// own transport.
func TestWithRequestLoggingKeepsTheClient(t *testing.T) {
	in := &http.Client{Timeout: 3 * time.Second}
	out := withRequestLogging(in, testAccountID)

	if in.Transport != nil {
		t.Error("the injected client was rewritten")
	}
	if out == in {
		t.Error("withRequestLogging returned the client it was given")
	}
	if out.Transport == nil {
		t.Error("the copy has no logging transport")
	}
	if out.Timeout != in.Timeout {
		t.Errorf("timeout = %v, want %v", out.Timeout, in.Timeout)
	}

	withRequestLogging(http.DefaultClient, testAccountID)
	if http.DefaultClient.Transport != nil {
		t.Error("http.DefaultClient got a transport")
	}
}

// TestServiceRequestsAreLogged pins the integration: the client behind a
// Service reports its calls with the account ID.
func TestServiceRequestsAreLogged(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[]}`)
	}))

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	line := buf.String()
	for _, want := range []string{
		"account=" + testAccountID,
		"method=GET",
		"/users/me/labels",
		"status=200",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q does not contain %q", line, want)
		}
	}
}
