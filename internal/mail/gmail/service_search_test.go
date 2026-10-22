package gmail

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestSearchThreadsQueriesEveryFolder pins the search request: the query goes
// to threads.list as written, without a label filter, and the page is built the
// same way a folder page is.
func TestSearchThreadsQueriesEveryFolder(t *testing.T) {
	const query = "from:ada has:attachment"
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body, "t2": t2Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		q := r.URL.Query()
		if got := q.Get("q"); got != query {
			t.Errorf("q = %q, want %q", got, query)
		}
		if got := q.Get("labelIds"); got != "" {
			t.Errorf("labelIds = %q, want none: a search spans every folder", got)
		}
		if got := q.Get("maxResults"); got != "25" {
			t.Errorf("maxResults = %q, want 25", got)
		}
		if got := q.Get("pageToken"); got != "prev" {
			t.Errorf("pageToken = %q, want prev", got)
		}
		if got := q.Get("fields"); got == "" {
			t.Error("fields is empty, want the trimmed list fields")
		}
		writeJSON(t, w, listBody)
	})

	page, err := svc.SearchThreads(context.Background(), mail.SearchRequest{
		Query:     query,
		PageToken: "prev",
		Limit:     25,
	})
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("threads.list calls = %d, want 1", listCalls)
	}
	if len(page.Threads) != 2 || page.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want t1 then t2", page.Threads)
	}
	if page.Total != 42 || page.NextPageToken != "tok2" {
		t.Fatalf("page = total %d, token %q; want 42 and tok2", page.Total, page.NextPageToken)
	}
	if got := batch.calls(); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("metadata batches = %v, want one batch of two threads", got)
	}
}

// TestSearchThreadsRejectsAnEmptyQuery keeps the empty-string case out of the
// API: it is a caller bug, not a request for all mail.
func TestSearchThreadsRejectsAnEmptyQuery(t *testing.T) {
	var reached bool
	svc := newThreadService(t, &threadBatchServer{}, func(w http.ResponseWriter, r *http.Request) {
		reached = true
		writeJSON(t, w, `{"threads":[]}`)
	})

	for _, query := range []string{"", "   "} {
		if _, err := svc.SearchThreads(context.Background(), mail.SearchRequest{Query: query}); !errors.Is(err, mail.ErrInvalidArgument) {
			t.Fatalf("query %q returned %v, want ErrInvalidArgument", query, err)
		}
	}
	if reached {
		t.Fatal("an empty query still reached the API")
	}
}

// TestListThreadsRequiresAFolder covers the other half of the contract: an
// empty folder is a caller bug too, not a request for the inbox.
func TestListThreadsRequiresAFolder(t *testing.T) {
	var reached bool
	svc := newThreadService(t, &threadBatchServer{}, func(w http.ResponseWriter, r *http.Request) {
		reached = true
		writeJSON(t, w, `{"threads":[]}`)
	})

	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{}); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	if reached {
		t.Fatal("an empty folder still reached the API")
	}
}
