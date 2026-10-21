package ui

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/x/explorer"
	"github.com/zhsj/yauzin/internal/cursor"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/tray"
)

// Run owns the window lifecycle. The tray keeps the process alive after the
// window is closed, so the App instance survives across windows and keeps the
// loaded mail, the current folder and the draft state intact.
func Run() {
	// Single instance: the first process owns the bus name and serves the
	// Open method on it; later launches call Open to bring the window back
	// and exit. An unreachable bus only costs the guard, not the app.
	//
	// The bus serves Open from its own goroutine, which can call it before
	// newApp has returned; the atomic pointer keeps that window panic-free.
	var current atomic.Pointer[App]
	own, err := singleinstance.Acquire(singleinstance.Name, func() {
		if a := current.Load(); a != nil {
			a.requestOpen()
		}
	})
	if err != nil {
		slog.Warn("single instance check failed", "err", err)
		own = true
	}
	if !own {
		if err := singleinstance.Activate(singleinstance.Name); err != nil {
			slog.Warn("activate the running instance", "err", err)
		}
		os.Exit(0)
	}

	// The cursor theme is resolved in attach, right before every window is
	// created: doing it here as well would only resolve it twice.
	a := newApp()
	current.Store(a)

	t, err := tray.Start(i18n.T("app.brand"), tray.Handlers{
		OnOpen: a.requestOpen,
		OnQuit: a.requestQuit,
	}, tray.Icons{Idle: trayIdlePNG(), Active: trayUnreadPNG()})
	if err != nil {
		slog.Warn("system tray unavailable, closing the window quits", "err", err)
	}
	defer t.Close()
	a.tray = t
	a.refreshTrayIcon()

	for {
		if a.quitting.Load() {
			break
		}
		w := new(app.Window)
		a.attach(w)
		a.eventLoop(w)
		a.detach()
		if a.quitting.Load() || t == nil {
			break
		}
		// Gio cannot hide a window on Wayland, so closing it destroys the
		// window and the process waits here for the tray to reopen one.
		select {
		case <-a.openCh:
		case <-a.quitCh:
		}
	}

	a.cleanupTemp()
	t.Close()
	a.closeServices()
	// app.Main never returns on Linux; the process has to end itself.
	os.Exit(0)
}

// closeServices lets every backend release its connections. The tray keeps the
// process alive across windows, so this belongs to the exit path rather than to
// detach.
func (a *App) closeServices() {
	for id, svc := range a.services {
		if err := svc.Close(); err != nil {
			slog.Warn("close mail service", "account", id, "err", err)
		}
	}
}

// eventLoop runs until the window is destroyed.
func (a *App) eventLoop(w *app.Window) {
	var ops op.Ops
	for {
		e := w.Event()
		if a.explorer != nil {
			a.explorer.ListenEvents(e)
		}
		switch e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			a.perf.onFrame(e.Now)
			a.drain()
			a.startInitialLoad()
			a.handleKeys(gtx)
			a.layout(gtx)
			// Hover labels are queued during layout and replayed here, on top
			// of everything else and outside every clip.
			a.drawTooltips(gtx)
			e.Frame(gtx.Ops)
			if acts := a.windowActions; acts != 0 {
				a.windowActions = 0
				a.performWindowActions(w, acts)
			}
		case app.ConfigEvent:
			// Config.Decorated reports whether the platform decorates the
			// window; without that the application draws its own title bar.
			a.decorated = e.Config.Decorated
			a.maximized = e.Config.Mode == app.Maximized
			w.Invalidate()
		case app.DestroyEvent:
			return
		}
	}
}

// performWindowActions runs the requests the title bar collected during the
// frame. The mode reset after a minimize works around the Gio Wayland
// behaviour documented at windowSteps: the window keeps its title bar and gets
// its maximize and drag gestures back once it is restored.
func (a *App) performWindowActions(w *app.Window, acts system.Action) {
	perform, resetMode := windowSteps(acts)
	w.Perform(perform)
	if resetMode {
		w.Option(app.Windowed.Option())
	}
}

// attach binds a fresh window to the existing App state.
func (a *App) attach(w *app.Window) {
	// Gio reads the cursor theme when it creates the window, so re-resolve it
	// here to follow theme changes made while the app sat in the tray.
	applyCursorTheme()
	// Assume the platform decorates the new window until it reports otherwise.
	// That avoids drawing a title bar for the first frame on compositors that
	// will add their own frame, which is the common case on KDE.
	a.decorated = true
	a.maximized = false
	w.Option(
		app.Title(i18n.T("app.brand")),
		app.Size(unit.Dp(1200), unit.Dp(840)),
		app.MinSize(unit.Dp(560), unit.Dp(420)),
		// Leave decoration to the app so Config.Decorated tells the truth. The
		// title bar is drawn by the app in windowcontrols.go and topbar.go.
		// Platforms that decorate regardless (X11, KWin by default) keep their
		// own frame and the app draws nothing.
		app.Decorated(false),
	)
	a.winMu.Lock()
	a.win = w
	a.explorer = explorer.NewExplorer(w)
	a.winMu.Unlock()
	if a.loaded {
		// detach cancelled whatever was in flight, and the mailbox may have
		// changed while the app sat in the tray: refresh before the first
		// frame instead of showing the state of the closed window.
		a.reloadAll()
	}
}

// detach drops the destroyed window so background work stops invalidating it.
// In-flight requests are cancelled: their answers can no longer be shown, and
// the tray may keep the process without a window for a long time.
func (a *App) detach() {
	a.cancelRequests()
	// The cancelled callbacks never report back, so the loading flags they
	// would have cleared are reset here.
	a.toolbar.setLoading(false)
	a.loadingThread = false
	a.winMu.Lock()
	a.win = nil
	a.explorer = nil
	a.winMu.Unlock()
}

func (a *App) window() *app.Window {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	return a.win
}

// invalidate requests a redraw. Without a window the request is dropped: the
// next window draws the state as it is by then.
func (a *App) invalidate() {
	if w := a.window(); w != nil {
		w.Invalidate()
	}
}

// windowExplorer returns the file picker of the current window. Workers must
// not read the field directly: detach clears it on the UI goroutine while they
// may still be running.
func (a *App) windowExplorer() *explorer.Explorer {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	return a.explorer
}

// requestOpen shows the window again. It runs on a tray goroutine.
func (a *App) requestOpen() {
	if w := a.window(); w != nil {
		w.Invalidate()
		return
	}
	select {
	case a.openCh <- struct{}{}:
	default:
	}
}

// requestQuit ends the process. It runs on a tray goroutine.
func (a *App) requestQuit() {
	a.quitting.Store(true)
	if w := a.window(); w != nil {
		// Closing the window makes eventLoop return, which unwinds Run.
		w.Perform(system.ActionClose)
		return
	}
	select {
	case a.quitCh <- struct{}{}:
	default:
	}
}

// refreshTrayIcon mirrors the unread state of the inbox into the notification
// area: the red seal while mail is waiting, the grey one otherwise.
func (a *App) refreshTrayIcon() {
	if a.tray == nil {
		return
	}
	unread := 0
	for _, f := range a.folders {
		if f.Kind == mail.FolderInbox {
			unread = f.Unread
			break
		}
	}
	a.tray.SetActive(unread > 0)
}

// applyCursorTheme exports the desktop cursor theme for Gio's backends. Gio
// reads XCURSOR_THEME/XCURSOR_SIZE when it creates a window, so this runs before
// the first window and again whenever the window is rebuilt.
func applyCursorTheme() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cursor.Apply(ctx)
}

// postRetryDelay is how long post waits before re-requesting a redraw. By
// then the event loop has run out of events and Gio accepts invalidations
// again, so a single retry is enough to wake the second frame.
const postRetryDelay = 100 * time.Millisecond

// post schedules fn on the UI goroutine and requests a redraw. It is safe to
// call from any goroutine.
func (a *App) post(fn func()) {
	a.postMu.Lock()
	a.queue = append(a.queue, fn)
	a.postMu.Unlock()
	w := a.window()
	if w == nil {
		// Without a window the callback just waits: the first frame of the
		// next window drains the queue before laying anything out.
		return
	}
	w.Invalidate()
	// Gio merges invalidations and drops the ones issued while a frame is
	// still being processed (its flag stays down until the event loop runs
	// out of events). A callback posted during that window would otherwise
	// never schedule the second frame; retry once the loop has settled.
	// The retry is a single slot: a burst of callbacks shares one timer,
	// because the callback re-checks the queue when it fires.
	a.postMu.Lock()
	if a.retry == nil {
		a.retry = time.AfterFunc(postRetryDelay, a.retryInvalidate)
	}
	a.postMu.Unlock()
}

// retryInvalidate wakes the event loop once more if callbacks are still
// queued. It runs on the timer goroutine, so it touches the queue and the
// slot only while holding postMu.
func (a *App) retryInvalidate() {
	a.postMu.Lock()
	a.retry = nil
	pending := len(a.queue) > 0
	a.postMu.Unlock()
	if pending {
		if w := a.window(); w != nil {
			w.Invalidate()
		}
	}
}

func (a *App) drain() {
	a.postMu.Lock()
	q := a.queue
	a.queue = nil
	a.postMu.Unlock()
	for _, fn := range q {
		fn()
	}
}

func (a *App) tempDir() (string, error) {
	if a.tmpDir != "" {
		return a.tmpDir, nil
	}
	dir, err := os.MkdirTemp("", "yauzin-mail-")
	if err != nil {
		return "", err
	}
	a.tmpDir = dir
	return dir, nil
}

func (a *App) cleanupTemp() {
	if a.tmpDir == "" {
		return
	}
	if err := os.RemoveAll(a.tmpDir); err != nil {
		logErr("cleanup temp dir", err)
	}
	a.tmpDir = ""
}
