package ui

import (
	"gioui.org/layout"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// sidebarView is the fixed left column: the compose pill plus the system
// folders and Gmail style labels. Accounts are deliberately not listed here.
type sidebarView struct {
	app      *App
	compose  *pillButton
	addLabel *iconButton
	folders  []*navItem
	// labels holds the rows currently visible, which is the flattened tree
	// minus the subtrees the user folded.
	labels []*navItem
	// tree is the label hierarchy read from the names. It is rebuilt on every
	// refresh while the rows themselves are cached in its nodes.
	tree *labelTree
	// collapsed folds one label subtree. It lives in memory only: Gmail keeps
	// this across sessions, the sidebar opens fully expanded on every start.
	collapsed map[string]bool
	// list scrolls the whole column. The number of labels comes from the
	// backend, so the sidebar cannot assume it fits the window.
	list layout.List
}

func newSidebar(a *App) *sidebarView {
	s := &sidebarView{app: a, collapsed: map[string]bool{}}
	s.compose = newComposePill(i18n.T("sidebar.compose"), iconComposeBlue, func() {
		a.openCompose(composeNew, nil)
	})
	s.addLabel = newIconButton(iconPlus, 24, func() {
		a.notify(i18n.T("sidebar.labelsUnavailable"), false)
	})
	return s
}

func (s *sidebarView) reload() {
	a := s.app
	// The list draws the same labels the sidebar does, so the index is rebuilt
	// here: this is the one place a fetched folder list reaches the views.
	a.labels = newLabelIndex(a.folders)
	s.folders = s.folders[:0]
	labelFolders := make([]mail.Folder, 0, len(a.folders))
	for _, f := range a.folders {
		if f.Kind == mail.FolderLabel {
			labelFolders = append(labelFolders, f)
			continue
		}
		// The folder view is exactly the active folder: a search leaves it
		// empty, so no row is highlighted while the list shows results.
		active := f.ID == a.activeFolder
		item := newNavItem(f.ID, folderDisplayName(f), folderIcon(f.Kind), a.selectFolder)
		item.setCount(f.Unread)
		item.setActive(active)
		s.folders = append(s.folders, item)
	}
	// The tree keeps the fold state: a refresh (a new page, marking a message
	// read) must not pop the labels the user just folded shut.
	s.tree = buildLabelTree(labelFolders, a.selectFolder)
	s.flattenLabels()
	a.refreshTrayIcon()
}

// flattenLabels rebuilds the visible rows after a fold or a refresh.
func (s *sidebarView) flattenLabels() {
	if s.tree == nil {
		s.labels = s.labels[:0]
		return
	}
	s.labels = s.tree.flatten(s.collapsed, s.app.activeFolder, s.toggleLabel)
}

// toggleLabel folds or unfolds one label subtree.
func (s *sidebarView) toggleLabel(id string) {
	if s.collapsed == nil {
		s.collapsed = map[string]bool{}
	}
	s.collapsed[id] = !s.collapsed[id]
	s.flattenLabels()
}

func (s *sidebarView) Layout(gtx layout.Context) layout.Dimensions {
	th := s.app.th
	return layout.Inset{Top: 8, Right: 8, Bottom: 8, Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// One scroll region for the whole column rather than nested lists: the
		// compose pill scrolls out of the way with the folders it sits above.
		// Folding a label changes the row count in the middle of the layout, so
		// both the slice and the count are snapshotted before the first row.
		labels := s.labels
		folderStart := 2 // 0 is the compose pill, 1 is the gap under it
		labelsStart := folderStart + len(s.folders)
		rows := labelsStart
		if len(labels) > 0 {
			// A gap and the section header sit between the folders and the labels.
			rows = labelsStart + 2 + len(labels)
		}
		s.list.Axis = layout.Vertical
		return s.list.Layout(gtx, rows, func(gtx layout.Context, i int) layout.Dimensions {
			switch {
			case i == 0:
				return layout.Inset{Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return s.compose.Layout(gtx, th)
				})
			case i == 1:
				return vgap(gtx, 14)
			case i < labelsStart:
				item := s.folders[i-folderStart]
				return layout.Inset{Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return item.Layout(gtx, th)
				})
			case i == labelsStart:
				return vgap(gtx, 8)
			case i == labelsStart+1:
				return layout.Inset{Top: 12, Bottom: 4, Left: 14, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return th.sectionLabel(gtx, i18n.T("sidebar.labels"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return s.addLabel.Layout(gtx, th)
						}),
					)
				})
			default:
				item := labels[i-labelsStart-2]
				return layout.Inset{Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return item.Layout(gtx, th)
				})
			}
		})
	})
}

func folderIcon(kind mail.FolderKind) icon {
	switch kind {
	case mail.FolderStarred:
		return iconStarFilled
	case mail.FolderSent:
		return iconSent
	case mail.FolderTrash:
		return iconTrash
	default:
		return iconInbox
	}
}
