package mock

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yauzin/internal/mail"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := New(mail.Account{ID: "test", Kind: mail.KindGmail, Name: "Ada", Email: "ada@example.com"})
	s.latency = 0
	return s
}

func TestListThreadsPagination(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	first, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if first.Total != inboxThreads {
		t.Fatalf("total = %d, want %d", first.Total, inboxThreads)
	}
	if len(first.Threads) != 50 {
		t.Fatalf("page 1 threads = %d, want 50", len(first.Threads))
	}
	if first.NextPageToken == "" {
		t.Fatal("page 1 of 3 carries no cursor")
	}

	seen := map[string]bool{}
	pages := []mail.ThreadPage{first}
	// Walk the cursors the backend hands out, like the UI does.
	token := first.NextPageToken
	for page := 2; token != ""; page++ {
		p, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", PageToken: token, Limit: 50})
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		if page > 3 {
			t.Fatal("pagination never ended")
		}
		pages = append(pages, p)
		token = p.NextPageToken
	}
	if len(pages) != 3 {
		t.Fatalf("walked %d pages, want 3", len(pages))
	}
	var threadCount int
	for _, p := range pages {
		for _, th := range p.Threads {
			if seen[th.ID] {
				t.Fatalf("thread %s returned on multiple pages", th.ID)
			}
			seen[th.ID] = true
			threadCount++
			if th.MessageCount < 1 || th.MessageCount != len(th.MessageIDs) {
				t.Fatalf("thread %s has count %d and %d ids", th.ID, th.MessageCount, len(th.MessageIDs))
			}
		}
	}
	if threadCount != first.Total {
		t.Fatalf("collected %d threads, total says %d", threadCount, first.Total)
	}
}

func TestThreadsSortedNewestFirst(t *testing.T) {
	s := newTestService(t)

	page, err := s.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for i := 1; i < len(page.Threads); i++ {
		if page.Threads[i-1].LastDate.Before(page.Threads[i].LastDate) {
			t.Fatalf("thread %d is older than thread %d", i-1, i)
		}
	}
	for _, th := range page.Threads {
		if len(th.Participants) == 0 {
			t.Fatalf("thread %s has no participants", th.ID)
		}
		if th.Subject == "" {
			t.Fatalf("thread %s has no subject", th.ID)
		}
	}
}

func TestGetThreadReturnsMessagesOldestFirst(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var multi mail.ThreadSummary
	for _, th := range page.Threads {
		if th.MessageCount > 1 {
			multi = th
			break
		}
	}
	if multi.ID == "" {
		t.Fatal("the seeded inbox has no multi message conversation")
	}

	msgs, err := s.GetThread(ctx, multi.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if len(msgs) != multi.MessageCount {
		t.Fatalf("messages = %d, want %d", len(msgs), multi.MessageCount)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Date.Before(msgs[i-1].Date) {
			t.Fatalf("message %d is older than message %d", i, i-1)
		}
	}
	if msgs[len(msgs)-1].Subject != "Re: "+multi.Subject && msgs[len(msgs)-1].Subject != multi.Subject {
		t.Fatalf("unexpected newest subject %q for thread subject %q", msgs[len(msgs)-1].Subject, multi.Subject)
	}
}

func TestUnreadCountersUseConversations(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	folders, err := s.Folders(ctx)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	inboxUnread := 0
	for _, f := range folders {
		if f.ID == "inbox" {
			inboxUnread = f.Unread
		}
	}

	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := 0
	for _, th := range page.Threads {
		if th.Unread() {
			want++
		}
	}
	if inboxUnread != want {
		t.Fatalf("inbox unread = %d, want %d unread threads", inboxUnread, want)
	}

	// Marking a conversation read must drop the folder counter by one.
	target := page.Threads[0]
	if !target.Unread() {
		t.Fatalf("expected the newest conversation to be unread")
	}
	if err := s.SetFlag(ctx, target.MessageIDs, mail.FlagRead, true); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	folders, err = s.Folders(ctx)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	for _, f := range folders {
		if f.ID == "inbox" && f.Unread != inboxUnread-1 {
			t.Fatalf("inbox unread = %d after marking one thread read, want %d", f.Unread, inboxUnread-1)
		}
	}
}

func TestArchiveHidesThread(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	target := page.Threads[0]
	if err := s.Archive(ctx, target.MessageIDs); err != nil {
		t.Fatalf("archive: %v", err)
	}
	after, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list after archive: %v", err)
	}
	if after.Total != page.Total-1 {
		t.Fatalf("total after archive = %d, want %d", after.Total, page.Total-1)
	}
	for _, th := range after.Threads {
		if th.ID == target.ID {
			t.Fatalf("archived thread %s still listed", target.ID)
		}
	}
}

func TestWaitRespectsContext(t *testing.T) {
	s := &Service{latency: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.wait(ctx); err == nil {
		t.Fatal("expected context error")
	}
}

// seedStats summarises the stored conversations: how many hold each message
// count, and how many of them are shown as unread. It takes the lock, so the
// assertions below do not each walk s.threads by hand.
type seedStats struct {
	sizes  map[int]int
	unread int
}

func stats(s *Service) seedStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := seedStats{sizes: map[int]int{}}
	for _, th := range s.threads {
		st.sizes[len(th.msgs)]++
		if buildThreadSummary(th.id, th.msgs).UnreadCount > 0 {
			st.unread++
		}
	}
	return st
}

func TestSeedMixesConversationSizes(t *testing.T) {
	got := stats(newTestService(t))
	for size := 1; size <= 3; size++ {
		if got.sizes[size] == 0 {
			t.Fatalf("seed has no %d-message conversation: %v", size, got.sizes)
		}
	}
	if got.unread == 0 {
		t.Fatal("seed has no unread conversation, so the badges stay untested")
	}
}

func TestSearchSummaryCoversWholeConversation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	// Only the oldest message of a merged conversation mentions the needle, so
	// a summary built from the matches alone would report a single-message
	// conversation.
	s.mu.Lock()
	var target *thread
	var counts []int
	for _, th := range s.threads {
		counts = append(counts, len(th.msgs))
		if target == nil && len(th.msgs) >= 2 {
			target = th
		}
	}
	if target == nil {
		s.mu.Unlock()
		t.Fatalf("no merged conversation in the seed: %v", counts)
	}
	for _, e := range target.msgs {
		e.msg.Subject = "ordinary subject"
		e.msg.Snippet = "ordinary text"
		e.msg.TextBody = "ordinary text"
	}
	target.msgs[0].msg.Subject = "needle subject"
	target.msgs[0].msg.Snippet = "needle"
	target.msgs[0].msg.TextBody = "needle"
	wantIDs := len(target.msgs)
	s.mu.Unlock()

	page, err := s.ListThreads(ctx, mail.ListRequest{Query: "needle", Limit: 50})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("search matched %d conversations, want 1", page.Total)
	}
	sum := page.Threads[0]
	if sum.ID != target.id {
		t.Fatalf("search matched %s, want %s", sum.ID, target.id)
	}
	if sum.MessageCount != wantIDs || len(sum.MessageIDs) != wantIDs {
		t.Fatalf("summary reports %d messages and %d ids, want %d",
			sum.MessageCount, len(sum.MessageIDs), wantIDs)
	}
	msgs, err := s.GetThread(ctx, sum.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if len(msgs) != len(sum.MessageIDs) {
		t.Fatalf("opened %d messages but the row lists %d ids", len(msgs), len(sum.MessageIDs))
	}

	// Archiving the row has to take the whole conversation out of the inbox: a
	// partial ID list would leave the other messages visible.
	if err := s.Archive(ctx, sum.MessageIDs); err != nil {
		t.Fatalf("archive: %v", err)
	}
	after, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list after archive: %v", err)
	}
	for _, th := range after.Threads {
		if th.ID == sum.ID {
			t.Fatalf("archived conversation %s is still listed", sum.ID)
		}
	}
}

func TestDeleteRemovesSentMessageFromSent(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	if err := s.Send(ctx, mail.Draft{
		To:      []mail.Address{{Name: "Bob", Email: "bob@example.com"}},
		Subject: "hello",
		Body:    "body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 200})
	if err != nil {
		t.Fatalf("list sent: %v", err)
	}
	if sent.Total != 8 {
		t.Fatalf("sent holds %d conversations, want 8", sent.Total)
	}
	target := sent.Threads[0]

	if err := s.Delete(ctx, target.MessageIDs); err != nil {
		t.Fatalf("delete: %v", err)
	}
	sent, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 200})
	if err != nil {
		t.Fatalf("list sent after delete: %v", err)
	}
	for _, th := range sent.Threads {
		if th.ID == target.ID {
			t.Fatalf("deleted conversation %s is still in Sent", target.ID)
		}
	}
	trash, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "trash", Limit: 200})
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	found := false
	for _, th := range trash.Threads {
		if th.ID == target.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("deleted conversation %s is missing from Trash", target.ID)
	}
}

func TestSendOwnsTheDraftItStores(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	to := []mail.Address{{Name: "Bob", Email: "bob@example.com"}}
	cc := []mail.Address{{Name: "Carol", Email: "carol@example.com"}}
	if err := s.Send(ctx, mail.Draft{To: to, Cc: cc, Subject: "subject", Body: "body"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	// The composer may keep editing the slices it passed in; the stored message
	// must not change with them.
	to[0].Email = "mallory@example.com"
	cc[0].Email = "mallory@example.com"

	sent, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 1})
	if err != nil {
		t.Fatalf("list sent: %v", err)
	}
	msgs, err := s.GetThread(ctx, sent.Threads[0].ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	msg := msgs[0]
	if len(msg.To) != 1 || msg.To[0].Email != "bob@example.com" {
		t.Fatalf("stored To changed with the caller's slice: %+v", msg.To)
	}
	if len(msg.Cc) != 1 || msg.Cc[0].Email != "carol@example.com" {
		t.Fatalf("stored Cc changed with the caller's slice: %+v", msg.Cc)
	}
}

func TestSetFlagRejectsUnknownFlag(t *testing.T) {
	s := newTestService(t)
	err := s.SetFlag(context.Background(), []string{"anything"}, mail.Flag(99), true)
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("set flag returned %v, want ErrInvalidArgument", err)
	}
}

func TestKnownFoldersAndLabels(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	for _, id := range []string{"inbox", "starred", "sent", "trash", "label:work"} {
		if _, err := s.ListThreads(ctx, mail.ListRequest{FolderID: id, Limit: 10}); err != nil {
			t.Fatalf("folder %q: %v", id, err)
		}
	}
	for _, id := range []string{"inbx", "label:nope"} {
		if _, err := s.ListThreads(ctx, mail.ListRequest{FolderID: id, Limit: 10}); !errors.Is(err, mail.ErrNotFound) {
			t.Fatalf("folder %q returned %v, want ErrNotFound", id, err)
		}
	}
}

func TestListThreadsRejectsMalformedToken(t *testing.T) {
	s := newTestService(t)
	for _, token := range []string{"not-a-cursor", encodeToken(-1)} {
		if _, err := s.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox", PageToken: token}); !errors.Is(err, mail.ErrInvalidArgument) {
			t.Fatalf("token %q returned %v, want ErrInvalidArgument", token, err)
		}
	}
}

func TestListThreadsReturnsEmptyPagePastTheEnd(t *testing.T) {
	s := newTestService(t)
	page, err := s.ListThreads(context.Background(), mail.ListRequest{
		FolderID:  "inbox",
		PageToken: encodeToken(math.MaxInt),
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Threads) != 0 || page.NextPageToken != "" {
		t.Fatalf("page holds %d threads and cursor %q, want an empty last page",
			len(page.Threads), page.NextPageToken)
	}
	if page.Total != inboxThreads {
		t.Fatalf("total = %d, want %d", page.Total, inboxThreads)
	}
}

func TestDeleteIgnoresUnknownIDs(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	before, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := s.Delete(ctx, []string{"does-not-exist"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if after.Total != before.Total {
		t.Fatalf("inbox total went from %d to %d", before.Total, after.Total)
	}
}

func TestSaveAttachment(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var msg *mail.Message
	var att mail.Attachment
	for _, th := range page.Threads {
		if !th.HasAttachments {
			continue
		}
		msgs, err := s.GetThread(ctx, th.ID)
		if err != nil {
			t.Fatalf("get thread: %v", err)
		}
		for _, m := range msgs {
			if len(m.Attachments) > 0 {
				msg, att = m, m.Attachments[0]
			}
		}
		if msg != nil {
			break
		}
	}
	if msg == nil {
		t.Fatal("the seeded inbox has no attachment")
	}

	dir := t.TempDir()
	path, err := s.SaveAttachment(ctx, msg.ID, att.ID, dir)
	if err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("attachment written to %q, want a file inside %q", path, dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("attachment mode %o, want 600", perm)
	}

	if _, err := s.SaveAttachment(ctx, msg.ID, "no-such-attachment", dir); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("unknown attachment returned %v, want ErrNotFound", err)
	}

	// A hostile file name must not escape the directory it is written to.
	if err := s.Send(ctx, mail.Draft{
		To:          []mail.Address{{Email: "bob@example.com"}},
		Subject:     "with attachment",
		Body:        "body",
		Attachments: []mail.DraftAttachment{{Name: "../../escape.txt"}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 1})
	if err != nil {
		t.Fatalf("list sent: %v", err)
	}
	msgs, err := s.GetThread(ctx, sent.Threads[0].ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	evil := msgs[0]
	if len(evil.Attachments) != 1 {
		t.Fatalf("sent message has %d attachments, want 1", len(evil.Attachments))
	}
	evilPath, err := s.SaveAttachment(ctx, evil.ID, evil.Attachments[0].ID, dir)
	if err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if filepath.Dir(evilPath) != dir {
		t.Fatalf("attachment escaped to %q", evilPath)
	}
}
