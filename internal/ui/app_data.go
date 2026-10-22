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

// loadFolderNames loads the sidebar names without the unread counters or label
// colours, then runs next on the UI goroutine.
func (a *App) loadFolderNames(next func()) {
	svc := a.service()
	if svc == nil {
		return
	}
	cancelRequest(&a.folderCancel)
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
	req := mail.ListRequest{
		FolderID:  a.activeFolder,
		Query:     a.query,
		PageToken: token,
		Limit:     perPage,
	}
	guard := func() bool { return gen == a.listGen && accID == a.activeAcc }
	a.toolbar.setLoading(true)
	var result mail.ThreadPage
	a.listCancel = a.async("list threads", timeoutList, guard,
		func(ctx context.Context) error {
			var err error
			result, err = svc.ListThreads(ctx, req)
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

// clearSearchInput empties the search box and returns the list to the folder.
// The focus stays in the field, so a new query can be typed right away.
func (a *App) clearSearchInput() {
	a.top.clearSearch()
	a.setQuery("")
	a.invalidate()
}

func (a *App) setQuery(q string) {
	q = strings.TrimSpace(q)
	if q == a.query {
		return
	}
	a.query = q
	a.resetSelection()
	a.clearList()
	a.goToPage(1)
	a.side.reload()
}

func (a *App) selectFolder(id string) {
	if a.activeFolder == id && a.query == "" {
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
	a.resetSelection()
	a.clearList()
	a.hideOverlay()
	a.reloadFolders()
	a.loadThreads()
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

// openMessageSource fetches the original message and shows it as a page in the
// browser. The source is read on demand and written off the UI goroutine: the
// reader never keeps it, and a large message must not stall the frame.
func (a *App) openMessageSource(m *mail.Message) {
	svc := a.service()
	if m == nil || svc == nil {
		return
	}
	dir, err := a.tempDir()
	if err != nil {
		logErr("temp dir", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
		return
	}
	id, meta := m.ID, a.sourceMeta(m)
	var path string
	a.async("raw message", timeoutThread, nil,
		func(ctx context.Context) error {
			raw, err := svc.RawMessage(ctx, id)
			if err != nil {
				return err
			}
			path, err = writeMessageSource(dir, id, meta, raw)
			return err
		},
		func() {
			if err := openPath(path); err != nil {
				logErr("open source", err)
				a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
			}
		},
		func(err error) {
			a.notify(i18n.T("toast.openFailed", map[string]string{"Error": errorText(err)}), true)
		})
}

// sourceMeta collects the header block of the original message page. The
// values are display strings: the page only escapes them.
func (a *App) sourceMeta(m *mail.Message) sourceMeta {
	return sourceMeta{
		MessageID: m.ID,
		Account:   a.accountName(),
		From:      m.From.String(),
		To:        mail.FormatAddressList(m.To),
		Cc:        mail.FormatAddressList(m.Cc),
		Subject:   m.Subject,
		Date:      formatFullTime(m.Date),
	}
}

func logErr(op string, err error) {
	slog.Error("mail operation failed", "op", op, "err", err)
}
