package gmail

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// historyServer serves the change check and the list it guards. changed lists
// the conversations users.history.list reports; status overrides its answer and
// pages makes it keep returning a nextPageToken.
type historyServer struct {
	mu        sync.Mutex
	historyID string
	changed   []string
	status    int
	pages     int
	ids       []string
	batch     *threadBatchServer
	calls     []string
}

func newHistoryService(t *testing.T, ids ...string) (*Service, *historyServer) {
	t.Helper()
	s := &historyServer{
		historyID: "100",
		ids:       ids,
		batch:     &threadBatchServer{threads: map[string]string{}},
	}
	for _, id := range ids {
		s.batch.threads[id] = rowThread(t, id, 1)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		id := s.historyID
		s.mu.Unlock()
		writeJSON(t, w, `{"historyId":"`+id+`"}`)
	})
	mux.HandleFunc("/users/me/history", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, r.URL.Query().Get("startHistoryId"))
		status, id, changed, pages := s.status, s.historyID, append([]string(nil), s.changed...), s.pages
		if pages > 0 {
			s.pages--
		}
		s.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			writeErrorJSON(t, w, status,
				`{"error":{"code":`+strconv.Itoa(status)+`,"message":"Invalid request."}}`)
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
		body.WriteString(`]`)
		if pages > 0 {
			body.WriteString(`,"nextPageToken":"p` + strconv.Itoa(pages) + `"`)
		}
		body.WriteString(`}`)
		writeJSON(t, w, body.String())
	})
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		var body strings.Builder
		body.WriteString(`{"threads":[`)
		for i, id := range s.ids {
			if i > 0 {
				body.WriteString(",")
			}
			body.WriteString(`{"id":"` + id + `"}`)
		}
		body.WriteString(`],"resultSizeEstimate":` + strconv.Itoa(len(s.ids)) + `}`)
		writeJSON(t, w, body.String())
	})
	mux.HandleFunc("/batch", s.batch.handler(t))
	svc, _ := newTestService(t, mux)
	return svc, s
}

func (s *historyServer) set(changed []string, historyID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changed, s.historyID = changed, historyID
}

func (s *historyServer) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *historyServer) historyCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// TestHistoryInvalidatesOnlyChangedRows is the whole point of the history call:
// one moved conversation costs one threads.get, not one per row on the page.
func TestHistoryInvalidatesOnlyChangedRows(t *testing.T) {
	svc, srv := newHistoryService(t, "t1", "t2")
	req := mail.ListRequest{FolderID: "inbox"}

	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if got := len(srv.batch.calls()); got != 1 {
		t.Fatalf("threads.get batches = %d, want 1", got)
	}

	srv.set([]string{"t1"}, "200")
	if changed, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	} else if !changed {
		t.Fatal("the check missed the change")
	}

	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads after the change: %v", err)
	}
	calls := srv.batch.calls()
	if len(calls) != 2 {
		t.Fatalf("threads.get batches = %d, want 2", len(calls))
	}
	if len(calls[1]) != 1 || calls[1][0] != "t1" {
		t.Fatalf("second batch = %v, want only t1", calls[1])
	}
	if got := srv.historyCalls(); len(got) != 1 || got[0] != "100" {
		t.Fatalf("history calls = %v, want one from 100", got)
	}
}

// TestHistoryExpiredDropsEverything covers a baseline the API no longer knows:
// the cache is dropped rather than trusted.
func TestHistoryExpiredDropsEverything(t *testing.T) {
	svc, srv := newHistoryService(t, "t1", "t2")
	req := mail.ListRequest{FolderID: "inbox"}

	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	srv.mu.Lock()
	srv.historyID, srv.status = "200", http.StatusNotFound
	srv.mu.Unlock()
	if changed, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	} else if !changed {
		t.Fatal("an expired history was not reported as a change")
	}

	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads after the reset: %v", err)
	}
	calls := srv.batch.calls()
	if len(calls) != 2 || len(calls[1]) != 2 {
		t.Fatalf("batches = %v, want a full refetch of both rows", calls)
	}
}

// TestHistoryErrorKeepsTheCacheAndTheBaseline covers a failed history read: the
// check reports it and leaves both the cache and the baseline alone, so the
// next check covers the same range again.
func TestHistoryErrorKeepsTheCacheAndTheBaseline(t *testing.T) {
	svc, srv := newHistoryService(t, "t1")
	req := mail.ListRequest{FolderID: "inbox"}

	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	srv.mu.Lock()
	srv.historyID, srv.status = "200", http.StatusBadRequest
	srv.mu.Unlock()
	if _, err := svc.CheckChanges(context.Background()); err == nil {
		t.Fatal("a failed history read was reported as a completed check")
	}
	if got := len(srv.batch.calls()); got != 1 {
		t.Fatalf("threads.get batches = %d after a failed check, want 1", got)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads after the failed check: %v", err)
	}
	if got := len(srv.batch.calls()); got != 1 {
		t.Fatalf("threads.get batches = %d, want the cache kept", got)
	}

	// The baseline did not move, so the next check asks from the same place.
	srv.setStatus(0)
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges retry: %v", err)
	}
	calls := srv.historyCalls()
	if len(calls) != 2 || calls[1] != "100" {
		t.Fatalf("history calls = %v, want the retry to start from 100 again", calls)
	}
}

// TestHistoryTooLongDropsEverything bounds the walk: a range wider than the page
// limit is cheaper to re-read than to page through.
func TestHistoryTooLongDropsEverything(t *testing.T) {
	svc, srv := newHistoryService(t, "t1", "t2")
	req := mail.ListRequest{FolderID: "inbox"}

	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	srv.mu.Lock()
	srv.historyID, srv.changed, srv.pages = "200", []string{"t1"}, 100
	srv.mu.Unlock()
	if _, err := svc.CheckChanges(context.Background()); err != nil {
		t.Fatalf("CheckChanges: %v", err)
	}
	if got := len(srv.historyCalls()); got != historyPageLimit {
		t.Fatalf("history calls = %d, want the %d page limit", got, historyPageLimit)
	}

	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads after the reset: %v", err)
	}
	if got := len(srv.batch.calls()); got != 2 {
		t.Fatalf("threads.get batches = %d, want a full refetch", got)
	}
}
