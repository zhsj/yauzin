package mock

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
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
	folders, err = s.FolderDetails(ctx, folders)
	if err != nil {
		t.Fatalf("folder details: %v", err)
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
	folders, err = s.FolderDetails(ctx, folders)
	if err != nil {
		t.Fatalf("folder details: %v", err)
	}
	for _, f := range folders {
		if f.ID == "inbox" && f.Unread != inboxUnread-1 {
			t.Fatalf("inbox unread = %d after marking one thread read, want %d", f.Unread, inboxUnread-1)
		}
	}
}

// TestFolderDetailsReportsConversationTotals keeps the folder total in step
// with the list: the "N of Total" label reads the folder's Total, so it must
// equal the count ListThreads reports for the same folder.
func TestFolderDetailsReportsConversationTotals(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	folders, err := s.Folders(ctx)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	folders, err = s.FolderDetails(ctx, folders)
	if err != nil {
		t.Fatalf("folder details: %v", err)
	}

	for _, f := range folders {
		page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: f.ID, Limit: 200})
		if err != nil {
			t.Fatalf("list %q: %v", f.ID, err)
		}
		if f.Total != page.Total {
			t.Fatalf("folder %q total = %d, want %d", f.ID, f.Total, page.Total)
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

	page, err := s.SearchThreads(ctx, mail.SearchRequest{Query: "needle", Limit: 50})
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

// TestSearchSpansEveryFolderExceptTrash covers the scope of a search: a
// conversation outside the inbox is found, and trashing it takes it out again.
func TestSearchSpansEveryFolderExceptTrash(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	const subject = "needle sent"
	if err := s.Send(ctx, mail.Draft{
		To:      []mail.Address{{Name: "Bob", Email: "bob@example.com"}},
		Subject: subject,
		Body:    "needle body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	// The sent conversation is not in the inbox, so finding it proves the
	// search is not folder scoped.
	inbox, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	page, err := s.SearchThreads(ctx, mail.SearchRequest{Query: subject})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Total != 1 {
		t.Fatalf("search matched %d conversations, want 1", page.Total)
	}
	hit := page.Threads[0]
	for _, th := range inbox.Threads {
		if th.ID == hit.ID {
			t.Fatal("the sent conversation is also in the inbox, so the scope is untested")
		}
	}

	if err := s.Delete(ctx, hit.MessageIDs); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := s.SearchThreads(ctx, mail.SearchRequest{Query: subject})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if after.Total != 0 {
		t.Fatalf("search after delete matched %d conversations, want none: trash is out of scope", after.Total)
	}
}

// TestSpamIsAFolderOfItsOwn covers the spam semantics: the demo junk has its
// own folder with a count, it is neither in the inbox nor reachable by search,
// and moving it to the trash takes it out of spam the way Gmail does.
func TestSpamIsAFolderOfItsOwn(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	folders, err := s.Folders(ctx)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	folders, err = s.FolderDetails(ctx, folders)
	if err != nil {
		t.Fatalf("folder details: %v", err)
	}
	var spamFolder mail.Folder
	for _, f := range folders {
		if f.Kind == mail.FolderSpam {
			spamFolder = f
		}
	}
	if spamFolder.ID != "spam" || spamFolder.Total == 0 || spamFolder.Unread == 0 {
		t.Fatalf("spam folder = %+v, want conversations and unread mail", spamFolder)
	}

	spam, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "spam", Limit: 200})
	if err != nil {
		t.Fatalf("list spam: %v", err)
	}
	target := spam.Threads[0]

	inbox, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	for _, th := range inbox.Threads {
		if th.ID == target.ID {
			t.Fatalf("spam conversation %s also shows in the inbox", target.ID)
		}
	}
	found, err := s.SearchThreads(ctx, mail.SearchRequest{Query: target.Subject})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if found.Total != 0 {
		t.Fatalf("search matched %d spam conversations, want none", found.Total)
	}

	if err := s.Delete(ctx, target.MessageIDs); err != nil {
		t.Fatalf("delete: %v", err)
	}
	spam, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "spam", Limit: 200})
	if err != nil {
		t.Fatalf("list spam after delete: %v", err)
	}
	for _, th := range spam.Threads {
		if th.ID == target.ID {
			t.Fatalf("deleted conversation %s is still in Spam", target.ID)
		}
	}
	trash, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "trash", Limit: 200})
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	inTrash := false
	for _, th := range trash.Threads {
		if th.ID == target.ID {
			inTrash = true
		}
	}
	if !inTrash {
		t.Fatalf("deleted spam conversation %s is missing from Trash", target.ID)
	}
}

// TestMarkSpamMovesTheThreadBetweenInboxAndSpam covers both directions of the
// spam mark: filing leaves the current folder and clearing puts the
// conversation back in the inbox, where the undo is visible.
func TestMarkSpamMovesTheThreadBetweenInboxAndSpam(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	inbox, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	target := inbox.Threads[0]
	inboxTotal := inbox.Total

	if err := s.MarkSpam(ctx, target.MessageIDs, true); err != nil {
		t.Fatalf("mark spam: %v", err)
	}
	inbox, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list inbox after mark: %v", err)
	}
	if inbox.Total != inboxTotal-1 {
		t.Fatalf("inbox holds %d conversations after the mark, want %d", inbox.Total, inboxTotal-1)
	}
	for _, th := range inbox.Threads {
		if th.ID == target.ID {
			t.Fatalf("thread %s marked as spam is still in the inbox", target.ID)
		}
	}
	spam, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "spam", Limit: 200})
	if err != nil {
		t.Fatalf("list spam: %v", err)
	}
	if !containsThread(spam, target.ID) {
		t.Fatalf("thread %s marked as spam is missing from Spam", target.ID)
	}
	found, err := s.SearchThreads(ctx, mail.SearchRequest{Query: target.Subject})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if containsThread(found, target.ID) {
		t.Fatalf("thread %s marked as spam is reachable by search", target.ID)
	}

	if err := s.MarkSpam(ctx, target.MessageIDs, false); err != nil {
		t.Fatalf("clear spam: %v", err)
	}
	spam, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "spam", Limit: 200})
	if err != nil {
		t.Fatalf("list spam after clear: %v", err)
	}
	if containsThread(spam, target.ID) {
		t.Fatalf("thread %s cleared of spam is still in Spam", target.ID)
	}
	inbox, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 200})
	if err != nil {
		t.Fatalf("list inbox after clear: %v", err)
	}
	if inbox.Total != inboxTotal || !containsThread(inbox, target.ID) {
		t.Fatalf("thread %s did not return to the inbox", target.ID)
	}
}

// TestMarkSpamRemovesSentMessageFromSent pins the folder exclusion Gmail gives
// spam: a conversation filed as spam leaves every other folder, Sent included.
func TestMarkSpamRemovesSentMessageFromSent(t *testing.T) {
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
	target := sent.Threads[0]

	if err := s.MarkSpam(ctx, target.MessageIDs, true); err != nil {
		t.Fatalf("mark spam: %v", err)
	}
	sent, err = s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 200})
	if err != nil {
		t.Fatalf("list sent after mark: %v", err)
	}
	if containsThread(sent, target.ID) {
		t.Fatalf("thread %s marked as spam is still in Sent", target.ID)
	}
	spam, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "spam", Limit: 200})
	if err != nil {
		t.Fatalf("list spam: %v", err)
	}
	if !containsThread(spam, target.ID) {
		t.Fatalf("thread %s marked as spam is missing from Spam", target.ID)
	}
}

// containsThread reports whether a page holds the conversation.
func containsThread(page mail.ThreadPage, id string) bool {
	for _, th := range page.Threads {
		if th.ID == id {
			return true
		}
	}
	return false
}

func TestSearchThreadsRequiresAQuery(t *testing.T) {
	s := newTestService(t)
	for _, query := range []string{"", "   "} {
		if _, err := s.SearchThreads(context.Background(), mail.SearchRequest{Query: query}); !errors.Is(err, mail.ErrInvalidArgument) {
			t.Fatalf("query %q returned %v, want ErrInvalidArgument", query, err)
		}
	}
}

func TestListThreadsRequiresAFolder(t *testing.T) {
	s := newTestService(t)
	if _, err := s.ListThreads(context.Background(), mail.ListRequest{}); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
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

	for _, id := range []string{"inbox", "starred", "sent", "spam", "trash", "label:work"} {
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

// TestFoldersExposeNestedLabels keeps the demo data showing what a real
// account shows: the hierarchy lives in the label name, and every child is a
// folder of its own that can be opened.
func TestFoldersExposeNestedLabels(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	folders, err := s.Folders(ctx)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	names := make(map[string]string, len(folders))
	for _, f := range folders {
		names[f.ID] = f.Name
	}
	if got := names["label:work"]; got != "工作" {
		t.Fatalf("parent label = %q, want 工作", got)
	}
	for _, id := range []string{"label:work:projA", "label:work:projB"} {
		name, ok := names[id]
		if !ok {
			t.Fatalf("nested label %q is missing from the folder list", id)
		}
		if !strings.HasPrefix(name, "工作/") {
			t.Fatalf("nested label %q is named %q, want a child of 工作", id, name)
		}
		if _, err := s.ListThreads(ctx, mail.ListRequest{FolderID: id, Limit: 10}); err != nil {
			t.Fatalf("folder %q: %v", id, err)
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

// TestRawMessageRendersSource covers the demo backend's raw read: the source
// carries the headers the reader's "view original" page shows, including the
// message ID, and the text part.
func TestRawMessageRendersSource(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Threads) == 0 {
		t.Fatal("the seeded inbox is empty")
	}
	msgs, err := s.GetThread(ctx, page.Threads[0].ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	msg := msgs[0]

	raw, err := s.RawMessage(ctx, msg.ID)
	if err != nil {
		t.Fatalf("raw message: %v", err)
	}
	text := string(raw)
	// Line endings are CRLF on the wire, so compare the body with them folded.
	folded := strings.ReplaceAll(text, "\r\n", "\n")
	for _, want := range []string{
		"Message-ID: <" + msg.ID + "@yauzin.invalid>",
		"From: " + msg.From.String(),
		"Subject: " + msg.Subject,
		`Content-Type: text/plain; charset="utf-8"`,
		msg.TextBody,
	} {
		haystack := text
		if strings.Contains(want, "\n") {
			haystack = folded
		}
		if !strings.Contains(haystack, want) {
			t.Fatalf("source misses %q:\n%s", want, text)
		}
	}

	if _, err := s.RawMessage(ctx, "no-such-message"); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("unknown message returned %v, want ErrNotFound", err)
	}
}

// TestSeededThreadsCarryThreadHeaders pins the demo data a reply is written
// from: every message has a Message-ID and a reply lists the chain before it.
func TestSeededThreadsCarryThreadHeaders(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	page, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "inbox", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var merged mail.ThreadSummary
	for _, th := range page.Threads {
		if th.MessageCount > 1 {
			merged = th
			break
		}
	}
	if merged.ID == "" {
		t.Fatal("the seeded inbox has no merged conversation")
	}
	msgs, err := s.GetThread(ctx, merged.ID)
	if err != nil {
		t.Fatalf("get thread: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("thread has %d messages", len(msgs))
	}
	for i, m := range msgs {
		if m.MessageID == "" {
			t.Fatalf("message %d has no Message-ID", i)
		}
		if i == 0 {
			continue
		}
		if len(m.References) == 0 {
			t.Fatalf("message %d has no References", i)
		}
		if last := m.References[len(m.References)-1]; last != msgs[i-1].MessageID {
			t.Fatalf("message %d references %q, want its parent %q", i, last, msgs[i-1].MessageID)
		}
	}
}

// TestSentMessageKeepsThreadHeaders pins that a reply sent through the demo
// backend keeps the ID and chain the composer decided, so the source view shows
// what went out.
func TestSentMessageKeepsThreadHeaders(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	when := time.Date(2026, 3, 5, 8, 7, 0, 0, time.FixedZone("CST", 8*3600))
	draft := mail.Draft{
		To:         []mail.Address{{Email: "bob@example.com"}},
		Subject:    "Re: hello",
		Body:       "reply",
		Date:       when,
		MessageID:  "<fixed@example.com>",
		InReplyTo:  "<parent@example.com>",
		References: []string{"<root@example.com>", "<parent@example.com>"},
	}
	if err := s.Send(ctx, draft); err != nil {
		t.Fatalf("send: %v", err)
	}
	// The draft is dated in the past, so the sent copy is not the newest row:
	// look it up by the ID the composer decided.
	sent, err := s.ListThreads(ctx, mail.ListRequest{FolderID: "sent", Limit: 200})
	if err != nil {
		t.Fatalf("list sent: %v", err)
	}
	var got *mail.Message
	for _, th := range sent.Threads {
		msgs, err := s.GetThread(ctx, th.ID)
		if err != nil {
			t.Fatalf("get thread: %v", err)
		}
		for _, m := range msgs {
			if m.MessageID == draft.MessageID {
				got = m
			}
		}
	}
	if got == nil {
		t.Fatalf("the sent folder does not hold %q", draft.MessageID)
	}
	if got.MessageID != draft.MessageID {
		t.Fatalf("Message-ID = %q, want %q", got.MessageID, draft.MessageID)
	}
	if gotRefs := strings.Join(got.References, " "); gotRefs != strings.Join(draft.References, " ") {
		t.Fatalf("References = %q, want %q", gotRefs, draft.References)
	}
	if !got.Date.Equal(when) {
		t.Fatalf("Date = %v, want %v", got.Date, when)
	}

	raw, err := s.RawMessage(ctx, got.ID)
	if err != nil {
		t.Fatalf("raw message: %v", err)
	}
	for _, want := range []string{
		"Message-ID: <fixed@example.com>",
		"In-Reply-To: <parent@example.com>",
		"References: <root@example.com> <parent@example.com>",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("source misses %q:\n%s", want, raw)
		}
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
