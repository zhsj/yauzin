package ui

import "time"

// pollInterval is how often the running process asks the backend whether the
// mailbox moved. It is one light metadata call per account; the loads that
// follow a change are what cost quota, and they only happen when it did.
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

// pollChanges reacts to one tick. The backend already dropped its cache when
// the mailbox moved, so this only decides what has to be reloaded now.
func (a *App) pollChanges() {
	if a.service() == nil {
		return
	}
	a.checkChanges(func(changed bool) {
		if !changed {
			return
		}
		// The sidebar feeds the tray badge as well as the window, so it is
		// reloaded even while the app sits in the tray.
		a.reloadFolders()
		if !a.canRefreshList() {
			return
		}
		a.refreshThreads()
	})
}

// canRefreshList reports whether a poll may replace the page on screen. Without
// a window there is nothing to replace: attach checks and reloads the list when
// one comes back, which also picks up whatever changed since. A list request in
// flight means the user just switched folder, page or query, and replacing it
// would cancel their own request and, on an empty page, walk them back one
// page; the next tick catches up.
func (a *App) canRefreshList() bool {
	if a.window() == nil {
		return false
	}
	return !a.toolbar.loading
}
