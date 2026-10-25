package ui

import "github.com/zhsj/yauzin/internal/inboxwatch"

// applyInboxOutcome mirrors one poll result onto the tray and, when the inbox
// gained unread conversations, publishes the desktop notification. An outcome
// that read nothing leaves the tray alone, so a quiet tick cannot undo what the
// sidebar loaded.
func (a *App) applyInboxOutcome(out inboxwatch.Outcome) {
	if !out.Updated {
		return
	}
	if a.tray != nil {
		a.tray.SetActive(out.Active)
	}
	if out.Notice != nil && a.notifier != nil {
		a.notifier.Show(*out.Notice)
	}
}

// resetInboxState forgets the unread baseline and puts the tray back to idle.
// It runs after the active account changes, so a result that arrives for the
// previous account is discarded by the watcher.
func (a *App) resetInboxState() {
	if a.tray != nil {
		a.tray.SetActive(false)
	}
	if a.watcher != nil {
		a.watcher.Reset(a.activeAcc)
	}
}

// openInboxFromNotification runs on the notification goroutine and marshals the
// click onto the UI thread.
func (a *App) openInboxFromNotification() {
	a.post(a.showInboxFromNotification)
}

// showInboxFromNotification brings the window back on the inbox list. Without a
// window it only prepares the state and asks the lifecycle loop for a window;
// attach then loads the inbox. A window is never created from a posted
// callback, because that would reopen a window the user just closed.
func (a *App) showInboxFromNotification() {
	if a.window() == nil {
		a.activeFolder = "inbox"
		a.query = ""
		a.top.clearSearch()
		a.resetSelection()
		a.clearList()
		a.requestOpen()
		return
	}
	if a.activeFolder != "inbox" || a.query != "" {
		a.selectFolder("inbox")
		a.requestOpen()
		return
	}
	// Already on the inbox: reload the first page so the mail that raised the
	// notification is visible.
	a.hideOverlay()
	a.goToPage(1)
	a.reloadFolders()
	a.requestOpen()
}
