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

func (a *App) reloadAll() {
	a.reloadFolders()
	a.loadThreads()
}

func (a *App) reloadFolders() {
	svc := a.service()
	if svc == nil {
		return
	}
	cancelRequest(&a.folderCancel)
	a.folderGen++
	gen, accID := a.folderGen, a.activeAcc
	var folders []mail.Folder
	a.folderCancel = a.async("folders", timeoutFolders,
		func() bool { return gen == a.folderGen && accID == a.activeAcc },
		func(ctx context.Context) error {
			var err error
			folders, err = svc.Folders(ctx)
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

// loadThreads loads the current page and resets the list scroll: it serves
// page, folder and search changes, where the previous position is meaningless.
func (a *App) loadThreads() {
	a.loadThreadsKeepingScroll(false)
}

// refreshThreads reloads the current page after a mutation and keeps the
// scroll position, so archiving a row does not jump back to the top.
func (a *App) refreshThreads() {
	a.loadThreadsKeepingScroll(true)
}

func (a *App) loadThreadsKeepingScroll(keepScroll bool) {
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
	a.toolbar.setLoading(true)
	var result mail.ThreadPage
	a.listCancel = a.async("list threads", timeoutList,
		func() bool { return gen == a.listGen && accID == a.activeAcc },
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
			if keepScroll {
				a.list.refreshThreads(result.Threads)
			} else {
				a.list.setThreads(result.Threads)
			}
			a.clampCursor()
			a.toolbar.setPage(page, result.Total, len(result.Threads), result.NextPageToken != "")
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
	a.hideOverlay()
	a.goToPage(1)
	a.side.reload()
}

func (a *App) activeAccount() mail.Account {
	for _, acc := range a.accounts {
		if acc.ID == a.activeAcc {
			return acc
		}
	}
	if len(a.accounts) > 0 {
		return a.accounts[0]
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
	// Cursors are per account and per folder: the new account starts over.
	a.page, a.tokens = 1, []string{""}
	a.query = ""
	a.top.clearSearch()
	a.resetSelection()
	a.list.clearSelection()
	a.list.setThreads(nil)
	a.hideOverlay()
	a.reloadFolders()
	a.loadThreads()
	a.notify(i18n.T("toast.accountSwitched", map[string]string{"Name": a.accountName()}), false)
}

func (a *App) openHTMLInBrowser(m *mail.Message) {
	if m == nil || strings.TrimSpace(m.HTMLBody) == "" {
		return
	}
	dir, err := a.tempDir()
	if err != nil {
		logErr("temp dir", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
		return
	}
	path, err := writeMessageHTML(dir, m.ID, m.HTMLBody)
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
