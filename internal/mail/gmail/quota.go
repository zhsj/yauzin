package gmail

import (
	"context"
	"errors"
	"math"
	"strconv"
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
//
// Gmail enforces the per-minute limit on fixed 60 second windows, so the
// budget counts what the trailing minute spent instead of pacing a bucket. A
// bucket with a minute's capacity refills while the same minute is still
// running, which lets a busy client spend nearly twice the limit before the
// window ends; that is what tripped 403 "quota exceeded" while the bucket still
// reported 3,728 of 5,400 units used.
const (
	// quotaPerMinute is the per-user-per-project limit.
	quotaPerMinute = 6000.0
	// quotaMargin keeps a twentieth of the limit in reserve for what the table
	// cannot see: upload chunks, another instance of the same account, and the
	// seconds the per-second slots round away.
	quotaMargin = 0.95
	// quotaWindow is how far back a reservation looks.
	quotaWindow = time.Minute
	// budgetWaitLimit bounds how long a background request waits for quota
	// before it is dropped instead. The next poll retries it anyway.
	budgetWaitLimit = 2 * time.Second
)

// windowSeconds is quotaWindow in whole seconds. The counter keeps one slot per
// second, which is also the resolution of the wait it asks for.
const windowSeconds = int64(quotaWindow / time.Second)

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

// quotaSpend is what one call costs and how much of the window it left spent.
// It travels with the request, so a single log line answers both "how expensive
// was this call" and "how much of the minute is gone".
type quotaSpend struct {
	units    float64
	used     float64
	capacity float64
	waited   time.Duration
}

// report renders the window as "used/capacity". Both figures describe the
// trailing minute, which is the window Gmail charges against: used is what the
// last 60 seconds spent, capacity the window's own limit (the per-minute limit
// minus the safety margin, so 5,700 for the 6,000 limit). Unlike a bucket's
// fill level, used can be compared with the server's own count directly.
func (q quotaSpend) report() string {
	return strconv.FormatFloat(q.used, 'f', 0, 64) + "/" +
		strconv.FormatFloat(q.capacity, 'f', 0, 64)
}

// logAttrs renders the quota fields of a request log line. A request that did
// not reserve anything on its own (an upload chunk, whose session paid once
// already) renders nothing, so its line keeps the old shape.
func (q quotaSpend) logAttrs() []any {
	if q.units <= 0 {
		return nil
	}
	return []any{"units", int64(q.units), "quota", q.report(), "wait", q.waited}
}

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

// budget is the account's quota over a sliding window. It remembers what each
// of the last 60 seconds spent, so a reservation is accepted exactly when the
// trailing minute can still afford it: unlike a refilling bucket it cannot
// spend the allowance twice inside one of Gmail's fixed minutes.
type budget struct {
	mu    sync.Mutex
	limit float64

	// slots holds the spend of the last windowSeconds seconds, one per second,
	// indexed by sec % windowSeconds. slot.sec names the second a slot holds, so
	// a slot that is older than the window is simply overwritten.
	slots [windowSeconds]slot

	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// slot is what one second of the window spent.
type slot struct {
	// sec is the unix second the slot holds; zero means empty.
	sec   int64
	units float64
}

// newBudget builds the counter of one account. perMinute is the limit the
// account may spend within any minute; now and sleep are injectable so a test
// does not wait.
func newBudget(perMinute float64, now func() time.Time, sleep func(context.Context, time.Duration) error) *budget {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	return &budget{
		limit: math.Max(perMinute*quotaMargin, 1),
		now:   now,
		sleep: sleep,
	}
}

// spent is the spend the trailing window still holds, and drops the slots that
// fell out of it. The caller holds mu.
func (b *budget) spent(now int64) float64 {
	var used float64
	for i := range b.slots {
		s := &b.slots[i]
		if s.units == 0 {
			continue
		}
		// A spend at second s counts through second s+windowSeconds-1, so it
		// leaves the window as the clock reaches s+windowSeconds.
		if s.sec+windowSeconds <= now {
			*s = slot{}
			continue
		}
		used += s.units
	}
	return used
}

// spend records units at second sec. The caller holds mu.
func (b *budget) spend(sec int64, units float64) {
	s := &b.slots[sec%windowSeconds]
	if s.sec != sec {
		*s = slot{sec: sec}
	}
	s.units += units
}

// waitFor is how long the oldest spend has to age out to free the shortfall
// need = spent(now)+units-limit. Walking the seconds from the oldest to the
// newest until it is covered gives the moment the request may go. The caller
// holds mu and has called spent(now).
func (b *budget) waitFor(now int64, need float64) time.Duration {
	for sec := now - windowSeconds + 1; sec <= now; sec++ {
		s := b.slots[sec%windowSeconds]
		if s.sec != sec || s.units == 0 {
			continue
		}
		need -= s.units
		if need <= 0 {
			return time.Duration(sec+windowSeconds-now) * time.Second
		}
	}
	// Only reachable when a single call is larger than the whole window, which
	// reserve clamps away; the full span is the most a caller could wait.
	return quotaWindow
}

// reserve spends units, waiting for the window to slide when it is full. A
// background request that would wait longer than budgetWaitLimit is dropped
// with errBudgetSkip.
//
// The returned spend carries the window state as it stands right after the
// deduction, taken under the same lock, so two concurrent requests cannot
// report the same "used" figure.
func (b *budget) reserve(ctx context.Context, units float64, class costClass) (quotaSpend, error) {
	spend := quotaSpend{units: units}
	if units <= 0 {
		return spend, nil
	}
	// A single call can never cost more than the whole window; clamping keeps
	// an oversized batch from waiting for something that can never happen.
	units = math.Min(units, b.limit)
	spend.units = units
	for {
		b.mu.Lock()
		now := b.now().Unix()
		used := b.spent(now)
		if used+units <= b.limit {
			b.spend(now, units)
			spend.used = used + units
			spend.capacity = b.limit
			b.mu.Unlock()
			return spend, nil
		}
		wait := b.waitFor(now, used+units-b.limit)
		b.mu.Unlock()
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if class == classBackground && spend.waited+wait > budgetWaitLimit {
			return spend, errBudgetSkip
		}
		if err := b.sleep(ctx, wait); err != nil {
			return spend, err
		}
		spend.waited += wait
	}
}
