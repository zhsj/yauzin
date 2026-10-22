package ui

import (
	"testing"

	"gioui.org/layout"
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
