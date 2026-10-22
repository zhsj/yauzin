package gmail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/mail"
)

// fakeStore is the accounts.Store the backend sees in tests.
type fakeStore struct {
	mu          sync.Mutex
	credentials accounts.OAuth
	tokens      map[string]accounts.Token
	saved       []accounts.Token
}

func newFakeStore(accountID, accessToken string) *fakeStore {
	return &fakeStore{
		credentials: accounts.OAuth{
			Provider:     mail.KindGmail,
			ClientID:     "client-id",
			ClientSecret: "client-secret",
		},
		tokens: map[string]accounts.Token{
			accountID: {
				AccessToken:  accessToken,
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				ExpiresAt:    time.Now().Add(time.Hour),
			},
		},
	}
}

func (f *fakeStore) Credentials(kind mail.Kind) (accounts.OAuth, bool) {
	if f.credentials.Provider != kind {
		return accounts.OAuth{}, false
	}
	return f.credentials, true
}

func (f *fakeStore) Token(accountID string) (accounts.Token, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[accountID]
	return t, ok
}

func (f *fakeStore) SaveToken(accountID string, t accounts.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = map[string]accounts.Token{}
	}
	f.tokens[accountID] = t
	f.saved = append(f.saved, t)
	return nil
}

const testAccountID = "gmail:user@example.com"

// newTestService serves handler as the Gmail API and returns the backend.
func newTestService(t *testing.T, handler http.Handler, opts ...Option) (*Service, *fakeStore) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	store := newFakeStore(testAccountID, "access-token")
	all := append([]Option{WithBaseURL(srv.URL)}, opts...)
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail, Email: "user@example.com"}, store, all...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, store
}

func writeJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

// writeErrorJSON writes an error response. The content type has to be set
// before WriteHeader, or net/http fills in text/plain and the OAuth client
// cannot read the error code from the body.
func writeErrorJSON(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

// batchServer answers the Gmail batch endpoint. It decodes the embedded
// labels.get calls of each request and replies with one application/http part
// per call, using the same index the client put into the request Content-ID.
type batchServer struct {
	mu       sync.Mutex
	batches  [][]string // label IDs per batch request, in request order
	details  map[string]string
	statuses map[string]int
}

func (b *batchServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" {
			t.Errorf("batch path = %q", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("batch authorization = %q", got)
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
			if got := target.Query().Get("fields"); got != labelDetailFields {
				t.Errorf("batch fields = %q, want %q", got, labelDetailFields)
			}
			ids = append(ids, path.Base(target.Path))
		}
		b.mu.Lock()
		b.batches = append(b.batches, ids)
		b.mu.Unlock()

		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		for i, id := range ids {
			status := http.StatusOK
			if s, ok := b.statuses[id]; ok {
				status = s
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", "application/http")
			header.Set("Content-ID", fmt.Sprintf("<response-label-%d>", i))
			part, err := mw.CreatePart(header)
			if err != nil {
				t.Errorf("batch response part: %v", err)
				return
			}
			body := b.details[id]
			if body == "" {
				body = "{}"
			}
			fmt.Fprintf(part, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\n\r\n%s\n",
				status, http.StatusText(status), body)
		}
		if err := mw.Close(); err != nil {
			t.Errorf("batch response: %v", err)
		}
	}
}

func (b *batchServer) calls() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]string(nil), b.batches...)
}

// folderListBody is what labels.list returns: partial label resources without
// the unread counters or colours.
const folderListBody = `{"labels":[
	{"id":"INBOX","name":"INBOX","type":"system"},
	{"id":"STARRED","name":"STARRED","type":"system"},
	{"id":"SENT","name":"SENT","type":"system"},
	{"id":"TRASH","name":"TRASH","type":"system"},
	{"id":"DRAFT","name":"DRAFT","type":"system"},
	{"id":"SPAM","name":"SPAM","type":"system"},
	{"id":"Label_1","name":"Work","type":"user"},
	{"id":"Label_2","name":"Bills","type":"user"}
]}`

// folderDetails is the per-label answer of labels.get.
func folderDetails() map[string]string {
	return map[string]string{
		"INBOX":   `{"messagesUnread":3}`,
		"STARRED": `{"messagesUnread":1}`,
		"Label_1": `{"messagesUnread":2,"color":{"backgroundColor":"#0b57d0"}}`,
	}
}

func newFolderService(t *testing.T, batch *batchServer, listBody string) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fields"); strings.Contains(got, "messagesUnread") {
			t.Errorf("labels.list fields = %q, want only id/name/type", got)
		}
		writeJSON(t, w, listBody)
	})
	mux.HandleFunc("/batch", batch.handler(t))
	svc, _ := newTestService(t, mux)
	return svc
}

func TestFoldersReadsCountsFromBatchedLabelDetails(t *testing.T) {
	batch := &batchServer{details: folderDetails()}
	svc := newFolderService(t, batch, folderListBody)

	folders, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != 6 {
		t.Fatalf("folders = %+v, want 4 system + 2 labels", folders)
	}
	want := []struct {
		id     string
		kind   mail.FolderKind
		unread int
	}{
		{"inbox", mail.FolderInbox, 3},
		{"starred", mail.FolderStarred, 1},
		{"sent", mail.FolderSent, 0},
		{"trash", mail.FolderTrash, 0},
		{"Label_1", mail.FolderLabel, 2},
		{"Label_2", mail.FolderLabel, 0},
	}
	for i, w := range want {
		got := folders[i]
		if got.ID != w.id || got.Kind != w.kind || got.Unread != w.unread {
			t.Fatalf("folder %d = %+v, want id=%q kind=%q unread=%d", i, got, w.id, w.kind, w.unread)
		}
	}
	if got := folders[4]; got.Name != "Work" || got.Color != "#0b57d0" {
		t.Fatalf("user label = %+v, want Work with its colour", got)
	}
	batches := batch.calls()
	if len(batches) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(batches))
	}
	if len(batches[0]) != 6 {
		t.Fatalf("batch calls = %v, want one per displayed label", batches[0])
	}
}

func TestFoldersToleratesADeletedLabel(t *testing.T) {
	batch := &batchServer{
		details:  folderDetails(),
		statuses: map[string]int{"Label_2": http.StatusNotFound},
	}
	svc := newFolderService(t, batch, folderListBody)

	folders, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != 6 {
		t.Fatalf("folders = %+v, want the surviving labels", folders)
	}
	if got := folders[4]; got.ID != "Label_1" || got.Unread != 2 {
		t.Fatalf("folder 4 = %+v, want the counted user label", got)
	}
	// A label deleted between the list and the batch keeps its row, without a
	// count, instead of failing the whole folder list.
	if got := folders[5]; got.ID != "Label_2" || got.Unread != 0 {
		t.Fatalf("folder 5 = %+v, want Label_2 without a count", got)
	}
}

func TestFoldersFailsWhenNoLabelDetailSucceeds(t *testing.T) {
	statuses := make(map[string]int, len(folderDetails())+2)
	for _, id := range []string{"INBOX", "STARRED", "SENT", "TRASH", "Label_1", "Label_2"} {
		statuses[id] = http.StatusForbidden
	}
	batch := &batchServer{details: folderDetails(), statuses: statuses}
	svc := newFolderService(t, batch, folderListBody)

	if _, err := svc.Folders(context.Background()); err == nil {
		t.Fatal("Folders returned no error although every detail call failed")
	}
}

func TestFoldersFailsWhenTheBatchRequestFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, folderListBody)
	})
	mux.HandleFunc("/batch", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusInternalServerError, `{"error":{"code":500,"message":"boom"}}`)
	})
	svc, _ := newTestService(t, mux)

	if _, err := svc.Folders(context.Background()); err == nil {
		t.Fatal("Folders returned no error although the batch request failed")
	}
}

func TestFoldersSplitsLargeLabelLists(t *testing.T) {
	const labels = 120
	var list strings.Builder
	list.WriteString(`{"labels":[`)
	for i := 0; i < labels; i++ {
		if i > 0 {
			list.WriteString(",")
		}
		fmt.Fprintf(&list, `{"id":"Label_%d","name":"L%d","type":"user"}`, i, i)
	}
	list.WriteString(`]}`)

	batch := &batchServer{}
	svc := newFolderService(t, batch, list.String())

	folders, err := svc.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != labels {
		t.Fatalf("folders = %d, want %d", len(folders), labels)
	}
	batches := batch.calls()
	if len(batches) != 2 {
		t.Fatalf("batch requests = %d, want 2 (100 + 20)", len(batches))
	}
	if len(batches[0]) != batchLimit || len(batches[1]) != labels-batchLimit {
		t.Fatalf("batch sizes = %d/%d, want %d/%d", len(batches[0]), len(batches[1]), batchLimit, labels-batchLimit)
	}
}

const listBody = `{"threads":[{"id":"t1"},{"id":"t2"}],"nextPageToken":"tok2","resultSizeEstimate":42}`

const t1Body = `{"id":"t1","messages":[
	{"id":"m1","labelIds":["INBOX","UNREAD"],"snippet":"first","internalDate":"1700000000000",
	 "payload":{"headers":[
		{"name":"From","value":"Bob <bob@example.com>"},
		{"name":"To","value":"user@example.com"},
		{"name":"Subject","value":"Hello"},
		{"name":"Date","value":"Tue, 14 Nov 2023 22:13:20 +0000"}]}},
	{"id":"m2","labelIds":["INBOX","STARRED"],"snippet":"second","internalDate":"1700000100000",
	 "payload":{"headers":[
		{"name":"From","value":"Alice <alice@example.com>"},
		{"name":"To","value":"user@example.com"},
		{"name":"Subject","value":"Re: Hello"},
		{"name":"Date","value":"Tue, 14 Nov 2023 23:13:20 +0000"}]}}
]}`

const t2Body = `{"id":"t2","messages":[
	{"id":"m3","labelIds":["INBOX"],"snippet":"file","internalDate":"1700000200000",
	 "payload":{"mimeType":"multipart/mixed","headers":[
		{"name":"From","value":"Alice <alice@example.com>"},
		{"name":"Subject","value":"Report"}],
		"parts":[{"filename":"report.pdf","mimeType":"application/pdf","body":{"attachmentId":"a1","size":1234}}]}}
]}`

// threadBatchServer answers the batch endpoint with one metadata thread per
// embedded threads.get. threads maps a thread ID to its body; a thread missing
// there answers 404, as if it had been deleted after the list.
type threadBatchServer struct {
	mu       sync.Mutex
	batches  [][]string // thread IDs per batch request, in request order
	threads  map[string]string
	statuses map[string]int
}

func (b *threadBatchServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/batch" {
			t.Errorf("batch path = %q", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("batch authorization = %q", got)
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
			q := target.Query()
			if got := q.Get("format"); got != "metadata" {
				t.Errorf("format = %q, want metadata", got)
			}
			if got := q.Get("fields"); got != metadataFields {
				t.Errorf("fields = %q, want %q", got, metadataFields)
			}
			if got := q["metadataHeaders"]; len(got) != 5 {
				t.Errorf("metadataHeaders = %v, want From/To/Cc/Subject/Date", got)
			}
			ids = append(ids, path.Base(target.Path))
		}
		b.mu.Lock()
		b.batches = append(b.batches, ids)
		b.mu.Unlock()

		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		for i, id := range ids {
			status, body := http.StatusOK, b.threads[id]
			if s, ok := b.statuses[id]; ok {
				status = s
			}
			if status == http.StatusOK && body == "" {
				status = http.StatusNotFound
			}
			if status != http.StatusOK {
				body = `{"error":{"code":` + strconv.Itoa(status) + `,"message":"Requested entity was not found."}}`
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", "application/http")
			header.Set("Content-ID", fmt.Sprintf("<response-call-%d>", i))
			part, err := mw.CreatePart(header)
			if err != nil {
				t.Errorf("batch response part: %v", err)
				return
			}
			fmt.Fprintf(part, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\n\r\n%s\n",
				status, http.StatusText(status), body)
		}
		if err := mw.Close(); err != nil {
			t.Errorf("batch response: %v", err)
		}
	}
}

func (b *threadBatchServer) calls() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]string(nil), b.batches...)
}

// newThreadService serves list as threads.list and batch as the batch endpoint.
func newThreadService(t *testing.T, batch *threadBatchServer, list http.HandlerFunc) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", list)
	mux.HandleFunc("/batch", batch.handler(t))
	svc, _ := newTestService(t, mux)
	return svc
}

func TestListThreadsAssemblesConversations(t *testing.T) {
	var queryAsked bool
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body, "t2": t2Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		queryAsked = true
		q := r.URL.Query()
		if got := q.Get("maxResults"); got != "50" {
			t.Errorf("maxResults = %q, want 50", got)
		}
		if got := q.Get("labelIds"); got != "INBOX" {
			t.Errorf("labelIds = %q, want INBOX", got)
		}
		if got := q.Get("q"); got != "invoice" {
			t.Errorf("q = %q, want invoice", got)
		}
		if got := q.Get("pageToken"); got != "prev" {
			t.Errorf("pageToken = %q, want prev", got)
		}
		if got := q.Get("fields"); !strings.Contains(got, "resultSizeEstimate") {
			t.Errorf("fields = %q, want resultSizeEstimate", got)
		}
		writeJSON(t, w, listBody)
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{
		FolderID:  "inbox",
		Query:     "invoice",
		PageToken: "prev",
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if !queryAsked {
		t.Fatal("list request never reached the API")
	}
	if page.NextPageToken != "tok2" || page.Total != 42 {
		t.Fatalf("page = %+v, want token tok2 and total 42", page)
	}
	if len(page.Threads) != 2 {
		t.Fatalf("threads = %d, want 2", len(page.Threads))
	}

	th := page.Threads[0]
	if th.ID != "t1" || th.Subject != "Re: Hello" || th.Snippet != "second" {
		t.Fatalf("summary = %+v, want the newest message's subject and snippet", th)
	}
	if th.MessageCount != 2 || th.UnreadCount != 1 || !th.Starred {
		t.Fatalf("summary counts = %+v", th)
	}
	if th.AccountID != testAccountID {
		t.Fatalf("account = %q", th.AccountID)
	}
	if len(th.Participants) != 2 || th.Participants[0].Email != "alice@example.com" || th.Participants[1].Email != "bob@example.com" {
		t.Fatalf("participants = %+v, want alice then bob", th.Participants)
	}
	if !th.LastDate.Equal(time.UnixMilli(1700000100000)) {
		t.Fatalf("last date = %v", th.LastDate)
	}
	if got := strings.Join(th.Labels, ","); got != "INBOX,UNREAD,STARRED" {
		t.Fatalf("labels = %q", got)
	}
	if got := strings.Join(th.MessageIDs, ","); got != "m1,m2" {
		t.Fatalf("message IDs = %q, want oldest first", got)
	}
	if !page.Threads[1].HasAttachments {
		t.Fatal("t2 should carry an attachment")
	}
	// The metadata of both conversations must arrive in a single batch
	// request, in list order.
	batches := batch.calls()
	if len(batches) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(batches))
	}
	if got := strings.Join(batches[0], ","); got != "t1,t2" {
		t.Fatalf("batch calls = %q, want t1,t2", got)
	}
}

func TestListThreadsSkipsThreadDeletedBetweenCalls(t *testing.T) {
	batch := &threadBatchServer{threads: map[string]string{"t1": t1Body}}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"gone"}]}`)
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want only t1", page.Threads)
	}
	batches := batch.calls()
	if len(batches) != 1 || strings.Join(batches[0], ",") != "t1,gone" {
		t.Fatalf("batch calls = %v, want one batch with both listed threads", batches)
	}
}

func TestListThreadsFailsOnOtherErrors(t *testing.T) {
	batch := &threadBatchServer{
		threads:  map[string]string{"t1": t1Body},
		statuses: map[string]int{"t1": http.StatusInternalServerError},
	}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err == nil {
		t.Fatal("ListThreads hid a backend failure")
	} else if errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("error = %v, want a transport failure", err)
	}
}

func TestListThreadsUsesUserLabelIDVerbatim(t *testing.T) {
	var got string
	batch := &threadBatchServer{}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("labelIds")
		writeJSON(t, w, `{"threads":[]}`)
	})
	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "Label_1"}); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if got != "Label_1" {
		t.Fatalf("labelIds = %q, want Label_1", got)
	}
}

func TestListThreadsSendsNoBatchForEmptyPage(t *testing.T) {
	batch := &threadBatchServer{}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[],"resultSizeEstimate":0}`)
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if page.Total != 0 || len(page.Threads) != 0 {
		t.Fatalf("page = %+v, want an empty page", page)
	}
	if got := batch.calls(); len(got) != 0 {
		t.Fatalf("batch requests = %v, want none for an empty page", got)
	}
}

func TestListThreadsTotalWithoutEstimate(t *testing.T) {
	// t1 is gone by the time the batch runs; losing its metadata must not hide
	// the list, and the missing estimate must read as "unknown".
	batch := &threadBatchServer{}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if page.Total != -1 {
		t.Fatalf("total = %d, want -1 when Gmail omits the estimate", page.Total)
	}
	if len(page.Threads) != 0 {
		t.Fatalf("threads = %+v, want the deleted one dropped", page.Threads)
	}
}

func TestListThreadsSplitsLargeMetadataBatches(t *testing.T) {
	const threads = 250
	var list strings.Builder
	list.WriteString(`{"threads":[`)
	metadata := make(map[string]string, threads)
	for i := 0; i < threads; i++ {
		id := fmt.Sprintf("t%d", i)
		if i > 0 {
			list.WriteString(",")
		}
		fmt.Fprintf(&list, `{"id":%q}`, id)
		metadata[id] = fmt.Sprintf(`{"id":%q,"messages":[]}`, id)
	}
	list.WriteString(`]}`)

	batch := &threadBatchServer{threads: metadata}
	svc := newThreadService(t, batch, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, list.String())
	})

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(page.Threads) != threads {
		t.Fatalf("threads = %d, want %d", len(page.Threads), threads)
	}
	for i, th := range page.Threads {
		if want := fmt.Sprintf("t%d", i); th.ID != want {
			t.Fatalf("thread %d = %q, want %q", i, th.ID, want)
		}
	}
	batches := batch.calls()
	if len(batches) != 3 {
		t.Fatalf("batch requests = %d, want 3 (100+100+50)", len(batches))
	}
	sizes := []int{batchLimit, batchLimit, threads - 2*batchLimit}
	for i, size := range sizes {
		if len(batches[i]) != size {
			t.Fatalf("batch %d = %d calls, want %d", i, len(batches[i]), size)
		}
	}
}

func TestGetThreadParsesMessages(t *testing.T) {
	const body = `{"id":"t1","messages":[
	{"id":"m1","labelIds":["UNREAD"],"snippet":"hi","internalDate":"1700000000000",
	 "payload":{"mimeType":"multipart/alternative","headers":[
		{"name":"From","value":"Alice <alice@example.com>"},
		{"name":"To","value":"user@example.com, second@example.com"},
		{"name":"Cc","value":"cc@example.com"},
		{"name":"Subject","value":"Hello"},
		{"name":"Date","value":"Tue, 14 Nov 2023 22:13:20 +0000"}],
		"parts":[
			{"mimeType":"text/plain","body":{"data":"cGxhaW4gdGV4dA"}},
			{"mimeType":"text/html","body":{"data":"PGI-aHRtbDwvYj4"}},
			{"filename":"notes.txt","mimeType":"text/plain","body":{"attachmentId":"att1","size":12}}
		]}}
]}`
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "full" {
			t.Errorf("format = %q, want full", got)
		}
		writeJSON(t, w, body)
	}))

	msgs, err := svc.GetThread(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.TextBody != "plain text" || m.HTMLBody != "<b>html</b>" {
		t.Fatalf("bodies = %q / %q", m.TextBody, m.HTMLBody)
	}
	if len(m.To) != 2 || m.To[1].Email != "second@example.com" {
		t.Fatalf("to = %+v", m.To)
	}
	if len(m.Cc) != 1 || m.Cc[0].Email != "cc@example.com" {
		t.Fatalf("cc = %+v", m.Cc)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].ID != "att1" || m.Attachments[0].Size != 12 {
		t.Fatalf("attachments = %+v", m.Attachments)
	}
	if !m.HasAttachments || !m.Unread || m.Date.IsZero() {
		t.Fatalf("message flags = %+v", m.MessageSummary)
	}
}

func TestGetThreadErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, mail.ErrNotFound},
		{http.StatusBadRequest, mail.ErrInvalidArgument},
		{http.StatusUnauthorized, mail.ErrUnauthorized},
	} {
		mux := http.NewServeMux()
		mux.HandleFunc("/users/me/threads/t1", func(w http.ResponseWriter, r *http.Request) {
			writeErrorJSON(t, w, tc.status, `{"error":{"code":`+strconv.Itoa(tc.status)+`,"message":"nope"}}`)
		})
		// A 401 makes the client refresh; the refused refresh must land in
		// ErrUnauthorized instead of reaching the real Google endpoint.
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			writeErrorJSON(t, w, http.StatusBadRequest,
				`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		store := newFakeStore(testAccountID, "stale-token")
		svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail}, store,
			WithBaseURL(srv.URL),
			func(o *options) { o.tokenURL = srv.URL + "/token" },
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = svc.Close() })

		_, err = svc.GetThread(context.Background(), "t1")
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d gave %v, want %v", tc.status, err, tc.want)
		}
	}
}

func TestGetThreadRejectsEmptyID(t *testing.T) {
	svc, _ := newTestService(t, http.NotFoundHandler())
	if _, err := svc.GetThread(context.Background(), " "); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestUnauthorizedRefreshesOnceAndPersists(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	var refreshForm string
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			writeErrorJSON(t, w, http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)
			return
		}
		writeJSON(t, w, `{"labels":[]}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		refreshForm = string(body)
		writeJSON(t, w, `{"access_token":"fresh-token","token_type":"Bearer","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newFakeStore(testAccountID, "stale-token")
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail}, store,
		WithBaseURL(srv.URL),
		func(o *options) { o.tokenURL = srv.URL + "/token" },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if requests != 2 {
		t.Fatalf("API requests = %d, want the 401 and one retry", requests)
	}
	if !strings.Contains(refreshForm, "grant_type=refresh_token") {
		t.Fatalf("refresh form = %q", refreshForm)
	}
	tok, ok := store.Token(testAccountID)
	if !ok || tok.AccessToken != "fresh-token" {
		t.Fatalf("stored token = %+v, want the refreshed one", tok)
	}
}

func TestRefreshFailureIsUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newFakeStore(testAccountID, "stale-token")
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail}, store,
		WithBaseURL(srv.URL),
		func(o *options) { o.tokenURL = srv.URL + "/token" },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.Folders(context.Background()); !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestRefreshWithoutGmailCredentialsIsUnauthorized(t *testing.T) {
	var mu sync.Mutex
	refreshed := false
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusUnauthorized, `{"error":{"code":401,"message":"Invalid Credentials"}}`)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		refreshed = true
		mu.Unlock()
		writeJSON(t, w, `{"access_token":"fresh-token","token_type":"Bearer","expires_in":3600}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A client stored for another backend must not be used to refresh a Gmail
	// token, even though it carries a client ID and secret.
	store := newFakeStore(testAccountID, "stale-token")
	store.credentials = accounts.OAuth{Provider: mail.KindIMAP, ClientID: "id", ClientSecret: "secret"}
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail}, store,
		WithBaseURL(srv.URL),
		func(o *options) { o.tokenURL = srv.URL + "/token" },
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.Folders(context.Background()); !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshed {
		t.Fatal("refreshed with credentials of another provider")
	}
}

func TestMutatingOperationsAreUnsupported(t *testing.T) {
	svc, _ := newTestService(t, http.NotFoundHandler())
	ctx := context.Background()
	cases := map[string]error{
		"SetFlag": func() error { return svc.SetFlag(ctx, []string{"t1"}, mail.FlagRead, true) }(),
		"Archive": func() error { return svc.Archive(ctx, []string{"t1"}) }(),
		"Delete":  func() error { return svc.Delete(ctx, []string{"t1"}) }(),
		"Send":    func() error { return svc.Send(ctx, mail.Draft{}) }(),
	}
	if _, err := svc.SaveAttachment(ctx, "m1", "a1", "/tmp"); !errors.Is(err, mail.ErrUnsupported) {
		cases["SaveAttachment"] = err
	}
	for op, err := range cases {
		if !errors.Is(err, mail.ErrUnsupported) {
			t.Fatalf("%s = %v, want ErrUnsupported", op, err)
		}
	}
}

func TestServiceUsesProxy(t *testing.T) {
	var hits int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.HasSuffix(r.URL.Path, "/users/me/labels") {
			t.Errorf("proxied path = %q", r.URL.Path)
		}
		writeJSON(t, w, `{"labels":[]}`)
	}))
	defer proxy.Close()

	store := newFakeStore(testAccountID, "access-token")
	svc, err := New(mail.Account{ID: testAccountID, Kind: mail.KindGmail}, store,
		WithBaseURL("http://gmail.invalid/gmail/v1"),
		WithProxyURL(proxy.URL),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.Folders(context.Background()); err != nil {
		t.Fatalf("Folders through proxy: %v", err)
	}
	if hits != 1 {
		t.Fatalf("proxy hits = %d, want 1", hits)
	}
}

func TestNewRejectsBadProxy(t *testing.T) {
	store := newFakeStore(testAccountID, "access-token")
	if _, err := New(mail.Account{ID: testAccountID}, store, WithProxyURL("127.0.0.1:1080")); err == nil {
		t.Fatal("New accepted a proxy address without a scheme")
	}
}

func TestParseProxy(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"  ", "", false},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", false},
		{"socks5://127.0.0.1:1080", "socks5://127.0.0.1:1080", false},
		{"socks5://user:pass@proxy.example.com:1080", "socks5://user:pass@proxy.example.com:1080", false},
		{"socks5h://proxy.example.com:1080", "", true},
		{"http://proxy.example.com:8080/", "http://proxy.example.com:8080", false},
		{"127.0.0.1:8080", "", true},
		{"ftp://127.0.0.1:8080", "", true},
		{"http://127.0.0.1", "", true},
		{"http://127.0.0.1:8080/some/path", "", true},
	} {
		u, err := ParseProxy(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseProxy(%q) = %v, want an error", tc.raw, u)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseProxy(%q): %v", tc.raw, err)
		}
		got := ""
		if u != nil {
			got = u.String()
		}
		if got != tc.want {
			t.Fatalf("ParseProxy(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
