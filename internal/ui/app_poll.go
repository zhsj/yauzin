package ui

import (
	"context"
	"log/slog"
	"time"

	"github.com/zhsj/yauzin/internal/inboxwatch"
)

// pollInterval is how often the running process asks the backend whether the
// mailbox moved. It is one light metadata call per account; the follow-up read
// only happens when it did, and it only serves the tray and the desktop
// notification.
const pollInterval = time.Minute

// startPolling starts the change check ticker. Run calls it once for the whole
// process, so a tray-resident app keeps watching the mailbox without a window.
// A second call is a no-op.
func (a *App) startPolling() {
	if a.pollStop != nil {
		return
	}
	stop := make(chan struct{})
	a.pollStop = stop
	ticker := time.NewTicker(pollInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// The check runs off the UI goroutine and reports back through
				// the queue, like every other backend call.
				a.post(a.pollChanges)
			}
		}
	}()
}

// stopPolling retires the ticker. It runs on the lifecycle goroutine on the way
// out, before the backends are closed.
func (a *App) stopPolling() {
	if a.pollStop == nil {
		return
	}
	close(a.pollStop)
	a.pollStop = nil
}

// pollChanges reacts to one tick. The poll only maintains the tray and the
// desktop notification: it never reloads the sidebar or the thread list, so it
// cannot cancel a request the user just started and the window keeps showing
// whatever it showed until the next explicit refresh.
func (a *App) pollChanges() {
	a.pollInboxUnread()
}

// pollInboxUnread reads the unread inbox conversations of the active account
// and applies the result to the tray and the notification. Without a tray and
// without a notification backend there is nothing to maintain, so the poll
// costs nothing.
func (a *App) pollInboxUnread() {
	if a.tray == nil && a.notifier == nil {
		return
	}
	svc := a.service()
	if svc == nil || a.watcher == nil {
		return
	}
	accID := a.activeAcc
	var out inboxwatch.Outcome
	a.async("inbox unread", timeoutPoll, func() bool { return accID == a.activeAcc },
		func(ctx context.Context) error {
			var err error
			out, err = a.watcher.Poll(ctx, accID, svc)
			return err
		},
		func() { a.applyInboxOutcome(out) },
		func(err error) {
			// The poll runs once a minute: an unreachable mailbox must not raise
			// a status bar every tick, and the next one retries anyway.
			slog.Debug("inbox unread failed", "err", err)
		})
}
