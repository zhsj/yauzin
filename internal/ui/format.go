package ui

import (
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/htmltext"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// folderDisplayName localizes the system folders. User created labels keep the
// name supplied by the backend because it is data, not UI copy.
func folderDisplayName(f mail.Folder) string {
	switch f.Kind {
	case mail.FolderInbox:
		return i18n.T("folder.inbox")
	case mail.FolderStarred:
		return i18n.T("folder.starred")
	case mail.FolderSent:
		return i18n.T("folder.sent")
	case mail.FolderTrash:
		return i18n.T("folder.trash")
	default:
		return f.Name
	}
}

// formatListTime renders the compact timestamp of a conversation row.
func formatListTime(t time.Time) string {
	return formatListTimeAt(t, time.Now())
}

// formatListTimeAt is the testable form of formatListTime.
func formatListTimeAt(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(now.Location())
	switch {
	case local.Year() == now.Year() && local.YearDay() == now.YearDay():
		return local.Format(i18n.T("date.today"))
	case local.Year() == now.Year():
		return local.Format(i18n.T("date.thisYear"))
	default:
		return local.Format(i18n.T("date.older"))
	}
}

// formatFullTime renders the expanded timestamp shown on an opened message. The
// list already renders in local time, so the expanded form has to use the same
// zone or one message appears to move by the sender's offset.
func formatFullTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.Local).Format(i18n.T("date.full"))
}

func quotedBody(m *mail.Message) string {
	text := m.TextBody
	if strings.TrimSpace(text) == "" {
		text = htmltext.ToText(m.HTMLBody)
	}
	if strings.TrimSpace(text) == "" {
		return i18n.T("compose.noBody")
	}
	return text
}

func replySubject(subject string) string {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(s), "re:") {
		return s
	}
	return "Re: " + s
}

func forwardSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(s), "fwd:") {
		return s
	}
	return "Fwd: " + s
}
