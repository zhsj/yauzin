// Package ui is the Gio front end. App owns the state and the child views,
// which live in one file each (topbar, sidebar, messagelist, reader, compose)
// and call back through *App. App's own methods are grouped by domain:
// app_lifecycle for the window and the callback queue, app_data for folders and
// paging, app_thread for conversations and bulk actions, app_overlay for the
// frame and the floating panels, and app_settings for stored preferences.
package ui

import (
	"context"
	"errors"
	"image"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/logging"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/gmail"
	"github.com/zhsj/yauzin/internal/mail/mock"
	"github.com/zhsj/yauzin/internal/prefs"
	"github.com/zhsj/yauzin/internal/tray"
)

type composeMode string

const (
	composeNew     composeMode = "new"
	composeReply   composeMode = "reply"
	composeForward composeMode = "forward"
)

// perPage matches Gmail's default page size.
const perPage = 50

// overlay is any full-window layer drawn above the mailbox.
type overlay interface {
	Layout(gtx layout.Context, a *App) layout.Dimensions
}

// App owns the whole UI state. All fields are written on the UI goroutine
// except for the window handle, the post queue and the quit flag, which are
// shared with the tray callbacks.
type App struct {
	// Window and process lifetime.
	// winMu guards win, which is nil while the process lives only in the tray.
	winMu    sync.Mutex
	win      *app.Window
	th       *theme
	explorer *explorer.Explorer

	// Accounts and the backend that serves each one.
	accounts []mail.Account
	services map[string]mail.Service
	// accountStore holds the OAuth credentials and tokens of the real accounts.
	// It is shared with the Gmail backends, which write refreshed tokens back.
	accountStore *accounts.Store
	// newService builds the backend of an account added while the app runs. It
	// is nil for an App assembled by a test that injects no factory.
	newService func(mail.Account) mail.Service
	// proxy is the shared outbound proxy, empty for a direct connection. Only
	// the accounts whose stored entry opts in use it.
	proxy string
	// factory reflects the proxy setting into the backends it builds; nil when
	// the app was assembled without real accounts.
	factory *serviceFactory
	// logLevel is the normalized console log level. The logger itself is
	// process wide; the field only mirrors the choice for the settings panel.
	logLevel string
	// accountAdd is the sign-in dialog while it is open. It owns the pending
	// authorization context so closing the panel can cancel the wait.
	accountAdd *accountDialog

	// The active mailbox: which account, folder and query the list shows.
	activeAcc    string
	activeFolder string
	query        string
	folders      []mail.Folder
	// labels resolves the ids a thread carries to the chips its row draws. The
	// sidebar rebuilds it whenever it applies a new folder list.
	labels labelIndex

	// Cursor paging and the sidebar fold.
	// page is the 1-based page number shown in the list toolbar. Backends hand
	// out opaque cursors, so tokens keeps the one that opened each page reached
	// so far: tokens[i] opens page i+1 and tokens[0] is always the empty cursor.
	page   int
	tokens []string

	sidebarCollapsed bool

	// Decoration mode, mirrored from the platform so the title bar can adapt.
	// decorated reports whether the platform draws the window decorations.
	// When it does not, the app draws Gio's title bar itself and the header
	// switches to the compact CSD layout.
	decorated bool
	// maximized mirrors the window mode, so the title bar can show the right
	// maximize glyph and toggle it.
	maximized bool
	// csd forces the client-side title bar even though the platform reports
	// server-side decorations. Window managers that hide their frame without
	// saying so (KWin answers server-side for its "no border" rule) need it.
	csd bool
	// windowActions collects the title bar requests of the current frame; they
	// run after the frame so a close cannot tear the window down mid-layout.
	windowActions system.Action

	// The open conversation and the list cursor the keyboard acts on.
	selectedThread string
	thread         mail.ThreadSummary
	msgs           []*mail.Message
	expanded       map[string]bool
	loadingThread  bool

	// cursorThread is the row the keyboard acts on, -1 when there is none.
	cursorThread int
	// cursorMoved asks the list to scroll the cursor back into view on the
	// next layout.
	cursorMoved bool
	// searchFocused mirrors the focus of the search editor: single-letter
	// shortcuts must not steal keys while the user types a query.
	searchFocused bool
	// blurRequested asks the next frame to move the keyboard focus back to
	// the window root, away from the search editor.
	blurRequested bool

	// Child views, each rebuilt only when its content changes.
	top     *topBar
	side    *sidebarView
	toolbar *listToolbar
	list    *messageListView
	reader  *readerView
	status  *statusBar
	perf    *perfMonitor

	// The floating layer: at most one panel or menu at a time.
	overlay  overlay
	compose  *composeView
	settings *settingsView
	menu     *menuOverlay

	// Request generations and the cancel of the request each one supersedes.
	listGen, folderGen, msgGen int

	// listCancel, folderCancel and msgCancel stop the in-flight request of
	// their domain when a newer one supersedes it. Only the UI goroutine
	// touches them.
	listCancel, folderCancel, msgCancel context.CancelFunc

	// Callbacks handed to the UI goroutine from a worker or the tray.
	postMu sync.Mutex
	queue  []func()
	// retry is the single pending redraw retry shared by every post call:
	// one timer per burst, not one per callback. Guarded by postMu.
	retry *time.Timer

	// Startup gate, tray wakeups and the scratch directory for attachments.
	// startOnce gates the initial folders/threads fetch so it runs exactly
	// once, no matter how many windows the lifecycle loop creates. loaded
	// records that the first fetch happened, so a window recreated after a
	// detach refreshes instead of trusting stale data.
	startOnce sync.Once
	loaded    bool

	// openCh and quitCh wake the lifecycle loop while no window is open. Both
	// are buffered so tray callbacks never block.
	openCh   chan struct{}
	quitCh   chan struct{}
	quitting atomic.Bool

	// tray mirrors the unread state into the notification area. It is nil when
	// the platform has no notification area.
	tray *tray.Tray

	tmpDir string

	// pointer is the pointer position in window coordinates, captured by the
	// root pointer area. The tip replay anchors the hover labels next to it.
	pointer image.Point
}

// headerHeight is the top bar height for the current decoration mode. Under a
// client-side title bar the header shrinks so the two rows stay compact.
func (a *App) headerHeight() unit.Dp {
	if a.clientSide() {
		return topBarHeightCSD
	}
	return topBarHeight
}

// clientSide reports whether the application draws the window title bar. The
// setting only applies on Wayland: Gio always asks the X11 window manager to
// decorate, so a bar drawn there would duplicate the frame.
func (a *App) clientSide() bool {
	return !a.decorated || (a.csd && os.Getenv("WAYLAND_DISPLAY") != "")
}

// appOptions is the single assembly point for the backends. newApp fills it
// from accounts.json (stored Gmail accounts plus the shipped demo one), and
// newService builds the backend of each. The zero value keeps the demo account
// served by the mock. A newService that returns nil leaves the account without
// a backend, which is how tests install their own fake.
type appOptions struct {
	accounts   []mail.Account
	newService func(mail.Account) mail.Service
	// The real-account assembly: the shared credential store, the proxy the
	// accounts may use and the factory that reflects proxy changes. They are
	// nil for the zero options and for test apps.
	store   *accounts.Store
	proxy   string
	factory *serviceFactory
}

// demoAccountID identifies the shipped demo mailbox, which is always served by
// the mock backend so the app opens with something to show.
const demoAccountID = "demo-gmail"

// demoAccounts is the account list of a zero configuration app.
func demoAccounts() []mail.Account {
	return []mail.Account{
		{ID: demoAccountID, Kind: mail.KindGmail, Name: "Ada Chen", Email: "ada.chen@gmail.com"},
	}
}

// withDefaults fills the empty fields of opts.
func (o appOptions) withDefaults() appOptions {
	if len(o.accounts) == 0 {
		o.accounts = demoAccounts()
	}
	if o.newService == nil {
		o.newService = func(acc mail.Account) mail.Service { return mock.New(acc) }
	}
	return o
}

func newApp() *App {
	pref, err := prefs.LoadOrZero()
	if err != nil {
		// A settings file that cannot be read must not keep the window from
		// opening: the defaults are used and the next save rewrites the file.
		slog.Warn("read settings", "err", err)
	}
	// The stored level takes effect before anything below can log, so a
	// quiet level also silences the startup warnings.
	logging.SetLevel(pref.LogLevel)
	i18n.SetLanguage(resolveLanguage(pref.Language))

	store, err := accounts.Open(prefs.Dir())
	if err != nil {
		// A broken accounts file costs the stored accounts, not the startup.
		slog.Warn("read accounts", "err", err)
	}
	factory := &serviceFactory{store: store, proxy: pref.Proxy}
	return newAppWith(pref, appOptions{
		accounts:   configuredAccounts(store),
		newService: factory.newService,
		store:      store,
		proxy:      pref.Proxy,
		factory:    factory,
	})
}

// configuredAccounts lists the stored accounts followed by the demo one. An
// account without a refresh token cannot be read, so it is skipped with a
// warning instead of failing on the first request. The same applies to an
// entry without a kind: it predates the typed format and cannot be handed to a
// backend, so the user signs in again to write a new one.
func configuredAccounts(store *accounts.Store) []mail.Account {
	out := make([]mail.Account, 0, len(store.List())+1)
	for _, e := range store.List() {
		if e.Kind == "" {
			slog.Warn("skip account without a kind", "account", e.ID)
			continue
		}
		if e.Token.RefreshToken == "" {
			slog.Warn("skip account without a refresh token", "account", e.ID)
			continue
		}
		out = append(out, mail.Account{ID: e.ID, Kind: e.Kind, Name: e.Name, Email: e.Email})
	}
	return append(out, demoAccounts()...)
}

// serviceFactory builds the backend of one account and follows the proxy
// setting: changing it replaces the backends that use it through the same
// value. Gmail is the only real backend for now; the kind switch is the point
// where another one is added.
type serviceFactory struct {
	store *accounts.Store
	proxy string
}

func (f *serviceFactory) newService(acc mail.Account) mail.Service {
	if acc.ID == demoAccountID {
		return mock.New(acc)
	}
	if acc.Kind != mail.KindGmail {
		slog.Warn("no backend for account kind", "account", acc.ID, "kind", acc.Kind)
		return nil
	}
	entry, ok := f.store.Entry(acc.ID)
	if !ok {
		return nil
	}
	var opts []gmail.Option
	if entry.UseProxy {
		opts = append(opts, gmail.WithProxyURL(f.proxy))
	}
	svc, err := gmail.New(acc, f.store, opts...)
	if err != nil {
		slog.Warn("build gmail backend", "account", acc.ID, "err", err)
		return nil
	}
	return svc
}

// newAppWith assembles the app from already resolved settings and backends. It
// touches no global state besides the views, so tests build their app here
// instead of duplicating the field list.
func newAppWith(pref prefs.Settings, opts appOptions) *App {
	opts = opts.withDefaults()
	a := &App{
		th:           newTheme(loadFonts()),
		services:     map[string]mail.Service{},
		accounts:     opts.accounts,
		accountStore: opts.store,
		newService:   opts.newService,
		proxy:        opts.proxy,
		factory:      opts.factory,
		logLevel:     logging.Normalize(pref.LogLevel),
		page:         1,
		tokens:       []string{""},
		expanded:     map[string]bool{},
		decorated:    true,
		csd:          pref.ClientDecorations,
		status:       &statusBar{},
		cursorThread: -1,
		openCh:       make(chan struct{}, 1),
		quitCh:       make(chan struct{}, 1),
	}

	for _, acc := range a.accounts {
		if svc := opts.newService(acc); svc != nil {
			a.services[acc.ID] = svc
		}
	}
	a.activeAcc = a.accounts[0].ID
	a.activeFolder = "inbox"

	a.top = newTopBar(a)
	a.side = newSidebar(a)
	// The list must exist before the toolbar: the toolbar binds a.list method
	// values (e.g. a.list.clearSelection) as button callbacks.
	a.list = newMessageList(a)
	a.toolbar = newListToolbar(a)
	a.reader = newReader(a)
	a.perf = newPerfMonitor(a)
	if pref.ShowPerfMonitor {
		a.perf.setVisible(true)
	}
	return a
}

// startInitialLoad fetches the folders and the first page of threads the
// first time a frame is drawn. newApp must not kick this off: the load can
// finish before Gio re-arms the window's invalidation flag, and a redraw
// request issued while a frame is still being processed is silently dropped,
// which used to leave the mail list empty until the next pointer event.
func (a *App) startInitialLoad() {
	a.startOnce.Do(func() {
		a.reloadAll()
		a.loaded = true
	})
}

// rowLabels returns the chips a conversation row draws: the user labels of the
// conversation except the one the list is currently filtered by, which every
// row would repeat.
func (a *App) rowLabels(sum mail.ThreadSummary) []labelChip {
	return a.labels.chips(sum.Labels, a.activeFolder)
}

// resolveLanguage picks the language for this run: an explicit preference
// first, then the system locale, then English.
func resolveLanguage(pref string) string {
	if pref != "" && !strings.EqualFold(pref, "auto") {
		return i18n.Match(pref)
	}
	return i18n.Match(i18n.SystemLocale())
}

// Timeouts for the backend calls. They are semantic rather than per-call knobs:
// folders and lists must answer fast enough to keep the UI honest, a mutation
// may take a little longer, and a send may wait on a slow SMTP server.
const (
	timeoutFolders  = 15 * time.Second
	timeoutList     = 30 * time.Second
	timeoutThread   = 30 * time.Second
	timeoutMutation = 20 * time.Second
	timeoutSend     = 2 * time.Minute
	timeoutAttach   = time.Minute
)

// async runs fn off the UI goroutine with a timeout and posts the outcome back
// to the queue. guard runs on the UI goroutine before either callback: when it
// reports false the answer belongs to a superseded request (another account,
// folder, page or conversation) and is dropped. A nil guard keeps the result.
// onError may be nil, in which case the shared failure toast is shown.
//
// The returned cancel stops the request. Callers whose request can be
// superseded store it, so the next one cancels instead of letting the old one
// run to its timeout.
func (a *App) async(op string, timeout time.Duration, guard func() bool, fn func(context.Context) error, onSuccess func(), onError func(error)) context.CancelFunc {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		defer cancel()
		err := fn(ctx)
		a.post(func() {
			if guard != nil && !guard() {
				return
			}
			if err != nil {
				// A request that was superseded or belongs to a closed window
				// is not a failure the user needs to hear about.
				if errors.Is(err, context.Canceled) {
					return
				}
				logErr(op, err)
				if onError != nil {
					onError(err)
					return
				}
				a.notify(i18n.T("toast.failed", map[string]string{"Error": errorText(err)}), true)
				return
			}
			if onSuccess != nil {
				onSuccess()
			}
		})
	}()
	return cancel
}

// cancelRequest stops the request behind p, if any, and clears it.
func cancelRequest(p *context.CancelFunc) {
	if *p != nil {
		(*p)()
		*p = nil
	}
}

// cancelRequests stops every in-flight backend call. The tray keeps the app
// alive after its window closes, so detached work must not keep running.
func (a *App) cancelRequests() {
	cancelRequest(&a.listCancel)
	cancelRequest(&a.folderCancel)
	cancelRequest(&a.msgCancel)
}
