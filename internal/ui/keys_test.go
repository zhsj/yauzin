package ui

import (
	"strconv"
	"testing"

	"gioui.org/layout"

	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

// TestShortcutTableIsConsistent guards the invariants the dispatch and the
// shortcuts page rely on: one row per action, one bit per row, readable caps,
// display order by group, and a translation for every label.
func TestShortcutTableIsConsistent(t *testing.T) {
	if got, want := len(shortcutTable), 13; got != want {
		t.Fatalf("shortcut table has %d rows, want %d", got, want)
	}
	bits := map[uint32]string{}
	names := map[string]string{}
	groups := map[shortcutGroup]bool{}
	prev := groupGlobal
	for _, s := range shortcutTable {
		if s.bit == 0 {
			t.Errorf("%s has no bit", s.action)
		}
		if other, ok := bits[s.bit]; ok {
			t.Errorf("%s shares its bit with %s", s.action, other)
		}
		bits[s.bit] = s.action

		if len(s.names) == 0 {
			t.Errorf("%s has no key names", s.action)
		}
		for _, name := range s.names {
			if other, ok := names[string(name)]; ok {
				t.Errorf("key %q is bound to both %s and %s", name, other, s.action)
			}
			names[string(name)] = s.action
		}
		if len(s.caps) == 0 {
			t.Errorf("%s has no key caps", s.action)
		}
		for _, cap := range s.caps {
			if cap == "" {
				t.Errorf("%s has an empty key cap", s.action)
			}
		}

		// The page groups rows by walking the table once, so a group may not
		// reappear after a later one started.
		if s.group < prev {
			t.Errorf("%s is out of display order", s.action)
		}
		prev = s.group
		groups[s.group] = true

		if got := i18n.T(s.action); got == s.action || got == "" {
			t.Errorf("no translation for %s", s.action)
		}
	}
	if len(groups) != 3 {
		t.Errorf("shortcut table covers %d groups, want 3", len(groups))
	}
}

func cursorThreads(n int) []mail.ThreadSummary {
	out := make([]mail.ThreadSummary, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, mail.ThreadSummary{
			ID:         "t" + strconv.Itoa(i),
			MessageIDs: []string{"m" + strconv.Itoa(i)},
		})
	}
	return out
}

func TestMoveCursorStopsAtTheEdges(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(3))
	a.cursorThread = -1

	a.moveCursor(1)
	if got := a.cursorID(); got != "t0" {
		t.Fatalf("cursor = %q after the first move, want t0", got)
	}

	a.moveCursor(-1)
	if got := a.cursorID(); got != "t0" {
		t.Fatalf("cursor = %q at the top edge, want t0", got)
	}

	a.moveCursor(10)
	if got := a.cursorID(); got != "t2" {
		t.Fatalf("cursor = %q at the bottom edge, want t2", got)
	}
}

func TestClampCursorAfterReload(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(5))
	a.cursorThread = 4

	a.list.setThreads(cursorThreads(2))
	a.clampCursor()
	if got := a.cursorID(); got != "t1" {
		t.Fatalf("cursor = %q after the page shrank, want t1", got)
	}

	a.list.setThreads(nil)
	a.clampCursor()
	if a.cursorThread != -1 {
		t.Fatalf("cursor index = %d on an empty page, want -1", a.cursorThread)
	}
}

func TestRefreshThreadsKeepsTheScrollPosition(t *testing.T) {
	a := testApp(t)
	a.list.list.Position = layout.Position{First: 12, Offset: 40, Count: 5}

	a.list.refreshThreads(cursorThreads(20))
	if got := a.list.list.Position.First; got != 12 {
		t.Fatalf("first visible row = %d after a refresh, want the previous 12", got)
	}

	a.list.setThreads(cursorThreads(20))
	if got := a.list.list.Position.First; got != 0 {
		t.Fatalf("first visible row = %d after a page change, want 0", got)
	}
}

func TestPageChangeDropsTheCursor(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(3))
	a.cursorThread = 2
	a.rememberCursor(1, "cursor-2")

	a.goToPage(2)
	if a.cursorThread != -1 {
		t.Fatalf("cursor index = %d after a page change, want -1", a.cursorThread)
	}
}
