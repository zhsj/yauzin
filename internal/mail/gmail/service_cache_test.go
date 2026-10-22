package gmail

import (
	"context"
	"net/http"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestFoldersAreCached(t *testing.T) {
	var listCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, folderListBody)
	})
	svc, _ := newTestService(t, mux)

	for i := 0; i < 3; i++ {
		if _, err := svc.Folders(context.Background()); err != nil {
			t.Fatalf("Folders: %v", err)
		}
	}
	if listCalls != 1 {
		t.Fatalf("labels.list calls = %d, want 1", listCalls)
	}
}

func TestFoldersReturnIsIsolated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, folderListBody)
	})
	svc, _ := newTestService(t, mux)

	first, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	first[0].Name = "MUTATED"

	second, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if second[0].Name != "INBOX" {
		t.Fatalf("cached folder name = %q, want INBOX", second[0].Name)
	}
}

func TestFolderDetailsAreCached(t *testing.T) {
	batch := &batchServer{details: folderDetails()}
	svc := newFolderService(t, batch, folderListBody)

	folders, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.FolderDetails(context.Background(), folders); err != nil {
			t.Fatalf("FolderDetails: %v", err)
		}
	}
	if got := batch.calls(); len(got) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(got))
	}
}

func TestListThreadsAreCached(t *testing.T) {
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body, "t2": t2Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, listBody)
	})

	req := mail.ListRequest{FolderID: "inbox"}
	for i := 0; i < 3; i++ {
		if _, err := svc.ListThreads(context.Background(), req); err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
	}
	if listCalls != 1 {
		t.Fatalf("threads.list calls = %d, want 1", listCalls)
	}
	if got := batch.calls(); len(got) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(got))
	}
}

func TestListThreadsReturnIsIsolated(t *testing.T) {
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	req := mail.ListRequest{FolderID: "inbox"}
	first, err := svc.ListThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	// Simulate the UI's optimistic update: mutating the returned summary in
	// place must not change the cached page.
	first.Threads[0].UnreadCount = 0
	first.Threads[0].Labels[0] = "MUTATED"

	second, err := svc.ListThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if second.Threads[0].UnreadCount != 1 {
		t.Fatalf("cached UnreadCount = %d, want 1", second.Threads[0].UnreadCount)
	}
	if len(second.Threads[0].Labels) == 0 || second.Threads[0].Labels[0] != "INBOX" {
		t.Fatalf("cached labels = %v, want INBOX first", second.Threads[0].Labels)
	}
}

func TestSearchThreadsAreCached(t *testing.T) {
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	req := mail.SearchRequest{Query: "invoice"}
	for i := 0; i < 3; i++ {
		if _, err := svc.SearchThreads(context.Background(), req); err != nil {
			t.Fatalf("SearchThreads: %v", err)
		}
	}
	if listCalls != 1 {
		t.Fatalf("threads.list calls = %d, want 1", listCalls)
	}
	if got := batch.calls(); len(got) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(got))
	}
}

// TestSearchAndListCachesAreSeparate keeps the two views from answering for one
// another: a cached search must not serve a folder page or the other way round.
func TestSearchAndListCachesAreSeparate(t *testing.T) {
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	for i := 0; i < 2; i++ {
		if _, err := svc.SearchThreads(context.Background(), mail.SearchRequest{Query: "invoice"}); err != nil {
			t.Fatalf("SearchThreads: %v", err)
		}
		if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
	}
	if listCalls != 2 {
		t.Fatalf("threads.list calls = %d, want one per view", listCalls)
	}
	if got := batch.calls(); len(got) != 2 {
		t.Fatalf("batch requests = %d, want one per view", len(got))
	}
}

func TestSearchThreadsReturnIsIsolated(t *testing.T) {
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	req := mail.SearchRequest{Query: "invoice"}
	first, err := svc.SearchThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	first.Threads[0].Labels[0] = "MUTATED"

	second, err := svc.SearchThreads(context.Background(), req)
	if err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if len(second.Threads[0].Labels) == 0 || second.Threads[0].Labels[0] != "INBOX" {
		t.Fatalf("cached labels = %v, want INBOX first", second.Threads[0].Labels)
	}
}

func TestInvalidateDropsCachedResponses(t *testing.T) {
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	req := mail.ListRequest{FolderID: "inbox"}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("threads.list calls before invalidate = %d, want 1", listCalls)
	}

	svc.Invalidate()
	if _, err := svc.ListThreads(context.Background(), req); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("threads.list calls after invalidate = %d, want 2", listCalls)
	}
}

func TestInvalidateDropsTheSearchCache(t *testing.T) {
	var listCalls int
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	req := mail.SearchRequest{Query: "invoice"}
	if _, err := svc.SearchThreads(context.Background(), req); err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if _, err := svc.SearchThreads(context.Background(), req); err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("threads.list calls before invalidate = %d, want 1", listCalls)
	}

	svc.Invalidate()
	if _, err := svc.SearchThreads(context.Background(), req); err != nil {
		t.Fatalf("SearchThreads: %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("threads.list calls after invalidate = %d, want 2", listCalls)
	}
}
