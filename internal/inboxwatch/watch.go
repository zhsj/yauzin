// Package inboxwatch keeps the process-level view of the unread conversation
// in the inbox.
//
// One Poll checks whether the mailbox moved and, when it did, reads the newest
// unread conversations of the system label INBOX. It reports whether the tray
// should show unread mail and, when an unread conversation is new, a desktop
// notification describing it. The package never touches the window, the
// sidebar or the thread list.
package inboxwatch

import (
	"context"
	"sync"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/notify"
)

const (
	// query selects unread conversations of the inbox only. Gmail and the
	// generic search contract pass it through as written.
	query = "label:inbox is:unread"
	// limit is how many unread conversations one poll reads: enough for the
	// notification subjects, small enough to stay one batch of metadata calls.
	limit = 3
)

// Mark is the last observed state of one unread conversation.
type Mark struct {
	// Unread is the conversation's unread message count.
	Unread int
	// LastMessage identifies the newest message seen in the conversation.
	LastMessage string
}

// Outcome is what one poll learned. Updated is false when nothing was read
// (no change, or a result belonging to a superseded account), which leaves the
// caller's tray state alone.
type Outcome struct {
	// Active reports whether the inbox has at least one unread conversation.
	Active bool
	// Updated reports whether the poll actually read the inbox.
	Updated bool
	// Notice is the desktop notification to publish, nil when no unread
	// conversation is new.
	Notice *notify.Notice
}

// Watcher tracks the unread conversations of one account. It is safe for
// concurrent use; Poll runs off the UI thread while Reset runs on it.
type Watcher struct {
	mu sync.Mutex
	// bound records that Reset named the account, including the empty account
	// used when the last mailbox was removed. It keeps such a reset from being
	// mistaken for a watcher that was never bound.
	bound bool
	// account is the account the current marks belong to.
	account string
	// known is false until the first successful read of the account.
	known bool
	marks map[string]Mark
}

// New returns a watcher without a baseline.
func New() *Watcher { return &Watcher{} }

// Reset drops the baseline and binds the watcher to accountID. A result that
// arrives afterwards for the previous account is discarded by Poll.
func (w *Watcher) Reset(accountID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bound = true
	w.account = accountID
	w.known = false
	w.marks = nil
}

// Poll reads the unread conversations of the account when the mailbox moved,
// or when the watcher has no baseline yet. A nil service is a no-op.
func (w *Watcher) Poll(ctx context.Context, accountID string, svc mail.Service) (Outcome, error) {
	if svc == nil {
		return Outcome{}, nil
	}
	changed, err := svc.CheckChanges(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if !changed && w.knownFor(accountID) {
		return Outcome{}, nil
	}
	page, err := svc.SearchThreads(ctx, mail.SearchRequest{Query: query, Limit: limit})
	if err != nil {
		return Outcome{}, err
	}
	return w.update(accountID, page), nil
}

// knownFor reports whether the watcher already holds a baseline for accountID.
func (w *Watcher) knownFor(accountID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.known && w.account == accountID
}

// update records page and decides what the tray and the notification should
// show. A page from another account is dropped.
func (w *Watcher) update(accountID string, page mail.ThreadPage) Outcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.bound {
		w.bound = true
		w.account = accountID
	}
	if w.account != accountID {
		return Outcome{}
	}

	out := Outcome{Active: len(page.Threads) > 0 || page.Total > 0, Updated: true}
	if w.known {
		total := page.Total
		if total < len(page.Threads) {
			total = len(page.Threads)
		}
		subjects := make([]string, 0, limit)
		for _, th := range page.Threads {
			old, seen := w.marks[th.ID]
			if seen && !isNew(th, old) {
				continue
			}
			subjects = append(subjects, subjectLine(th.Subject))
			if len(subjects) == limit {
				break
			}
		}
		if len(subjects) > 0 {
			out.Notice = buildNotice(subjects, total)
		}
	}

	w.marks = make(map[string]Mark, len(page.Threads))
	for _, th := range page.Threads {
		w.marks[th.ID] = Mark{Unread: th.UnreadCount, LastMessage: lastMessageID(th)}
	}
	w.known = true
	w.account = accountID
	return out
}

// isNew reports whether the conversation carries unread mail the previous
// snapshot did not describe: its unread count grew, or its newest message
// changed.
func isNew(th mail.ThreadSummary, old Mark) bool {
	return th.UnreadCount > old.Unread || lastMessageID(th) != old.LastMessage
}

// lastMessageID names the newest message of the conversation. The identifiers
// are ordered oldest first.
func lastMessageID(th mail.ThreadSummary) string {
	if len(th.MessageIDs) == 0 {
		return ""
	}
	return th.MessageIDs[len(th.MessageIDs)-1]
}
