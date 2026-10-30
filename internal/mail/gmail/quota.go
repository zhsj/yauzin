package gmail

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

// Gmail bills quota units per method call and does not discount a batch: every
// embedded call is charged at its own method's price. The table below is the
// one place the package knows those prices; docs/GMAIL-QUOTA.md records the
// same numbers plus their source, so the two must be changed together.
//
// The per-user limit is 6,000 units per minute per project (the user's own
// OAuth client is the project), which is what the budget below spends. The
// project wide 1,200,000 units per minute is not modelled: one desktop user
// with a handful of accounts cannot reach it.
const (
	// quotaPerMinute is the per-user-per-project limit.
	quotaPerMinute = 6000.0
	// quotaMargin keeps a tenth of the limit in reserve, so a request the
	// table underestimates still fits.
	quotaMargin = 0.9
	// budgetWaitLimit bounds how long a background request waits for quota
	// before it is dropped instead. The next poll retries it anyway.
	budgetWaitLimit = 2 * time.Second
)

// unitCosts lists the price of every call this package sends. threads.get and
// messages.get depend on the format and are priced by methodUnits.
var unitCosts = map[string]float64{
	"users.getProfile":         1,
	"users.history.list":       2,
	"labels.list":              1,
	"labels.get":               1,
	"threads.list":             10,
	"threads.modify":           10,
	"threads.trash":            20,
	"threads.untrash":          10,
	"threads.delete":           20,
	"messages.list":            5,
	"messages.modify":          5,
	"messages.batchModify":     50,
	"messages.trash":           20,
	"messages.untrash":         5,
	"messages.delete":          10,
	"messages.send":            100,
	"messages.attachments.get": 20,
}

// The two-tier prices: a full payload costs three times a metadata read.
const (
	costThreadMetadata  = 40
	costThreadFull      = 120
	costMessageMetadata = 20
	costMessageFull     = 60
)

// methodUnits returns the quota one call of op costs. format only matters for
// the two calls that return whole messages.
func methodUnits(op, format string) float64 {
	switch op {
	case "threads.get":
		if isFullFormat(format) {
			return costThreadFull
		}
		return costThreadMetadata
	case "messages.get":
		if isFullFormat(format) {
			return costMessageFull
		}
		return costMessageMetadata
	}
	return unitCosts[op]
}

// isFullFormat reports whether format makes an API call carry the whole
// message, which is the tier Gmail charges more for.
func isFullFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "full", "raw", "html":
		return true
	}
	return false
}

// costClass decides what a request does when the budget is empty.
type costClass int

const (
	// classInteractive waits for quota: a user is looking at the answer.
	classInteractive costClass = iota
	// classBackground gives up instead, so a resident process never queues
	// behind its own polling.
	classBackground
)

// errBudgetSkip reports a background request the budget dropped. It never
// reaches the user: the caller treats it like a failed background read.
var errBudgetSkip = errors.New("gmail: dropped by the quota budget")

// classOf decides the priority of an operation. mail.Service carries no hint
// about who called, so the decision is made per operation: the change check is
// the one call the background loop owns, and it yields rather than delays a
// check that the next tick repeats anyway.
func classOf(op string) costClass {
	switch op {
	case "users.getProfile", "users.history.list":
		return classBackground
	}
	return classInteractive
}

// budget is a token bucket holding the account's quota for one minute. It is
// refilled continuously, so a burst is allowed up to the capacity and the
// average settles at quotaPerMinute.
type budget struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	perSec   float64
	last     time.Time

	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// newBudget builds the bucket of one account. perMinute is the limit the
// account may spend; now and sleep are injectable so a test does not wait.
func newBudget(perMinute float64, now func() time.Time, sleep func(context.Context, time.Duration) error) *budget {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	capacity := math.Max(perMinute*quotaMargin, 1)
	return &budget{
		capacity: capacity,
		tokens:   capacity,
		perSec:   capacity / 60,
		now:      now,
		sleep:    sleep,
	}
}

// refill adds the quota the elapsed time earned. The caller holds mu.
func (b *budget) refill() {
	now := b.now()
	if b.last.IsZero() {
		b.last = now
		return
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = math.Min(b.tokens+elapsed.Seconds()*b.perSec, b.capacity)
		b.last = now
	}
}

// reserve spends units, waiting for the bucket to refill when it is empty. A
// background request that would wait longer than budgetWaitLimit is dropped
// with errBudgetSkip. It returns how long the call had to wait.
func (b *budget) reserve(ctx context.Context, units float64, class costClass) (time.Duration, error) {
	if units <= 0 {
		return 0, nil
	}
	// A single call can never cost more than the bucket holds; clamping keeps
	// an oversized batch from blocking forever.
	units = math.Min(units, b.capacity)
	var waited time.Duration
	for {
		b.mu.Lock()
		b.refill()
		if b.tokens >= units {
			b.tokens -= units
			b.mu.Unlock()
			return waited, nil
		}
		missing := units - b.tokens
		wait := time.Duration(missing / b.perSec * float64(time.Second))
		b.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if class == classBackground && waited+wait > budgetWaitLimit {
			return waited, errBudgetSkip
		}
		if err := b.sleep(ctx, wait); err != nil {
			return waited, err
		}
		waited += wait
	}
}
