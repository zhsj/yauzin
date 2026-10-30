package gmail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/zhsj/yauzin/internal/mail"
)

// Service is one Gmail account.
type Service struct {
	acc mail.Account
	c   *client

	// cache memoizes the read responses of this account. It has no expiry: an
	// entry stays valid until something says the mailbox moved, which is
	// CheckChanges or a write of this account (see mutate.go).
	cache *serviceCache

	// checkMu guards the change-check state below. It is never held across a
	// request: a check compares and clears under it, but the profile call runs
	// without it.
	checkMu sync.Mutex
	// historyID is the mailbox state the last check recorded, empty until the
	// first one.
	historyID string
	// checking counts the checks in flight. While it is non-zero, a cached read
	// that misses waits on checkDone instead of sending its request.
	checking int
	// checkDone is closed when checking drops back to zero.
	checkDone chan struct{}

	closeOnce sync.Once
}

var _ mail.Service = (*Service)(nil)

// New builds the backend of one authorized account.
func New(acc mail.Account, store Store, opts ...Option) (*Service, error) {
	c, err := newClient(acc.ID, store, opts)
	if err != nil {
		return nil, err
	}
	return &Service{acc: acc, c: c, cache: newServiceCache()}, nil
}

// Account returns the account this service was built for.
func (s *Service) Account() mail.Account { return s.acc }

// CheckChanges compares the mailbox history id with the one the previous call
// recorded and drops the cached rows of the conversations that moved.
//
// users.getProfile is the cheapest way to read the current history id: one
// request, no message data, one quota unit. Only when it moved does the check
// ask the history which conversations changed (2 units per page), so an idle
// minute costs one unit and a new message costs the history call plus the rows
// it touched.
//
// The first call records the id as the baseline and drops whatever was cached
// before it, so a caller that checks before it loads always holds data at least
// as new as the baseline.
func (s *Service) CheckChanges(ctx context.Context) (bool, error) {
	s.beginCheck()
	defer s.endCheck()

	q := url.Values{}
	q.Set("fields", "historyId")
	var profile Profile
	if err := s.c.do(ctx, "users.getProfile", "GET", "/users/me/profile", q, &profile); err != nil {
		return false, err
	}
	id := strings.TrimSpace(profile.HistoryID)
	if id == "" {
		return false, fmt.Errorf("gmail users.getProfile: response carries no history id")
	}

	s.checkMu.Lock()
	baseline := s.historyID
	s.checkMu.Unlock()
	if baseline == id {
		return false, nil
	}

	if baseline == "" {
		// Nothing to compare against yet: this call only sets the baseline, and
		// everything cached before it described an older state.
		s.cache.clear()
		s.setBaseline(id)
		slog.Debug("gmail history baseline", "account", logAccount(s.acc.ID), "history_id", id)
		return false, nil
	}

	ids, latest, err := s.listChangedThreads(ctx, baseline)
	if err != nil {
		if !errors.Is(err, errHistoryExpired) && !errors.Is(err, errHistoryTooLong) {
			// The baseline stays where it was, so the next check covers the
			// same range again instead of silently dropping a change.
			return false, err
		}
		// The history cannot describe the range: drop everything and start
		// over from the id this check observed.
		s.cache.clear()
		s.setBaseline(id)
		slog.Debug("gmail history reset",
			"account", logAccount(s.acc.ID), "from", baseline, "to", id)
		return true, nil
	}

	// Only the conversations the history named can have changed: everything
	// else in the cache still describes the mailbox.
	s.cache.invalidateThreads(ids)
	if latest == "" {
		latest = id
	}
	s.setBaseline(latest)
	slog.Debug("gmail mailbox changed",
		"account", logAccount(s.acc.ID), "from", baseline, "to", latest,
		"threads", len(ids))
	return true, nil
}

// setBaseline records the history id the cache now describes.
func (s *Service) setBaseline(id string) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.historyID = id
}

// beginCheck registers a check in flight.
func (s *Service) beginCheck() {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.checking++
	if s.checkDone == nil {
		s.checkDone = make(chan struct{})
	}
}

// endCheck retires the check and wakes the reads waiting for it. CheckChanges
// clears the cache before this runs, so a woken read captures the new epoch.
func (s *Service) endCheck() {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.checking--
	if s.checking == 0 && s.checkDone != nil {
		close(s.checkDone)
		s.checkDone = nil
	}
}

// waitCheck holds a cache miss back while a check is in flight: a response
// fetched now could describe the state the check is about to drop. The wait
// honours ctx, so a cancelled request leaves the queue immediately.
func (s *Service) waitCheck(ctx context.Context) error {
	s.checkMu.Lock()
	done := s.checkDone
	s.checkMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetThread returns every message of one conversation, oldest first.
//
// The answer is cached for the few conversations the reader opened: a
// threads.get with format=full costs 120 quota units, so reopening one the user
// just read must not pay again. A conversation the list already described as a
// single message is read as that message instead, which costs half.
//
// The returned slice belongs to the caller, but the messages in it are shared
// with the cache and must be treated as read-only.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, mail.ErrInvalidArgument
	}
	if cached, ok := s.cache.bodies.get(threadID); ok {
		return slices.Clone(cached), nil
	}
	if err := s.waitCheck(ctx); err != nil {
		return nil, err
	}
	// A read that waited may have been beaten to the entry by another one.
	if cached, ok := s.cache.bodies.get(threadID); ok {
		return slices.Clone(cached), nil
	}
	gen := s.cache.bodies.epoch()
	msgs, err := s.fetchThreadMessages(ctx, threadID)
	if err != nil {
		return nil, err
	}
	s.cache.bodies.setAt(gen, threadID, msgs)
	return slices.Clone(msgs), nil
}

// fetchThreadMessages reads one whole conversation. A conversation the row
// cache knows to hold a single message is fetched as that message: messages.get
// costs 60 units, threads.get with format=full costs 120.
func (s *Service) fetchThreadMessages(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if row, ok := s.cache.rows.get(threadID); ok && len(row.MessageIDs) == 1 {
		q := url.Values{}
		q.Set("format", "full")
		var m Message
		if err := s.c.do(ctx, "messages.get", "GET",
			"/users/me/messages/"+url.PathEscape(row.MessageIDs[0]), q, &m); err != nil {
			return nil, err
		}
		return []*mail.Message{s.message(&m)}, nil
	}
	q := url.Values{}
	q.Set("format", "full")
	var th Thread
	if err := s.c.do(ctx, "threads.get", "GET", "/users/me/threads/"+url.PathEscape(threadID), q, &th); err != nil {
		return nil, err
	}
	out := make([]*mail.Message, 0, len(th.Messages))
	for _, m := range th.Messages {
		out = append(out, s.message(m))
	}
	return out, nil
}

// SaveMessage writes the RFC 822 source of one message to w.
//
// format=raw is the only way to see the headers Gmail parsed away, so this is a
// read of its own instead of a field on GetThread. The source arrives as one
// base64url string in the JSON response, which the streamed scanner decodes
// into w as it arrives: neither the encoded text nor the message ever sits in
// memory as a whole.
func (s *Service) SaveMessage(ctx context.Context, messageID string, w io.Writer, progress func(mail.Progress)) error {
	if strings.TrimSpace(messageID) == "" || w == nil {
		return mail.ErrInvalidArgument
	}
	q := url.Values{}
	q.Set("format", "raw")
	var written int64
	err := s.c.doStream(ctx, "messages.get", "/users/me/messages/"+url.PathEscape(messageID), q, progress, func(r io.Reader) error {
		field, err := scanStringField(r, rawField)
		if err != nil {
			// A message the account can no longer see answers with the same
			// shape and no source, so a missing raw is reported as missing.
			return err
		}
		n, err := copyToTarget(w, base64.NewDecoder(base64.URLEncoding, newPaddingReader(field)))
		written = n
		if err != nil {
			return err
		}
		if n == 0 {
			return mail.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.Debug("gmail message saved",
		"account", logAccount(s.acc.ID),
		"message", messageID,
		"bytes", written)
	return nil
}

// Close releases the idle connections this account owns.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.c.close() })
	return nil
}
