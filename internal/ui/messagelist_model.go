package ui

import (
	"gioui.org/layout"

	"github.com/zhsj/yauzin/internal/mail"
)

// setThreads replaces the page contents and clears the bulk selection, which
// never survives a page change, as well as the scroll position.
func (l *messageListView) setThreads(threads []mail.ThreadSummary) {
	l.replace(threads, false)
}

// refreshThreads replaces the page contents after a mutation on the same page:
// the scroll position survives, so archiving a row does not send the list back
// to the top.
func (l *messageListView) refreshThreads(threads []mail.ThreadSummary) {
	l.replace(threads, true)
}

func (l *messageListView) replace(threads []mail.ThreadSummary, keepScroll bool) {
	l.threads = threads
	l.byID = make(map[string]mail.ThreadSummary, len(threads))
	for _, th := range threads {
		l.byID[th.ID] = th
	}
	l.selected = map[string]bool{}
	if cap(l.rows) < len(threads) {
		l.rows = make([]*messageRow, len(threads))
	}
	l.rows = l.rows[:len(threads)]
	for i := range l.rows {
		if l.rows[i] == nil {
			l.rows[i] = newMessageRow(l)
		}
	}
	if !keepScroll {
		l.list.Position = layout.Position{}
	}
}

// reveal scrolls the list so the keyboard cursor is visible. A layout.List is
// scrolled programmatically by setting Position before Layout runs.
func (l *messageListView) reveal(cursor int) {
	if cursor < 0 {
		return
	}
	pos := l.list.Position
	if pos.Count == 0 {
		// Nothing measured yet: the first frame places the rows itself.
		return
	}
	if cursor < pos.First || cursor >= pos.First+pos.Count {
		l.list.Position.First = cursor
		l.list.Position.Offset = 0
	}
}

func (l *messageListView) thread(id string) (mail.ThreadSummary, bool) {
	sum, ok := l.byID[id]
	return sum, ok
}

// mutateThread rewrites the cached summary of id together with its row, so the
// optimistic update and the copy the list draws cannot drift apart. The index
// skips threads the list no longer holds.
func (l *messageListView) mutateThread(id string, set func(*mail.ThreadSummary)) {
	sum, ok := l.byID[id]
	if !ok {
		return
	}
	set(&sum)
	l.byID[id] = sum
	for i := range l.threads {
		if l.threads[i].ID == id {
			set(&l.threads[i])
			break
		}
	}
}

func (l *messageListView) toggleSelection(id string, checked bool) {
	if id == "" {
		return
	}
	if checked {
		l.selected[id] = true
	} else {
		delete(l.selected, id)
	}
}

func (l *messageListView) selectAll() {
	for _, th := range l.threads {
		l.selected[th.ID] = true
	}
}

func (l *messageListView) clearSelection() {
	l.selected = map[string]bool{}
}

func (l *messageListView) selectionCount() int { return len(l.selected) }

// selectedIDs returns the selected conversations in list order.
func (l *messageListView) selectedIDs() []string {
	out := make([]string, 0, len(l.selected))
	for _, th := range l.threads {
		if l.selected[th.ID] {
			out = append(out, th.ID)
		}
	}
	return out
}

// messageIDs flattens the message ids of the given conversations.
func (l *messageListView) messageIDs(threadIDs []string) []string {
	var out []string
	for _, id := range threadIDs {
		if sum, ok := l.byID[id]; ok {
			out = append(out, sum.MessageIDs...)
		}
	}
	return out
}

// pageMessageIDs flattens every conversation on the current page.
func (l *messageListView) pageMessageIDs() []string {
	ids := make([]string, 0, len(l.threads))
	for _, th := range l.threads {
		ids = append(ids, th.MessageIDs...)
	}
	return ids
}
