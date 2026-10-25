package ui

import (
	"context"
	"log/slog"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// defaultCheckCooldown is how long an automatic refresh trusts the answer of
// the last mailbox check. Startup, a window reattach and a burst of reload
// intents all ask the same question, and users.getProfile is the one request
// such a refresh always sends: without a cooldown a loop of those intents turns
// into a stream of Gmail calls. A request the user made passes force and is
// never delayed.
const defaultCheckCooldown = 30 * time.Second

// checkSlot names what a caller does with the answer of a check. The two slots
// exist because they are not interchangeable: a reload redraws the window, a
// poll refreshes the tray and the notification. Both can be waiting on the same
// check, and merging them must not drop either.
type checkSlot int

const (
	slotReload checkSlot = iota // reload what is on screen
	slotPoll                    // read the unread inbox for the tray
)

// checkState is the bookkeeping of the mailbox check. Only the UI goroutine
// touches it, like listGen and folderGen.
//
// The service fields compare backends by interface identity: every backend the
// app builds is a pointer, so the comparison is an identity check. The account
// ID is compared as well, because a re-login or a proxy change replaces the
// backend under the same account, and the new baseline describes a connection
// the old one never saw.
type checkState struct {
	// svc, accID and gen describe the check in flight. gen is 0 while none
	// runs; it makes a late answer of a superseded check fail the ownership
	// test in finishCheck instead of clearing the state of its replacement.
	svc   mail.Service
	accID string
	gen   uint64
	// pendingReload and pendingPoll are the newest continuations of each kind
	// that arrived while the check was in flight. Intents in the same slot mean
	// the same thing, so the newest one wins; the two slots coexist because a
	// reload does not replace the tray read.
	pendingReload func(bool)
	pendingPoll   func(bool)

	// last* describe the most recent finished check, the answer the cooldown
	// reuses.
	lastSvc     mail.Service
	lastAcc     string
	lastAt      time.Time
	lastChanged bool
}

// await records an intent that arrived while a check was in flight.
func (st *checkState) await(slot checkSlot, then func(bool)) {
	if slot == slotPoll {
		st.pendingPoll = then
		return
	}
	st.pendingReload = then
}

// checkChanges asks the backend whether the mailbox moved and runs then with
// the answer. force marks a request the user made: it skips the cooldown, while
// the automatic entries (startup, window reattach) reuse a recent answer.
func (a *App) checkChanges(then func(changed bool), force bool) {
	a.requestCheck(then, force, slotReload)
}

// checkChangesForPoll is checkChanges for the process-level poll. It never
// forces, but otherwise shares the same check and the same cooldown: the poll
// used to call CheckChanges itself, which is how a burst of poll intents could
// bypass the single-flight rule and turn into a stream of users.getProfile.
func (a *App) checkChangesForPoll(then func(changed bool)) {
	a.requestCheck(then, false, slotPoll)
}

// requestCheck is the only place that starts a users.getProfile. Concurrent
// calls against the same backend share one request; a burst of intents is
// merged into the slots of checkState and answered once. A different backend (a
// re-login or a proxy change replaced it) always starts its own check, because
// the previous baseline describes a connection that is gone.
func (a *App) requestCheck(then func(changed bool), force bool, slot checkSlot) {
	svc := a.service()
	if svc == nil {
		return
	}
	accID := a.activeAcc
	st := &a.check
	if st.gen != 0 && st.svc == svc && st.accID == accID {
		st.await(slot, then)
		return
	}
	if !force && st.lastSvc == svc && st.lastAcc == accID && time.Since(st.lastAt) < a.checkCooldown {
		then(st.lastChanged)
		return
	}

	st.gen++
	gen := st.gen
	st.svc, st.accID = svc, accID
	st.pendingReload, st.pendingPoll = nil, nil
	var changed bool
	guard := func() bool { return accID == a.activeAcc }
	a.async("check changes", timeoutCheck, guard,
		func(ctx context.Context) error {
			var err error
			changed, err = svc.CheckChanges(ctx)
			return err
		},
		func() { a.finishCheck(gen, svc, accID, changed, then, slot) },
		func(err error) {
			// A failed check is not a failure the user needs to hear about:
			// the load that follows reports whatever is really wrong with the
			// backend, and an offline minute must not raise a toast.
			slog.Debug("mailbox check failed", "err", err)
			a.finishCheck(gen, svc, accID, false, then, slot)
		})
}

// finishCheck retires check gen and runs the continuations that waited for it,
// the reload first and then the poll: a reload may replace the state the poll's
// notification describes. A check a later one superseded only runs its own
// continuation, because the shared state belongs to the newer check.
func (a *App) finishCheck(gen uint64, svc mail.Service, accID string, changed bool, then func(bool), slot checkSlot) {
	st := &a.check
	if st.gen != gen {
		then(changed)
		return
	}
	st.gen = 0
	st.svc, st.accID = nil, ""
	st.lastSvc, st.lastAcc, st.lastAt, st.lastChanged = svc, accID, time.Now(), changed
	reload, poll := st.pendingReload, st.pendingPoll
	st.pendingReload, st.pendingPoll = nil, nil
	if slot == slotPoll {
		if poll == nil {
			poll = then
		}
	} else if reload == nil {
		reload = then
	}
	if reload != nil {
		reload(changed)
	}
	if poll != nil {
		poll(changed)
	}
}
