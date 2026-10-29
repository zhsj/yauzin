package ui

import (
	"context"
	"log/slog"
	"strings"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func (a *App) toggleSidebar() { a.sidebarCollapsed = !a.sidebarCollapsed }

// service returns the backend for the active account.
func (a *App) service() mail.Service { return a.services[a.activeAcc] }

// reloadAll loads the sidebar and the current page in a fixed order: the folder
// names first, then the page rows, then the folder counters. Each step starts
// on the success callback of the previous one, so the requests run one stage at
// a time.
func (a *App) reloadAll() {
	a.loadFolderNames(func() {
		a.loadThreadsKeepingScroll(false, a.loadFolderDetails)
	})
}

// reloadFolders reloads just the sidebar: the names, then their counters.
func (a *App) reloadFolders() {
	a.loadFolderNames(a.loadFolderDetails)
}

// reloadChecked checks the mailbox and then reloads it in full. Every full load
// goes through it, so the history baseline the check records always precedes
// the data it describes: a response cached before the baseline could already be
// stale by the time the baseline was taken.
//
// It is the automatic entry (startup, window reattach): it reuses the answer of
// a check that just ran instead of asking Gmail again. A request the user made
// uses reloadCheckedNow.
func (a *App) reloadChecked() { a.reloadCheckedWith(false) }

// reloadCheckedNow is reloadChecked for a request the user made. It always
// checks the mailbox, skipping the cooldown an automatic refresh honours.
func (a *App) reloadCheckedNow() { a.reloadCheckedWith(true) }

// reloadCheckedWith arms the spinner and starts the check; the full load runs
// on its answer.
func (a *App) reloadCheckedWith(force bool) {
	if a.service() == nil {
		return
	}
	// The check is part of the load the user asked for, so the spinner covers
	// it: a refresh that only started turning when the rows were requested
	// would look slow to react.
	a.toolbar.setLoading(true)
	a.checkChanges(func(bool) { a.reloadAll() }, force)
}

// loadFolderNames loads the sidebar names without the unread counters or label
// colours, then runs next on the UI goroutine.
func (a *App) loadFolderNames(next func()) {
	svc := a.service()
	if svc == nil {
		return
	}
	// A newer load leaves the request already in flight alone: the generation
	// guard below drops its answer, and cancelling it here would let the next
	// refresh kill the sidebar reload a completed check just started.
	a.folderGen++
	gen, accID := a.folderGen, a.activeAcc
	guard := func() bool { return gen == a.folderGen && accID == a.activeAcc }
	var folders []mail.Folder
	a.folderCancel = a.async("folders", timeoutFolders, guard,
		func(ctx context.Context) error {
			var err error
			folders, err = svc.Folders(ctx)
			return err
		},
		func() {
			a.folders = folders
			a.side.reload()
			if next != nil {
				next()
			}
		},
		func(err error) {
			// reloadChecked armed the spinner before the check: a sidebar
			// failure means the list stage it would have cleared it in never
			// runs.
			a.toolbar.setLoading(false)
			a.notify(i18n.T("toast.foldersFailed", map[string]string{"Error": errorText(err)}), true)
		})
}

// loadFolderDetails fills the unread counters and colours of the folders that
// loadFolderNames put on screen.
func (a *App) loadFolderDetails() {
	svc := a.service()
	if svc == nil {
		return
	}
	names := append([]mail.Folder(nil), a.folders...)
	if len(names) == 0 {
		return
	}
	gen, accID := a.folderGen, a.activeAcc
	guard := func() bool { return gen == a.folderGen && accID == a.activeAcc }
	var folders []mail.Folder
	a.folderCancel = a.async("folder details", timeoutFolders, guard,
		func(ctx context.Context) error {
			var err error
			folders, err = svc.FolderDetails(ctx, names)
			return err
		},
		func() {
			a.folders = folders
			a.side.reload()
			// The counters land here and nowhere else, so this is where the
			// tray badge follows them: start, refresh, poll and account switch
			// all end in this callback.
			a.refreshTrayIcon()
		},
		func(err error) {
			a.notify(i18n.T("toast.foldersFailed", map[string]string{"Error": errorText(err)}), true)
		})
}

// goToPage loads a page by number. Only pages whose cursor is known can be
// opened, which is what keeps the toolbar from asking for a page the backend
// never pointed at.
func (a *App) goToPage(page int) {
	if page < 1 {
		page = 1
	}
	if page == 1 {
		// The first page always starts at the empty cursor, and the cursors seen
		// afterwards may be stale once the list is reloaded.
		a.tokens = []string{""}
	}
	if _, ok := a.pageToken(page); !ok {
		return
	}
	a.page = page
	// The cursor belongs to the rows of one page: a page change drops it.
	a.cursorThread = -1
	a.loadThreads()
}

// pageToken returns the cursor that opens page, when it has been seen.
func (a *App) pageToken(page int) (string, bool) {
	if page <= 1 {
		return "", true
	}
	if page-1 < len(a.tokens) && a.tokens[page-1] != "" {
		return a.tokens[page-1], true
	}
	return "", false
}

// rememberCursor records where the page after page starts and drops the
// cursors that pointed past it.
func (a *App) rememberCursor(page int, next string) {
	for len(a.tokens) < page {
		a.tokens = append(a.tokens, "")
	}
	a.tokens = append(a.tokens[:page], next)
}

// clearList empties the page before the folder, the query or the account
// changes: the rows on screen describe the view being left, so they would keep
// drawing their label chips until the new page arrives.
func (a *App) clearList() {
	// setThreads already drops the bulk selection and puts the scroll back at
	// the top; setPage then blanks the range label and disables the arrows.
	a.list.setThreads(nil)
	a.toolbar.setPage(1, 0, 0, false)
}

// loadThreads loads the current page and resets the list scroll: it serves
// page, folder and search changes, where the previous position is meaningless.
func (a *App) loadThreads() {
	a.loadThreadsKeepingScroll(false, nil)
}

// refreshThreads reloads the current page after a mutation and keeps the
// scroll position, so archiving a row does not jump back to the top.
func (a *App) refreshThreads() {
	a.loadThreadsKeepingScroll(true, nil)
}

// loadThreadsKeepingScroll loads the current page and, when after is not nil,
// runs it on the UI goroutine once the page is on screen.
func (a *App) loadThreadsKeepingScroll(keepScroll bool, after func()) {
	svc := a.service()
	if svc == nil {
		return
	}
	token, ok := a.pageToken(a.page)
	if !ok {
		token, a.page = "", 1
	}
	cancelRequest(&a.listCancel)
	a.listGen++
	gen, accID := a.listGen, a.activeAcc
	page := a.page
	// Snapshot the view before the worker starts: a folder switch or an
	// account change must not be read from the worker goroutine.
	folder, query := a.activeFolder, a.query
	guard := func() bool { return gen == a.listGen && accID == a.activeAcc }
	a.toolbar.setLoading(true)
	var result mail.ThreadPage
	a.listCancel = a.async("list threads", timeoutList, guard,
		func(ctx context.Context) error {
			var err error
			if query != "" {
				result, err = svc.SearchThreads(ctx, mail.SearchRequest{
					Query:     query,
					PageToken: token,
					Limit:     perPage,
				})
				return err
			}
			result, err = svc.ListThreads(ctx, mail.ListRequest{
				FolderID:  folder,
				PageToken: token,
				Limit:     perPage,
			})
			return err
		},
		func() {
			a.toolbar.setLoading(false)
			// Archiving or deleting can empty the page that is on screen; step
			// back one page until something is left.
			if len(result.Threads) == 0 && page > 1 {
				a.goToPage(page - 1)
				return
			}
			a.page = page
			a.rememberCursor(page, result.NextPageToken)
			if len(result.Threads) == 0 {
				// An empty page: drop whatever the previous view or a refresh
				// left behind.
				a.list.setThreads(nil)
			} else if keepScroll {
				// A refresh keeps the scroll position and the rows the user
				// ticked.
				a.list.mergeThreads(result.Threads)
			} else {
				a.list.setThreads(result.Threads)
			}
			a.clampCursor()
			a.toolbar.setPage(page, result.Total, len(result.Threads), result.NextPageToken != "")
			if after != nil {
				after()
			}
		},
		func(err error) {
			a.toolbar.setLoading(false)
			a.notify(errorText(err), true)
		})
}

// clearSearchInput empties the search box without leaving the search view: the
// list keeps the results of the query that is still active, so only the text
// being edited changes. The focus stays in the field, ready for the next query.
func (a *App) clearSearchInput() {
	a.top.clearSearch()
}

// setQuery switches the list to the global search view. An empty query is not a
// search, so it is ignored; a real one drops the folder selection, which is
// what tells every view that the list no longer belongs to a folder.
func (a *App) setQuery(q string) {
	q = strings.TrimSpace(q)
	if q == "" || q == a.query {
		return
	}
	a.query = q
	a.activeFolder = ""
	a.resetSelection()
	a.clearList()
	a.goToPage(1)
	a.side.reload()
}

func (a *App) selectFolder(id string) {
	if a.activeFolder == id {
		a.hideOverlay()
		return
	}
	a.activeFolder = id
	a.query = ""
	a.top.clearSearch()
	a.blurRequested = true
	a.resetSelection()
	a.clearList()
	a.hideOverlay()
	a.goToPage(1)
	a.side.reload()
}

// folderTotal returns the conversation count FolderDetails stored for the given
// folder or label. It reports zero when the folder is unknown or its details
// have not arrived yet, so callers fall back to the list's own estimate.
func (a *App) folderTotal(id string) int {
	for _, f := range a.folders {
		if f.ID == id {
			return f.Total
		}
	}
	return 0
}

// viewingKind reports whether the list is showing a folder of the given kind.
// The folder list is the source of truth; the ID fallback covers the frame
// before it arrives, where the built-in folders still carry their names as IDs.
func (a *App) viewingKind(kind mail.FolderKind) bool {
	for _, f := range a.folders {
		if f.ID == a.activeFolder {
			return f.Kind == kind
		}
	}
	return a.activeFolder == string(kind)
}

// viewingSpam reports whether the list is showing the spam folder. Spam is
// kept out of search and labels, so a page in that folder holds nothing but
// spam, which is what lets one button serve both directions.
func (a *App) viewingSpam() bool { return a.viewingKind(mail.FolderSpam) }

// viewingTrash reports whether the list is showing the trash. The trash hides
// the spam button: filing a message there as spam would change nothing.
func (a *App) viewingTrash() bool { return a.viewingKind(mail.FolderTrash) }

// showsSpamAction reports whether the current folder gets a spam button. It is
// the one place the trash exclusion is decided, so the reader, the rows and the
// bulk toolbar cannot disagree.
func (a *App) showsSpamAction() bool { return !a.viewingTrash() }

// activeAccount returns the mailbox in use. An empty activeAcc means no
// mailbox is selected (the account was removed); it must not fall back to the
// first one, or the header would name a mailbox the list never loaded.
func (a *App) activeAccount() mail.Account {
	for _, acc := range a.accounts {
		if acc.ID == a.activeAcc {
			return acc
		}
	}
	return mail.Account{}
}

func (a *App) accountName() string {
	acc := a.activeAccount()
	if acc.ID == "" {
		return i18n.T("app.brand")
	}
	return acc.DisplayName()
}

func (a *App) switchAccount(id string) {
	if id == "" || id == a.activeAcc {
		a.hideOverlay()
		return
	}
	if _, ok := a.services[id]; !ok {
		return
	}
	a.activeAcc = id
	// Cursors, folders and labels are per account: the new account starts over
	// in its inbox. A label id from the previous account would otherwise be
	// sent to a backend that does not know it.
	a.activeFolder = "inbox"
	a.page, a.tokens = 1, []string{""}
	a.query = ""
	a.top.clearSearch()
	a.folders = nil
	a.side.reload()
	a.resetInboxState()
	a.resetSelection()
	a.clearList()
	a.hideOverlay()
	// Checking first keeps the new account's cache free of entries fetched
	// before its baseline.
	a.toolbar.setLoading(true)
	a.checkChanges(func(bool) {
		a.reloadFolders()
		a.loadThreads()
	}, true)
	a.notify(i18n.T("toast.accountSwitched", map[string]string{"Name": a.accountName()}), false)
	a.rememberActiveAccount(id)
}

// previewMode selects the policy of a browser preview.
type previewMode int

const (
	// previewSafe injects the strict CSP: no scripts, no remote resources.
	previewSafe previewMode = iota
	// previewFull only declares the charset, so the message runs as it was
	// written. The menu names it unsafe; asking for it is the confirmation.
	previewFull
)

// openHTMLInBrowser hands the HTML body to the browser under the given policy.
func (a *App) openHTMLInBrowser(m *mail.Message, mode previewMode) {
	if m == nil || strings.TrimSpace(m.HTMLBody) == "" {
		return
	}
	dir, err := a.tempDir()
	if err != nil {
		logErr("temp dir", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
		return
	}
	write := writeMessageHTML
	if mode == previewFull {
		write = writeMessageHTMLUnsafe
	}
	path, err := write(dir, m.ID, m.HTMLBody)
	if err != nil {
		logErr("write html", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
		return
	}
	if err := openPath(path); err != nil {
		logErr("open html", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
	}
}

func logErr(op string, err error) {
	slog.Error("mail operation failed", "op", op, "err", err)
}
