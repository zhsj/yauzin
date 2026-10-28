package ui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/cursor"
	"github.com/zhsj/yauzin/internal/env"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/logging"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mailto"
	"github.com/zhsj/yauzin/internal/notify"
	"github.com/zhsj/yauzin/internal/profiling"
	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/tray"
)

// Run owns the window lifecycle. args are the arguments of this launch: a
// mailto URI among them opens a composer once the app exists. The tray keeps
// the process alive after the window is closed, so the App instance survives
// across windows and keeps the loaded mail, the current folder and the draft
// state intact.
func Run(args []string) {
	// The console logger belongs to the process, not to an App: install it
	// before anything below can log. newApp then applies the stored level.
	logging.Init()

	// Single instance: the first process owns the bus name and serves the Open
	// and OpenURI methods on it; later launches call one of them to bring the
	// window back (OpenURI with a mailto URI) and exit. An unreachable bus only
	// costs the guard, not the app.
	//
	// The bus serves those methods from its own goroutine, which can call them
	// before newApp has returned; the atomic pointer keeps that window
	// panic-free, and a URI that arrives in it is buffered for replay below.
	var (
		current   atomic.Pointer[App]
		pendingMu sync.Mutex
		pending   []string
	)
	own, err := singleinstance.Acquire(singleinstance.Name, func(uri string) {
		pendingMu.Lock()
		defer pendingMu.Unlock()
		a := current.Load()
		if a == nil {
			if uri != "" {
				pending = append(pending, uri)
			}
			return
		}
		if uri == "" {
			a.requestOpen()
			return
		}
		a.requestMailto(uri)
	})
	if err != nil {
		slog.Warn("single instance check failed", "err", err)
		own = true
	}
	if !own {
		// The instance that owns the name takes over: a plain launch asks for
		// its window, a mailto launch hands it the URI.
		var err error
		if uri, ok := mailto.FromArgs(args); ok {
			err = singleinstance.ActivateURI(singleinstance.Name, uri)
		} else {
			err = singleinstance.Activate(singleinstance.Name)
		}
		if err != nil {
			slog.Warn("activate the running instance", "err", err)
		}
		os.Exit(0)
	}

	// Profiling is opt-in and never part of startup: a port that is invalid
	// or busy only costs the endpoints, not the app.
	if port, ok, err := env.PprofPort(); err != nil {
		slog.Warn("invalid profile port", "err", err)
	} else if ok {
		if addr, err := profiling.Start(port); err != nil {
			slog.Warn("profiles unavailable", "err", err)
		} else {
			slog.Info("serving profiles", "addr", addr.String())
		}
	}

	// The cursor theme is resolved in attach, right before every window is
	// created: doing it here as well would only resolve it twice.
	a := newApp()
	pendingMu.Lock()
	current.Store(a)
	queued := pending
	pending = nil
	pendingMu.Unlock()
	// The launch arguments go first and the buffered activations after them:
	// the composer is one slot, so the last URI written is the one on screen.
	// Neither asks for a window here; the lifecycle loop is about to create the
	// first one, and a token left behind would reopen the next window closed.
	if uri, ok := mailto.FromArgs(args); ok {
		a.enqueueMailto(uri)
	}
	for _, uri := range queued {
		a.enqueueMailto(uri)
	}
	// Before the tray and the window loop: an interrupt while the process
	// sits in the notification area must clean up just like a quit does.
	a.watchInterrupt()

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
	// The notifier belongs to the process as well: a click on a notification
	// asks for the window through the same requestOpen path as the tray.
	a.notifier = notify.Start(a.openInboxFromNotification)
	// Polling belongs to the process, not to a window: the tray and the
	// desktop notifications keep watching the mailbox while the window is
	// closed.
	a.startPolling()

	for !a.quitting.Load() {
		w := new(app.Window)
		a.attach(w)
		a.eventLoop(w)
		a.detach()
		if a.quitting.Load() || t == nil {
			break
		}
		// Gio cannot hide a window on Wayland, so closing it destroys the
		// window and the process waits here for the tray to reopen one.
		a.waitForWindow()
	}

	// The ticker must stop before the exit path closes the backends.
	a.stopPolling()
	if a.notifier != nil {
		a.notifier.Close()
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
	// An openCh token posted before this window existed is answered by creating
	// it. Draining the token keeps it from reopening the next window the user
	// closes, which is why the startup mailto arguments post without one.
	select {
	case <-a.openCh:
	default:
	}
	// Gio reads the cursor theme when it creates the window, so re-resolve it
	// here to follow theme changes made while the app sat in the tray.
	applyCursorTheme()
	// Assume the platform decorates the new window until it reports otherwise:
	// that keeps the first frame free of a title bar, and the ConfigEvent of
	// the first frames corrects it wherever the platform leaves the
	// decorations to the app.
	a.decorated = true
	a.maximized = false
	w.Option(
		app.Title(i18n.T("app.brand")),
		app.Size(unit.Dp(1200), unit.Dp(840)),
		app.MinSize(unit.Dp(560), unit.Dp(420)),
		// Leave decoration to the app so Config.Decorated tells the truth. The
		// title bar is drawn by the app in windowcontrols.go and topbar.go;
		// platforms that decorate regardless (X11, Windows, macOS) keep their
		// own frame and the app draws nothing. On Wayland the compositor
		// answers client-side because internal/waylanddeco requests it.
		app.Decorated(false),
	)
	a.winMu.Lock()
	a.win = w
	a.explorer = explorer.NewExplorer(w)
	a.winMu.Unlock()
	if a.loaded {
		// detach cancelled whatever was in flight, and the mailbox may have
		// changed while the app sat in the tray: check and refresh before the
		// first frame instead of showing the state of the closed window.
		a.reloadChecked()
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

// waitForWindow blocks the lifecycle loop until the tray asks for a window or
// the process quits. Posted callbacks wake it too, but only to run them: a poll
// or a status expiry must not reopen the window the user just closed. Draining
// here also keeps every App field single-writer while no window exists.
func (a *App) waitForWindow() {
	for {
		select {
		case <-a.openCh:
			return
		case <-a.quitCh:
			return
		case <-a.wake:
			a.drain()
		}
	}
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

// watchInterrupt removes the scratch directory when the process is stopped
// with Ctrl+C. Run only cleans up on its own exit path, so without this every
// interrupted run left its directory behind. Termination and hang-up keep
// their default action: the interrupt is the one signal a desktop session
// sends on its way to a restart, so it is the one worth cleaning up for.
func (a *App) watchInterrupt() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		sig := <-ch
		slog.Info("terminating on signal", "signal", sig.String())
		// A second signal ends the process at once, without waiting for a
		// cleanup that may be slow on a large staging directory.
		signal.Reset(sig)
		a.cleanupTemp()
		os.Exit(0)
	}()
}

// refreshTrayIcon mirrors the unread state of the inbox into the notification
// area: the red seal while mail is waiting, the grey one otherwise.
func (a *App) refreshTrayIcon() {
	if a.tray == nil {
		return
	}
	a.tray.SetActive(inboxUnread(a.folders) > 0)
}

// inboxUnread returns the unread count of the inbox, or zero when the sidebar
// carries no inbox entry.
func inboxUnread(folders []mail.Folder) int {
	for _, f := range folders {
		if f.Kind == mail.FolderInbox {
			return f.Unread
		}
	}
	return 0
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
	// Wake the lifecycle loop even without a window: in the tray it is what
	// drains the queue, and a callback that only ran when the next window
	// opened would arrive long after it stopped meaning anything. A wake only
	// runs the queue, it never reopens a window (see waitForWindow).
	select {
	case a.wake <- struct{}{}:
	default:
	}
	w := a.window()
	if w == nil {
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

// errTempClosed reports that the scratch directory is gone. The process is
// exiting by then, and creating another one would leave behind exactly the
// leftover that the signal handler is about to remove.
var errTempClosed = errors.New("temporary directory removed")

// runtimeDirName is the directory below $XDG_RUNTIME_DIR that holds the staged
// attachments and HTML previews of this run.
const runtimeDirName = "yauzin"

// tempDir returns the scratch directory of this run, creating it on first use.
func (a *App) tempDir() (string, error) {
	a.tmpMu.Lock()
	defer a.tmpMu.Unlock()
	if a.tmpClosed {
		return "", errTempClosed
	}
	if a.tmpDir != "" {
		return a.tmpDir, nil
	}
	dir, err := createTempDir()
	if err != nil {
		return "", err
	}
	a.tmpDir = dir
	return dir, nil
}

// cleanupTemp removes the scratch directory and keeps tempDir from creating a
// new one. It runs both on the lifecycle loop's exit path and on the signal
// handler, so it is idempotent and safe to call concurrently.
func (a *App) cleanupTemp() {
	a.tmpMu.Lock()
	defer a.tmpMu.Unlock()
	a.tmpClosed = true
	if a.tmpDir == "" {
		return
	}
	if err := os.RemoveAll(a.tmpDir); err != nil {
		logErr("cleanup temp dir", err)
	}
	a.tmpDir = ""
}

// createTempDir prefers the private runtime directory of the login session:
// the system removes it when the session ends, so a run that is killed without
// ever reaching a cleanup path leaves nothing behind for good.
func createTempDir() (string, error) {
	if dir, ok := createRuntimeDir(); ok {
		return dir, nil
	}
	return os.MkdirTemp("", "yauzin-mail-")
}

// createRuntimeDir creates $XDG_RUNTIME_DIR/yauzin and reports whether it is
// usable. Nothing in the runtime directory may be trusted: the path is fixed
// per session, so a directory left by a run that was killed outright is
// removed first. The single-instance guard runs before the first window exists
// and makes sure no other process owns it.
func createRuntimeDir() (string, bool) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(base) {
		return "", false
	}
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		return "", false
	}
	dir := filepath.Join(base, runtimeDirName)
	if err := os.RemoveAll(dir); err != nil {
		return "", false
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", false
	}
	return dir, true
}
