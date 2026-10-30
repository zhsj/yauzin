package gmail

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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

	// historyStatus overrides the answer of users.history.list; 0 means 200.
	historyStatus int
	// changed lists the conversation IDs the history reports, empty when the
	// check should find nothing specific.
	changed []string

	profileCalls, labelCalls, listCalls, historyCalls int
	threadCalls                                       int

	profileGate    chan struct{}
	profileStarted chan struct{}

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
	mux.HandleFunc("/users/me/history", s.history(t))
	mux.HandleFunc("/users/me/labels", s.labels(t))
	mux.HandleFunc("/users/me/threads", s.threads(t))
	mux.HandleFunc("/users/me/threads/", s.thread(t))
	mux.HandleFunc("/batch", s.batch.handler(t))
	svc, _ := newTestService(t, mux)
	t.Cleanup(s.release)
	return svc, s
}

// release unblocks the profile gate, so a failing test cannot leave the test
// server hanging during cleanup.
func (s *changesServer) release() {
	s.mu.Lock()
	profile := s.profileGate
	s.profileGate = nil
	s.mu.Unlock()
	closeGate(profile)
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
	case "history":
		return s.historyCalls
	case "thread":
		return s.threadCalls
	}
	return -1
}

// history answers users.history.list with the conversations the test named and
// the current history id, which is what the check turns into the new baseline.
func (s *changesServer) history(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.historyCalls++
		status, id := s.historyStatus, s.historyID
		changed := append([]string(nil), s.changed...)
		s.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			writeErrorJSON(t, w, status,
				`{"error":{"code":`+strconv.Itoa(status)+`,"message":"Requested entity was not found."}}`)
			return
		}
		var body strings.Builder
		body.WriteString(`{"historyId":"` + id + `","history":[`)
		for i, threadID := range changed {
			if i > 0 {
				body.WriteString(",")
			}
			body.WriteString(`{"id":"` + id + `","messages":[{"threadId":"` + threadID + `"}]}`)
		}
		body.WriteString(`]}`)
		writeJSON(t, w, body.String())
	}
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
		s.mu.Unlock()
		writeJSON(t, w, `{"threads":[{"id":"t1"}],"resultSizeEstimate":1}`)
	}
}

// thread answers one threads.get, which is what opening a conversation costs.
func (s *changesServer) thread(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.threadCalls++
		s.mu.Unlock()
		writeJSON(t, w, t1Body)
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

// TestUncachedThreadWaitsForCheck covers the same contract for the reader: a
// conversation the cache does not hold must not pay for a body the check is
// about to drop.
func TestUncachedThreadWaitsForCheck(t *testing.T) {
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
		_, err := svc.GetThread(context.Background(), "t1")
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
			t.Fatalf("GetThread: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the read never resumed after the check finished")
	}
	<-checked
	if got := srv.count("thread"); got != 1 {
		t.Fatalf("threads.get calls = %d, want 1", got)
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

// TestHistoryChangeDropsTheListAndSearchCaches covers the two page caches: a
// moved mailbox makes the next browse and the next search fetch again, while
// repeated reads of an unchanged one stay cached.
func TestHistoryChangeDropsTheListAndSearchCaches(t *testing.T) {
	svc, srv := newChangesService(t)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	list := mail.ListRequest{FolderID: "inbox"}
	search := mail.SearchRequest{Query: "invoice"}
	for i := 0; i < 2; i++ {
		if _, err := svc.ListThreads(context.Background(), list); err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
		if _, err := svc.SearchThreads(context.Background(), search); err != nil {
			t.Fatalf("SearchThreads: %v", err)
		}
	}
	if got := srv.count("threads"); got != 2 {
		t.Fatalf("threads.list calls = %d, want one per view", got)
	}

	srv.setHistoryID("200")
	if changed, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	} else if !changed {
		t.Fatal("the check missed the change")
	}
	if _, err := svc.ListThreads(context.Background(), list); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if _, err := svc.SearchThreads(context.Background(), search); err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if got := srv.count("threads"); got != 4 {
		t.Fatalf("threads.list calls after the change = %d, want 4", got)
	}
}

// TestCheckChangesLogsAHistoryChange pins the observable side of a change: a
// quiet check logs nothing, a moved one names both ids, and the first call only
// records a baseline.
func TestCheckChangesLogsAHistoryChange(t *testing.T) {
	svc, srv := newChangesService(t)
	buf := captureLogs(t, slog.LevelDebug)

	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "gmail history baseline") {
		t.Fatalf("baseline log missing: %q", got)
	}
	if strings.Contains(buf.String(), "gmail mailbox changed") {
		t.Fatal("the baseline call reported a change")
	}

	buf.Reset()
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if strings.Contains(buf.String(), "gmail mailbox changed") {
		t.Fatal("an unchanged mailbox logged a change")
	}

	buf.Reset()
	srv.setHistoryID("200")
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "gmail mailbox changed") {
		t.Fatalf("change log missing: %q", got)
	}
	if !strings.Contains(got, "from=100") || !strings.Contains(got, "to=200") {
		t.Fatalf("change log does not name both ids: %q", got)
	}
}
