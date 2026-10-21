package ui

import (
	"gioui.org/io/key"
	"gioui.org/layout"
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
)

// shortcutGroup orders the sections of the shortcuts page. The constants are
// declared in display order, which shortcutSections relies on.
type shortcutGroup int

const (
	groupGlobal shortcutGroup = iota
	groupList
	groupReader
)

// sectionID is the i18n key of the section heading this group is shown under.
func (g shortcutGroup) sectionID() string {
	switch g {
	case groupList:
		return "shortcut.groupList"
	case groupReader:
		return "shortcut.groupReader"
	default:
		return "shortcut.groupGlobal"
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
// group, so the page never repeats a row.
var shortcutTable = []shortcut{
	{"shortcut.search", groupGlobal, keySlash, []key.Name{"/"}, []string{"/"}},
	{"shortcut.compose", groupGlobal, keyCompose, []key.Name{"C"}, []string{"C"}},
	{"shortcut.close", groupGlobal, keyEscape, []key.Name{key.NameEscape}, []string{"Esc"}},
	{"shortcut.archive", groupGlobal, keyArchive, []key.Name{"E"}, []string{"E"}},
	{"shortcut.delete", groupGlobal, keyDelete, []key.Name{key.NameDeleteForward}, []string{"Del"}},
	{"shortcut.star", groupGlobal, keyStar, []key.Name{"S"}, []string{"S"}},
	{"shortcut.read", groupGlobal, keyRead, []key.Name{"U"}, []string{"U"}},
	{"shortcut.next", groupList, keyNext, []key.Name{"J", key.NameDownArrow}, []string{"J", "↓"}},
	{"shortcut.prev", groupList, keyPrev, []key.Name{"K", key.NameUpArrow}, []string{"K", "↑"}},
	{"shortcut.open", groupList, keyOpen, []key.Name{key.NameEnter, key.NameReturn}, []string{"Enter"}},
	{"shortcut.select", groupList, keyToggle, []key.Name{"X"}, []string{"X"}},
	{"shortcut.reply", groupReader, keyReply, []key.Name{"R"}, []string{"R"}},
	{"shortcut.forward", groupReader, keyForward, []key.Name{"F"}, []string{"F"}},
}

// handleKeys routes the keyboard before anything is laid out. The rules, in
// order: Esc keeps its "status bar, then overlay, then conversation, then
// search" priority; the search field owns every other key while it has focus,
// because its editor needs the characters; an overlay owns the rest, also for
// its editors; the remaining keys act on the keyboard cursor in the list, or on
// the open conversation.
//
// Shortcuts are only read when no editor can own the keyboard. Reading is
// destructive: Router.Event deletes the matched event from the frame, so a
// shortcut filter for a bare key would swallow the very characters (and the
// Enter) the focused editor is waiting for.
func (a *App) handleKeys(gtx layout.Context) {
	a.trackSearchFocus(gtx)
	if a.blurRequested {
		a.blurRequested = false
		gtx.Execute(key.FocusCmd{Tag: a})
	}

	if a.overlay == nil && a.searchFocused {
		// Esc is the only key the search field shares with the shortcuts: it
		// closes the status bar, then the conversation, then the search.
		// Everything else belongs to the editor.
		if pressed(gtx, key.NameEscape) {
			a.escape(gtx)
		}
		return
	}

	bits := readShortcuts(gtx)
	// Esc runs before the overlay gate, because it is the key that closes it.
	if bits&keyEscape != 0 {
		a.escape(gtx)
	}
	if a.overlay != nil {
		return
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
	a.readerKeys(bits)
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

// readShortcuts collects the pressed shortcuts of this frame.
func readShortcuts(gtx layout.Context) uint32 {
	var bits uint32
	for _, s := range shortcutTable {
		for _, name := range s.names {
			if pressed(gtx, name) {
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
		return
	}
	if a.query != "" || a.searchFocused {
		a.top.clearSearch()
		a.setQuery("")
		a.blurRequested = true
	}
}

// listKeys acts on the row under the keyboard cursor.
func (a *App) listKeys(bits uint32) {
	switch {
	case bits&keyNext != 0:
		a.moveCursor(1)
	case bits&keyPrev != 0:
		a.moveCursor(-1)
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

// readerKeys acts on the open conversation.
func (a *App) readerKeys(bits uint32) {
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
}

// moveCursor moves the keyboard cursor by delta rows and asks the list to
// reveal it.
func (a *App) moveCursor(delta int) {
	n := len(a.list.threads)
	if n == 0 {
		return
	}
	next := a.cursorThread + delta
	if a.cursorThread < 0 {
		next = 0
	}
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	if next == a.cursorThread {
		return
	}
	a.cursorThread = next
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
