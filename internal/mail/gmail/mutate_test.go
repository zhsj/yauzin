package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// embeddedCall is one request a test server received inside a batch.
type embeddedCall struct {
	method  string
	target  string
	headers map[string]string
	body    string
}

// readEmbedded parses one application/http part into the call it carries.
func readEmbedded(t *testing.T, part io.Reader) embeddedCall {
	t.Helper()
	raw, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read embedded call: %v", err)
	}
	head, body, _ := strings.Cut(string(raw), "\r\n\r\n")
	lines := strings.Split(head, "\r\n")
	fields := strings.Fields(lines[0])
	if len(fields) != 3 {
		t.Fatalf("embedded request line = %q", lines[0])
	}
	call := embeddedCall{
		method:  fields[0],
		target:  fields[1],
		headers: make(map[string]string, len(lines)),
		body:    body,
	}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("embedded header line = %q", line)
		}
		call.headers[name] = strings.TrimSpace(value)
	}
	return call
}

// writeBatchServer answers the batch endpoint with the embedded calls it
// received, recording the calls of every batch request. statuses and bodies
// override the 200 with an empty JSON object that a call answers with.
type writeBatchServer struct {
	mu       sync.Mutex
	batches  [][]embeddedCall
	statuses map[string]int
	bodies   map[string]string
}

func (b *writeBatchServer) handler(t *testing.T) http.HandlerFunc {
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
		var calls []embeddedCall
		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("batch request part: %v", err)
				return
			}
			calls = append(calls, readEmbedded(t, p))
		}
		b.mu.Lock()
		b.batches = append(b.batches, calls)
		b.mu.Unlock()

		mw := multipart.NewWriter(w)
		w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		for i, call := range calls {
			status := http.StatusOK
			if s, ok := b.statuses[call.target]; ok {
				status = s
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", "application/http")
			header.Set("Content-ID", fmt.Sprintf("<response-call-%d>", i))
			part, err := mw.CreatePart(header)
			if err != nil {
				t.Errorf("batch response part: %v", err)
				return
			}
			if status == http.StatusNoContent {
				fmt.Fprintf(part, "HTTP/1.1 %d %s\r\n\r\n", status, http.StatusText(status))
				continue
			}
			payload := b.bodies[call.target]
			if payload == "" {
				payload = "{}"
			}
			fmt.Fprintf(part, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
				status, http.StatusText(status), len(payload), payload)
		}
		if err := mw.Close(); err != nil {
			t.Errorf("batch response: %v", err)
		}
	}
}

// recorded returns one slice per batch request, in request order.
func (b *writeBatchServer) recorded() [][]embeddedCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][]embeddedCall, len(b.batches))
	for i, calls := range b.batches {
		out[i] = append([]embeddedCall(nil), calls...)
	}
	return out
}

// modifyRecorder keeps the bodies messages.batchModify was called with.
type modifyRecorder struct {
	mu   sync.Mutex
	reqs []modifyRequest
}

func (m *modifyRecorder) add(req modifyRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs = append(m.reqs, req)
}

func (m *modifyRecorder) calls() []modifyRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modifyRequest(nil), m.reqs...)
}

// newMutationService serves the batch endpoint with batch and the
// messages.batchModify endpoint with body, which records the decoded requests.
func newMutationService(t *testing.T, batch *writeBatchServer, mods *modifyRecorder) *Service {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/messages/batchModify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("batchModify method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("batchModify content type = %q", got)
		}
		var req modifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("batchModify body: %v", err)
		}
		mods.add(req)
		// Gmail answers a label-only batchModify with an empty 204.
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/batch", batch.handler(t))
	svc, _ := newTestService(t, mux)
	return svc
}

func TestSetFlagMarkReadRemovesUnread(t *testing.T) {
	mods := &modifyRecorder{}
	svc := newMutationService(t, &writeBatchServer{}, mods)

	if err := svc.SetFlag(context.Background(), []string{"m1", "m2"}, mail.FlagRead, true); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	calls := mods.calls()
	if len(calls) != 1 {
		t.Fatalf("batchModify calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if !slices.Equal(got.IDs, []string{"m1", "m2"}) {
		t.Fatalf("ids = %v", got.IDs)
	}
	if !slices.Equal(got.RemoveLabelIDs, []string{labelUnread}) || len(got.AddLabelIDs) != 0 {
		t.Fatalf("remove = %v add = %v, want UNREAD removed", got.RemoveLabelIDs, got.AddLabelIDs)
	}
}

func TestSetFlagMarkUnreadAddsUnread(t *testing.T) {
	mods := &modifyRecorder{}
	svc := newMutationService(t, &writeBatchServer{}, mods)

	if err := svc.SetFlag(context.Background(), []string{"m1"}, mail.FlagRead, false); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	got := mods.calls()[0]
	if !slices.Equal(got.AddLabelIDs, []string{labelUnread}) || len(got.RemoveLabelIDs) != 0 {
		t.Fatalf("add = %v remove = %v, want UNREAD added", got.AddLabelIDs, got.RemoveLabelIDs)
	}
}

func TestSetFlagStarTogglesStarred(t *testing.T) {
	for _, tc := range []struct {
		name   string
		on     bool
		add    []string
		remove []string
	}{
		{name: "star", on: true, add: []string{labelStarred}},
		{name: "unstar", on: false, remove: []string{labelStarred}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mods := &modifyRecorder{}
			svc := newMutationService(t, &writeBatchServer{}, mods)
			if err := svc.SetFlag(context.Background(), []string{"m1"}, mail.FlagStarred, tc.on); err != nil {
				t.Fatalf("SetFlag: %v", err)
			}
			got := mods.calls()[0]
			if !slices.Equal(got.AddLabelIDs, tc.add) || !slices.Equal(got.RemoveLabelIDs, tc.remove) {
				t.Fatalf("add = %v remove = %v, want add=%v remove=%v",
					got.AddLabelIDs, got.RemoveLabelIDs, tc.add, tc.remove)
			}
		})
	}
}

func TestSetFlagRejectsAnUnknownFlag(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	if err := svc.SetFlag(context.Background(), []string{"m1"}, mail.Flag(99), true); !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestWriteWithoutIDsSendsNothing(t *testing.T) {
	svc, _ := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	ctx := context.Background()
	if err := svc.SetFlag(ctx, nil, mail.FlagRead, true); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	if err := svc.Archive(ctx, []string{"", "  "}); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := svc.Delete(ctx, nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestArchiveRemovesTheInboxLabel(t *testing.T) {
	mods := &modifyRecorder{}
	svc := newMutationService(t, &writeBatchServer{}, mods)

	if err := svc.Archive(context.Background(), []string{"m1", "m2"}); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	got := mods.calls()[0]
	if !slices.Equal(got.RemoveLabelIDs, []string{labelInbox}) || len(got.AddLabelIDs) != 0 {
		t.Fatalf("remove = %v add = %v, want INBOX removed", got.RemoveLabelIDs, got.AddLabelIDs)
	}
}

func TestModifySplitsAtTheAPILimit(t *testing.T) {
	mods := &modifyRecorder{}
	svc := newMutationService(t, &writeBatchServer{}, mods)
	ids := make([]string, modifyBatchSize+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("m%d", i)
	}

	if err := svc.SetFlag(context.Background(), ids, mail.FlagStarred, true); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	calls := mods.calls()
	if len(calls) != 2 {
		t.Fatalf("batchModify calls = %d, want 2", len(calls))
	}
	if got := len(calls[0].IDs); got != modifyBatchSize {
		t.Fatalf("first call ids = %d, want %d", got, modifyBatchSize)
	}
	if got := len(calls[1].IDs); got != 1 {
		t.Fatalf("second call ids = %d, want 1", got)
	}
}

func TestDeleteTrashesEveryMessageInOneBatch(t *testing.T) {
	batch := &writeBatchServer{}
	svc := newMutationService(t, batch, &modifyRecorder{})

	if err := svc.Delete(context.Background(), []string{"m1", "m2"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	batches := batch.recorded()
	if len(batches) != 1 {
		t.Fatalf("batch requests = %d, want 1", len(batches))
	}
	if len(batches[0]) != 2 {
		t.Fatalf("embedded calls = %d, want 2", len(batches[0]))
	}
	for i, id := range []string{"m1", "m2"} {
		call := batches[0][i]
		if call.method != http.MethodPost {
			t.Fatalf("call %d method = %q, want POST", i, call.method)
		}
		if want := "/users/me/messages/" + id + "/trash"; call.target != want {
			t.Fatalf("call %d target = %q, want %q", i, call.target, want)
		}
		if call.headers["Content-Length"] != "0" {
			t.Fatalf("call %d content length = %q, want 0", i, call.headers["Content-Length"])
		}
	}
}

func TestDeleteToleratesAMessageAlreadyGone(t *testing.T) {
	batch := &writeBatchServer{statuses: map[string]int{
		"/users/me/messages/m1/trash": http.StatusNotFound,
	}}
	svc := newMutationService(t, batch, &modifyRecorder{})

	if err := svc.Delete(context.Background(), []string{"m1", "m2"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if batches := batch.recorded(); len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("batches = %+v, want one batch with both calls", batches)
	}
}

func TestDeleteReportsAMissingScopeAsUnauthorized(t *testing.T) {
	const body = `{"error":{"code":403,"message":"Request had insufficient authentication scopes.",` +
		`"errors":[{"reason":"insufficientPermissions"}],"status":"PERMISSION_DENIED"}}`
	batch := &writeBatchServer{
		statuses: map[string]int{"/users/me/messages/m1/trash": http.StatusForbidden},
		bodies:   map[string]string{"/users/me/messages/m1/trash": body},
	}
	svc := newMutationService(t, batch, &modifyRecorder{})

	if err := svc.Delete(context.Background(), []string{"m1"}); !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestWriteClearsTheReadCache(t *testing.T) {
	var listCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, folderListBody)
	})
	mux.HandleFunc("/users/me/messages/batchModify", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	svc, _ := newTestService(t, mux)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := svc.Folders(ctx); err != nil {
			t.Fatalf("Folders: %v", err)
		}
	}
	if listCalls != 1 {
		t.Fatalf("labels.list calls = %d, want the second read served from cache", listCalls)
	}

	if err := svc.SetFlag(ctx, []string{"m1"}, mail.FlagStarred, true); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}
	if _, err := svc.Folders(ctx); err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("labels.list calls = %d, want the write to drop the cache", listCalls)
	}
}

func TestInsufficientScopeAsksForAuthorization(t *testing.T) {
	const body = `{"error":{"code":403,"message":"Request had insufficient authentication scopes.",` +
		`"errors":[{"reason":"insufficientPermissions"}],"status":"PERMISSION_DENIED"}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusForbidden, body)
	})
	svc, _ := newTestService(t, mux)

	if _, err := svc.Folders(context.Background()); !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestRateLimitedForbiddenIsNotUnauthorized(t *testing.T) {
	const body = `{"error":{"code":403,"message":"User-rate limit exceeded.",` +
		`"errors":[{"reason":"rateLimitExceeded"}],"status":"RESOURCE_EXHAUSTED"}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/labels", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusForbidden, body)
	})
	svc, _ := newTestService(t, mux)

	err := func() error { _, err := svc.Folders(context.Background()); return err }()
	if err == nil {
		t.Fatal("Folders succeeded on a rate-limited answer")
	}
	if errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want a 403 that is not ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "User-rate limit exceeded") {
		t.Fatalf("error = %v, want the server detail kept", err)
	}
}

func TestBatchBodyEncodesEveryMethod(t *testing.T) {
	body, boundary, err := batchBody("/gmail/v1", []batchTarget{
		{path: "/users/me/messages/m1/trash", method: http.MethodPost},
		{path: "/users/me/messages/batchModify", method: http.MethodPost, body: []byte(`{"ids":["m1"]}`)},
		{path: "/users/me/threads/t1", query: map[string][]string{"format": {"metadata"}}},
	})
	if err != nil {
		t.Fatalf("batchBody: %v", err)
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)

	trash := readEmbedded(t, nextPart(t, mr))
	if trash.method != http.MethodPost {
		t.Fatalf("trash method = %q, want POST", trash.method)
	}
	if want := "/gmail/v1/users/me/messages/m1/trash"; trash.target != want {
		t.Fatalf("trash target = %q, want %q", trash.target, want)
	}
	if trash.headers["Content-Length"] != "0" || trash.body != "" {
		t.Fatalf("trash length = %q body = %q, want an empty POST", trash.headers["Content-Length"], trash.body)
	}

	modify := readEmbedded(t, nextPart(t, mr))
	if got := modify.headers["Content-Type"]; got != "application/json" {
		t.Fatalf("modify content type = %q", got)
	}
	if got := modify.headers["Content-Length"]; got != fmt.Sprint(len(`{"ids":["m1"]}`)) {
		t.Fatalf("modify content length = %q", got)
	}
	if modify.body != `{"ids":["m1"]}` {
		t.Fatalf("modify body = %q", modify.body)
	}

	get := readEmbedded(t, nextPart(t, mr))
	if get.method != http.MethodGet {
		t.Fatalf("get method = %q, want GET", get.method)
	}
	if want := "/gmail/v1/users/me/threads/t1?format=metadata"; get.target != want {
		t.Fatalf("get target = %q, want %q", get.target, want)
	}
	if _, ok := get.headers["Content-Length"]; ok {
		t.Fatalf("get carried Content-Length = %q", get.headers["Content-Length"])
	}
	if _, err := mr.NextPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("parts after the third = %v, want EOF", err)
	}
}

// nextPart returns the next part of a batch body.
func nextPart(t *testing.T, mr *multipart.Reader) *multipart.Part {
	t.Helper()
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("next part: %v", err)
	}
	return part
}
