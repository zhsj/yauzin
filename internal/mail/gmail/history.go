package gmail

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/zhsj/yauzin/internal/mail"
)

// historyTypes are the change kinds a list row shows. Everything a row renders
// (unread count, star, snippet, participants, labels) rides on a message
// arriving or leaving a conversation or a label moving on one of its messages.
var historyTypes = []string{"messageAdded", "messageDeleted", "labelAdded", "labelRemoved"}

// historyPageLimit bounds how many history pages one check reads: 2 units per
// page, so walking a huge range is worse than dropping the cache and reading
// the list again.
const historyPageLimit = 5

var (
	// errHistoryExpired reports a startHistoryId the API no longer knows. The
	// whole cache has to go.
	errHistoryExpired = errors.New("gmail: history expired")
	// errHistoryTooLong reports a change range wider than historyPageLimit.
	// Dropping the cache is cheaper than walking it.
	errHistoryTooLong = errors.New("gmail: history range too large")
)

// listChangedThreads asks the mailbox which conversations moved since baseline
// and returns their IDs. It costs 2 quota units per page, which is why the
// change check only runs it after the 1-unit profile call reported a change.
//
// It reports the history id the answer describes, so the caller can make that
// id the new baseline: every change up to that point is in the result.
func (s *Service) listChangedThreads(ctx context.Context, baseline string) ([]string, string, error) {
	seen := make(map[string]struct{}, 32)
	pageToken := ""
	latest := ""
	for page := 0; page < historyPageLimit; page++ {
		q := url.Values{}
		q.Set("startHistoryId", baseline)
		q.Set("maxResults", "500")
		for _, t := range historyTypes {
			q.Add("historyTypes", t)
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var resp ListHistoryResponse
		if err := s.c.do(ctx, "users.history.list", "GET", "/users/me/history", q, &resp); err != nil {
			if errors.Is(err, mail.ErrNotFound) || historyStartRejected(err) {
				return nil, "", errHistoryExpired
			}
			return nil, "", err
		}
		for _, h := range resp.History {
			historyThreadIDs(h, seen)
		}
		if id := strings.TrimSpace(resp.HistoryID); id != "" {
			latest = id
		}
		if resp.NextPageToken == "" {
			ids := make([]string, 0, len(seen))
			for id := range seen {
				ids = append(ids, id)
			}
			return ids, latest, nil
		}
		pageToken = resp.NextPageToken
	}
	return nil, "", errHistoryTooLong
}

// historyThreadIDs adds every conversation one history record names.
func historyThreadIDs(h *History, seen map[string]struct{}) {
	if h == nil {
		return
	}
	add := func(m *Message) {
		if m == nil {
			return
		}
		if id := strings.TrimSpace(m.ThreadID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, m := range h.Messages {
		add(m)
	}
	for _, e := range h.MessagesAdded {
		if e != nil {
			add(e.Message)
		}
	}
	for _, e := range h.MessagesDeleted {
		if e != nil {
			add(e.Message)
		}
	}
	for _, e := range h.LabelsAdded {
		if e != nil {
			add(e.Message)
		}
	}
	for _, e := range h.LabelsRemoved {
		if e != nil {
			add(e.Message)
		}
	}
}

// historyStartRejected reports the 400 the API answers with when the
// startHistoryId is malformed or no longer known.
func historyStartRejected(err error) bool {
	var failure *apiFailure
	if !errors.As(err, &failure) || failure.status != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(failure.detail), "history")
}
