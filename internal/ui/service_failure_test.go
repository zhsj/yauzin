package ui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/notify"
)

// fakeService is a mail.Service whose answers the test decides. It records the
// calls it received so the tests can assert on the arguments the UI sent.
type fakeService struct {
	mu sync.Mutex

	foldersErr error
	setFlagErr error

	// checkChanged and checkErr answer CheckChanges; calls records the order of
	// the backend calls the UI made, so a test can pin "check before load".
	// checkGate, when set, holds the check until the test closes it, which is
	// how a test observes a check that is still in flight.
	checkChanged bool
	checkErr     error
	checkGate    chan struct{}
	calls        []string

	// foldersGate, when set, holds Folders until the test closes it.
	// foldersCancelled counts the sidebar loads their context cut short, so a
	// test can pin that a superseded refresh left the request alone.
	foldersGate      chan struct{}
	foldersCancelled int

	// searchPage and searchErr answer the inbox poll's SearchThreads;
	// searchCalls keeps the requests so a test can pin the query and limit.
	searchPage  mail.ThreadPage
	searchErr   error
	searchCalls []mail.SearchRequest

	setFlagCalls []setFlagCall
}

// record appends name to the call log.
func (f *fakeService) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

// callLog returns the recorded calls.
func (f *fakeService) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// countCalls returns how many times one recorded call ran.
func (f *fakeService) countCalls(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

type setFlagCall struct {
	ids  []string
	flag mail.Flag
	on   bool
}

func (f *fakeService) Folders(ctx context.Context) ([]mail.Folder, error) {
	f.record("folders")
	f.mu.Lock()
	gate, err := f.foldersGate, f.foldersErr
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			f.mu.Lock()
			f.foldersCancelled++
			f.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return []mail.Folder{{ID: "inbox", Kind: mail.FolderInbox}}, nil
}

// cancelledFolders returns how many sidebar loads were cancelled.
func (f *fakeService) cancelledFolders() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.foldersCancelled
}

func (f *fakeService) FolderDetails(_ context.Context, folders []mail.Folder) ([]mail.Folder, error) {
	f.record("details")
	if f.foldersErr != nil {
		return nil, f.foldersErr
	}
	return folders, nil
}

// CheckChanges answers what the test configured. The demo data of this fake
// never moves on its own, so an unconfigured fake reports no change.
func (f *fakeService) CheckChanges(ctx context.Context) (bool, error) {
	f.record("check")
	f.mu.Lock()
	changed, err, gate := f.checkChanged, f.checkErr, f.checkGate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return changed, err
}

func (f *fakeService) ListThreads(context.Context, mail.ListRequest) (mail.ThreadPage, error) {
	f.record("list")
	return mail.ThreadPage{}, nil
}

func (f *fakeService) SearchThreads(_ context.Context, req mail.SearchRequest) (mail.ThreadPage, error) {
	f.record("search")
	f.mu.Lock()
	f.searchCalls = append(f.searchCalls, req)
	page, err := f.searchPage, f.searchErr
	f.mu.Unlock()
	return page, err
}

// searchRequests returns the requests the poll sent.
func (f *fakeService) searchRequests() []mail.SearchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mail.SearchRequest(nil), f.searchCalls...)
}

func (f *fakeService) GetThread(context.Context, string) ([]*mail.Message, error) {
	return nil, mail.ErrNotFound
}

func (f *fakeService) SetFlag(_ context.Context, ids []string, flag mail.Flag, on bool) error {
	f.mu.Lock()
	f.setFlagCalls = append(f.setFlagCalls, setFlagCall{ids: append([]string{}, ids...), flag: flag, on: on})
	err := f.setFlagErr
	f.mu.Unlock()
	return err
}

func (f *fakeService) Archive(context.Context, []string) error { return nil }

func (f *fakeService) Delete(context.Context, []string) error { return nil }

func (f *fakeService) Send(context.Context, mail.Draft) error { return nil }

func (f *fakeService) SaveAttachment(context.Context, string, string, string) (string, error) {
	return "", mail.ErrUnsupported
}

func (f *fakeService) RawMessage(context.Context, string) ([]byte, error) {
	return nil, mail.ErrUnsupported
}

func (f *fakeService) Close() error { return nil }

// useService installs svc as the backend of the test app's only account.
func useService(a *App, svc mail.Service) {
	a.services[a.activeAcc] = svc
}

// waitFor drains the post queue until cond holds. Callbacks posted by the
// worker goroutines only run in drain, so the test drives them itself.
func waitFor(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.drain()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// blockingService keeps ListThreads and SearchThreads waiting until their
// context is done, so the test can observe whether a superseded request is
// cancelled or left running.
type blockingService struct {
	fakeService
	started  chan struct{}
	canceled chan error
	once     sync.Once
}

func (b *blockingService) ListThreads(ctx context.Context, _ mail.ListRequest) (mail.ThreadPage, error) {
	return b.block(ctx)
}

func (b *blockingService) SearchThreads(ctx context.Context, _ mail.SearchRequest) (mail.ThreadPage, error) {
	return b.block(ctx)
}

func (b *blockingService) block(ctx context.Context) (mail.ThreadPage, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	select {
	case b.canceled <- ctx.Err():
	default:
	}
	return mail.ThreadPage{}, ctx.Err()
}

// gatedService holds Archive until the test releases it, so the test can change
// the open conversation while the mutation is still in flight.
type gatedService struct {
	fakeService
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedService) Archive(context.Context, []string) error {
	g.once.Do(func() { close(g.started) })
	<-g.release
	return nil
}

func TestArchiveDoesNotCloseAnotherConversation(t *testing.T) {
	a := testApp(t)
	svc := &gatedService{started: make(chan struct{}), release: make(chan struct{})}
	useService(a, svc)

	a.thread = mail.ThreadSummary{ID: "t1", MessageIDs: []string{"m1"}}
	a.archiveThread()
	<-svc.started

	// The user opens another conversation while the archive is in flight.
	a.thread = mail.ThreadSummary{ID: "t2", MessageIDs: []string{"m2"}}
	a.selectedThread = "t2"
	close(svc.release)

	waitFor(t, a, "the archive result", func() bool { return a.status.visible() })

	if a.thread.ID != "t2" || a.selectedThread != "t2" {
		t.Fatalf("open conversation = %q/%q after archiving the previous one, want t2/t2",
			a.thread.ID, a.selectedThread)
	}
}

func TestSupersededListRequestIsCancelled(t *testing.T) {
	a := testApp(t)
	defer a.cancelRequests()

	svc := &blockingService{started: make(chan struct{}), canceled: make(chan error, 2)}
	useService(a, svc)

	a.loadThreads()
	<-svc.started
	// A second load supersedes the first one; the first request must stop
	// instead of running to its own timeout.
	a.loadThreads()

	select {
	case err := <-svc.canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("superseded request returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the superseded request kept running instead of being cancelled")
	}
}

// TestSupersededSearchRequestIsCancelled mirrors the list case on the search
// path: submitting a new query must stop the search still in flight.
func TestSupersededSearchRequestIsCancelled(t *testing.T) {
	a := testApp(t)
	defer a.cancelRequests()

	svc := &blockingService{started: make(chan struct{}), canceled: make(chan error, 2)}
	useService(a, svc)

	a.setQuery("one")
	<-svc.started
	a.setQuery("two")

	select {
	case err := <-svc.canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("superseded search returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the superseded search kept running instead of being cancelled")
	}
}

func TestMarkThreadReadRevertsOnFailure(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{setFlagErr: errors.New("backend down")}
	useService(a, svc)

	sum := mail.ThreadSummary{
		ID:          "t1",
		Subject:     "Subject",
		MessageIDs:  []string{"m1", "m2"},
		UnreadCount: 2,
	}
	a.list.setThreads([]mail.ThreadSummary{sum})

	a.markThreadRead(sum, true)
	waitFor(t, a, "the failure notification", func() bool { return a.status.visible() })

	got, ok := a.list.thread(sum.ID)
	if !ok {
		t.Fatalf("thread %q disappeared from the list", sum.ID)
	}
	if got.UnreadCount != 2 {
		t.Fatalf("unread count = %d after a failed mark-read, want the previous 2", got.UnreadCount)
	}
}

func TestMarkThreadReadWithoutServiceReverts(t *testing.T) {
	a := testApp(t)
	sum := mail.ThreadSummary{ID: "t1", MessageIDs: []string{"m1"}, UnreadCount: 1}
	a.list.setThreads([]mail.ThreadSummary{sum})

	a.markThreadRead(sum, true)

	got, _ := a.list.thread(sum.ID)
	if got.UnreadCount != 1 {
		t.Fatalf("unread count = %d without a backend, want the previous 1", got.UnreadCount)
	}
}

func TestToggleThreadReadByIDFlipsUnreadState(t *testing.T) {
	for _, tc := range []struct {
		name string
		// unread is the state the row starts in; wantRead is the FlagRead
		// value the backend must receive and wantLeft the optimistic count.
		unread   int
		wantRead bool
		wantLeft int
	}{
		{name: "unread becomes read", unread: 2, wantRead: true, wantLeft: 0},
		{name: "read becomes unread", unread: 0, wantRead: false, wantLeft: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			svc := &fakeService{}
			useService(a, svc)

			sum := mail.ThreadSummary{
				ID:          "t1",
				MessageIDs:  []string{"m1", "m2"},
				UnreadCount: tc.unread,
			}
			a.list.setThreads([]mail.ThreadSummary{sum})

			a.toggleThreadReadByID(sum.ID)

			waitFor(t, a, "the SetFlag call", func() bool {
				svc.mu.Lock()
				defer svc.mu.Unlock()
				return len(svc.setFlagCalls) == 1
			})

			svc.mu.Lock()
			call := svc.setFlagCalls[0]
			svc.mu.Unlock()
			if call.flag != mail.FlagRead || call.on != tc.wantRead {
				t.Fatalf("flag = %v on = %v, want read/%v", call.flag, call.on, tc.wantRead)
			}

			got, ok := a.list.thread(sum.ID)
			if !ok {
				t.Fatalf("thread %q disappeared from the list", sum.ID)
			}
			if got.UnreadCount != tc.wantLeft {
				t.Fatalf("unread count = %d after toggling from %d, want %d",
					got.UnreadCount, tc.unread, tc.wantLeft)
			}
		})
	}
}

func TestReloadFoldersReportsFailure(t *testing.T) {
	a := testApp(t)
	useService(a, &fakeService{foldersErr: errors.New("backend down")})

	a.reloadFolders()
	waitFor(t, a, "the folders failure notification", func() bool { return a.status.visible() })

	if !a.status.isErr {
		t.Fatal("folders failure was reported as success")
	}
}

func TestSetFlagReceivesThreadMessages(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)

	sum := mail.ThreadSummary{ID: "t1", MessageIDs: []string{"m1", "m2"}, UnreadCount: 2}
	a.list.setThreads([]mail.ThreadSummary{sum})
	a.markThreadRead(sum, true)

	waitFor(t, a, "the SetFlag call", func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return len(svc.setFlagCalls) == 1
	})

	svc.mu.Lock()
	defer svc.mu.Unlock()
	call := svc.setFlagCalls[0]
	if call.flag != mail.FlagRead || !call.on {
		t.Fatalf("flag = %v on = %v, want read/true", call.flag, call.on)
	}
	if len(call.ids) != 2 {
		t.Fatalf("ids = %v, want both messages of the conversation", call.ids)
	}
}

// fakeNotifier records the desktop notifications the app publishes.
type fakeNotifier struct {
	mu      sync.Mutex
	notices []notify.Notice
	closed  bool
}

func (f *fakeNotifier) Show(notice notify.Notice) {
	f.mu.Lock()
	f.notices = append(f.notices, notice)
	f.mu.Unlock()
}

func (f *fakeNotifier) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

// sent returns the published notifications.
func (f *fakeNotifier) sent() []notify.Notice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notify.Notice(nil), f.notices...)
}

// wasClosed reports whether Close ran.
func (f *fakeNotifier) wasClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
