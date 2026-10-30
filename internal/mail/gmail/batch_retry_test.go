package gmail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// rateLimitedBody is the answer Gmail gives a call that outran its per-minute
// quota. The reason is what apiErrorFor reads to classify it.
const rateLimitedBody = `{"error":{"code":403,"status":"RESOURCE_EXHAUSTED",` +
	`"message":"Quota exceeded for quota metric 'Total Query Cost'.",` +
	`"errors":[{"reason":"rateLimitExceeded"}]}}`

// flakyBatchServer answers the batch endpoint like threadBatchServer, but a
// thread listed in refusals answers 403 rateLimitExceeded that many times
// before it starts answering its body, so a retry round is visible.
type flakyBatchServer struct {
	mu       sync.Mutex
	batches  [][]string // thread IDs per batch request, in request order
	threads  map[string]string
	refusals map[string]int
}

func (b *flakyBatchServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" {
			t.Errorf("batch path = %q", r.URL.Path)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("batch request: %v", err)
			return
		}
		var ids []string
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("batch request part: %v", err)
				return
			}
			raw, _ := io.ReadAll(p)
			line, _, _ := strings.Cut(string(raw), "\r\n")
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[0] != http.MethodGet {
				t.Errorf("batch request line = %q", line)
				continue
			}
			target, err := url.Parse(fields[1])
			if err != nil {
				t.Errorf("batch request target %q: %v", fields[1], err)
				continue
			}
			ids = append(ids, path.Base(target.Path))
		}

		b.mu.Lock()
		b.batches = append(b.batches, ids)
		refused := make([]bool, len(ids))
		for i, id := range ids {
			if b.refusals[id] > 0 {
				b.refusals[id]--
				refused[i] = true
			}
		}
		b.mu.Unlock()

		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		for i, id := range ids {
			status, body := http.StatusOK, b.threads[id]
			if refused[i] {
				status, body = http.StatusForbidden, rateLimitedBody
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", "application/http")
			header.Set("Content-ID", fmt.Sprintf("<response-call-%d>", i))
			part, err := mw.CreatePart(header)
			if err != nil {
				t.Errorf("batch response part: %v", err)
				return
			}
			_, _ = fmt.Fprintf(part, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\n\r\n%s\n",
				status, http.StatusText(status), body)
		}
		if err := mw.Close(); err != nil {
			t.Errorf("batch response: %v", err)
		}
	}
}

func (b *flakyBatchServer) calls() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]string(nil), b.batches...)
}

// newFlakyThreadService serves list as threads.list and batch as the batch
// endpoint of s.
func newFlakyThreadService(t *testing.T, s *flakyBatchServer, list http.HandlerFunc) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", list)
	mux.HandleFunc("/batch", s.handler(t))
	svc, _ := newTestService(t, mux)
	return svc
}

// TestListThreadsRetriesRateLimitedPart pins the recovery the page needs: one
// rate limited row is asked for again on its own, and the rows that already
// answered are neither lost nor paid for twice.
func TestListThreadsRetriesRateLimitedPart(t *testing.T) {
	batch := &flakyBatchServer{
		threads:  map[string]string{"t1": t1Body, "t2": t2Body},
		refusals: map[string]int{"t2": 1},
	}
	svc := newFlakyThreadService(t, batch, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"t2"}]}`)
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(page.Threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(page.Threads))
	}
	calls := batch.calls()
	if len(calls) != 2 {
		t.Fatalf("batch requests = %v, want two: the first and the retry", calls)
	}
	if got := strings.Join(calls[0], ","); got != "t1,t2" {
		t.Fatalf("first batch = %q, want t1,t2", got)
	}
	if got := strings.Join(calls[1], ","); got != "t2" {
		t.Fatalf("retry batch = %q, want only the rate limited t2", got)
	}
}

// TestListThreadsReportsRateLimitedPart checks the retries are bounded and that
// the error keeps its category: the UI has to tell a rate limit from a broken
// backend, which the previous bare error did not.
func TestListThreadsReportsRateLimitedPart(t *testing.T) {
	batch := &flakyBatchServer{
		threads:  map[string]string{"t1": t1Body, "t2": t2Body},
		refusals: map[string]int{"t2": retryAttempts + 1},
	}
	svc := newFlakyThreadService(t, batch, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"t2"}]}`)
	})

	_, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if !errors.Is(err, mail.ErrRateLimited) {
		t.Fatalf("ListThreads error = %v, want ErrRateLimited", err)
	}
	if calls := batch.calls(); len(calls) != retryAttempts+1 {
		t.Fatalf("batch requests = %d, want %d", len(calls), retryAttempts+1)
	}
}
