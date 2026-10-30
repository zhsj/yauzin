package gmail

import (
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// summarize folds the messages of a conversation into one list row. It walks
// the messages once: the row needs every message's flags and labels, and the
// headline fields come from the newest one, so a second pass would only read
// the same headers again.
func summarize(accountID string, th *Thread) mail.ThreadSummary {
	sum := mail.ThreadSummary{ID: th.ID, AccountID: accountID}
	labels := make([]string, 0, 8)
	seenLabels := make(map[string]struct{}, 8)
	// The senders are collected oldest first and read backwards below, which
	// keeps the row's participant order newest first without a second pass over
	// the messages and their headers.
	senders := make([][]mail.Address, len(th.Messages))

	var newest *Message
	var newestHeaders map[string]string
	var newestDate time.Time

	for i, m := range th.Messages {
		h := m.headers()
		sum.MessageIDs = append(sum.MessageIDs, m.ID)
		if m.hasFlag("UNREAD") {
			sum.UnreadCount++
		}
		if m.hasFlag("STARRED") {
			sum.Starred = true
		}
		if m.hasAttachment() {
			sum.HasAttachments = true
		}
		for _, l := range m.LabelIDs {
			if _, ok := seenLabels[l]; ok {
				continue
			}
			seenLabels[l] = struct{}{}
			labels = append(labels, l)
		}
		senders[i] = parseAddressList(h["from"])

		// The newest message supplies the row's headline fields. Gmail returns
		// thread messages oldest first, but compare dates so a malformed order
		// does not pick the wrong one.
		if d := m.timestamp(h); newest == nil || d.After(newestDate) {
			newest, newestHeaders, newestDate = m, h, d
		}
	}

	if newest != nil {
		sum.Subject = newestHeaders["subject"]
		sum.Snippet = newest.Snippet
		sum.LastDate = newestDate
		sum.MessageCount = len(th.Messages)
	}
	for i := len(senders) - 1; i >= 0; i-- {
		if len(senders[i]) > 0 {
			sum.Participants = mail.AppendUniqueAddresses(sum.Participants, senders[i][0])
		}
	}
	sum.Labels = labels
	return sum
}
