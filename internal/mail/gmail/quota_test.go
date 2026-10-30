package gmail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestMethodUnitsMatchesTheQuotaTable pins the prices the budget spends. The
// numbers mirror docs/GMAIL-QUOTA.md; changing one without the other is a bug.
func TestMethodUnitsMatchesTheQuotaTable(t *testing.T) {
	for _, tc := range []struct {
		op, format string
		want       float64
	}{
		{"users.getProfile", "", 1},
		{"users.history.list", "", 2},
		{"labels.list", "", 1},
		{"labels.get", "", 1},
		{"threads.list", "", 10},
		{"threads.get", "metadata", 40},
		{"threads.get", "", 40},
		{"threads.get", "full", 120},
		{"threads.modify", "", 10},
		{"threads.trash", "", 20},
		{"messages.get", "metadata", 20},
		{"messages.get", "full", 60},
		{"messages.get", "raw", 60},
		{"messages.modify", "", 5},
		{"messages.batchModify", "", 50},
		{"messages.trash", "", 20},
		{"messages.send", "", 100},
	} {
		if got := methodUnits(tc.op, tc.format); got != tc.want {
			t.Errorf("methodUnits(%q, %q) = %v, want %v", tc.op, tc.format, got, tc.want)
		}
	}
}

// TestBudgetRefillsOverTime checks the bucket stops a burst it cannot afford
// and lets it through once the minute has earned the units.
func TestBudgetRefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	perMinute := 6000.0
	b := newBudget(perMinute, clock.now, clock.sleep)

	// The bucket starts full, so one page fits without waiting, and the spend
	// reports the state the request left behind.
	spend, err := b.reserve(context.Background(), 1010, classInteractive)
	if err != nil || spend.waited != 0 {
		t.Fatalf("first reserve = %+v, %v; want no wait", spend, err)
	}
	if spend.units != 1010 || spend.used != 1010 || spend.capacity != perMinute*quotaMargin {
		t.Fatalf("spend = %+v, want units/used 1010 of %v", spend, perMinute*quotaMargin)
	}
	if got := spend.report(); got != "1010/5400" {
		t.Fatalf("report = %q, want 1010/5400", got)
	}
	// Drain what is left.
	if _, err := b.reserve(context.Background(), 6000*quotaMargin-1010, classInteractive); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// One more page has to wait for the refill: 1010 units at 90 units/s.
	spend, err = b.reserve(context.Background(), 1010, classInteractive)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	want := time.Duration(1010 / (perMinute * quotaMargin / 60) * float64(time.Second))
	if spend.waited < want-time.Second || spend.waited > want+time.Second {
		t.Fatalf("wait = %v, want about %v", spend.waited, want)
	}
}

// TestBudgetDropsBackgroundRequest checks a resident process never queues its
// own polling behind a full minute of quota.
func TestBudgetDropsBackgroundRequest(t *testing.T) {
	clock := newFakeClock()
	b := newBudget(600, clock.now, clock.sleep)
	if _, err := b.reserve(context.Background(), 540, classInteractive); err != nil {
		t.Fatalf("drain: %v", err)
	}
	// 100 units at 9 units/s is over the wait limit.
	if _, err := b.reserve(context.Background(), 100, classBackground); !errors.Is(err, errBudgetSkip) {
		t.Fatalf("background reserve = %v, want errBudgetSkip", err)
	}
	// The same request is worth waiting for when a user asked for it.
	if _, err := b.reserve(context.Background(), 100, classInteractive); err != nil {
		t.Fatalf("interactive reserve: %v", err)
	}
}

// TestBudgetHonoursContext checks a cancelled caller stops waiting for quota.
func TestBudgetHonoursContext(t *testing.T) {
	clock := newFakeClock()
	// Long waits: the sleep below still returns at once, so the context is the
	// only thing that can end the loop.
	b := newBudget(60, clock.now, func(ctx context.Context, _ time.Duration) error {
		return ctx.Err()
	})
	if _, err := b.reserve(context.Background(), 54, classInteractive); err != nil {
		t.Fatalf("drain: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.reserve(ctx, 54, classInteractive); !errors.Is(err, context.Canceled) {
		t.Fatalf("reserve on cancelled context = %v, want context.Canceled", err)
	}
}

// TestClassOfYieldsOnlyTheChecks pins which calls the background loop owns.
func TestClassOfYieldsOnlyTheChecks(t *testing.T) {
	if got := classOf("users.getProfile"); got != classBackground {
		t.Errorf("classOf(getProfile) = %v, want background", got)
	}
	for _, op := range []string{"threads.list", "threads.get", "messages.batchModify", "labels.list"} {
		if got := classOf(op); got != classInteractive {
			t.Errorf("classOf(%q) = %v, want interactive", op, got)
		}
	}
}

// TestAPIErrorClassifiesRateLimits checks the sentinel the retry layer and the
// UI need.
func TestAPIErrorClassifiesRateLimits(t *testing.T) {
	limited := `{"error":{"code":403,"status":"RESOURCE_EXHAUSTED",` +
		`"message":"User-rate limit exceeded.","errors":[{"reason":"rateLimitExceeded"}]}}`
	permission := `{"error":{"code":403,"message":"Insufficient Permission",` +
		`"errors":[{"reason":"insufficientPermissions"}]}}`
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    error
		retried bool
	}{
		{"rate limit", http.StatusForbidden, limited, mail.ErrRateLimited, true},
		{"too many requests", http.StatusTooManyRequests, `{}`, mail.ErrRateLimited, true},
		{"server error", http.StatusInternalServerError, `{}`, nil, true},
		{"permission", http.StatusForbidden, permission, mail.ErrUnauthorized, false},
		{"missing", http.StatusNotFound, `{}`, mail.ErrNotFound, false},
		{"bad request", http.StatusBadRequest, `{}`, mail.ErrInvalidArgument, false},
	} {
		err := apiError("threads.list", fakeResponse(tc.status, tc.body))
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
		if got := retryable(err); got != tc.retried {
			t.Errorf("%s: retryable = %v, want %v", tc.name, got, tc.retried)
		}
	}
}

// fakeClock drives a budget without waiting: sleeping moves the clock forward.
type fakeClock struct {
	at time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Unix(1700000000, 0)}
}

func (c *fakeClock) now() time.Time { return c.at }

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.at = c.at.Add(d)
	return nil
}

// fakeResponse builds the response apiError reads.
func fakeResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestClientRetriesARetryableAnswer checks a 500 is sent again and the caller
// sees the retry's success instead of the first failure.
func TestClientRetriesARetryableAnswer(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeErrorJSON(t, w, http.StatusInternalServerError, `{"error":{"code":500,"message":"boom"}}`)
			return
		}
		t.Helper()
		writeJSON(t, w, `{"labels":[]}`)
	})
	svc, _ := newTestService(t, mux)

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2: one failure and one retry", calls)
	}
}

// TestClientGivesUpAfterTheRetryBudget checks the loop is bounded.
func TestClientGivesUpAfterTheRetryBudget(t *testing.T) {
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeErrorJSON(t, w, http.StatusInternalServerError, `{"error":{"code":500,"message":"boom"}}`)
	})
	svc, _ := newTestService(t, mux)

	if _, err := svc.Folders(context.Background()); err == nil {
		t.Fatal("Folders succeeded although every attempt failed")
	}
	if want := retryAttempts + 1; calls != want {
		t.Fatalf("calls = %d, want %d", calls, want)
	}
}
