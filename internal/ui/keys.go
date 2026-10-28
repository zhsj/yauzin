package ui

import (
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"

	"github.com/zhsj/yauzin/internal/i18n"
)

// Shortcut bits. They are collected once per frame so no shortcut can leave an
// unread event in the input queue for the next frame.
const (
	keySlash uint32 = 1 << iota
	keyCompose
	keyNext
	keyPrev
	keyOpen
	keyToggle
	keyArchive
	keyDelete
	keyStar
	keyRead
	keyReply
	keyForward
	keyEscape
	keyCloseWindow
	keyQuit
	keyScrollUp
	keyScrollDown
	keyPageUp
	keyPageDown
	keyTop
	keyBottom
)

// shortcutGroup orders the sections of the shortcuts page. The constants are
// declared in display order, which shortcutSections relies on.
type shortcutGroup int

const (
	groupGlobal shortcutGroup = iota
	groupList
	groupScroll
	groupReader
)

// title is the section heading this group is shown under. The translations
// are called with literal keys so the catalog check can see every message the
// shortcuts page can show.
func (g shortcutGroup) title() string {
	switch g {
	case groupList:
		return i18n.T("shortcut.groupList")
	case groupScroll:
		return i18n.T("shortcut.groupScroll")
	case groupReader:
		return i18n.T("shortcut.groupReader")
	default:
		return i18n.T("shortcut.groupGlobal")
	}
}

// shortcut is one action of the keyboard. It is the single source for both the
// dispatch and the shortcuts page: names are what Gio reports, caps are what
// the page shows. The two are separate because a label is not always the key
// name (Enter and Return share one cap) and because arrows and named keys read
// better as short caps than as their Unicode names.
type shortcut struct {
	// action is the i18n key of the label shown on the shortcuts page.
	action string
	group  shortcutGroup
	bit    uint32
	// global marks the window and process shortcuts: they are read before the
	// editor gates, while a search field or an overlay owns the keyboard, so
	// they always have a modifier. Rows without it wait for an unmodified
	// keyboard, because reading a bare key would swallow a character the
	// focused editor is waiting for.
	global bool
	// mods is the modifier combination the filter requires. Zero means the
	// bare key, which Gio only matches when no modifier is held.
	mods key.Modifiers
	// names are the Gio key names that trigger the action. Letters are upper
	// case because Gio reports them that way (the shift modifier decides the
	// name, not the letters typed).
	names []key.Name
	// caps are the key labels shown on the shortcuts page, right of the
	// action. They may be fewer than names (Enter/Return share a cap).
	caps []string
}

// shortcutTable lists every implemented shortcut in display order. Actions the
// list and the reader share (archive, delete, star, read) live in the global
// group, so the page never repeats a row. The window and process shortcuts
// (Ctrl+W, Ctrl+Q) carry global, which the dispatch reads before the editors.
var shortcutTable = []shortcut{
	{"shortcut.search", groupGlobal, keySlash, false, 0, []key.Name{"/"}, []string{"/"}},
	{"shortcut.compose", groupGlobal, keyCompose, false, 0, []key.Name{"C"}, []string{"C"}},
	{"shortcut.close", groupGlobal, keyEscape, false, 0, []key.Name{key.NameEscape}, []string{"Esc"}},
	{"shortcut.archive", groupGlobal, keyArchive, false, 0, []key.Name{"E"}, []string{"E"}},
	{"shortcut.delete", groupGlobal, keyDelete, false, 0, []key.Name{key.NameDeleteForward}, []string{"Del"}},
	{"shortcut.star", groupGlobal, keyStar, false, 0, []key.Name{"S"}, []string{"S"}},
	{"shortcut.read", groupGlobal, keyRead, false, 0, []key.Name{"U"}, []string{"U"}},
	{"shortcut.closeWindow", groupGlobal, keyCloseWindow, true, key.ModShortcut, []key.Name{"W"}, []string{"Ctrl", "W"}},
	{"shortcut.quit", groupGlobal, keyQuit, true, key.ModShortcut, []key.Name{"Q"}, []string{"Ctrl", "Q"}},
	{"shortcut.next", groupList, keyNext, false, 0, []key.Name{"J"}, []string{"J"}},
	{"shortcut.prev", groupList, keyPrev, false, 0, []key.Name{"K"}, []string{"K"}},
	{"shortcut.open", groupList, keyOpen, false, 0, []key.Name{key.NameEnter, key.NameReturn}, []string{"Enter"}},
	{"shortcut.select", groupList, keyToggle, false, 0, []key.Name{"X"}, []string{"X"}},
	{"shortcut.up", groupScroll, keyScrollUp, false, 0, []key.Name{key.NameUpArrow}, []string{"↑"}},
	{"shortcut.down", groupScroll, keyScrollDown, false, 0, []key.Name{key.NameDownArrow}, []string{"↓"}},
	{"shortcut.pageUp", groupScroll, keyPageUp, false, 0, []key.Name{key.NamePageUp}, []string{"PgUp"}},
	{"shortcut.pageDown", groupScroll, keyPageDown, false, 0, []key.Name{key.NamePageDown}, []string{"PgDn"}},
	{"shortcut.top", groupScroll, keyTop, false, 0, []key.Name{key.NameHome}, []string{"Home"}},
	{"shortcut.bottom", groupScroll, keyBottom, false, 0, []key.Name{key.NameEnd}, []string{"End"}},
	{"shortcut.reply", groupReader, keyReply, false, 0, []key.Name{"R"}, []string{"R"}},
	{"shortcut.forward", groupReader, keyForward, false, 0, []key.Name{"F"}, []string{"F"}},
}

// title translates the action shown on the shortcuts page. The literal keys
// are spelled out so the catalog check can see every message the page can
// show; a new row without an entry here falls back to its identifier and the
// consistency test catches it.
func (sc shortcut) title() string {
	switch sc.action {
	case "shortcut.search":
		return i18n.T("shortcut.search")
	case "shortcut.compose":
		return i18n.T("shortcut.compose")
	case "shortcut.close":
		return i18n.T("shortcut.close")
	case "shortcut.archive":
		return i18n.T("shortcut.archive")
	case "shortcut.delete":
		return i18n.T("shortcut.delete")
	case "shortcut.star":
		return i18n.T("shortcut.star")
	case "shortcut.read":
		return i18n.T("shortcut.read")
	case "shortcut.closeWindow":
		return i18n.T("shortcut.closeWindow")
	case "shortcut.quit":
		return i18n.T("shortcut.quit")
	case "shortcut.next":
		return i18n.T("shortcut.next")
	case "shortcut.prev":
		return i18n.T("shortcut.prev")
	case "shortcut.open":
		return i18n.T("shortcut.open")
	case "shortcut.select":
		return i18n.T("shortcut.select")
	case "shortcut.up":
		return i18n.T("shortcut.up")
	case "shortcut.down":
		return i18n.T("shortcut.down")
	case "shortcut.pageUp":
		return i18n.T("shortcut.pageUp")
	case "shortcut.pageDown":
		return i18n.T("shortcut.pageDown")
	case "shortcut.top":
		return i18n.T("shortcut.top")
	case "shortcut.bottom":
		return i18n.T("shortcut.bottom")
	case "shortcut.reply":
		return i18n.T("shortcut.reply")
	case "shortcut.forward":
		return i18n.T("shortcut.forward")
	default:
		return sc.action
	}
}

// handleKeys routes the keyboard before anything is laid out. The rules, in
// order: the window and process shortcuts (Ctrl+W, Ctrl+Q) run first, even
// while an editor owns the keyboard; the search field owns every other key
// while it has focus, because its editor needs the characters; an overlay owns
// the rest as well, so only Esc is read above its editors; the remaining keys
// act on the keyboard cursor in the list, or on the open conversation.
//
// Reading a shortcut is destructive: Router.Event deletes the matched event
// from the frame, so a filter for a bare key would swallow the very characters
// (and the Enter) the focused editor is waiting for. The global layer is safe
// to read early because its rows require a modifier the editors leave unbound;
// everything else is only read when no editor can own the keyboard.
func (a *App) handleKeys(gtx layout.Context) {
	a.trackSearchFocus(gtx)
	if a.blurRequested {
		a.blurRequested = false
		gtx.Execute(key.FocusCmd{Tag: a})
	}

	// Quit wins when both arrive in the same frame: it ends the process, which
	// closes the window on the way out.
	if bits := readShortcuts(gtx, true); bits != 0 {
		if bits&keyQuit != 0 {
			a.requestQuit()
			return
		}
		if bits&keyCloseWindow != 0 {
			// Same path as the title bar close button: the window is torn down
			// after the frame, never in the middle of the layout.
			a.windowActions |= system.ActionClose
			return
		}
	}

	if a.overlay == nil && a.searchFocused {
		// Esc is the only key the search field shares with the shortcuts: it
		// closes the status bar, then the conversation. It never clears the
		// query. Everything else belongs to the editor.
		if pressed(gtx, key.NameEscape) {
			a.escape(gtx)
		}
		return
	}

	if a.overlay != nil {
		// An overlay owns the keyboard for its editors, so the shortcut table
		// is not read at all: doing so would drain the very keys they need
		// (Del is also the list's delete). Esc is the one key read above them,
		// because it is what closes the panel.
		if pressed(gtx, key.NameEscape) {
			a.escape(gtx)
		}
		return
	}

	bits := readShortcuts(gtx, false)
	// Esc runs before the remaining keys: it closes the status bar or the open
	// conversation instead of acting on the list.
	if bits&keyEscape != 0 {
		a.escape(gtx)
	}
	if bits&keySlash != 0 {
		a.focusSearch(gtx)
		return
	}
	if bits&keyCompose != 0 {
		a.openCompose(composeNew, nil)
		return
	}
	if a.selectedThread == "" {
		a.listKeys(bits)
		return
	}
	a.readerKeys(gtx, bits)
}

// trackSearchFocus mirrors the focus state of the search editor, which is the
// only text field outside an overlay.
//
// The state is sampled from the input source instead of draining focus events:
// a key.FocusFilter also matches the target's EditEvent, SnippetEvent,
// SelectionEvent and CompositionEvent, and reading an event removes it from the
// frame. Draining them here left the editor without the text events it needs,
// so the field took the focus but never showed a character.
func (a *App) trackSearchFocus(gtx layout.Context) {
	a.searchFocused = gtx.Focused(&a.top.search)
}

// focusSearch hands the keyboard to the search field.
func (a *App) focusSearch(gtx layout.Context) {
	a.searchFocused = true
	gtx.Execute(key.FocusCmd{Tag: &a.top.search})
}

// pressed reports whether name was pressed in this frame and drains its
// events.
func pressed(gtx layout.Context, name key.Name) bool {
	hit := false
	for {
		ev, ok := gtx.Event(key.Filter{Name: name})
		if !ok {
			return hit
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			hit = true
		}
	}
}

// pressedWith reports whether name was pressed with exactly the given
// modifiers.
func pressedWith(gtx layout.Context, name key.Name, mods key.Modifiers) bool {
	hit := false
	for {
		ev, ok := gtx.Event(key.Filter{Name: name, Required: mods})
		if !ok {
			return hit
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			hit = true
		}
	}
}

// readShortcuts collects the pressed shortcuts of this frame for one half of
// the table: the global layer (window and process shortcuts, read while an
// editor may own the keyboard) or the rest, which waits for an unmodified
// keyboard.
func readShortcuts(gtx layout.Context, global bool) uint32 {
	var bits uint32
	for _, s := range shortcutTable {
		if s.global != global {
			continue
		}
		for _, name := range s.names {
			if pressedWith(gtx, name, s.mods) {
				bits |= s.bit
			}
		}
	}
	return bits
}

func (a *App) escape(gtx layout.Context) {
	if a.status.visible() {
		a.status.clear()
		return
	}
	if a.overlay != nil {
		a.hideOverlay()
		return
	}
	if a.selectedThread != "" {
		a.closeThread()
	}
}

// listKeys acts on the row under the keyboard cursor.
func (a *App) listKeys(bits uint32) {
	switch {
	case bits&(keyNext|keyScrollDown) != 0:
		a.moveCursor(1)
	case bits&(keyPrev|keyScrollUp) != 0:
		a.moveCursor(-1)
	}
	if bits&keyPageDown != 0 {
		a.moveCursor(a.pageCursorStep())
	}
	if bits&keyPageUp != 0 {
		a.moveCursor(-a.pageCursorStep())
	}
	if bits&keyTop != 0 {
		a.setCursor(0)
	}
	if bits&keyBottom != 0 {
		a.setCursor(len(a.list.threads) - 1)
	}
	if bits&keyOpen != 0 {
		if sum, ok := a.list.thread(a.cursorID()); ok {
			a.openThread(sum)
		}
		return
	}
	id := a.cursorID()
	if id == "" {
		return
	}
	if bits&keyToggle != 0 {
		a.list.toggleSelection(id, !a.list.selected[id])
		a.invalidate()
	}
	if bits&keyArchive != 0 {
		a.archiveThreadByID(id)
	}
	if bits&keyDelete != 0 {
		a.deleteThreadByID(id)
	}
	if bits&keyStar != 0 {
		a.toggleThreadStar(id)
	}
	if bits&keyRead != 0 {
		a.toggleThreadReadByID(id)
	}
}

// readerKeys acts on the open conversation. J/K move to the neighbouring
// conversation of the page; the arrow and paging keys scroll the body, which is
// the only way to read long mail without the pointer.
func (a *App) readerKeys(gtx layout.Context, bits uint32) {
	if bits&keyNext != 0 {
		a.openAdjacentThread(1)
		return
	}
	if bits&keyPrev != 0 {
		a.openAdjacentThread(-1)
		return
	}
	if bits&keyReply != 0 {
		if m := a.reader.newestForReply; m != nil {
			a.openCompose(composeReply, m)
		}
		return
	}
	if bits&keyForward != 0 {
		if m := a.reader.newestForReply; m != nil {
			a.openCompose(composeForward, m)
		}
		return
	}
	if bits&keyArchive != 0 {
		a.archiveThread()
	}
	if bits&keyDelete != 0 {
		a.deleteThread()
	}
	if bits&keyStar != 0 {
		a.toggleThreadStar(a.thread.ID)
	}
	if bits&keyRead != 0 {
		a.toggleThreadRead()
	}
	switch {
	case bits&keyScrollUp != 0:
		a.reader.scrollBy(-gtx.Dp(readerScrollStep))
	case bits&keyScrollDown != 0:
		a.reader.scrollBy(gtx.Dp(readerScrollStep))
	case bits&keyPageUp != 0:
		a.reader.scrollPage(gtx, -1)
	case bits&keyPageDown != 0:
		a.reader.scrollPage(gtx, 1)
	}
	if bits&keyTop != 0 {
		a.reader.scrollToEdge(false)
	}
	if bits&keyBottom != 0 {
		a.reader.scrollToEdge(true)
	}
}

// openAdjacentThread opens the conversation delta rows away from the one being
// read and moves the keyboard cursor with it, so the list highlights the row the
// reader went to. Conversations opened outside the list (a mailto draft) have no
// neighbours and leave the reader alone, as does the edge of the page.
func (a *App) openAdjacentThread(delta int) {
	for i := range a.list.threads {
		if a.list.threads[i].ID != a.thread.ID {
			continue
		}
		if next := i + delta; next >= 0 && next < len(a.list.threads) {
			a.setCursor(next)
			a.openThread(a.list.threads[next])
		}
		return
	}
}

// pageCursorStep is how many rows one PageUp/PageDown moves the keyboard
// cursor: as many as the list showed on the previous frame, so the row just
// below the edge becomes the one under the cursor.
func (a *App) pageCursorStep() int {
	if n := a.list.list.Position.Count; n > 1 {
		return n
	}
	return 1
}

// moveCursor moves the keyboard cursor by delta rows and asks the list to
// reveal it.
func (a *App) moveCursor(delta int) {
	if len(a.list.threads) == 0 {
		return
	}
	if a.cursorThread < 0 {
		// The first move lands on the first row, from either direction.
		a.setCursor(0)
		return
	}
	a.setCursor(a.cursorThread + delta)
}

// setCursor moves the keyboard cursor to row i, clamped to the page, and asks
// the list to reveal it.
func (a *App) setCursor(i int) {
	n := len(a.list.threads)
	if n == 0 {
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	if i == a.cursorThread {
		return
	}
	a.cursorThread = i
	a.cursorMoved = true
	a.invalidate()
}

// clampCursor keeps the cursor inside the rows of the page that just loaded.
func (a *App) clampCursor() {
	n := len(a.list.threads)
	switch {
	case n == 0:
		a.cursorThread = -1
	case a.cursorThread >= n:
		a.cursorThread = n - 1
	}
}

// cursorID is the conversation the cursor points at, empty when there is none.
func (a *App) cursorID() string {
	if a.cursorThread < 0 || a.cursorThread >= len(a.list.threads) {
		return ""
	}
	return a.list.threads[a.cursorThread].ID
}
