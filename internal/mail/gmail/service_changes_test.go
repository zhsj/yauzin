package gmail

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// changesServer serves the endpoints the change check touches and counts them,
// so a test can tell a cache hit from a network read. The two gates hold a
// response until the test releases it, which is what makes an in-flight check
// observable.
type changesServer struct {
	mu sync.Mutex

	historyID     string
	omitHistoryID bool
	profileFields string

	profileCalls, labelCalls, listCalls int

	profileGate    chan struct{}
	profileStarted chan struct{}
	threadsGate    chan struct{}
	threadsStarted chan struct{}

	batch *threadBatchServer
}

func newChangesService(t *testing.T) (*Service, *changesServer) {
	t.Helper()
	s := &changesServer{
		historyID: "100",
		batch:     &threadBatchServer{threads: map[string]string{"t1": t1Body}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/profile", s.profile(t))
	mux.HandleFunc("/users/me/labels", s.labels(t))
	mux.HandleFunc("/users/me/threads", s.threads(t))
	mux.HandleFunc("/batch", s.batch.handler(t))
	svc, _ := newTestService(t, mux)
	t.Cleanup(s.release)
	return svc, s
}

// release unblocks every gate, so a failing test cannot leave the test server
// hanging during cleanup.
func (s *changesServer) release() {
	s.mu.Lock()
	profile, threads := s.profileGate, s.threadsGate
	s.profileGate, s.threadsGate = nil, nil
	s.mu.Unlock()
	closeGate(profile)
	closeGate(threads)
}

func closeGate(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case <-ch:
	default:
		close(ch)
	}
}

func (s *changesServer) setHistoryID(id string) {
	s.mu.Lock()
	s.historyID = id
	s.mu.Unlock()
}

// count returns how many requests one endpoint received.
func (s *changesServer) count(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch endpoint {
	case "profile":
		return s.profileCalls
	case "labels":
		return s.labelCalls
	case "threads":
		return s.listCalls
	}
	return -1
}

func (s *changesServer) profile(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.profileCalls++
		s.profileFields = r.URL.Query().Get("fields")
		gate, started := s.profileGate, s.profileStarted
		id, omit := s.historyID, s.omitHistoryID
		s.mu.Unlock()
		signal(started)
		if gate != nil {
			<-gate
		}
		if omit {
			writeJSON(t, w, `{"emailAddress":"user@example.com"}`)
			return
		}
		writeJSON(t, w, `{"historyId":"`+id+`"}`)
	}
}

func (s *changesServer) labels(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.labelCalls++
		s.mu.Unlock()
		writeJSON(t, w, folderListBody)
	}
}

func (s *changesServer) threads(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.listCalls++
		gate, started := s.threadsGate, s.threadsStarted
		s.mu.Unlock()
		signal(started)
		if gate != nil {
			<-gate
		}
		writeJSON(t, w, `{"threads":[{"id":"t1"}],"resultSizeEstimate":1}`)
	}
}

// signal reports to a one-slot channel without ever blocking the handler.
func signal(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// within runs fn and fails the test when it takes longer than d.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v", what, d)
	}
}

func TestCheckChangesRecordsBaselineAndDropsCache(t *testing.T) {
	svc, srv := newChangesService(t)

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if got := srv.count("labels"); got != 1 {
		t.Fatalf("labels.list calls = %d, want 1", got)
	}

	changed, err := svc.CheckChanges(context.Background())
	if err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if changed {
		t.Fatal("the first check reported a change instead of recording a baseline")
	}
	if got := srv.count("profile"); got != 1 {
		t.Fatalf("users.getProfile calls = %d, want 1", got)
	}
	if got := srv.profileFields; got != "historyId" {
		t.Fatalf("users.getProfile fields = %q, want historyId", got)
	}

	// The baseline drops whatever was cached before it: an entry fetched
	// without one could describe a state older than the baseline.
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders after the baseline: %v", err)
	}
	if got := srv.count("labels"); got != 2 {
		t.Fatalf("labels.list calls after the baseline = %d, want 2", got)
	}
}

func TestCheckChangesKeepsTheCacheWhenNothingMoved(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	changed, err := svc.CheckChanges(context.Background())
	if err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if changed {
		t.Fatal("a check reported a change on an unchanged mailbox")
	}
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if got := srv.count("labels"); got != 1 {
		t.Fatalf("labels.list calls = %d, want the cached 1", got)
	}
}

func TestCheckChangesDropsTheCacheWhenTheMailboxMoved(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	srv.setHistoryID("200")
	changed, err := svc.CheckChanges(context.Background())
	if err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if !changed {
		t.Fatal("the check missed a moved history id")
	}
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if got := srv.count("labels"); got != 2 {
		t.Fatalf("labels.list calls after the change = %d, want 2", got)
	}
}

// TestCheckChangesDetectsAnOlderHistoryID pins the comparison to inequality
// rather than ordering: a mailbox migrated between accounts can report a lower
// id and the change still has to be seen.
func TestCheckChangesDetectsAnOlderHistoryID(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	srv.setHistoryID("050")
	changed, err := svc.CheckChanges(context.Background())
	if err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if !changed {
		t.Fatal("a lower history id was not reported as a change")
	}
}

func TestCheckChangesRejectsAResponseWithoutHistoryID(t *testing.T) {
	svc, srv := newChangesService(t)
	srv.mu.Lock()
	srv.omitHistoryID = true
	srv.mu.Unlock()

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if _, err := svc.CheckChanges(context.Background()); err == nil {
		t.Fatal("a profile without historyId was accepted as a check")
	}
	// A failed check must not drop the cache: it learned nothing.
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if got := srv.count("labels"); got != 1 {
		t.Fatalf("labels.list calls = %d, want the cached 1", got)
	}
}

// TestCachedReadIsNotBlockedByCheck covers the contract's fast path: a cache hit
// answers while a check is still waiting for the network.
func TestCachedReadIsNotBlockedByCheck(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}

	gate, started := make(chan struct{}), make(chan struct{}, 1)
	srv.mu.Lock()
	srv.profileGate, srv.profileStarted = gate, started
	srv.mu.Unlock()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		if _, err := svc.CheckChanges(context.Background()); err != nil {
			t.Errorf("CheckChanges: %v", err)
		}
	}()
	<-started

	within(t, 2*time.Second, "a cached read during a check", func() {
		if _, err := svc.Folders(context.Background()); err != nil {
			t.Errorf("Folders: %v", err)
		}
	})
	close(gate)
	<-checked
}

// TestUncachedReadWaitsForCheck covers the other half of the contract: a read
// that needs the network waits for the check instead of fetching data the check
// may be about to drop.
func TestUncachedReadWaitsForCheck(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	gate, started := make(chan struct{}), make(chan struct{}, 1)
	srv.mu.Lock()
	srv.profileGate, srv.profileStarted = gate, started
	srv.mu.Unlock()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		if _, err := svc.CheckChanges(context.Background()); err != nil {
			t.Errorf("CheckChanges: %v", err)
		}
	}()
	<-started

	read := make(chan error, 1)
	go func() {
		_, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
		read <- err
	}()
	select {
	case err := <-read:
		t.Fatalf("a cache miss returned while a check was in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(gate)
	select {
	case err := <-read:
		if err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read never resumed after the check finished")
	}
	<-checked
	if got := srv.count("threads"); got != 1 {
		t.Fatalf("threads.list calls = %d, want 1", got)
	}
}

// TestUncachedReadLeavesWhenItsContextEnds keeps the wait cancellable: a closed
// window or a superseded request must not queue behind a check.
func TestUncachedReadLeavesWhenItsContextEnds(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	gate, started := make(chan struct{}), make(chan struct{}, 1)
	srv.mu.Lock()
	srv.profileGate, srv.profileStarted = gate, started
	srv.mu.Unlock()
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		if _, err := svc.CheckChanges(context.Background()); err != nil {
			t.Errorf("CheckChanges: %v", err)
		}
	}()
	<-started
	defer func() {
		close(gate)
		<-checked
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.ListThreads(ctx, mail.ListRequest{FolderID: "inbox"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListThreads err = %v, want context.Canceled", err)
	}
	if got := srv.count("threads"); got != 0 {
		t.Fatalf("threads.list calls = %d after a cancelled wait, want 0", got)
	}
}

// TestInvalidateDuringAReadDropsItsPage is the epoch guard end to end: a page
// whose request started before a clear must not land in the cache afterwards.
// Invalidate stands in for CheckChanges here because the account has a single
// request slot, so a check cannot run while a read is still on the wire; the
// read's last leg (after its slot is released) is what the guard protects.
func TestInvalidateDuringAReadDropsItsPage(t *testing.T) {
	svc, srv := newChangesService(t)

	gate, started := make(chan struct{}), make(chan struct{}, 1)
	srv.mu.Lock()
	srv.threadsGate, srv.threadsStarted = gate, started
	srv.mu.Unlock()

	slow := make(chan error, 1)
	go func() {
		_, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
		slow <- err
	}()
	<-started

	svc.Invalidate()
	close(gate)
	if err := <-slow; err != nil {
		t.Fatalf("the slow ListThreads: %v", err)
	}

	// The stale page was dropped, so the next read has to go to the network.
	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if got := srv.count("threads"); got != 2 {
		t.Fatalf("threads.list calls = %d, want 2: the cleared cache kept the stale page", got)
	}
}
