package ui

import (
	"log/slog"

	"github.com/zhsj/yauzin/internal/mailto"
)

// requestMailto opens a composer for uri in the running instance. It runs on a
// D-Bus goroutine, so it only parses and posts: the composer itself is built on
// the UI goroutine.
func (a *App) requestMailto(uri string) {
	a.enqueueMailto(uri)
	a.requestOpen()
}

// enqueueMailto posts the composer for uri without asking for a window. The
// startup arguments use it: the first window is about to be created anyway, and
// an openCh token left behind would reopen a window the user just closed.
func (a *App) enqueueMailto(uri string) {
	m, ok := mailto.Parse(uri)
	if !ok {
		// The URI carries the recipients and the body, so only the fact that
		// it was rejected may reach the log.
		slog.Warn("ignore a mailto URI that cannot be decoded")
		return
	}
	a.post(func() { a.openMailto(m) })
}

// openMailto replaces the composer with one filled from the link. The composer
// is a single slot, so an unsent draft is discarded: that is the agreed
// behaviour of a mailto activation rather than an accident.
func (a *App) openMailto(m mailto.Message) {
	a.showOverlay(newComposeFromMailto(a, m))
}
