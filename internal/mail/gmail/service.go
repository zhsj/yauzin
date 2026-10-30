// Package gmail implements mail.Service on top of the Gmail REST API: reading
// folders, conversations and message sources, and the mailbox changes the list
// and the reader make (flags, archive, trash). It never imports the UI or Gio,
// so it can be tested as a plain Go package.
//
// The account is authorized with a user-supplied OAuth client (see SignIn) and
// the gmail.modify scope, which covers the label changes behind those actions
// but not permanent deletion.
package gmail

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

const (
	defaultPageSize = 50
	maxPageSize     = 500
	// metadataFields trims the thread response to what the list row renders.
	// payload is requested whole: the metadata format carries no bodies, but
	// the part tree is what tells the row whether a file is attached.
	metadataFields = "id,messages(id,labelIds,snippet,internalDate,payload)"
)

// metadataQuery is the query of one threads.get call that fills a list row.
func metadataQuery() url.Values {
	q := url.Values{}
	q.Set("format", "metadata")
	q.Set("fields", metadataFields)
	for _, h := range []string{"From", "To", "Cc", "Subject", "Date"} {
		q.Add("metadataHeaders", h)
	}
	return q
}

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
// recorded, dropping every cached response when it moved.
//
// users.getProfile is the cheapest way to read the current history id: one
// request, no message data, one quota unit. The first call records the id as
// the baseline and drops whatever was cached before it, so a caller that checks
// before it loads always holds data at least as new as the baseline.
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
	defer s.checkMu.Unlock()
	baseline := s.historyID
	if baseline == id {
		return false, nil
	}
	s.historyID = id
	// Every entry predates the state this call observed, so it cannot be kept.
	s.cache.clear()
	if baseline == "" {
		// Nothing to compare against yet: this call only sets the baseline.
		slog.Debug("gmail history baseline", "account", logAccount(s.acc.ID), "history_id", id)
		return false, nil
	}
	slog.Debug("gmail mailbox changed", "account", logAccount(s.acc.ID), "from", baseline, "to", id)
	return true, nil
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
	if err := s.c.do(ctx, "labels.list", "GET", "/users/me/labels", q, &resp); err != nil {
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

	folders := s.folders(selected, specs)
	s.cache.folders.setAt(gen, cacheKeyFolders, folders)
	return slices.Clone(folders), nil
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

// ListThreads returns one page of conversations of one folder, newest first.
//
// A page the cache still holds is assembled from the row cache and costs no
// request at all; a page that has to be listed costs the threads.list call plus
// a threads.get for every row the cache is missing.
func (s *Service) ListThreads(ctx context.Context, req mail.ListRequest) (mail.ThreadPage, error) {
	labelID, err := labelIDFor(req.FolderID)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	limit := pageLimit(req.Limit)
	key := threadsCacheKey(labelID, req.PageToken, limit)
	if page, ok := s.cache.pages.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	if err := s.waitCheck(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	if page, ok := s.cache.pages.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	gen := s.cache.pages.epoch()
	page, err := s.listThreads(ctx, labelID, "", req.PageToken, limit)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	s.cache.pages.setAt(gen, key, page)
	return s.pageFromIDs(ctx, page)
}

// SearchThreads returns one page of conversations matching the query, newest
// first. The query goes to threads.list as written and without a label filter,
// so it spans every folder; Gmail's includeSpamTrash default keeps trash and
// spam out, which is how the web search behaves.
func (s *Service) SearchThreads(ctx context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return mail.ThreadPage{}, mail.ErrInvalidArgument
	}
	limit := pageLimit(req.Limit)
	key := searchCacheKey(query, req.PageToken, limit)
	if page, ok := s.cache.search.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	if err := s.waitCheck(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	// A read that waited may have been beaten to the entry by another one.
	if page, ok := s.cache.search.get(key); ok {
		return s.pageFromIDs(ctx, page)
	}
	gen := s.cache.search.epoch()
	page, err := s.listThreads(ctx, "", query, req.PageToken, limit)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	s.cache.search.setAt(gen, key, page)
	return s.pageFromIDs(ctx, page)
}

// pageLimit clamps a requested page size to the backend's bounds.
func pageLimit(limit int) int {
	if limit <= 0 {
		return defaultPageSize
	}
	if limit > maxPageSize {
		return maxPageSize
	}
	return limit
}

// listThreads performs one threads.list call and returns the conversation IDs
// of the page. An empty labelID does not filter by folder, which is what a
// search needs; an empty query browses the label instead.
//
// includeSpamTrash is raised for the spam and trash folders: the API leaves
// both out of the results by default, so labelIds=SPAM or TRASH would come back
// empty. A search keeps the default false, which is what limits it to mail that
// is neither spam nor in the trash.
func (s *Service) listThreads(ctx context.Context, labelID, query, pageToken string, limit int) (pageEntry, error) {
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(limit))
	q.Set("fields", "nextPageToken,resultSizeEstimate,threads/id")
	if labelID != "" {
		q.Set("labelIds", labelID)
	}
	if hidesByDefault(labelID) {
		q.Set("includeSpamTrash", "true")
	}
	if query != "" {
		q.Set("q", query)
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}

	var list ListThreadsResponse
	if err := s.c.do(ctx, "threads.list", "GET", "/users/me/threads", q, &list); err != nil {
		return pageEntry{}, err
	}

	ids := make([]string, 0, len(list.Threads))
	for _, th := range list.Threads {
		if th == nil || th.ID == "" {
			continue
		}
		ids = append(ids, th.ID)
	}
	total := int(list.ResultSizeEstimate)
	if list.ResultSizeEstimate <= 0 && len(ids) > 0 {
		// Gmail omits the estimate for some queries; the contract uses -1 to
		// say the backend cannot tell.
		total = -1
	}
	return pageEntry{IDs: ids, Total: total, NextPageToken: list.NextPageToken}, nil
}

// GetThread returns every message of one conversation, oldest first.
//
// The answer is cached for the few conversations the reader opened: a
// threads.get with format=full costs 120 quota units, so reopening one the user
// just read must not pay again. A conversation the list already described as a
// single message is read as that message instead, which costs half.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, mail.ErrInvalidArgument
	}
	if cached, ok := s.cache.bodies.get(threadID); ok {
		return cloneMessages(cached), nil
	}
	gen := s.cache.bodies.epoch()
	msgs, err := s.fetchThreadMessages(ctx, threadID)
	if err != nil {
		return nil, err
	}
	s.cache.bodies.setAt(gen, threadID, msgs)
	return cloneMessages(msgs), nil
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
		"account", s.acc.Email,
		"message", messageID,
		"bytes", written)
	return nil
}

// Close releases the idle connections this account owns.
func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.c.close() })
	return nil
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

// summarize folds the messages of a conversation into one list row.
func summarize(accountID string, th *Thread) mail.ThreadSummary {
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
	var newest *Message
	var newestDate time.Time
	for _, m := range th.Messages {
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
		from := parseAddressList(th.Messages[i].headers()["from"])
		if len(from) > 0 {
			sum.Participants = mail.AppendUniqueAddresses(sum.Participants, from[0])
		}
	}
	sum.Labels = labels
	return sum
}
