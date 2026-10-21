package ui

import (
	"gioui.org/layout"

	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

// sidebarView is the fixed left column: the compose pill plus the system
// folders and Gmail style labels. Accounts are deliberately not listed here.
type sidebarView struct {
	app      *App
	compose  *pillButton
	addLabel *iconButton
	folders  []*navItem
	labels   []*navItem
	// list scrolls the whole column. The number of labels comes from the
	// backend, so the sidebar cannot assume it fits the window.
	list layout.List
}

func newSidebar(a *App) *sidebarView {
	s := &sidebarView{app: a}
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
	s.folders = s.folders[:0]
	s.labels = s.labels[:0]
	for _, f := range a.folders {
		active := f.ID == a.activeFolder && a.query == ""
		if f.Kind == mail.FolderLabel {
			item := newNavItemBar(f.ID, f.Name, hexColor(f.Color), a.selectFolder)
			item.setCount(f.Unread)
			item.setActive(active)
			s.labels = append(s.labels, item)
			continue
		}
		item := newNavItem(f.ID, folderDisplayName(f), folderIcon(f.Kind), a.selectFolder)
		item.setCount(f.Unread)
		item.setActive(active)
		s.folders = append(s.folders, item)
	}
	a.refreshTrayIcon()
}

func (s *sidebarView) Layout(gtx layout.Context) layout.Dimensions {
	th := s.app.th
	return layout.Inset{Top: 8, Right: 8, Bottom: 8, Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// One scroll region for the whole column rather than nested lists: the
		// compose pill scrolls out of the way with the folders it sits above.
		folderStart := 2 // 0 is the compose pill, 1 is the gap under it
		labelsStart := folderStart + len(s.folders)
		rows := labelsStart
		if len(s.labels) > 0 {
			// A gap and the section header sit between the folders and the labels.
			rows = labelsStart + 2 + len(s.labels)
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
				item := s.labels[i-labelsStart-2]
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
