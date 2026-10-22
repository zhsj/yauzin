package ui

import (
	"testing"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func TestGoToPageNeedsAKnownCursor(t *testing.T) {
	a := testApp(t)

	// Nothing has reported a cursor yet, so page 2 is unreachable.
	a.goToPage(2)
	if a.page != 1 {
		t.Fatalf("page = %d after asking for an unknown page, want 1", a.page)
	}

	a.rememberCursor(1, "cursor-2")
	a.goToPage(2)
	if a.page != 2 {
		t.Fatalf("page = %d, want 2", a.page)
	}

	a.rememberCursor(2, "cursor-3")
	a.goToPage(3)
	if a.page != 3 {
		t.Fatalf("page = %d, want 3", a.page)
	}

	// The last page has no cursor, so there is nothing after it.
	a.rememberCursor(3, "")
	if _, ok := a.pageToken(4); ok {
		t.Fatal("page 4 is reachable although the backend ended on page 3")
	}
}

func TestGoToFirstPageResetsCursors(t *testing.T) {
	a := testApp(t)
	a.rememberCursor(1, "cursor-2")
	a.rememberCursor(2, "cursor-3")

	a.goToPage(1)
	if a.page != 1 {
		t.Fatalf("page = %d, want 1", a.page)
	}
	if len(a.tokens) != 1 || a.tokens[0] != "" {
		t.Fatalf("cursors = %v, want only the empty first-page cursor", a.tokens)
	}
	if _, ok := a.pageToken(2); ok {
		t.Fatal("page 2 is still reachable after a reset")
	}
}

func TestRememberCursorDropsStalePages(t *testing.T) {
	a := testApp(t)
	a.rememberCursor(1, "cursor-2")
	a.rememberCursor(2, "cursor-3")

	// Reloading page 1 with no next page must invalidate everything after it.
	a.rememberCursor(1, "")
	if len(a.tokens) != 2 {
		t.Fatalf("cursors = %v, want two entries", a.tokens)
	}
	if _, ok := a.pageToken(2); ok {
		t.Fatal("a stale cursor survived the reload")
	}
}

func TestToolbarRangeLabel(t *testing.T) {
	tests := []struct {
		name                       string
		page, total, count         int
		hasNext                    bool
		wantStart, wantEnd, wantOK int
	}{
		{name: "first page", page: 1, total: 112, count: 25, hasNext: true, wantStart: 1, wantEnd: 25, wantOK: 112},
		{name: "middle page", page: 2, total: 112, count: 25, hasNext: true, wantStart: 26, wantEnd: 50, wantOK: 112},
		{name: "last page", page: 5, total: 112, count: 12, hasNext: false, wantStart: 101, wantEnd: 112, wantOK: 112},
		{name: "empty page", page: 1, total: 0, count: 0, hasNext: false, wantStart: 0, wantEnd: 0, wantOK: 0},
		// A page with rows but no total still renders its range; rangeText
		// decides whether to show "of N" separately.
		{name: "unknown total", page: 1, total: -1, count: 25, hasNext: true, wantStart: 1, wantEnd: 25, wantOK: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bar listToolbar
			bar.setPage(tt.page, tt.total, tt.count, tt.hasNext)
			if bar.rangeStart != tt.wantStart || bar.rangeEnd != tt.wantEnd || bar.rangeTotal != tt.wantOK {
				t.Fatalf("range = %d-%d of %d, want %d-%d of %d",
					bar.rangeStart, bar.rangeEnd, bar.rangeTotal, tt.wantStart, tt.wantEnd, tt.wantOK)
			}
			if bar.hasNext != tt.hasNext {
				t.Fatalf("hasNext = %v, want %v", bar.hasNext, tt.hasNext)
			}
			if bar.page != tt.page {
				t.Fatalf("page = %d, want %d", bar.page, tt.page)
			}
		})
	}
}

// TestToolbarRangeTextPrefersFolderTotal covers the folder total override:
// browsing a folder shows the accurate count from FolderDetails instead of the
// unreliable resultSizeEstimate, while a search keeps the list estimate.
func TestToolbarRangeTextPrefersFolderTotal(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.DefaultLanguage) })
	i18n.SetLanguage("en")

	a := testApp(t)
	a.folders = []mail.Folder{{ID: "inbox", Kind: mail.FolderInbox, Total: 42}}
	a.activeFolder = "inbox"
	a.toolbar.setPage(1, 999, 25, true)

	if got := a.folderTotal("inbox"); got != 42 {
		t.Fatalf("folderTotal(inbox) = %d, want 42", got)
	}
	if got := a.folderTotal("nope"); got != 0 {
		t.Fatalf("folderTotal(nope) = %d, want 0", got)
	}

	// Browsing: the accurate folder total wins over the estimate.
	if got, want := a.toolbar.rangeText(), "1–25 of 42"; got != want {
		t.Fatalf("browsing range = %q, want %q", got, want)
	}

	// A search has no active folder, so the list estimate stays.
	a.query = "invoice"
	a.activeFolder = ""
	if got, want := a.toolbar.rangeText(), "1–25 of 999"; got != want {
		t.Fatalf("search range = %q, want %q", got, want)
	}

	// Browsing with the folder total not yet loaded falls back to the estimate
	// instead of hiding the range.
	a.query = ""
	a.activeFolder = "inbox"
	a.folders = nil
	if got, want := a.toolbar.rangeText(), "1–25 of 999"; got != want {
		t.Fatalf("unknown-total range = %q, want %q", got, want)
	}
}
