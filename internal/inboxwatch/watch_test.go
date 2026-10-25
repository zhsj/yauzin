package inboxwatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// fakeService answers the read the watcher makes and records the searches.
type fakeService struct {
	page      mail.ThreadPage
	searchErr error
	searches  []mail.SearchRequest
	checks    int
}

// CheckChanges counts its calls: the watcher must never make them, because
// detecting a change belongs to the UI's single-flight channel.
func (f *fakeService) CheckChanges(context.Context) (bool, error) {
	f.checks++
	return false, nil
}

func (f *fakeService) SearchThreads(_ context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	f.searches = append(f.searches, req)
	if f.searchErr != nil {
		return mail.ThreadPage{}, f.searchErr
	}
	return f.page, nil
}

func (f *fakeService) Folders(context.Context) ([]mail.Folder, error) { return nil, nil }
func (f *fakeService) FolderDetails(_ context.Context, folders []mail.Folder) ([]mail.Folder, error) {
	return folders, nil
}
func (f *fakeService) ListThreads(context.Context, mail.ListRequest) (mail.ThreadPage, error) {
	return mail.ThreadPage{}, nil
}
func (f *fakeService) GetThread(context.Context, string) ([]*mail.Message, error) {
	return nil, mail.ErrNotFound
}
func (f *fakeService) SetFlag(context.Context, []string, mail.Flag, bool) error { return nil }
func (f *fakeService) Archive(context.Context, []string) error                  { return nil }
func (f *fakeService) Delete(context.Context, []string) error                   { return nil }
func (f *fakeService) Send(context.Context, mail.Draft) error                   { return nil }
func (f *fakeService) SaveAttachment(context.Context, string, string, string) (string, error) {
	return "", mail.ErrUnsupported
}
func (f *fakeService) RawMessage(context.Context, string) ([]byte, error) {
	return nil, mail.ErrUnsupported
}
func (f *fakeService) Close() error { return nil }

// thread builds one conversation. ids are ordered oldest first.
func thread(id, subject string, unread int, ids ...string) mail.ThreadSummary {
	return mail.ThreadSummary{ID: id, Subject: subject, UnreadCount: unread, MessageIDs: ids}
}

// seed polls once with an empty baseline so the watcher records page.
func seed(t *testing.T, w *Watcher, svc *fakeService, page mail.ThreadPage) {
	t.Helper()
	svc.page = page
	out, err := w.Poll(context.Background(), "acc", svc, false)
	if err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	if !out.Updated || out.Notice != nil {
		t.Fatalf("seed outcome = %+v, want updated without a notice", out)
	}
}

// english pins the language so the expected notification strings are stable.
func english(t *testing.T) {
	t.Helper()
	i18n.SetLanguage("en")
	t.Cleanup(func() { i18n.SetLanguage("en") })
}

// TestPollDoesNotCheckTheMailbox pins the layering: the caller owns the change
// check (one single-flight channel in the UI), so the watcher only reads the
// inbox when told the mailbox moved or when it has no baseline yet.
func TestPollDoesNotCheckTheMailbox(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{page: mail.ThreadPage{
		Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")},
		Total:   1,
	}}

	if _, err := w.Poll(context.Background(), "acc", svc, false); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, err := w.Poll(context.Background(), "acc", svc, true); err != nil {
		t.Fatalf("poll on a change: %v", err)
	}
	if svc.checks != 0 {
		t.Fatalf("checks = %d, want the caller's answer used instead", svc.checks)
	}
	if got := len(svc.searches); got != 2 {
		t.Fatalf("searches = %d, want the baseline and the changed read", got)
	}
}

func TestPollSeedsBaselineWithoutNotifying(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{page: mail.ThreadPage{
		Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")},
		Total:   1,
	}}
	out, err := w.Poll(context.Background(), "acc", svc, false)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !out.Updated || !out.Active {
		t.Fatalf("outcome = %+v, want updated and active", out)
	}
	if out.Notice != nil {
		t.Fatal("the first read must build the baseline instead of notifying")
	}
	if len(svc.searches) != 1 {
		t.Fatalf("searches = %d, want one to build the baseline", len(svc.searches))
	}
	req := svc.searches[0]
	if req.Query != query || req.Limit != limit {
		t.Fatalf("search = %+v, want q=%q limit=%d", req, query, limit)
	}

	// With a baseline and no mailbox change the next tick reads nothing.
	out, err = w.Poll(context.Background(), "acc", svc, false)
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if out.Updated || out.Notice != nil {
		t.Fatalf("quiet poll = %+v, want no update", out)
	}
	if len(svc.searches) != 1 {
		t.Fatalf("searches = %d after a quiet poll, want 1", len(svc.searches))
	}
}

func TestPollNotifiesANewConversation(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	svc.page = mail.ThreadPage{
		Threads: []mail.ThreadSummary{
			thread("t2", "Second subject", 1, "m2"),
			thread("t1", "First", 1, "m1"),
		},
		Total: 5,
	}
	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Notice == nil {
		t.Fatal("a new unread conversation must notify")
	}
	wantBody := "Second subject\n\n5 unread conversations"
	if out.Notice.Body != wantBody {
		t.Fatalf("body = %q, want %q", out.Notice.Body, wantBody)
	}
	if out.Notice.Summary != "New mail in Inbox" || out.Notice.Action != "Open inbox" {
		t.Fatalf("notice = %+v, want the English title and action", out.Notice)
	}
}

func TestPollNotifiesMoreUnreadInAKnownConversation(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	svc.page = mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 2, "m1", "m2")}, Total: 2}
	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Notice == nil || !strings.Contains(out.Notice.Body, "First") {
		t.Fatalf("notice = %+v, want the growing conversation", out.Notice)
	}
}

func TestPollNotifiesANewMessageInAKnownConversation(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	// One message was read and one arrived: the unread count is unchanged but
	// the newest message is not.
	svc.page = mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1", "m2")}, Total: 1}
	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Notice == nil {
		t.Fatal("a new message in a known conversation must notify")
	}
}

func TestPollDoesNotRepeatAnUnchangedPage(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	page := mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1}
	seed(t, w, svc, page)

	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Notice != nil {
		t.Fatalf("notice = %+v, want none for the same page", out.Notice)
	}
	if !out.Updated || !out.Active {
		t.Fatalf("outcome = %+v, want an updated active tray", out)
	}
}

func TestPollReportsAnEmptyInbox(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	svc.page = mail.ThreadPage{}
	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !out.Updated || out.Active || out.Notice != nil {
		t.Fatalf("outcome = %+v, want an updated idle tray", out)
	}
}

func TestPollDropsAResultFromThePreviousAccount(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	w.Reset("other")
	out, err := w.Poll(context.Background(), "acc", svc, false)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Updated || out.Notice != nil {
		t.Fatalf("outcome = %+v, want a discarded result", out)
	}
}

func TestResetToNoAccountDropsAResult(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: 1})

	// The last mailbox was removed: an in-flight result of the old account
	// must not bind the watcher to it again.
	w.Reset("")
	out, err := w.Poll(context.Background(), "acc", svc, false)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Updated || out.Notice != nil {
		t.Fatalf("outcome = %+v, want a discarded result", out)
	}
}

func TestPollReportsReadErrors(t *testing.T) {
	english(t)
	svc := &fakeService{searchErr: errors.New("search failed")}
	if _, err := New().Poll(context.Background(), "acc", svc, true); err == nil {
		t.Fatal("a failed search must be reported")
	}
}

func TestPollWithoutAServiceIsANoOp(t *testing.T) {
	english(t)
	out, err := New().Poll(context.Background(), "acc", nil, true)
	if err != nil || out.Updated || out.Notice != nil {
		t.Fatalf("outcome = %+v err = %v, want a no-op", out, err)
	}
}

func TestSubjectLine(t *testing.T) {
	english(t)
	long := strings.Repeat("a", subjectMaxRunes+20)
	for _, tc := range []struct {
		name    string
		subject string
		want    string
	}{
		{"plain", "Quarterly report", "Quarterly report"},
		{"single line", "Re: hello\nworld\t!", "Re: hello world !"},
		{"empty", "  \n\t ", "(no subject)"},
		{"bounded", long, strings.Repeat("a", subjectMaxRunes-1) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := subjectLine(tc.subject)
			if got != tc.want {
				t.Fatalf("subjectLine(%q) = %q, want %q", tc.subject, got, tc.want)
			}
			if utf8.RuneCountInString(got) > subjectMaxRunes {
				t.Fatalf("subjectLine(%q) has %d runes, want at most %d",
					tc.subject, utf8.RuneCountInString(got), subjectMaxRunes)
			}
		})
	}
}

func TestNotificationTotalFallsBackToThePage(t *testing.T) {
	english(t)
	w := New()
	svc := &fakeService{}
	seed(t, w, svc, mail.ThreadPage{Threads: []mail.ThreadSummary{thread("t1", "First", 1, "m1")}, Total: -1})

	svc.page = mail.ThreadPage{
		Threads: []mail.ThreadSummary{
			thread("t2", "Second", 1, "m2"),
			thread("t3", "Third", 1, "m3"),
		},
		Total: -1,
	}
	out, err := w.Poll(context.Background(), "acc", svc, true)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if out.Notice == nil || !strings.HasSuffix(out.Notice.Body, "2 unread conversations") {
		t.Fatalf("body = %q, want the page size as the total", out.Notice.Body)
	}
}
