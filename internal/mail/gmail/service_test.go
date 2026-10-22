package gmail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	mu           sync.Mutex
	clientID     string
	clientSecret string
	tokens       map[string]accounts.Token
	saved        []accounts.Token
}

func newFakeStore(accountID, accessToken string) *fakeStore {
	return &fakeStore{
		clientID:     "client-id",
		clientSecret: "client-secret",
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

func (f *fakeStore) Credentials() (string, string) { return f.clientID, f.clientSecret }

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

func TestFoldersMapsSystemLabelsAndUserLabels(t *testing.T) {
	const body = `{"labels":[
		{"id":"INBOX","name":"INBOX","type":"system","messagesUnread":3},
		{"id":"STARRED","name":"STARRED","type":"system","messagesUnread":1},
		{"id":"SENT","name":"SENT","type":"system"},
		{"id":"TRASH","name":"TRASH","type":"system"},
		{"id":"DRAFT","name":"DRAFT","type":"system"},
		{"id":"SPAM","name":"SPAM","type":"system"},
		{"id":"Label_1","name":"Work","type":"user","messagesUnread":2,"color":{"backgroundColor":"#0b57d0"}},
		{"id":"Label_2","name":"Bills","type":"user"}
	]}`
	srv, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/labels" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("fields"); !strings.Contains(got, "messagesUnread") {
			t.Errorf("fields = %q, want messagesUnread", got)
		}
		writeJSON(t, w, body)
	}))

	folders, err := srv.Folders(context.Background())
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

func threadHandler(t *testing.T, list, thread1, thread2 string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, list)
	})
	mux.HandleFunc("/users/me/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "metadata" {
			t.Errorf("format = %q, want metadata", got)
		}
		writeJSON(t, w, thread1)
	})
	mux.HandleFunc("/users/me/threads/t2", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, thread2)
	})
	return mux
}

func TestListThreadsAssemblesConversations(t *testing.T) {
	var queryAsked bool
	mux := threadHandler(t, listBody, t1Body, t2Body)
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users/me/threads" {
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
		}
		mux.ServeHTTP(w, r)
	}))

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
}

func TestListThreadsSkipsThreadDeletedBetweenCalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"},{"id":"gone"}]}`)
	})
	mux.HandleFunc("/users/me/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, t1Body)
	})
	mux.HandleFunc("/users/me/threads/gone", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusNotFound, `{"error":{"code":404,"message":"Requested entity was not found."}}`)
	})
	svc, _ := newTestService(t, mux)

	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(page.Threads) != 1 || page.Threads[0].ID != "t1" {
		t.Fatalf("threads = %+v, want only t1", page.Threads)
	}
}

func TestListThreadsFailsOnOtherErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	})
	mux.HandleFunc("/users/me/threads/t1", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusInternalServerError, `{"error":{"code":500,"message":"backend error"}}`)
	})
	svc, _ := newTestService(t, mux)

	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"}); err == nil {
		t.Fatal("ListThreads hid a backend failure")
	} else if errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("error = %v, want a transport failure", err)
	}
}

func TestListThreadsUsesUserLabelIDVerbatim(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/threads", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("labelIds")
		writeJSON(t, w, `{"threads":[]}`)
	})
	svc, _ := newTestService(t, mux)
	if _, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "Label_1"}); err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if got != "Label_1" {
		t.Fatalf("labelIds = %q, want Label_1", got)
	}
}

func TestListThreadsTotalWithoutEstimate(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, `{"threads":[{"id":"t1"}]}`)
	}))
	// t1 metadata is missing from this server, which must not hide the list.
	page, err := svc.ListThreads(context.Background(), mail.ListRequest{FolderID: "inbox"})
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if page.Total != -1 {
		t.Fatalf("total = %d, want -1 when Gmail omits the estimate", page.Total)
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
