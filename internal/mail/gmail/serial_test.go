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
		defer atomic.AddInt32(&f.cur, -1)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		next(w, r)
	}
}

func (f *inflightBatches) max() int32 { return atomic.LoadInt32(&f.peak) }

// TestBatchesOfOneAccountNeverOverlap runs the two loads that reloadAll starts
// together and asserts the account never has both batches in flight: that
// overlap is what tripped Gmail's per-user concurrency limit.
func TestBatchesOfOneAccountNeverOverlap(t *testing.T) {
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
		folders, errs[0] = svc.Folders(context.Background())
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
	if peak := count.max(); peak != 1 {
		t.Fatalf("batch requests in flight peaked at %d, want 1", peak)
	}
}

// TestReqSlotWaitHonoursContext covers a load that is superseded while it
// waits for the account's request slot: it must stop at once instead of waiting
// for the request ahead of it.
func TestReqSlotWaitHonoursContext(t *testing.T) {
	svc, _ := newTestService(t, http.NewServeMux())
	c := svc.c

	if err := c.acquireReq(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer c.releaseReq()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.acquireReq(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting acquire returned %v, want context.DeadlineExceeded", err)
	}
}
