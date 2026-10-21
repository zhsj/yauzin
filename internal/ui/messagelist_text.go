package ui

import (
	"strings"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func senderName(addr mail.Address) string {
	if addr.Name != "" {
		return addr.Name
	}
	if addr.Email != "" {
		return addr.Email
	}
	return i18n.T("list.unknownSender")
}

// threadParticipants lists the distinct senders of a conversation, newest
// first, joined with commas.
func threadParticipants(sum mail.ThreadSummary) string {
	if len(sum.Participants) == 0 {
		return ""
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(sum.Participants))
	for _, addr := range sum.Participants {
		name := senderName(addr)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) > 3 {
		names = names[:3]
	}
	return strings.Join(names, ", ")
}

func subjectOrPlaceholder(subject string) string {
	if strings.TrimSpace(subject) == "" {
		return i18n.T("list.noSubject")
	}
	return subject
}

func snippetText(snippet string) string {
	return strings.TrimSpace(strings.ReplaceAll(snippet, "\n", " "))
}
