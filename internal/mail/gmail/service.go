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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	{id: "inbox", labelID: "INBOX", kind: mail.FolderInbox},
	{id: "starred", labelID: "STARRED", kind: mail.FolderStarred},
	{id: "sent", labelID: "SENT", kind: mail.FolderSent},
	{id: "trash", labelID: "TRASH", kind: mail.FolderTrash},
}

// Service is one Gmail account.
type Service struct {
	acc mail.Account
	c   *client

	// cache memoizes the read-only responses of this account. It has no
	// expiry: entries are valid until Invalidate drops them.
	cache *serviceCache

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

// Invalidate drops every cached response, so the next Folders, FolderDetails
// or ListThreads call fetches fresh data. The cache never expires on its own:
// this is the hook external logic (such as a future refresh or sync) calls
// when the mailbox has changed.
func (s *Service) Invalidate() { s.cache.clear() }

// Folders lists the four system folders plus the user labels, without the
// unread counters and label colours: those are read by FolderDetails, which the
// caller runs after the list is on screen.
func (s *Service) Folders(ctx context.Context) ([]mail.Folder, error) {
	if cached, ok := s.cache.folders.get(cacheKeyFolders); ok {
		return slices.Clone(cached), nil
	}
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
	s.cache.folders.set(cacheKeyFolders, folders)
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
		var err error
		details, err = s.c.labelDetails(ctx, ids)
		if err != nil {
			return nil, err
		}
		s.cache.details.set(key, details)
	}

	out := make([]mail.Folder, len(folders))
	for i, f := range folders {
		out[i] = f
		d := details[ids[i]]
		if d == nil {
			continue
		}
		out[i].Unread = d.MessagesUnread
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

	key := threadsCacheKey(labelID, req.Query, req.PageToken, limit)
	if cached, ok := s.cache.threads.get(key); ok {
		return cloneThreadPage(cached), nil
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

	var list ListThreadsResponse
	if err := s.c.do(ctx, "threads.list", "GET", "/users/me/threads", q, &list); err != nil {
		return mail.ThreadPage{}, err
	}

	summaries := func(live []*Thread) []mail.ThreadSummary {
		out := make([]mail.ThreadSummary, 0, len(live))
		for _, th := range live {
			out = append(out, summarize(s.acc.ID, th))
		}
		return out
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
	page := mail.ThreadPage{
		Threads:       summaries(threads),
		Total:         total,
		NextPageToken: list.NextPageToken,
	}
	s.cache.threads.set(key, page)
	return cloneThreadPage(page), nil
}

// fetchThreadMetadata loads the messages of every listed conversation in one
// batch request, keeping the list order. A conversation deleted between the two
// calls is dropped instead of failing the whole page.
func (s *Service) fetchThreadMetadata(ctx context.Context, refs []*Thread) ([]*Thread, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]*Thread, len(refs))
	found := make([]bool, len(refs))
	targets := make([]batchTarget, len(refs))
	q := metadataQuery()
	for i, ref := range refs {
		targets[i] = batchTarget{path: "/users/me/threads/" + url.PathEscape(ref.ID), query: q}
	}

	// live returns the kept conversations, in list order, dropping the ones
	// that were deleted between the list and the batch.
	live := func() []*Thread {
		rows := make([]*Thread, 0, len(refs))
		for i := range out {
			if found[i] {
				rows = append(rows, out[i])
			}
		}
		return rows
	}

	err := s.c.batchFetch(ctx, "threads.get", targets, func(i int, resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusOK:
		case http.StatusNotFound:
			// The conversation was deleted between the list and the batch:
			// drop the row and keep the rest of the page.
			return nil
		default:
			detail, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			return fmt.Errorf("gmail threads.get %s: %s: %s",
				refs[i].ID, http.StatusText(resp.StatusCode), strings.TrimSpace(string(detail)))
		}
		var th Thread
		if err := json.NewDecoder(resp.Body).Decode(&th); err != nil {
			return fmt.Errorf("gmail threads.get %s: decode response: %w", refs[i].ID, err)
		}
		out[i] = &th
		found[i] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return live(), nil
}

// GetThread returns every message of one conversation, oldest first.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, mail.ErrInvalidArgument
	}
	q := url.Values{}
	q.Set("format", "full")
	var th Thread
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

// RawMessage returns the RFC 822 source of one message.
//
// format=raw is the only way to see the headers Gmail parsed away, so this is a
// read of its own instead of a field on GetThread: the reader only asks for it
// when the user opens the original.
func (s *Service) RawMessage(ctx context.Context, messageID string) ([]byte, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, mail.ErrInvalidArgument
	}
	q := url.Values{}
	q.Set("format", "raw")
	var m Message
	if err := s.c.do(ctx, "messages.get", "GET", "/users/me/messages/"+url.PathEscape(messageID), q, &m); err != nil {
		return nil, err
	}
	if strings.TrimSpace(m.Raw) == "" {
		// A message the account can no longer see answers with the same shape
		// and no source, so an empty raw is reported as missing.
		return nil, mail.ErrNotFound
	}
	raw, err := decodeBase64URL(m.Raw)
	if err != nil {
		return nil, fmt.Errorf("gmail messages.get: undecodable raw message: %w", err)
	}
	return raw, nil
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
