package ui

import (
	"strconv"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestShortcutTableIsConsistent guards the invariants the dispatch and the
// shortcuts page rely on: one row per action, one bit per row, readable caps,
// display order by group, and a translation for every label.
func TestShortcutTableIsConsistent(t *testing.T) {
	if got, want := len(shortcutTable), 22; got != want {
		t.Fatalf("shortcut table has %d rows, want %d", got, want)
	}
	bits := map[uint32]string{}
	type binding struct {
		name key.Name
		mods key.Modifiers
	}
	names := map[binding]string{}
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

		// The global layer is read while an editor may own the keyboard, so
		// its rows have to require a modifier the editors leave unbound.
		if s.global && s.mods == 0 {
			t.Errorf("%s is global without a modifier", s.action)
		}

		if len(s.names) == 0 {
			t.Errorf("%s has no key names", s.action)
		}
		for _, name := range s.names {
			b := binding{name, s.mods}
			if other, ok := names[b]; ok {
				t.Errorf("key %q is bound to both %s and %s", name, other, s.action)
			}
			names[b] = s.action
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

		if got := s.title(); got == s.action || got == "" {
			t.Errorf("no translation for %s", s.action)
		}
	}
	if len(groups) != 4 {
		t.Errorf("shortcut table covers %d groups, want 4", len(groups))
	}
}

// TestArrowsScrollAndJKNavigate pins the split of the two pairs: the reader
// scrolls on the arrows and opens the neighbouring conversation on J/K, so the
// two may not share a row.
func TestArrowsScrollAndJKNavigate(t *testing.T) {
	want := map[string][]key.Name{
		"shortcut.next": {"J"},
		"shortcut.prev": {"K"},
		"shortcut.up":   {key.NameUpArrow},
		"shortcut.down": {key.NameDownArrow},
	}
	for _, s := range shortcutTable {
		names, ok := want[s.action]
		if !ok {
			continue
		}
		if len(s.names) != len(names) {
			t.Errorf("%s is bound to %v, want %v", s.action, s.names, names)
			continue
		}
		for i := range names {
			if s.names[i] != names[i] {
				t.Errorf("%s is bound to %v, want %v", s.action, s.names, names)
				break
			}
		}
		delete(want, s.action)
	}
	if len(want) != 0 {
		t.Fatalf("missing shortcut rows: %v", want)
	}
}

// TestGlobalShortcuts checks the window and process layer: Ctrl+W asks the
// window to close, Ctrl+Q ends the process, and the bare letters keep doing
// nothing. The first frame registers the key filters the router needs before
// the event can be queued.
func TestGlobalShortcuts(t *testing.T) {
	tests := []struct {
		name string
		ev   key.Event
		want func(a *App) bool
	}{
		{
			"ctrl+w closes the window",
			key.Event{Name: "W", Modifiers: key.ModShortcut, State: key.Press},
			func(a *App) bool { return a.windowActions&system.ActionClose != 0 },
		},
		{
			"ctrl+q quits",
			key.Event{Name: "Q", Modifiers: key.ModShortcut, State: key.Press},
			func(a *App) bool { return a.quitting.Load() },
		},
		{
			"bare w does nothing",
			key.Event{Name: "W", State: key.Press},
			func(a *App) bool { return a.windowActions == 0 && !a.quitting.Load() },
		},
		{
			"bare q does nothing",
			key.Event{Name: "Q", State: key.Press},
			func(a *App) bool { return a.windowActions == 0 && !a.quitting.Load() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testApp(t)
			router, ops, gtx := newRouterFrame(testWindow)
			runFrame(a, router, ops, gtx)

			router.Queue(tt.ev)
			runFrame(a, router, ops, gtx)
			if !tt.want(a) {
				t.Fatalf("%s: windowActions = %v, quitting = %v",
					tt.name, a.windowActions, a.quitting.Load())
			}
		})
	}
}

// TestOverlayKeepsEditorKeys pins the overlay rule: while a panel is open its
// editors own the keyboard, so handleKeys must leave Del for the focused
// editor instead of draining it through the list's delete shortcut.
func TestOverlayKeepsEditorKeys(t *testing.T) {
	a := testApp(t)
	a.showSettings()
	s := a.settings

	router, ops, gtx := newRouterFrame(testWindow)
	// The first frame registers the editor's filters, which the router needs
	// before it will accept the event.
	runFrame(a, router, ops, gtx)

	// Hand the keyboard to the proxy editor, the field the regression was
	// reported on, so the router routes the event to it.
	gtx.Execute(key.FocusCmd{Tag: s.proxy})
	router.Queue(key.Event{Name: key.NameDeleteForward, State: key.Press})

	a.handleKeys(gtx)
	if _, ok := gtx.Event(key.Filter{Name: key.NameDeleteForward}); !ok {
		t.Fatal("Del was consumed before the overlay editor could read it")
	}
	ops.Reset()
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

func TestMergeThreadsKeepsTheScrollPosition(t *testing.T) {
	a := testApp(t)
	a.list.list.Position = layout.Position{First: 12, Offset: 40, Count: 5}

	a.list.mergeThreads(cursorThreads(20))
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

// TestListPageAndEdgeKeys checks the paging keys on the list: PageUp/PageDown
// move the cursor by the rows the last frame showed, Home/End jump to the
// edges, and the arrows keep moving one row. The first frame is what registers
// the key filters and measures the visible rows.
func TestListPageAndEdgeKeys(t *testing.T) {
	a := testApp(t)
	a.list.setThreads(cursorThreads(40))
	router, ops, gtx := newRouterFrame(testWindow)
	runFrame(a, router, ops, gtx)

	step := a.list.list.Position.Count
	if step < 2 {
		t.Fatalf("the list showed %d rows, want at least 2 to page by", step)
	}
	a.cursorThread = 10

	router.Queue(key.Event{Name: key.NamePageDown, State: key.Press})
	a.handleKeys(gtx)
	if got := a.cursorThread; got != 10+step {
		t.Fatalf("cursor = %d after PgDn, want %d", got, 10+step)
	}

	router.Queue(key.Event{Name: key.NamePageUp, State: key.Press})
	a.handleKeys(gtx)
	if got := a.cursorThread; got != 10 {
		t.Fatalf("cursor = %d after PgUp, want 10", got)
	}

	router.Queue(key.Event{Name: key.NameEnd, State: key.Press})
	a.handleKeys(gtx)
	if got, want := a.cursorThread, len(a.list.threads)-1; got != want {
		t.Fatalf("cursor = %d after End, want %d", got, want)
	}

	router.Queue(key.Event{Name: key.NameHome, State: key.Press})
	a.handleKeys(gtx)
	if got := a.cursorThread; got != 0 {
		t.Fatalf("cursor = %d after Home, want 0", got)
	}

	router.Queue(key.Event{Name: key.NameDownArrow, State: key.Press})
	a.handleKeys(gtx)
	if got := a.cursorThread; got != 1 {
		t.Fatalf("cursor = %d after Down, want 1", got)
	}
}
