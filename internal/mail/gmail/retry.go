package gmail

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// retryAttempts bounds how many times one call is sent again before its error
// reaches the caller: a call is therefore sent at most retryAttempts+1 times.
const retryAttempts = 3

// retryBaseDelay is the first backoff step; every attempt doubles it and adds
// up to a quarter of the delay as jitter, so parallel clients drift apart.
// Tests shorten it.
var retryBaseDelay = time.Second

// retryLoop runs one call and, while the answer is worth another try, runs it
// again after a jittered backoff. attempt reports what one send produced: only
// a rate-limited or failed answer makes it return an error at all. The loop is
// therefore the one place that knows how often a call is sent and how long the
// next attempt waits; a cancelled context ends the wait with its own error.
func (c *client) retryLoop(ctx context.Context, op string, attempt func() error) error {
	for try := 0; ; try++ {
		err := attempt()
		if err == nil {
			return nil
		}
		if try >= retryAttempts || !retryable(err) {
			return err
		}
		slog.Debug("gmail retry",
			"account", logAccount(c.accountID), "op", op, "attempt", try+1, "err", err)
		if waitErr := sleepContext(ctx, retryDelay(try)); waitErr != nil {
			return waitErr
		}
	}
}

// retryDelay is the pause before attempt+1: an exponential step with up to a
// quarter of it as jitter, so repeated clients do not retry in lockstep.
func retryDelay(attempt int) time.Duration {
	base := retryBaseDelay << attempt
	jitter := base / 4
	if jitter > 0 {
		base += time.Duration(rand.Int64N(int64(jitter) + 1))
	}
	return base
}

// retryable reports whether err is worth sending again. Only the server's own
// rate limiting and its 5xx answers qualify: a transport failure is reported
// to the caller, and a rejected request would fail the same way twice.
func retryable(err error) bool {
	var failure *apiFailure
	if !errors.As(err, &failure) {
		return false
	}
	if failure.status == http.StatusTooManyRequests || failure.status >= 500 {
		return true
	}
	return failure.status == http.StatusForbidden && errors.Is(err, mail.ErrRateLimited)
}
