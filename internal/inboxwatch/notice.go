package inboxwatch

import (
	"strings"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/notify"
)

// subjectMaxRunes bounds one subject line. Notification servers truncate on
// their own, but a bound here keeps the body readable and the layout stable.
const subjectMaxRunes = 80

// buildNotice assembles the notification for the new unread conversations.
func buildNotice(subjects []string, total int) *notify.Notice {
	body := strings.Join(subjects, "\n") + "\n\n" + i18n.N("notify.count", total)
	return &notify.Notice{
		AppName: i18n.T("app.brand"),
		Summary: i18n.T("notify.title"),
		Body:    body,
		Action:  i18n.T("notify.open"),
	}
}

// subjectLine renders one subject as a single bounded line. A message without
// a subject falls back to the same placeholder the thread list uses.
func subjectLine(subject string) string {
	line := strings.Join(strings.Fields(subject), " ")
	if line == "" {
		return i18n.T("list.noSubject")
	}
	runes := []rune(line)
	if len(runes) > subjectMaxRunes {
		line = string(runes[:subjectMaxRunes-1]) + "…"
	}
	return line
}
