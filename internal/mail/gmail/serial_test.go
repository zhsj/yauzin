package gmail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// inflightBatches counts the batch requests a test server is handling. Two
// sequential batches pass through it one after the other; a peak above one
// means the account sent them at the same time.
type inflightBatches struct {
	cur, peak int32
	delay     time.Duration
	// started is closed when the first wrapped request arrives, which lets a
	// test start a second load while the first one still holds its slot.
	started     chan struct{}
	startedOnce sync.Once
}

func (f *inflightBatches) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&f.cur, 1)
		for {
			high := atomic.LoadInt32(&f.peak)
			if n <= high || atomic.CompareAndSwapInt32(&f.peak, high, n) {
				break
			}
		}
		if f.started != nil {
			f.startedOnce.Do(func() { close(f.started) })
		}
		defer atomic.AddInt32(&f.cur, -1)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		next(w, r)
	}
}

func (f *inflightBatches) max() int32 { return atomic.LoadInt32(&f.peak) }

// TestBatchesOfOneAccountStayWithinTheLimit runs the two loads that reloadAll
// starts together and asserts the account never has more requests in flight
// than maxConcurrent: the sidebar load (the label list plus its counters) and
// the page load each send a batch, and an unbounded fan-out is what trips
// Gmail's per-user concurrency limit.
func TestBatchesOfOneAccountStayWithinTheLimit(t *testing.T) {
	const labels = 120
	const threads = 120

	var labelList strings.Builder
	labelList.WriteString(`{"labels":[`)
	for i := 0; i < labels; i++ {
		if i > 0 {
			labelList.WriteString(",")
		}
		fmt.Fprintf(&labelList, `{"id":"Label_%d","name":"L%d","type":"user"}`, i, i)
	}
	labelList.WriteString(`]}`)

	var threadList strings.Builder
	threadList.WriteString(`{"threads":[`)
	metadata := make(map[string]string, threads)
	for i := 0; i < threads; i++ {
		id := fmt.Sprintf("t%d", i)
		if i > 0 {
			threadList.WriteString(",")
		}
		fmt.Fprintf(&threadList, `{"id":%q}`, id)
		metadata[id] = fmt.Sprintf(`{"id":%q,"messages":[]}`, id)
	}
	threadList.WriteString(`]}`)

	count := &inflightBatches{delay: 5 * time.Millisecond}
	labelBatch := count.wrap((&batchServer{}).handler(t))
	threadBatch := count.wrap((&threadBatchServer{threads: metadata}).handler(t))

	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, labelList.String())
	})
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, threadList.String())
	})
	mux.HandleFunc("/batch", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read batch request: %v", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if bytes.Contains(raw, []byte("/users/me/threads/")) {
			threadBatch(w, r)
			return
		}
		labelBatch(w, r)
	})

	svc, _ := newTestService(t, mux)
	var (
		wg      sync.WaitGroup
		folders []mail.Folder
		page    mail.ThreadPage
		errs    [2]error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		// The counters are a second request of the same load, and the batch it
		// sends is what makes the assertion meaningful: without it the sidebar
		// side never sends a batch at all.
		folders, errs[0] = svc.Folders(context.Background())
		if errs[0] != nil {
			return
		}
		folders, errs[0] = svc.FolderDetails(context.Background(), folders)
	}()
	go func() {
		defer wg.Done()
		page, errs[1] = svc.ListThreads(context.Background(), mail.ListRequest{
			FolderID: "inbox",
			Limit:    threads,
		})
	}()
	wg.Wait()

	if err := errors.Join(errs[0], errs[1]); err != nil {
		t.Fatalf("loads: %v", err)
	}
	if len(folders) != labels {
		t.Fatalf("folders = %d, want %d", len(folders), labels)
	}
	if len(page.Threads) != threads {
		t.Fatalf("threads = %d, want %d", len(page.Threads), threads)
	}
	if peak := count.max(); peak > int32(maxConcurrent) {
		t.Fatalf("batch requests in flight peaked at %d, want at most %d", peak, maxConcurrent)
	}
}

// TestReqSlotWaitHonoursContext covers a load that is superseded while it
// waits for the account's request slots: it must stop at once instead of
// waiting for the requests ahead of it.
func TestReqSlotWaitHonoursContext(t *testing.T) {
	svc, _ := newTestService(t, http.NewServeMux())
	c := svc.c

	for range maxConcurrent {
		if err := c.acquireReq(context.Background()); err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer c.releaseReq()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.acquireReq(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting acquire returned %v, want context.DeadlineExceeded", err)
	}
}

// twoLoadsBatchDelay keeps a batch in flight long enough for the second load of
// overlap to reach the server while the first one still holds its slot.
const twoLoadsBatchDelay = 30 * time.Millisecond

// twoLoadsServer answers the two loads reloadAll starts: the sidebar (folders
// plus their counters) and one page of a folder. Each of them sends one batch,
// which is what the request slot meters.
type twoLoadsServer struct {
	count *inflightBatches
	mux   *http.ServeMux
}

func newTwoLoadsServer(t *testing.T) *twoLoadsServer {
	t.Helper()
	s := &twoLoadsServer{count: &inflightBatches{delay: twoLoadsBatchDelay, started: make(chan struct{})}}
	labelBatch := s.count.wrap((&batchServer{}).handler(t))
	threadBatch := s.count.wrap((&threadBatchServer{threads: map[string]string{"t1": t1Body}}).handler(t))

	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"labels":[{"id":"INBOX","name":"INBOX","type":"system"}]}`)
	})
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}],"resultSizeEstimate":1}`)
	})
	mux.HandleFunc("/batch", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read batch request: %v", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if bytes.Contains(raw, []byte("/users/me/threads/")) {
			threadBatch(w, r)
			return
		}
		labelBatch(w, r)
	})
	s.mux = mux
	return s
}

// sidebar loads the folders and then their counters, which is one batch.
func (s *twoLoadsServer) sidebar(svc *Service) error {
	folders, err := svc.Folders(context.Background())
	if err != nil {
		return err
	}
	_, err = svc.FolderDetails(context.Background(), folders)
	return err
}

// page loads one page of the inbox, which is one batch as well.
func (s *twoLoadsServer) page(svc *Service) error {
	_, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox", Limit: 10})
	return err
}

// overlap starts the page load while the sidebar batch is in flight and returns
// the peak number of batch requests the server saw at once.
func (s *twoLoadsServer) overlap(t *testing.T, svc *Service) int32 {
	t.Helper()
	sidebarErr := make(chan error, 1)
	go func() { sidebarErr <- s.sidebar(svc) }()
	<-s.count.started
	if err := s.page(svc); err != nil {
		t.Errorf("page load: %v", err)
	}
	if err := <-sidebarErr; err != nil {
		t.Errorf("sidebar load: %v", err)
	}
	return s.count.max()
}

// TestMaxConcurrentOneSerializesTheTwoLoads pins the fallback the design notes
// promise: one request slot makes the sidebar batch and the page batch queue
// behind each other again.
func TestMaxConcurrentOneSerializesTheTwoLoads(t *testing.T) {
	s := newTwoLoadsServer(t)
	svc, _ := newTestService(t, s.mux, WithMaxConcurrent(1))
	if peak := s.overlap(t, svc); peak != 1 {
		t.Fatalf("batch requests in flight peaked at %d with one slot, want 1", peak)
	}
}

// TestDefaultConcurrencyOverlapsTheTwoLoads is the other half of the contract:
// the account's default slot count lets the page batch run while the sidebar
// batch is still in flight.
func TestDefaultConcurrencyOverlapsTheTwoLoads(t *testing.T) {
	s := newTwoLoadsServer(t)
	svc, _ := newTestService(t, s.mux)
	if peak := s.overlap(t, svc); peak != 2 {
		t.Fatalf("batch requests in flight peaked at %d, want 2", peak)
	}
}
