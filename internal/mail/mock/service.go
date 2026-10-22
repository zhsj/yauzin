// Package mock provides an in-memory mail backend used while the real Gmail
// and IMAP transports are being built. It behaves like a slow network service
// so loading states in the UI can be exercised.
package mock

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
	// inboxThreads controls how many conversations the demo inbox holds. It is
	// large enough to span several pages at the default page size.
	inboxThreads = 112
)

type stored struct {
	msg      *mail.Message
	sent     bool
	trash    bool
	archived bool
}

// thread groups the messages of one conversation, oldest first.
type thread struct {
	id   string
	msgs []*stored
}

// Service is the mock mail backend for one account.
type Service struct {
	mu      sync.Mutex
	acc     mail.Account
	threads []*thread
	labels  []mail.Folder
	latency time.Duration
}

var _ mail.Service = (*Service)(nil)

// New creates a populated mock service for the given account.
func New(acc mail.Account) *Service {
	s := &Service{acc: acc, latency: 140 * time.Millisecond}
	s.seed()
	return s
}

// Account returns the account this service was built for.
func (s *Service) Account() mail.Account { return s.acc }

func (s *Service) seed() {
	now := time.Now()
	s.labels = []mail.Folder{
		{ID: "label:work", Name: "工作", Kind: mail.FolderLabel, Color: "#0B57D0"},
		// Nested labels read their hierarchy from the name, like Gmail does:
		// the sidebar draws them indented under 工作 with a disclosure arrow.
		{ID: "label:work:projA", Name: "工作/项目A", Kind: mail.FolderLabel, Color: "#C2185B"},
		{ID: "label:work:projB", Name: "工作/项目B", Kind: mail.FolderLabel, Color: "#1A73E8"},
		{ID: "label:bills", Name: "账单", Kind: mail.FolderLabel, Color: "#1E8E3E"},
		{ID: "label:travel", Name: "旅行", Kind: mail.FolderLabel, Color: "#F4B400"},
		{ID: "label:github", Name: "GitHub", Kind: mail.FolderLabel, Color: "#9AA0A6"},
	}
	s.seedInbox(now)
	s.seedSent(now)
	s.seedTrash(now)
	s.sortThreads()
}

// seedInbox builds the paged inbox: one to three messages per conversation, so
// the merged threads and the single-message rows are both exercised, and every
// ninth conversation carries an attachment.
func (s *Service) seedInbox(now time.Time) {
	for i := 0; i < inboxThreads; i++ {
		tpl := templates[i%len(templates)]
		subject := threadSubject(tpl.subject, i/len(templates))
		// One to three messages per conversation, so single-message and
		// merged threads are both exercised.
		count := 1 + i%3
		newest := now.Add(-time.Duration(i)*37*time.Minute - 9*time.Minute)
		th := &thread{id: fmt.Sprintf("%s-t%03d", s.acc.ID, i)}

		for k := 0; k < count; k++ {
			date := newest.Add(-time.Duration(count-1-k) * 3 * time.Hour)
			msgSubject, snippet := subject, tpl.snippet
			if k > 0 {
				msgSubject = "Re: " + subject
				snippet = replySnippets[(i+k)%len(replySnippets)]
			}
			m := &mail.Message{
				MessageSummary: mail.MessageSummary{
					ID:        fmt.Sprintf("%s-t%03d-m%d", s.acc.ID, i, k),
					AccountID: s.acc.ID,
					From:      mail.Address{Name: tpl.from, Email: tpl.email},
					To:        []mail.Address{{Name: s.acc.DisplayName(), Email: s.acc.Email}},
					Subject:   msgSubject,
					Snippet:   snippet,
					Date:      date,
					Unread:    i%3 != 2 && k == count-1,
					Starred:   i%7 == 0 && k == count-1,
					Labels:    append([]string{}, tpl.labels...),
				},
				HTMLBody: htmlBody(tpl.subject, tpl.lead, tpl.bullets, tpl.from),
				TextBody: plainBody(tpl.lead, tpl.bullets, tpl.from),
			}
			if i%9 == 0 && k == count-1 {
				name := tpl.attachment
				if name == "" {
					name = "邮件附件.txt"
				}
				m.HasAttachments = true
				m.Attachments = []mail.Attachment{{
					ID:       fmt.Sprintf("%s-att-%d", m.ID, 0),
					Filename: name,
					MIMEType: "text/plain",
					Size:     int64(2048 + i*13),
				}}
			}
			th.msgs = append(th.msgs, &stored{msg: m})
		}
		s.threads = append(s.threads, th)
	}
}

// seedSent builds a week of replies the demo account has sent.
func (s *Service) seedSent(now time.Time) {
	for i := 0; i < 7; i++ {
		t := templates[(i+5)%len(templates)]
		m := &mail.Message{
			MessageSummary: mail.MessageSummary{
				ID:        fmt.Sprintf("%s-sent-%02d", s.acc.ID, i),
				AccountID: s.acc.ID,
				From:      mail.Address{Name: s.acc.DisplayName(), Email: s.acc.Email},
				To:        []mail.Address{{Name: t.from, Email: t.email}},
				Subject:   "Re: " + t.subject,
				Snippet:   "已收到，我这边周三之前整理好同步给你。",
				Date:      now.Add(-time.Duration(i*126+30) * time.Minute),
				Starred:   i == 2,
			},
			HTMLBody: `<div><p>你好，</p><p>已收到，我这边周三之前整理好同步给你。</p><p>Ada</p></div>`,
			TextBody: "你好，\n\n已收到，我这边周三之前整理好同步给你。\n\nAda",
		}
		s.threads = append(s.threads, &thread{
			id:   fmt.Sprintf("%s-sent-t%02d", s.acc.ID, i),
			msgs: []*stored{{msg: m, sent: true}},
		})
	}
}

// seedTrash builds the handful of discarded conversation the demo account has.
func (s *Service) seedTrash(now time.Time) {
	for i := 0; i < 3; i++ {
		t := templates[(i+9)%len(templates)]
		m := &mail.Message{
			MessageSummary: mail.MessageSummary{
				ID:        fmt.Sprintf("%s-trash-%02d", s.acc.ID, i),
				AccountID: s.acc.ID,
				From:      mail.Address{Name: t.from, Email: t.email},
				To:        []mail.Address{{Name: s.acc.DisplayName(), Email: s.acc.Email}},
				Subject:   t.subject,
				Snippet:   t.snippet,
				Date:      now.Add(-time.Duration(i*1440+90) * time.Minute),
			},
			HTMLBody: htmlBody(t.subject, t.lead, t.bullets, t.from),
			TextBody: plainBody(t.lead, t.bullets, t.from),
		}
		s.threads = append(s.threads, &thread{
			id:   fmt.Sprintf("%s-trash-t%02d", s.acc.ID, i),
			msgs: []*stored{{msg: m, trash: true}},
		})
	}
}

// sortThreads orders conversations by the timestamp of their newest message.
func (s *Service) sortThreads() {
	sort.SliceStable(s.threads, func(i, j int) bool {
		return lastDate(s.threads[i]).After(lastDate(s.threads[j]))
	})
}

func lastDate(th *thread) time.Time {
	if len(th.msgs) == 0 {
		return time.Time{}
	}
	return th.msgs[len(th.msgs)-1].msg.Date
}

// threadSubject keeps the conversation subjects of the demo data distinct so
// that repeated templates read like follow-up rounds instead of duplicates.
func threadSubject(base string, cycle int) string {
	if cycle == 0 {
		return base
	}
	if cycle <= len(followUps) {
		return base + " · " + followUps[cycle-1]
	}
	return fmt.Sprintf("%s · 第 %d 轮", base, cycle)
}

var followUps = []string{"跟进", "更新", "补充说明", "复核结果", "最终确认", "复盘"}

// replySnippets are used for follow up messages inside a conversation.
var replySnippets = []string{
	"收到，我补充两点：排期按上周的结论走，风险项已经同步给相关同学。",
	"我这边数据核对完了，有一处口径需要确认，详见正文。",
	"结论没有问题，我先把改动合进主干，明天再补一次验证。",
	"更新一下：客户反馈已经收到，剩下两个小问题排在明天处理。",
	"好的，那就按这个方案推进，需要我配合的部分随时说。",
}

func (s *Service) wait(ctx context.Context) error {
	timer := time.NewTimer(s.latency)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Folders implements mail.Service. Unread counters and colours are left to
// FolderDetails.
func (s *Service) Folders(ctx context.Context) ([]mail.Folder, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := []mail.Folder{
		{ID: "inbox", Name: "收件箱", Kind: mail.FolderInbox},
		{ID: "starred", Name: "已加星标", Kind: mail.FolderStarred},
		{ID: "sent", Name: "已发送", Kind: mail.FolderSent},
		{ID: "trash", Name: "垃圾箱", Kind: mail.FolderTrash},
	}
	for _, l := range s.labels {
		out = append(out, mail.Folder{ID: l.ID, Name: l.Name, Kind: l.Kind})
	}
	return out, nil
}

// FolderDetails implements mail.Service. Unread counters count conversations,
// which matches the granularity of the message list; colours are the ones the
// demo data was seeded with.
func (s *Service) FolderDetails(ctx context.Context, folders []mail.Folder) ([]mail.Folder, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	inboxUnread, starredUnread := 0, 0
	labelUnread := map[string]int{}
	labelColor := map[string]string{}
	for _, l := range s.labels {
		labelColor[l.ID] = l.Color
	}
	for _, th := range s.threads {
		if slices.ContainsFunc(th.msgs, func(e *stored) bool { return e.msg.Unread && matchFolder(e, "inbox") }) {
			inboxUnread++
		}
		if slices.ContainsFunc(th.msgs, func(e *stored) bool { return e.msg.Unread && matchFolder(e, "starred") }) {
			starredUnread++
		}
		for _, l := range s.labels {
			if slices.ContainsFunc(th.msgs, func(e *stored) bool { return e.msg.Unread && matchFolder(e, l.ID) }) {
				labelUnread[l.ID]++
			}
		}
	}

	out := make([]mail.Folder, len(folders))
	for i, f := range folders {
		out[i] = f
		out[i].Total = len(s.threadPool(f.ID, ""))
		switch f.Kind {
		case mail.FolderInbox:
			out[i].Unread = inboxUnread
		case mail.FolderStarred:
			out[i].Unread = starredUnread
		case mail.FolderLabel:
			out[i].Unread = labelUnread[f.ID]
			out[i].Color = labelColor[f.ID]
		}
	}
	return out, nil
}

// ListThreads implements mail.Service: one page of one folder, newest first.
func (s *Service) ListThreads(ctx context.Context, req mail.ListRequest) (mail.ThreadPage, error) {
	if err := s.wait(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return mail.ThreadPage{}, err
	}

	if err := s.validateFolder(req.FolderID); err != nil {
		return mail.ThreadPage{}, err
	}
	return page(s.threadPool(req.FolderID, ""), req.PageToken, req.Limit)
}

// SearchThreads implements mail.Service: one page of the conversations that
// match query in any folder except trash, like Gmail's all mail search.
func (s *Service) SearchThreads(ctx context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return mail.ThreadPage{}, mail.ErrInvalidArgument
	}
	if err := s.wait(ctx); err != nil {
		return mail.ThreadPage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return mail.ThreadPage{}, err
	}
	return page(s.threadPool("", query), req.PageToken, req.Limit)
}

// page cuts one page out of pool at the cursor. A cursor past the end is not an
// error: the conversation list shrank while the user was reading it, so the
// page is simply empty (which lets the UI step back one page).
func page(pool []mail.ThreadSummary, token string, limit int) (mail.ThreadPage, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	offset, err := decodeToken(token)
	if err != nil {
		return mail.ThreadPage{}, err
	}
	total := len(pool)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	threads := make([]mail.ThreadSummary, end-offset)
	copy(threads, pool[offset:end])
	out := mail.ThreadPage{Threads: threads, Total: total}
	if end < total {
		out.NextPageToken = encodeToken(end)
	}
	return out, nil
}

// encodeToken turns an offset into the opaque cursor handed to the caller. A
// real backend would use the provider's own cursor; keeping it opaque here is
// what lets the UI stay page-number agnostic.
func encodeToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeToken reads a cursor produced by encodeToken. An empty cursor is the
// first page.
func decodeToken(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("%w: malformed page token", mail.ErrInvalidArgument)
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("%w: malformed page token", mail.ErrInvalidArgument)
	}
	return offset, nil
}

// GetThread implements mail.Service. Messages are returned oldest first.
func (s *Service) GetThread(ctx context.Context, threadID string) ([]*mail.Message, error) {
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, th := range s.threads {
		if th.id != threadID {
			continue
		}
		if len(th.msgs) == 0 {
			break
		}
		out := make([]*mail.Message, 0, len(th.msgs))
		for _, e := range th.msgs {
			out = append(out, cloneMessage(e.msg))
		}
		return out, nil
	}
	return nil, mail.ErrNotFound
}

// threadPool aggregates the conversations matching a folder and search query.
// It must be called with the lock held.
func (s *Service) threadPool(folderID, query string) []mail.ThreadSummary {
	q := strings.ToLower(strings.TrimSpace(query))
	// The row always describes the whole conversation: the UI archives, deletes
	// and marks read through ThreadSummary.MessageIDs, so a summary built from
	// only the matching messages would act on part of a conversation. Trash is
	// the one place where the conversation really is its trashed messages.
	summaryScope := ""
	if folderID == "trash" {
		summaryScope = "trash"
	}
	// Searching spans every folder except trash, like Gmail's all mail search,
	// while browsing stays inside the selected folder.
	scope := folderID
	if q != "" {
		scope = ""
	}

	pool := make([]mail.ThreadSummary, 0, len(s.threads))
	for _, th := range s.threads {
		full := visibleMessages(th, summaryScope)
		if len(full) == 0 {
			continue
		}
		// The folder or the query only decides whether the conversation is
		// listed, never what the row says about it.
		if q != "" {
			if !slices.ContainsFunc(full, func(e *stored) bool { return matches(e.msg, q) }) {
				continue
			}
		} else if !slices.ContainsFunc(th.msgs, func(e *stored) bool { return matchFolder(e, scope) }) {
			continue
		}
		pool = append(pool, buildThreadSummary(th.id, full))
	}
	sort.SliceStable(pool, func(i, j int) bool {
		return pool[i].LastDate.After(pool[j].LastDate)
	})
	return pool
}

// validateFolder rejects folder IDs that are not part of this backend, so a
// stale or mistyped ID cannot silently render the inbox. An empty ID is
// rejected too: browsing always names a folder, and search is SearchThreads.
func (s *Service) validateFolder(folderID string) error {
	switch folderID {
	case "inbox", "starred", "sent", "trash":
		return nil
	}
	if strings.TrimSpace(folderID) == "" {
		return mail.ErrInvalidArgument
	}
	if strings.HasPrefix(folderID, "label:") {
		for _, l := range s.labels {
			if l.ID == folderID {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: unknown folder %q", mail.ErrNotFound, folderID)
}

// visibleMessages returns the messages of a conversation that belong to the
// given folder scope. An empty scope means "every message outside trash".
func visibleMessages(th *thread, folderID string) []*stored {
	out := make([]*stored, 0, len(th.msgs))
	for _, e := range th.msgs {
		if matchFolder(e, folderID) {
			out = append(out, e)
		}
	}
	return out
}

func matchFolder(e *stored, folderID string) bool {
	switch folderID {
	case "":
		return !e.trash
	case "starred":
		return e.msg.Starred && !e.trash
	case "sent":
		// Deleting a sent message has to take it out of Sent as well.
		return e.sent && !e.trash
	case "trash":
		return e.trash
	}
	if strings.HasPrefix(folderID, "label:") {
		return !e.trash && !e.sent && slices.Contains(e.msg.Labels, folderID)
	}
	return !e.trash && !e.sent && !e.archived
}

// buildThreadSummary merges the messages of a conversation. msgs must be sorted
// oldest first.
func buildThreadSummary(id string, msgs []*stored) mail.ThreadSummary {
	summary := mail.ThreadSummary{
		ID:         id,
		MessageIDs: make([]string, 0, len(msgs)),
	}
	labels := make(map[string]bool, 4)
	for i, e := range msgs {
		m := e.msg
		if i == 0 {
			summary.AccountID = m.AccountID
		}
		summary.MessageIDs = append(summary.MessageIDs, m.ID)
		if m.Unread {
			summary.UnreadCount++
		}
		if m.Starred {
			summary.Starred = true
		}
		if m.HasAttachments {
			summary.HasAttachments = true
		}
		for _, l := range m.Labels {
			if labels[l] {
				continue
			}
			labels[l] = true
			summary.Labels = append(summary.Labels, l)
		}
	}

	// Newest message supplies the list row text and the ordering date.
	newest := msgs[len(msgs)-1].msg
	summary.Subject = mail.NormalizeSubject(newest.Subject)
	summary.Snippet = newest.Snippet
	summary.LastDate = newest.Date
	summary.MessageCount = len(msgs)

	// Participants are collected newest first so the list row leads with the
	// most recent sender.
	for i := len(msgs) - 1; i >= 0; i-- {
		summary.Participants = mail.AppendUniqueAddresses(summary.Participants, msgs[i].msg.From)
	}
	return summary
}

func matches(m *mail.Message, q string) bool {
	haystack := strings.ToLower(m.Subject + " " + m.Snippet + " " + m.From.Name + " " + m.From.Email + " " + m.TextBody)
	return strings.Contains(haystack, q)
}

// eachStored visits the stored wrapper of every message whose ID is in ids. It
// must be called with the lock held. Walking the conversations avoids both the
// flattened copy and the per-message scan of the requested IDs.
func (s *Service) eachStored(ids []string, fn func(*stored)) {
	if len(ids) == 0 {
		return
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	for _, th := range s.threads {
		for _, e := range th.msgs {
			if _, ok := set[e.msg.ID]; ok {
				fn(e)
			}
		}
	}
}

// cloneMessage returns a copy so callers cannot mutate the store through the
// returned slice fields.
func cloneMessage(m *mail.Message) *mail.Message {
	cp := *m
	cp.Labels = append([]string{}, m.Labels...)
	cp.To = append([]mail.Address{}, m.To...)
	cp.Cc = append([]mail.Address{}, m.Cc...)
	cp.Attachments = append([]mail.Attachment{}, m.Attachments...)
	return &cp
}

// withStore runs a mutation with the store locked, after the injected latency
// and the context check every write shares. SaveAttachment deliberately does
// not use it: it looks the attachment up under the lock and writes the file
// after releasing it, and folding the write in here would put slow IO inside
// the critical section.
func (s *Service) withStore(ctx context.Context, fn func()) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	fn()
	return nil
}

// SetFlag implements mail.Service.
func (s *Service) SetFlag(ctx context.Context, ids []string, flag mail.Flag, on bool) error {
	switch flag {
	case mail.FlagRead, mail.FlagStarred:
	default:
		return fmt.Errorf("%w: flag %d", mail.ErrInvalidArgument, flag)
	}
	return s.withStore(ctx, func() {
		s.eachStored(ids, func(e *stored) {
			switch flag {
			case mail.FlagRead:
				e.msg.Unread = !on
			case mail.FlagStarred:
				e.msg.Starred = on
			}
		})
	})
}

// Archive implements mail.Service.
func (s *Service) Archive(ctx context.Context, ids []string) error {
	return s.withStore(ctx, func() {
		s.eachStored(ids, func(e *stored) { e.archived = true })
	})
}

// Delete implements mail.Service.
func (s *Service) Delete(ctx context.Context, ids []string) error {
	return s.withStore(ctx, func() {
		s.eachStored(ids, func(e *stored) {
			e.trash = true
			e.archived = false
		})
	})
}

// Send implements mail.Service.
func (s *Service) Send(ctx context.Context, draft mail.Draft) error {
	if len(draft.To) == 0 {
		return fmt.Errorf("%w: no recipients", mail.ErrInvalidArgument)
	}
	return s.withStore(ctx, func() { s.storeSent(draft) })
}

// storeSent builds the sent message and prepends its conversation. It runs
// with the lock held.
func (s *Service) storeSent(draft mail.Draft) {
	snippet := strings.TrimSpace(draft.Body)
	if len([]rune(snippet)) > 90 {
		snippet = string([]rune(snippet)[:90]) + "…"
	}
	m := &mail.Message{
		MessageSummary: mail.MessageSummary{
			ID:        fmt.Sprintf("%s-sent-%d", s.acc.ID, time.Now().UnixNano()),
			AccountID: s.acc.ID,
			From:      mail.Address{Name: s.acc.DisplayName(), Email: s.acc.Email},
			// The draft belongs to the caller; the stored message must not share
			// its backing arrays, since the composer may still edit them.
			To:      append([]mail.Address{}, draft.To...),
			Subject: draft.Subject,
			Snippet: snippet,
			Date:    time.Now(),
		},
		Cc:       append([]mail.Address{}, draft.Cc...),
		TextBody: draft.Body,
		HTMLBody: "<div>" + strings.ReplaceAll(html.EscapeString(draft.Body), "\n", "<br/>") + "</div>",
	}
	for _, a := range draft.Attachments {
		m.HasAttachments = true
		m.Attachments = append(m.Attachments, mail.Attachment{
			ID:       fmt.Sprintf("%s-att-%d", m.ID, len(m.Attachments)),
			Filename: a.Name,
			MIMEType: "application/octet-stream",
		})
	}
	now := time.Now()
	s.threads = append([]*thread{{
		id:   fmt.Sprintf("%s-sent-t%d", s.acc.ID, now.UnixNano()),
		msgs: []*stored{{msg: m, sent: true}},
	}}, s.threads...)
}

// SaveAttachment implements mail.Service.
func (s *Service) SaveAttachment(ctx context.Context, messageID, attachmentID, dir string) (string, error) {
	if err := s.wait(ctx); err != nil {
		return "", err
	}

	// Look the attachment up under the lock, then write the file without it: a
	// slow filesystem must not stall every other operation, and a context that
	// expires during the write is still observed before anything is committed.
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return "", err
	}
	var subject, filename string
	var found bool
	s.eachStored([]string{messageID}, func(e *stored) {
		for _, att := range e.msg.Attachments {
			if att.ID == attachmentID {
				subject, filename, found = e.msg.Subject, att.Filename, true
				return
			}
		}
	})
	s.mu.Unlock()
	if !found {
		return "", mail.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	name := filepath.Base(filename)
	if name == "." || name == ".." || name == string(filepath.Separator) || name == "" {
		name = "attachment.txt"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("Yauzin 演示附件\n\n所属邮件：%s\n文件名：%s\n\n接入真实后端后，这里会是原始附件内容。\n", subject, filename)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// RawMessage implements mail.Service. The demo data keeps no source, so the
// RFC 822 document is rendered from the parsed message: the headers a real
// backend would return, plus the plain-text part.
func (s *Service) RawMessage(ctx context.Context, messageID string) ([]byte, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, fmt.Errorf("%w: empty message ID", mail.ErrInvalidArgument)
	}
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var msg *mail.Message
	s.eachStored([]string{messageID}, func(e *stored) { msg = e.msg })
	s.mu.Unlock()
	if msg == nil {
		return nil, mail.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(sourceOf(msg)), nil
}

// sourceOf renders one message as RFC 822 text. Header order and CRLF line
// endings follow the wire format so the reader's source page looks like what a
// real backend returns.
func sourceOf(m *mail.Message) string {
	var b strings.Builder
	write := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fmt.Fprintf(&b, "%s: %s\r\n", name, value)
	}
	write("Message-ID", "<"+m.ID+"@yauzin.invalid>")
	write("From", m.From.String())
	write("To", mail.FormatAddressList(m.To))
	write("Cc", mail.FormatAddressList(m.Cc))
	write("Subject", m.Subject)
	if !m.Date.IsZero() {
		write("Date", m.Date.Format(time.RFC1123Z))
	}
	write("MIME-Version", "1.0")
	write("Content-Type", `text/plain; charset="utf-8"`)

	body := m.TextBody
	if strings.TrimSpace(body) == "" {
		body = "[此邮件只有 HTML 正文]"
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return b.String()
}

// Close implements mail.Service.
func (s *Service) Close() error { return nil }

func htmlBody(title, lead string, bullets []string, from string) string {
	var b strings.Builder
	b.WriteString(`<div style="font-size:14px;color:#202124">`)
	fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(title))
	fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(lead))
	if len(bullets) > 0 {
		b.WriteString("<ul>")
		for _, item := range bullets {
			fmt.Fprintf(&b, "<li>%s</li>", html.EscapeString(item))
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`<p>相关材料可以在这里查看：<a href="https://example.com/demo">打开详情</a>。</p>`)
	fmt.Fprintf(&b, `<p style="color:#5f6368">— %s</p>`, html.EscapeString(from))
	b.WriteString("</div>")
	return b.String()
}

func plainBody(lead string, bullets []string, from string) string {
	var b strings.Builder
	b.WriteString(lead)
	b.WriteString("\n\n")
	for _, item := range bullets {
		b.WriteString("• ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	b.WriteString("\n相关材料：https://example.com/demo\n\n— ")
	b.WriteString(from)
	return b.String()
}

type template struct {
	from       string
	email      string
	subject    string
	snippet    string
	lead       string
	bullets    []string
	labels     []string
	attachment string
}

var templates = []template{
	{
		from: "产品团队", email: "product@lumen.studio",
		subject: "Q4 路线图评审：需要你确认的三个取舍",
		snippet: "我们把下季度的候选需求压缩到 12 个，其中有三个取舍需要今天下班前确认。",
		lead:    "下午好，我们把下季度的候选需求压缩到 12 个，其中有三个取舍需要今天下班前确认。",
		bullets: []string{
			"移动端离线草稿是否进入 Q4 范围",
			"搜索体验重做是否延后到 1 月",
			"会员体系是否与消息中心解耦",
		},
		labels:     []string{"label:work"},
		attachment: "Q4-路线图.txt",
	},
	{
		from: "GitHub", email: "notifications@github.com",
		subject: "[yauzin] 3 个新的 Pull Request 等待评审",
		snippet: "feat(ui): gmail style message rows · fix(auth): refresh token race · chore: bump gio",
		lead:    "你的仓库 yauzin 有 3 个新的 Pull Request 等待评审。",
		bullets: []string{
			"#128 feat(ui): gmail style message rows",
			"#127 fix(auth): refresh token race",
			"#126 chore: bump gio to the latest patch",
		},
		labels: []string{"label:github", "label:work"},
	},
	{
		from: "财务共享中心", email: "finance@lumen.studio",
		subject: "9 月报销单已通过审核",
		snippet: "你提交的 9 月差旅报销（合计 3,280.00 元）已通过审核，预计 3 个工作日内到账。",
		lead:    "你提交的 9 月差旅报销（合计 3,280.00 元）已通过审核，预计 3 个工作日内到账。",
		bullets: []string{
			"单据编号：BX-2026-0917",
			"审批人：财务共享中心",
			"到账账户：尾号 4821",
		},
		labels:     []string{"label:bills"},
		attachment: "报销明细.txt",
	},
	{
		from: "Lin Wei", email: "lin.wei@lumen.studio",
		subject: "周五晚餐订在 7 点，可以吗？",
		snippet: "我订了公司附近那家云南菜，周五晚上 7 点，四个人。你要是加班我们就往后挪半小时。",
		lead:    "我订了公司附近那家云南菜，周五晚上 7 点，四个人。你要是加班我们就往后挪半小时。",
		bullets: []string{
			"地点：云上小馆（B 座对面）",
			"时间：周五 19:00",
			"人数：4 人",
		},
	},
	{
		from: "Notion", email: "team@makenotion.com",
		subject: "你的工作区本周更新摘要",
		snippet: "本周有 14 个页面被更新，3 条评论提到了你，还有 2 个待办即将到期。",
		lead:    "本周有 14 个页面被更新，3 条评论提到了你，还有 2 个待办即将到期。",
		bullets: []string{
			"产品/路线图：新增「邮件客户端」子页面",
			"设计/收件箱密度：评论中提到了你",
			"团队/周会纪要：2 个待办即将到期",
		},
	},
	{
		from: "云服务账单", email: "billing@cloud.example.com",
		subject: "Your invoice for September is available",
		snippet: "Your September invoice of $128.40 is ready. Payment will be charged to the card ending in 4821.",
		lead:    "Your September invoice of $128.40 is ready. Payment will be charged to the card ending in 4821.",
		bullets: []string{
			"Compute: $86.10",
			"Object storage: $22.30",
			"Network egress: $20.00",
		},
		labels:     []string{"label:bills"},
		attachment: "invoice-2026-09.txt",
	},
	{
		from: "Sora Tanabe", email: "sora@studio-north.jp",
		subject: "设计稿 v3：收件箱行的密度调整",
		snippet: "按上次讨论把行高从 72 降到 64，发件人和时间放在同一行，摘要保持单行省略。",
		lead:    "按上次讨论把行高从 72 降到 64，发件人和时间放在同一行，摘要保持单行省略。",
		bullets: []string{
			"未读行文字加粗，左侧保留 3px 主色竖条",
			"星标移到最左侧，点击区域扩大到 32px",
			"悬停背景使用 #EDF2FA",
		},
		labels: []string{"label:work", "label:work:projA"},
	},
	{
		from: "旅行雷达", email: "alert@travelradar.example",
		subject: "东京往返机票降价提醒：¥2,380 起",
		snippet: "你关注的上海—东京航线出现近 30 天低点，11 月中旬出发的直飞航班降至 ¥2,380。",
		lead:    "你关注的上海—东京航线出现近 30 天低点，11 月中旬出发的直飞航班降至 ¥2,380。",
		bullets: []string{
			"去程：11 月 14 日 09:20",
			"回程：11 月 19 日 18:40",
			"行李：23kg 托运",
		},
		labels: []string{"label:travel"},
	},
	{
		from: "HR 团队", email: "people@lumen.studio",
		subject: "10 月员工福利与体检安排",
		snippet: "本月体检开放预约，另外补充医疗保险的登记截止到 10 月 25 日。",
		lead:    "本月体检开放预约，另外补充医疗保险的登记截止到 10 月 25 日。",
		bullets: []string{
			"体检预约：10 月 12 日开放，先到先得",
			"补充医疗：10 月 25 日前完成登记",
			"健身房补贴：随 10 月工资发放",
		},
	},
	{
		from: "GitHub", email: "actions@github.com",
		subject: "[lumen/console] Build failed on main",
		snippet: "Build #4821 failed at step `go test ./...`: internal/ui: undefined: theme.ColorNameSurfaceTint",
		lead:    "Build #4821 failed at step go test ./... on branch main.",
		bullets: []string{
			"Commit: 7f3a1c9 feat(ui): add surface tint",
			"Duration: 2m 14s",
			"Runner: ubuntu-latest",
		},
		labels: []string{"label:github", "label:work", "label:work:projB"},
	},
	{
		from: "陈曦", email: "chen.xi@lumen.studio",
		subject: "转发：客户验收清单（附文档）",
		snippet: "客户把验收拆成了 6 项，其中第 3 项需要我们补充一封确认邮件，模板在附件里。",
		lead:    "客户把验收拆成了 6 项，其中第 3 项需要我们补充一封确认邮件，模板在附件里。",
		bullets: []string{
			"1. 环境部署确认",
			"2. 数据迁移核对",
			"3. 上线确认邮件（待补充）",
		},
		labels:     []string{"label:work"},
		attachment: "客户验收清单.txt",
	},
	{
		from: "Product Hunt Daily", email: "hello@producthunt.example",
		subject: "Today's top 5 launches",
		snippet: "A calmer inbox, a keyboard-first calendar, and three tools that make changelogs readable.",
		lead:    "A calmer inbox, a keyboard-first calendar, and three tools that make changelogs readable.",
		bullets: []string{
			"Calm Inbox — a focused mail client",
			"KeyCal — keyboard-first calendar",
			"Changelog Studio — readable release notes",
		},
	},
	{
		from: "账单提醒", email: "notice@card.example.com",
		subject: "信用卡账单已出账",
		snippet: "本期账单金额 2,846.20 元，最低还款 284.62 元，到期还款日 11 月 8 日。",
		lead:    "本期账单金额 2,846.20 元，最低还款 284.62 元，到期还款日 11 月 8 日。",
		bullets: []string{
			"账单周期：9 月 9 日 — 10 月 8 日",
			"到期还款日：11 月 8 日",
			"自动还款：已开启",
		},
		labels: []string{"label:bills"},
	},
	{
		from: "Marta", email: "marta@northwind.io",
		subject: "Re: onboarding flow feedback",
		snippet: "Thanks for the walkthrough — the empty state copy is much clearer now. Two small notes below.",
		lead:    "Thanks for the walkthrough — the empty state copy is much clearer now. Two small notes below.",
		bullets: []string{
			"Keep the primary action visible on 13-inch screens",
			"Add a hint when the account list is empty",
		},
		labels:     []string{"label:work"},
		attachment: "feedback-notes.txt",
	},
}
