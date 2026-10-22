// Package gmail implements the read-only half of mail.Service on top of the
// Gmail REST API. It never imports the UI or Gio, so it can be tested as a
// plain Go package.
//
// The account is authorized with a user-supplied OAuth client (see SignIn) and
// the readonly scope, so every mutating method reports mail.ErrUnsupported
// instead of pretending to change the mailbox.
package gmail

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

const (
	defaultPageSize = 50
	maxPageSize     = 500
	// metadataWorkers bounds the threads.get calls issued for one page. Gmail
	// has no batch endpoint for thread metadata, so a 50-row page is 50 calls;
	// six at a time keeps the first screen fast without running into the rate
	// limit.
	metadataWorkers = 6
	// metadataFields trims the thread response to what the list row renders.
	// payload is requested whole: the metadata format carries no bodies, but
	// the part tree is what tells the row whether a file is attached.
	metadataFields = "id,messages(id,labelIds,snippet,internalDate,payload)"
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
	{id: "inbox", labelID: "INBOX", kind: mail.FolderInbox},
	{id: "starred", labelID: "STARRED", kind: mail.FolderStarred},
	{id: "sent", labelID: "SENT", kind: mail.FolderSent},
	{id: "trash", labelID: "TRASH", kind: mail.FolderTrash},
}

// Service is one Gmail account.
type Service struct {
	acc mail.Account
	c   *client

	closeOnce sync.Once
}

var _ mail.Service = (*Service)(nil)

// New builds the backend of one authorized account.
func New(acc mail.Account, store Store, opts ...Option) (*Service, error) {
	c, err := newClient(acc.ID, store, opts)
	if err != nil {
		return nil, err
	}
	return &Service{acc: acc, c: c}, nil
}

// Account returns the account this service was built for.
func (s *Service) Account() mail.Account { return s.acc }

// Folders lists the four system folders plus the user labels.
func (s *Service) Folders(ctx context.Context) ([]mail.Folder, error) {
	var resp labelList
	q := url.Values{}
	q.Set("fields", "labels(id,name,type,messagesUnread,color/backgroundColor)")
	if err := s.c.do(ctx, "labels.list", "GET", "/users/me/labels", q, &resp); err != nil {
		return nil, err
	}

	byID := make(map[string]label, len(resp.Labels))
	for _, l := range resp.Labels {
		byID[l.ID] = l
	}
	out := make([]mail.Folder, 0, len(systemFolders)+len(resp.Labels))
	for _, spec := range systemFolders {
		l, ok := byID[spec.labelID]
		if !ok {
			continue
		}
		out = append(out, mail.Folder{
			ID:     spec.id,
			Kind:   spec.kind,
			Name:   l.Name,
			Unread: l.MessagesUnread,
		})
	}
	for _, l := range resp.Labels {
		if l.Type != "user" {
			continue
		}
		f := mail.Folder{
			ID:     l.ID,
			Name:   l.Name,
			Kind:   mail.FolderLabel,
			Unread: l.MessagesUnread,
		}
		if l.Color != nil {
			f.Color = l.Color.BackgroundColor
		}
		out = append(out, f)
	}
	return out, nil
}

// ListThreads returns one page of conversations, newest first.
func (s *Service) ListThreads(ctx context.Context, req mail.ListRequest) (mail.ThreadPage, error) {
	labelID, err := labelIDFor(req.FolderID)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(limit))
	q.Set("fields", "nextPageToken,resultSizeEstimate,threads/id")
	if labelID != "" {
		q.Set("labelIds", labelID)
	}
	if req.Query != "" {
		q.Set("q", req.Query)
	}
	if req.PageToken != "" {
		q.Set("pageToken", req.PageToken)
	}

	var list threadList
	if err := s.c.do(ctx, "threads.list", "GET", "/users/me/threads", q, &list); err != nil {
		return mail.ThreadPage{}, err
	}

	threads, err := s.fetchThreadMetadata(ctx, list.Threads)
	if err != nil {
		return mail.ThreadPage{}, err
	}

	total := int(list.ResultSizeEstimate)
	if list.ResultSizeEstimate <= 0 && len(list.Threads) > 0 {
		// Gmail omits the estimate for some queries; the contract uses -1 to
		// say the backend cannot tell.
		total = -1
	}
	summaries := make([]mail.ThreadSummary, 0, len(threads))
	for _, th := range threads {
		summaries = append(summaries, summarize(s.acc.ID, th))
	}
	return mail.ThreadPage{
		Threads:       summaries,
		Total:         total,
		NextPageToken: list.NextPageToken,
	}, nil
}

// fetchThreadMetadata loads the messages of every listed conversation with
// bounded concurrency, keeping the list order. A conversation deleted between
// the two calls is dropped instead of failing the whole page.
func (s *Service) fetchThreadMetadata(ctx context.Context, refs []threadRef) ([]thread, error) {
	out := make([]thread, len(refs))
	found := make([]bool, len(refs))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, metadataWorkers)
	for i, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, id string) {
			defer wg.Done()
			defer func() { <-sem }()

			q := url.Values{}
			q.Set("format", "metadata")
			q.Set("fields", metadataFields)
			for _, h := range []string{"From", "To", "Cc", "Subject", "Date"} {
				q.Add("metadataHeaders", h)
			}
			var th thread
			err := s.c.do(ctx, "threads.get", "GET", "/users/me/threads/"+url.PathEscape(id), q, &th)
			if err != nil {
				if isNotFound(err) {
					return
				}
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			out[i] = th
			found[i] = true
		}(i, ref.ID)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	live := out[:0]
	for i := range out {
		if found[i] {
			live = append(live, out[i])
		}
	}
	return live, nil
}

// GetThread returns every message of one conversation, oldest first.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, mail.ErrInvalidArgument
	}
	q := url.Values{}
	q.Set("format", "full")
	var th thread
	err := s.c.do(ctx, "threads.get", "GET", "/users/me/threads/"+url.PathEscape(threadID), q, &th)
	if err != nil {
		return nil, err
	}
	out := make([]*mail.Message, 0, len(th.Messages))
	for _, m := range th.Messages {
		out = append(out, s.message(m))
	}
	return out, nil
}

// SetFlag is not implemented: the account is authorized for reading only.
func (s *Service) SetFlag(context.Context, []string, mail.Flag, bool) error {
	return mail.ErrUnsupported
}

// Archive is not implemented: the account is authorized for reading only.
func (s *Service) Archive(context.Context, []string) error { return mail.ErrUnsupported }

// Delete is not implemented: the account is authorized for reading only.
func (s *Service) Delete(context.Context, []string) error { return mail.ErrUnsupported }

// Send is not implemented: the account is authorized for reading only.
func (s *Service) Send(context.Context, mail.Draft) error { return mail.ErrUnsupported }

// SaveAttachment is not implemented: the account is authorized for reading only.
func (s *Service) SaveAttachment(context.Context, string, string, string) (string, error) {
	return "", mail.ErrUnsupported
}

// Close releases the idle connections this account owns.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.c.close() })
	return nil
}

// labelIDFor maps a semantic folder ID back to the Gmail label it stands for.
// An empty ID is the inbox, and anything else is passed through: user labels
// are opaque Gmail IDs that only the API can judge.
func labelIDFor(folderID string) (string, error) {
	switch folderID {
	case "", "inbox":
		return "INBOX", nil
	case "starred":
		return "STARRED", nil
	case "sent":
		return "SENT", nil
	case "trash":
		return "TRASH", nil
	}
	if strings.TrimSpace(folderID) == "" {
		return "", mail.ErrInvalidArgument
	}
	return folderID, nil
}

// summarize folds the messages of a conversation into one list row.
func summarize(accountID string, th thread) mail.ThreadSummary {
	sum := mail.ThreadSummary{ID: th.ID, AccountID: accountID}
	labels := make([]string, 0, 8)
	seenLabels := make(map[string]struct{}, 8)

	for _, m := range th.Messages {
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
	}

	// The newest message supplies the row's headline fields. Gmail returns
	// thread messages oldest first, but compare dates so a malformed order does
	// not pick the wrong one.
	var newest *message
	var newestDate time.Time
	for i := range th.Messages {
		m := &th.Messages[i]
		d := m.date()
		if newest == nil || d.After(newestDate) {
			newest, newestDate = m, d
		}
	}
	if newest != nil {
		sum.Subject = newest.headers()["subject"]
		sum.Snippet = newest.Snippet
		sum.LastDate = newestDate
		sum.MessageCount = len(th.Messages)
	}
	for i := len(th.Messages) - 1; i >= 0; i-- {
		m := &th.Messages[i]
		from := parseAddressList(m.headers()["from"])
		if len(from) > 0 {
			sum.Participants = mail.AppendUniqueAddresses(sum.Participants, from[0])
		}
	}
	sum.Labels = labels
	return sum
}
