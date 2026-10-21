package ui

import "testing"

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
		{name: "first page", page: 1, total: 112, count: 50, hasNext: true, wantStart: 1, wantEnd: 50, wantOK: 112},
		{name: "middle page", page: 2, total: 112, count: 50, hasNext: true, wantStart: 51, wantEnd: 100, wantOK: 112},
		{name: "last page", page: 3, total: 112, count: 12, hasNext: false, wantStart: 101, wantEnd: 112, wantOK: 112},
		{name: "empty page", page: 1, total: 0, count: 0, hasNext: false, wantStart: 0, wantEnd: 0, wantOK: 0},
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
