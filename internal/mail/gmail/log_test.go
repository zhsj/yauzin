package gmail

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

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

// quotaLines returns the log lines that belong to the named message.
func quotaLines(buf *bytes.Buffer, msg string) []string {
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, `msg="`+msg+`"`) {
			out = append(out, line)
		}
	}
	return out
}

// TestSingleRequestLogsQuotaSpend pins the merged line: a request reports what
// it cost and where the window stood right after, so one line answers both
// questions.
func TestSingleRequestLogsQuotaSpend(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[]}`)
	}), withQuotaPerMinute(6000))

	ctx := context.Background()
	if _, err := svc.Folders(ctx); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	svc.cache.folders.clear()
	if _, err := svc.Folders(ctx); err != nil {
		t.Fatalf("Folders again: %v", err)
	}

	lines := quotaLines(buf, "gmail request")
	if len(lines) != 2 {
		t.Fatalf("request lines = %d, want 2", len(lines))
	}
	for _, want := range []string{"units=1 ", "quota=1/5700", "wait=0s", "status=200"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("first line %q does not contain %q", lines[0], want)
		}
	}
	// The second call sees the first one's spending.
	if !strings.Contains(lines[1], "quota=2/5700") {
		t.Errorf("second line %q does not report the accumulated spend", lines[1])
	}
	if n := len(quotaLines(buf, "gmail quota wait")); n != 0 {
		t.Errorf("a request that did not wait still logged a wait (%d)", n)
	}
}

// TestBatchRequestLogsQuotaSpend checks a batch reports the price of the whole
// envelope, not of one embedded call.
func TestBatchRequestLogsQuotaSpend(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body, "t2": t2Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"t2"}]}`)
	})
	// newThreadService uses the default budget; keep it and price the batch:
	// one unit for the envelope plus 40 per conversation.
	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	lines := quotaLines(buf, "gmail batch")
	if len(lines) != 1 {
		t.Fatalf("batch lines = %d, want 1", len(lines))
	}
	for _, want := range []string{"calls=2", "units=81 "} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("batch line %q does not contain %q", lines[0], want)
		}
	}
	if !strings.Contains(lines[0], "/") || !strings.Contains(lines[0], "wait=") {
		t.Errorf("batch line %q misses the window state", lines[0])
	}
}

// TestLogRequestOmitsQuotaWhenNothingWasCharged covers the upload chunks: the
// session paid the send once, so a chunk line keeps its old shape.
func TestLogRequestOmitsQuotaWhenNothingWasCharged(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	logRequest("gmail:user@example.com", http.MethodPut, "/upload/session", quotaSpend{},
		&http.Response{StatusCode: http.StatusCreated}, nil)

	lines := quotaLines(buf, "gmail request")
	if len(lines) != 1 {
		t.Fatalf("request lines = %d, want 1", len(lines))
	}
	for _, unwanted := range []string{"units=", "quota=", "wait="} {
		if strings.Contains(lines[0], unwanted) {
			t.Errorf("uncharged line %q carries %q", lines[0], unwanted)
		}
	}

	// The charged form renders all three.
	logRequest("gmail:user@example.com", http.MethodPost, "/users/me/messages/send", quotaSpend{
		units: 100, used: 100, capacity: 5700, waited: 250 * time.Millisecond,
	}, &http.Response{StatusCode: http.StatusOK}, nil)
	lines = quotaLines(buf, "gmail request")
	if !strings.Contains(lines[1], "units=100 ") || !strings.Contains(lines[1], "quota=100/5700") ||
		!strings.Contains(lines[1], "wait=250ms") {
		t.Errorf("charged line %q misses the quota fields", lines[1])
	}
}

// TestQuotaWaitIsLoggedBeforeTheRequest pins the line a stalled request writes
// before it sleeps: the request line that follows is DEBUG and says nothing
// until the call is sent, so without this one a user watching a frozen list at
// the default info level sees no reason for the delay.
func TestQuotaWaitIsLoggedBeforeTheRequest(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[]}`)
	}), withQuotaPerMinute(600))
	clock := newFakeClock()
	svc.c.budget.now = clock.now
	svc.c.budget.sleep = clock.sleep
	// Fill the window so the next call has to wait for it to slide.
	if _, err := svc.c.budget.reserve(context.Background(), 570, classInteractive, nil); err != nil {
		t.Fatalf("drain: %v", err)
	}

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	waits := quotaLines(buf, "gmail quota wait")
	if len(waits) != 1 {
		t.Fatalf("wait lines = %d, want 1", len(waits))
	}
	for _, want := range []string{
		"level=INFO",
		"account=user@example.com",
		"op=labels.list",
		"units=1 ",
		"quota=570/570",
		"wait=1m0s",
	} {
		if !strings.Contains(waits[0], want) {
			t.Errorf("wait line %q does not contain %q", waits[0], want)
		}
	}
	// The line explains the delay before the call it delays shows up.
	all := buf.String()
	if i, j := strings.Index(all, "gmail quota wait"), strings.Index(all, "gmail request"); i < 0 || j < 0 || i > j {
		t.Errorf("wait line is not logged before the request (%d vs %d): %q", i, j, all)
	}
}

// TestBackgroundQuotaSkipIsLogged checks a dropped background request still
// leaves a trace, now with the wait it refused to take.
func TestBackgroundQuotaSkipIsLogged(t *testing.T) {
	svc, _ := newTestService(t, http.NewServeMux(), withQuotaPerMinute(600))
	clock := newFakeClock()
	svc.c.budget.now = clock.now
	svc.c.budget.sleep = clock.sleep
	ctx := context.Background()
	// Empty the bucket, then ask as the background loop would: 40 units at
	// 9 units/s is 4.4s of waiting, more than the 2s limit.
	if _, err := svc.c.budget.reserve(ctx, 540, classInteractive, nil); err != nil {
		t.Fatalf("drain: %v", err)
	}
	buf := captureLogs(t, slog.LevelDebug)
	if _, err := svc.c.reserveQuota(ctx, "users.getProfile", 40); !errors.Is(err, errBudgetSkip) {
		t.Fatalf("reserveQuota = %v, want errBudgetSkip", err)
	}

	lines := quotaLines(buf, "gmail quota skip")
	if len(lines) != 1 {
		t.Fatalf("skip lines = %d, want 1", len(lines))
	}
	for _, want := range []string{"op=users.getProfile", "units=40", "wait="} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("skip line %q does not contain %q", lines[0], want)
		}
	}
}
