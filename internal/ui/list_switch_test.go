package ui

import (
	"context"
	"sync"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// assertListCleared checks the state every folder, query and account change
// leaves behind before the new page arrives: no rows, no bulk selection, the
// list back at the top and no range label naming the previous view.
func assertListCleared(t *testing.T, a *App) {
	t.Helper()
	if len(a.list.threads) != 0 || len(a.list.rows) != 0 {
		t.Fatalf("list kept %d threads / %d rows, want none", len(a.list.threads), len(a.list.rows))
	}
	if n := a.list.selectionCount(); n != 0 {
		t.Fatalf("selection count = %d, want 0", n)
	}
	if pos := a.list.list.Position; pos != (layout.Position{}) {
		t.Fatalf("scroll position = %+v, want the top", pos)
	}
	if got := a.toolbar.rangeText(); got != "" {
		t.Fatalf("range label = %q, want none", got)
	}
	if a.selectedThread != "" {
		t.Fatalf("reader still shows thread %q, want the empty state", a.selectedThread)
	}
}

// seedList fills the list with rows that belong to the view being left, so a
// missing clear leaves the old rows and their label chips on screen.
func seedList(a *App) {
	a.list.setThreads(cursorThreads(3))
	a.list.selected["t0"] = true
	a.list.list.Position = layout.Position{First: 2, Offset: 12, Count: 1}
	a.toolbar.setPage(1, 120, 3, true)
	a.selectedThread = "t0"
}

// TestSelectFolderClearsTheListImmediately guards the first frame of a folder
// or label switch: the rows of the previous view must be gone before the new
// page is requested, not after it arrives.
func TestSelectFolderClearsTheListImmediately(t *testing.T) {
	a := testApp(t)
	seedList(a)

	a.selectFolder("work")

	if a.activeFolder != "work" {
		t.Fatalf("active folder = %q, want work", a.activeFolder)
	}
	assertListCleared(t, a)
}

// TestSetQueryClearsTheListImmediately covers the search box, which replaces
// the page the same way a folder switch does.
func TestSetQueryClearsTheListImmediately(t *testing.T) {
	a := testApp(t)
	seedList(a)

	a.setQuery("invoice")

	if a.query != "invoice" {
		t.Fatalf("query = %q, want invoice", a.query)
	}
	assertListCleared(t, a)
}

// TestSearchLeavesTheFolderAndSearchesEverywhere covers the switch from a
// folder to the global search view: the query drops the folder selection, the
// request is a search instead of a folder page, and no sidebar row stays
// highlighted.
func TestSearchLeavesTheFolderAndSearchesEverywhere(t *testing.T) {
	rec := &listRecorderService{}
	a := testApp(t)
	useService(a, rec)
	a.folders = []mail.Folder{
		{ID: "inbox", Kind: mail.FolderInbox},
		{ID: "work", Name: "Work", Kind: mail.FolderLabel},
	}
	a.side.reload()
	a.activeFolder = "work"

	a.setQuery("from:ada")

	if a.activeFolder != "" {
		t.Fatalf("active folder = %q during a search, want none", a.activeFolder)
	}
	waitFor(t, a, "the search request", func() bool { return rec.lastSearch() == "from:ada" })
	if n := rec.listCount(); n != 0 {
		t.Fatalf("folder pages requested during a search = %d, want none", n)
	}
	for _, item := range a.side.folders {
		if item.active {
			t.Fatalf("folder %q stays highlighted during a search", item.id)
		}
	}
	for _, item := range a.side.labels {
		if item.active {
			t.Fatalf("label %q stays highlighted during a search", item.id)
		}
	}
}

// TestEmptySearchLeavesTheViewAlone pins the submission guard: an empty box is
// not a search, so it neither reloads the list nor moves the folder selection.
func TestEmptySearchLeavesTheViewAlone(t *testing.T) {
	a := testApp(t)
	a.activeFolder = "inbox"

	a.setQuery("   ")

	if a.query != "" || a.activeFolder != "inbox" {
		t.Fatalf("empty search changed the view: query %q, folder %q", a.query, a.activeFolder)
	}
}

// TestClearSearchInputKeepsTheResults covers the ✕ of the pill: it empties the
// text being edited without leaving the search view or dropping the rows.
func TestClearSearchInputKeepsTheResults(t *testing.T) {
	a := testApp(t)
	seedList(a)
	a.query = "invoice"
	a.activeFolder = ""
	a.top.search.SetText("invoice")

	a.clearSearchInput()

	if got := a.top.search.Text(); got != "" {
		t.Fatalf("search box = %q after the clear button, want empty", got)
	}
	if a.query != "invoice" || a.activeFolder != "" {
		t.Fatalf("clearing the box left the view: query %q, folder %q", a.query, a.activeFolder)
	}
	if len(a.list.threads) != 3 {
		t.Fatalf("list rows = %d after clearing the box, want the results to stay", len(a.list.threads))
	}
}

// TestEscapeKeepsTheSearch covers the Esc order: it closes the reader but never
// drops the query, so the list behind it stays the search results.
func TestEscapeKeepsTheSearch(t *testing.T) {
	a := testApp(t)
	seedList(a)
	a.query = "invoice"
	a.activeFolder = ""
	a.selectedThread = "t0"

	a.escape(testGtx(new(op.Ops), testWindow))

	if a.selectedThread != "" {
		t.Fatalf("reader still shows %q after Esc", a.selectedThread)
	}
	if a.query != "invoice" || a.activeFolder != "" {
		t.Fatalf("Esc left the search: query %q, folder %q", a.query, a.activeFolder)
	}
	if len(a.list.threads) != 3 {
		t.Fatalf("list rows = %d after Esc, want the search results to stay", len(a.list.threads))
	}
}

// listRecorderService remembers the list and search requests the UI sent, so a
// test can assert which view the mailbox was asked for. The worker goroutines
// call it while the test reads it, hence the lock.
type listRecorderService struct {
	fakeService
	mu       sync.Mutex
	reqs     []mail.ListRequest
	searches []mail.SearchRequest
}

func (s *listRecorderService) ListThreads(_ context.Context, req mail.ListRequest) (mail.ThreadPage, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return mail.ThreadPage{}, nil
}

func (s *listRecorderService) SearchThreads(_ context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	s.mu.Lock()
	s.searches = append(s.searches, req)
	s.mu.Unlock()
	return mail.ThreadPage{}, nil
}

// lastFolder returns the folder of the newest list request, or "" before the
// first.
func (s *listRecorderService) lastFolder() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		return ""
	}
	return s.reqs[len(s.reqs)-1].FolderID
}

// listCount returns how many folder pages were requested.
func (s *listRecorderService) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// lastSearch returns the query of the newest search, or "" before the first.
func (s *listRecorderService) lastSearch() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.searches) == 0 {
		return ""
	}
	return s.searches[len(s.searches)-1].Query
}

// TestSwitchAccountResetsTheFolder covers the label of the account being left:
// its id belongs to that mailbox, so the next account must be asked for its
// inbox instead of a folder it does not know.
func TestSwitchAccountResetsTheFolder(t *testing.T) {
	accounts := []mail.Account{
		{ID: "one", Kind: mail.KindGmail, Email: "one@example.com"},
		{ID: "two", Kind: mail.KindGmail, Email: "two@example.com"},
	}
	recorders := make(map[string]*listRecorderService, len(accounts))
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: accounts,
		newService: func(acc mail.Account) mail.Service {
			svc := &listRecorderService{}
			recorders[acc.ID] = svc
			return svc
		},
	})
	// The first account is showing a label when the switch happens.
	a.folders = []mail.Folder{
		{ID: "inbox", Kind: mail.FolderInbox},
		{ID: "work", Name: "Work", Kind: mail.FolderLabel},
	}
	a.side.reload()
	a.activeFolder = "work"
	seedList(a)

	a.switchAccount("two")

	if a.activeFolder != "inbox" {
		t.Fatalf("active folder = %q after the switch, want inbox", a.activeFolder)
	}
	if a.folders != nil {
		t.Fatalf("folders = %+v after the switch, want them cleared", a.folders)
	}
	if len(a.labels) != 0 {
		t.Fatalf("label index = %d entries after the switch, want none", len(a.labels))
	}
	assertListCleared(t, a)

	waitFor(t, a, "the first page of the second account", func() bool {
		return recorders["two"].lastFolder() != ""
	})
	if got := recorders["two"].lastFolder(); got != "inbox" {
		t.Fatalf("list requested folder %q, want inbox", got)
	}
}

// TestSwitchAccountRemembersTheAccount covers the stored choice: the next start
// reopens this mailbox, and the write keeps the settings the switch did not
// touch.
func TestSwitchAccountRemembersTheAccount(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	if err := prefs.Save(prefs.Settings{ActiveAccount: "one", Language: "en"}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	var saved []prefs.Settings
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: []mail.Account{
			{ID: "one", Kind: mail.KindGmail, Email: "one@example.com"},
			{ID: "two", Kind: mail.KindGmail, Email: "two@example.com"},
		},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
		saveSettings: func(s prefs.Settings) error {
			saved = append(saved, s)
			return nil
		},
	})

	a.switchAccount("two")

	if len(saved) != 1 {
		t.Fatalf("settings written %d times, want 1", len(saved))
	}
	if got := saved[0].ActiveAccount; got != "two" {
		t.Fatalf("stored account = %q, want two", got)
	}
	if got := saved[0].Language; got != "en" {
		t.Fatalf("stored language = %q, want the untouched en", got)
	}
}

// TestSwitchAccountSkipsAnAlreadyStoredChoice keeps the file from being
// rewritten when it already names the mailbox being opened.
func TestSwitchAccountSkipsAnAlreadyStoredChoice(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	if err := prefs.Save(prefs.Settings{ActiveAccount: "two"}); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	writes := 0
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: []mail.Account{
			{ID: "one", Kind: mail.KindGmail, Email: "one@example.com"},
			{ID: "two", Kind: mail.KindGmail, Email: "two@example.com"},
		},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
		saveSettings: func(prefs.Settings) error {
			writes++
			return nil
		},
	})

	a.switchAccount("two")

	if writes != 0 {
		t.Fatalf("settings written %d times, want none", writes)
	}
}
