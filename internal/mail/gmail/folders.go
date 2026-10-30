package gmail

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// folderSpec is one of the fixed folders the sidebar shows. Gmail identifies
// them with uppercase system label IDs; the app uses semantic IDs so the
// default folder and the mock backend stay the same.
type folderSpec struct {
	id      string
	labelID string
	kind    mail.FolderKind
}

var systemFolders = []folderSpec{
	{id: "inbox", labelID: labelInbox, kind: mail.FolderInbox},
	{id: "starred", labelID: labelStarred, kind: mail.FolderStarred},
	{id: "sent", labelID: labelSent, kind: mail.FolderSent},
	{id: "spam", labelID: labelSpam, kind: mail.FolderSpam},
	{id: "trash", labelID: labelTrash, kind: mail.FolderTrash},
}

// Folders lists the four system folders plus the user labels, without the
// unread counters and label colours: those are read by FolderDetails, which the
// caller runs after the list is on screen.
func (s *Service) Folders(ctx context.Context) ([]mail.Folder, error) {
	if cached, ok := s.cache.folders.get(cacheKeyFolders); ok {
		return slices.Clone(cached), nil
	}
	if err := s.waitCheck(ctx); err != nil {
		return nil, err
	}
	// A read that waited may have been beaten to the entry by another one.
	if cached, ok := s.cache.folders.get(cacheKeyFolders); ok {
		return slices.Clone(cached), nil
	}
	gen := s.cache.folders.epoch()
	var resp ListLabelsResponse
	q := url.Values{}
	q.Set("fields", "labels(id,name,type)")
	if err := s.c.do(ctx, "labels.list", "/users/me/labels", q, &resp); err != nil {
		return nil, err
	}

	byID := make(map[string]*Label, len(resp.Labels))
	for _, l := range resp.Labels {
		byID[l.ID] = l
	}
	specs := make(map[string]folderSpec, len(systemFolders))
	for _, spec := range systemFolders {
		specs[spec.labelID] = spec
	}

	// The sidebar order is fixed: system folders first, then the user labels in
	// the order the API returned them.
	selected := make([]*Label, 0, len(resp.Labels))
	for _, spec := range systemFolders {
		if l, ok := byID[spec.labelID]; ok {
			selected = append(selected, l)
		}
	}
	for _, l := range resp.Labels {
		if l.Type == "user" {
			selected = append(selected, l)
		}
	}

	entries := s.folders(selected, specs)
	s.cache.folders.setAt(gen, cacheKeyFolders, entries)
	return slices.Clone(entries), nil
}

// FolderDetails fills the unread counters and label colours of the folders
// Folders returned. The counters are read with one batch of labels.get calls
// per batchLimit labels.
func (s *Service) FolderDetails(ctx context.Context, folders []mail.Folder) ([]mail.Folder, error) {
	if len(folders) == 0 {
		return folders, nil
	}
	// Map each folder back to the Gmail label id whose detail it needs: system
	// folders carry semantic IDs, user labels carry their Gmail ID already.
	ids := make([]string, len(folders))
	for i, f := range folders {
		id, err := labelIDFor(f.ID)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}

	key := detailsCacheKey(ids)
	details, ok := s.cache.details.get(key)
	if !ok {
		if err := s.waitCheck(ctx); err != nil {
			return nil, err
		}
		details, ok = s.cache.details.get(key)
	}
	if !ok {
		gen := s.cache.details.epoch()
		var err error
		details, err = s.c.labelDetails(ctx, ids)
		if err != nil {
			return nil, err
		}
		s.cache.details.setAt(gen, key, details)
	}

	out := make([]mail.Folder, len(folders))
	for i, f := range folders {
		out[i] = f
		d := details[ids[i]]
		if d == nil {
			continue
		}
		out[i].Unread = d.MessagesUnread
		out[i].Total = d.ThreadsTotal
		if d.Color != nil {
			out[i].Color = d.Color.BackgroundColor
		}
	}
	return out, nil
}

// folders assembles the sidebar entries from the labels labels.list returned.
func (s *Service) folders(selected []*Label, specs map[string]folderSpec) []mail.Folder {
	out := make([]mail.Folder, 0, len(selected))
	for _, l := range selected {
		if spec, ok := specs[l.ID]; ok {
			out = append(out, mail.Folder{ID: spec.id, Kind: spec.kind, Name: l.Name})
			continue
		}
		out = append(out, mail.Folder{ID: l.ID, Name: l.Name, Kind: mail.FolderLabel})
	}
	return out
}

// labelIDFor maps a semantic folder ID back to the Gmail label it stands for.
// Anything that is not a system folder is passed through: user labels are
// opaque Gmail IDs that only the API can judge. An empty ID is a caller bug,
// because browsing always names a folder.
func labelIDFor(folderID string) (string, error) {
	switch folderID {
	case "inbox":
		return labelInbox, nil
	case "starred":
		return labelStarred, nil
	case "sent":
		return labelSent, nil
	case "spam":
		return labelSpam, nil
	case "trash":
		return labelTrash, nil
	}
	if strings.TrimSpace(folderID) == "" {
		return "", mail.ErrInvalidArgument
	}
	return folderID, nil
}

// hidesByDefault reports whether the label is one the API leaves out of a
// threads.list unless includeSpamTrash is true. Those two folders can only be
// browsed by asking for them explicitly; a search does not, which is how it
// stays limited to ordinary mail.
func hidesByDefault(labelID string) bool {
	return labelID == labelSpam || labelID == labelTrash
}
