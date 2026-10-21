package ui

import (
	"gioui.org/layout"
	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

// messageListView renders one page of conversations, each row a merged thread
// in Gmail's dense single-line style, with a toolbar above it.
type messageListView struct {
	app  *App
	list layout.List
	rows []*messageRow

	threads []mail.ThreadSummary
	byID    map[string]mail.ThreadSummary

	selected map[string]bool
}

func newMessageList(a *App) *messageListView {
	l := &messageListView{
		app:      a,
		byID:     map[string]mail.ThreadSummary{},
		selected: map[string]bool{},
	}
	l.list.Axis = layout.Vertical
	return l
}

func (l *messageListView) Layout(gtx layout.Context) layout.Dimensions {
	a := l.app
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.toolbar.Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(l.threads) == 0 {
				return a.th.emptyState(gtx, iconInbox, i18n.T("list.emptyTitle"), i18n.T("list.emptyHint"))
			}
			if a.cursorMoved {
				a.cursorMoved = false
				l.reveal(a.cursorThread)
			}
			return l.list.Layout(gtx, len(l.threads), func(gtx layout.Context, i int) layout.Dimensions {
				if i < 0 || i >= len(l.threads) || i >= len(l.rows) {
					return layout.Dimensions{}
				}
				sum := l.threads[i]
				selected := l.selected[sum.ID] || sum.ID == a.selectedThread
				return l.rows[i].Layout(gtx, sum, selected, i == a.cursorThread)
			})
		}),
	)
}
