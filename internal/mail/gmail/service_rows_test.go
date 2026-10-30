package gmail

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// rowServer answers the three reads the list and the reader make, counting each
// one so a test can tell which path was taken.
type rowServer struct {
	mu          sync.Mutex
	listCalls   int
	threadReads []string
	msgReads    []string
	listBody    string
	threads     map[string]string
	messages    map[string]string
}

func newRowServer(listBody string, threads map[string]string) *rowServer {
	return &rowServer{listBody: listBody, threads: threads, messages: map[string]string{}}
}

func (s *rowServer) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.listCalls++
		s.mu.Unlock()
		writeJSON(t, w, s.listBody)
	})
	mux.HandleFunc("/users/me/threads/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/users/me/threads/")
		s.mu.Lock()
		s.threadReads = append(s.threadReads, id)
		body := s.threads[id]
		s.mu.Unlock()
		if body == "" {
			writeErrorJSON(t, w, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			return
		}
		writeJSON(t, w, body)
	})
	mux.HandleFunc("/users/me/messages/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/users/me/messages/")
		s.mu.Lock()
		s.msgReads = append(s.msgReads, id)
		body := s.messages[id]
		s.mu.Unlock()
		if body == "" {
			writeErrorJSON(t, w, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
			return
		}
		writeJSON(t, w, body)
	})
	return mux
}

func (s *rowServer) counts() (lists int, threadReads, msgReads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listCalls, len(s.threadReads), len(s.msgReads)
}

const singleMessageBody = `{"id":"m1","labelIds":["INBOX"],"snippet":"hi","internalDate":"1700000000000",
	"payload":{"mimeType":"text/plain",
	"headers":[{"name":"Subject","value":"Hello"},{"name":"From","value":"Alice <alice@example.com>"}],
	"body":{"data":"aGk"}}}`

func rowThread(t *testing.T, id string, messages int) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(`{"id":"` + id + `","messages":[`)
	for i := 0; i < messages; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"` + id + `-m` + string(rune('0'+i)) + `","labelIds":["INBOX"],"snippet":"s",` +
			`"internalDate":"1700000000000","payload":{"headers":[{"name":"Subject","value":"S"}]}}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// TestRowsSurviveAPageReload pins the whole point of the row cache: when only
// the page order is gone, re-listing the page costs threads.list and nothing
// else. The old code paid 25 threads.get for exactly this.
func TestRowsSurviveAPageReload(t *testing.T) {
	srv := newRowServer(`{"threads":[{"id":"t1"},{"id":"t2"}]}`, map[string]string{
		"t1": rowThread(t, "t1", 1),
		"t2": rowThread(t, "t2", 2),
	})
	batch := &threadBatchServer{threads: srv.threads}
	mux := http.NewServeMux()
	mux.Handle("/", srv.handler(t))
	mux.HandleFunc("/batch", batch.handler(t))
	svc, _ := newTestService(t, mux)

	req := mail.ListRequest{FolderID: "inbox"}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if got := len(batch.calls()); got != 1 {
		t.Fatalf("threads.get batches = %d, want 1", got)
	}

	// A mailbox change drops the page order but keeps the rows.
	svc.cache.dropPages()

	page, err := svc.ListThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("ListThreads after change: %v", err)
	}
	if len(page.Threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(page.Threads))
	}
	lists, _, _ := srv.counts()
	if lists != 2 {
		t.Fatalf("threads.list calls = %d, want 2", lists)
	}
	if got := len(batch.calls()); got != 1 {
		t.Fatalf("threads.get batches = %d after the change, want still 1", got)
	}
}

// TestDeletedRowIsNotAskedAgain checks a conversation the API no longer has is
// remembered instead of being requested on every assembly.
func TestDeletedRowIsNotAskedAgain(t *testing.T) {
	srv := newRowServer(`{"threads":[{"id":"t1"},{"id":"gone"}]}`, map[string]string{
		"t1": rowThread(t, "t1", 1),
	})
	batch := &threadBatchServer{threads: srv.threads}
	mux := http.NewServeMux()
	mux.Handle("/", srv.handler(t))
	mux.HandleFunc("/batch", batch.handler(t))
	svc, _ := newTestService(t, mux)

	req := mail.ListRequest{FolderID: "inbox"}
	page, err := svc.ListThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want only t1", page.Threads)
	}
	calls := len(batch.calls())

	svc.cache.dropPages()
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads again: %v", err)
	}
	if got := len(batch.calls()); got != calls {
		t.Fatalf("threads.get batches = %d, want %d: the deleted row is remembered", got, calls)
	}
}

// TestGetThreadReadsASingleMessageDirectly pins the cheaper path for the common
// conversation of one message: messages.get costs half of threads.get.
func TestGetThreadReadsASingleMessageDirectly(t *testing.T) {
	srv := newRowServer(`{}`, nil)
	srv.messages["m1"] = singleMessageBody
	mux := http.NewServeMux()
	mux.Handle("/", srv.handler(t))
	svc, _ := newTestService(t, mux)
	svc.cache.rows.set("t1", mail.ThreadSummary{ID: "t1", MessageCount: 1, MessageIDs: []string{"m1"}})

	msgs, err := svc.GetThread(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" || msgs[0].Subject != "Hello" {
		t.Fatalf("messages = %+v, want one message m1", msgs)
	}
	_, threadReads, msgReads := srv.counts()
	if threadReads != 0 || msgReads != 1 {
		t.Fatalf("threads.get = %d, messages.get = %d; want 0 and 1", threadReads, msgReads)
	}

	// The second read is served from the body cache.
	if _, err := svc.GetThread(context.Background(), "t1"); err != nil {
		t.Fatalf("GetThread again: %v", err)
	}
	if _, _, msgReads := srv.counts(); msgReads != 1 {
		t.Fatalf("messages.get = %d after a cached read, want 1", msgReads)
	}
}

// TestGetThreadReadsAWholeThread checks a conversation of several messages
// still uses the thread call, which is cheaper than one call per message.
func TestGetThreadReadsAWholeThread(t *testing.T) {
	srv := newRowServer(`{}`, map[string]string{"t1": rowThread(t, "t1", 2)})
	mux := http.NewServeMux()
	mux.Handle("/", srv.handler(t))
	svc, _ := newTestService(t, mux)
	svc.cache.rows.set("t1", mail.ThreadSummary{ID: "t1", MessageCount: 2, MessageIDs: []string{"t1-m0", "t1-m1"}})

	msgs, err := svc.GetThread(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	_, threadReads, msgReads := srv.counts()
	if threadReads != 1 || msgReads != 0 {
		t.Fatalf("threads.get = %d, messages.get = %d; want 1 and 0", threadReads, msgReads)
	}
}

// TestGetThreadWithoutARowUsesTheThreadCall covers a conversation the list has
// not described yet: there is no message count to choose on.
func TestGetThreadWithoutARowUsesTheThreadCall(t *testing.T) {
	srv := newRowServer(`{}`, map[string]string{"t1": rowThread(t, "t1", 3)})
	mux := http.NewServeMux()
	mux.Handle("/", srv.handler(t))
	svc, _ := newTestService(t, mux)

	if _, err := svc.GetThread(context.Background(), "t1"); err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	_, threadReads, msgReads := srv.counts()
	if threadReads != 1 || msgReads != 0 {
		t.Fatalf("threads.get = %d, messages.get = %d; want 1 and 0", threadReads, msgReads)
	}
}
