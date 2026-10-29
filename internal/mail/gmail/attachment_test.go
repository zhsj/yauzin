package gmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
)

// blobHandler serves one attachment blob and fails the test if the payload
// endpoint is requested: a plain attachment selects the blob by ID alone.
func blobHandler(t *testing.T, blob string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("messages.get requested for a blob attachment")
		http.NotFound(w, r)
	})
	mux.HandleFunc("/users/me/messages/m1/attachments/a1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, blob)
	})
	return mux
}

// failingWriter fails every write; shortWriter accepts nothing without an
// error, which a save must not report as success.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

// countingWriter records how the content was delivered as well as what it was:
// a streamed save hands over many blocks, not one holding the whole file.
type countingWriter struct {
	bytes.Buffer
	writes int
	max    int
}

func (w *countingWriter) Write(b []byte) (int, error) {
	w.writes++
	if len(b) > w.max {
		w.max = len(b)
	}
	return w.Buffer.Write(b)
}

// failAfterWriter accepts limit bytes and then fails, which is what a full disk
// looks like halfway through a large save.
type failAfterWriter struct {
	limit int
	n     int
	err   error
}

func (w *failAfterWriter) Write(b []byte) (int, error) {
	if w.n+len(b) > w.limit {
		room := w.limit - w.n
		w.n = w.limit
		return room, w.err
	}
	w.n += len(b)
	return len(b), nil
}

func TestSaveAttachmentFromBlob(t *testing.T) {
	const content = "hello attachment"
	svc, _ := newTestService(t, blobHandler(t,
		fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content))))

	var out bytes.Buffer
	var updates []mail.Progress
	err := svc.SaveAttachment(context.Background(), "m1", "a1", &out, func(p mail.Progress) {
		updates = append(updates, p)
	})
	if err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if got := out.String(); got != content {
		t.Fatalf("saved %q, want %q", got, content)
	}

	if len(updates) == 0 {
		t.Fatal("save reported no progress")
	}
	for i := 1; i < len(updates); i++ {
		if updates[i].Done < updates[i-1].Done {
			t.Fatalf("progress went backwards: %v after %v", updates[i], updates[i-1])
		}
	}
	if last := updates[len(updates)-1]; last.Total <= 0 || last.Done != last.Total {
		t.Fatalf("last progress %v, want Done == Total > 0", last)
	}
}

// TestSaveAttachmentStreamsBlob pins that blob content reaches the target while
// it is still arriving: the write side sees many blocks, none of them the whole
// file, which is what makes the memory cost independent of the attachment size.
func TestSaveAttachmentStreamsBlob(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), 8<<10)
	svc, _ := newTestService(t, blobHandler(t,
		fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), base64.RawURLEncoding.EncodeToString(content))))

	var out countingWriter
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", &out, nil); err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if got := out.Bytes(); !bytes.Equal(got, content) {
		t.Fatalf("saved %d bytes, want %d matching the blob", len(got), len(content))
	}
	if out.writes < 2 || out.max >= len(content) {
		t.Fatalf("wrote %d bytes of %d in %d chunk(s), want a streamed save",
			out.Len(), len(content), out.writes)
	}
}

// TestSaveAttachmentBlobShapes covers the spellings the endpoint may use for the
// same blob: the fields in any order, and base64url with or without padding.
func TestSaveAttachmentBlobShapes(t *testing.T) {
	const content = "hello attachment"
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unpadded",
			body: fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content)),
		},
		{
			name: "padded",
			body: fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64Padded(content)),
		},
		{
			name: "attachment id first",
			body: fmt.Sprintf(`{"attachmentId":"a1","data":%q,"size":%d}`, b64(content), len(content)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t, blobHandler(t, tt.body))
			var out bytes.Buffer
			if err := svc.SaveAttachment(context.Background(), "m1", "a1", &out, nil); err != nil {
				t.Fatalf("save attachment: %v", err)
			}
			if got := out.String(); got != content {
				t.Fatalf("saved %q, want %q", got, content)
			}
		})
	}
}

// TestSaveAttachmentMissing pins the ways a blob can be absent: the endpoint
// answers 404, the response carries no data field, and the field is there but
// empty. Each one is a missing attachment, and none of them asks for the payload.
func TestSaveAttachmentMissing(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "deleted blob",
			status: http.StatusNotFound,
			body:   `{"error":{"code":404,"message":"not found"}}`,
		},
		{name: "no data field", body: `{"size":0}`},
		{name: "empty data field", body: `{"size":0,"data":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("payload requested for a blob attachment")
				http.NotFound(w, r)
			})
			mux.HandleFunc("/users/me/messages/m1/attachments/a1", func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 0 {
					writeErrorJSON(t, w, tt.status, tt.body)
					return
				}
				writeJSON(t, w, tt.body)
			})
			svc, _ := newTestService(t, mux)
			if err := svc.SaveAttachment(context.Background(), "m1", "a1", io.Discard, nil); !errors.Is(err, mail.ErrNotFound) {
				t.Fatalf("missing attachment returned %v, want ErrNotFound", err)
			}
		})
	}

	// A part whose content lives in the payload has no ID to download, so the
	// save reports it missing without touching the network.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("empty attachment ID made a request to %q", r.URL.Path)
		http.NotFound(w, r)
	})
	svc, _ := newTestService(t, mux)
	if err := svc.SaveAttachment(context.Background(), "m1", "", io.Discard, nil); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("empty attachment ID returned %v, want ErrNotFound", err)
	}
}

func TestSaveAttachmentInvalidArguments(t *testing.T) {
	svc, _ := newTestService(t, http.NotFoundHandler())
	tests := []struct {
		messageID string
		w         io.Writer
	}{
		{"", io.Discard},
		{" ", io.Discard},
		{"m1", nil},
	}
	for _, tt := range tests {
		if err := svc.SaveAttachment(context.Background(), tt.messageID, "a1", tt.w, nil); !errors.Is(err, mail.ErrInvalidArgument) {
			t.Fatalf("SaveAttachment(%q, %v) = %v, want ErrInvalidArgument", tt.messageID, tt.w, err)
		}
	}
}

// TestSaveAttachmentWriteFailure pins that a target which rejects or truncates
// the content fails the save instead of looking successful.
func TestSaveAttachmentWriteFailure(t *testing.T) {
	const content = "hello"
	svc, _ := newTestService(t, blobHandler(t,
		fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content))))

	writeErr := errors.New("disk full")
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", failingWriter{err: writeErr}, nil); !errors.Is(err, writeErr) {
		t.Fatalf("failing writer returned %v, want %v", err, writeErr)
	}
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", shortWriter{}, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short writer returned %v, want io.ErrShortWrite", err)
	}
}

// TestSaveAttachmentPartialWrite covers a target that dies after accepting part
// of a large blob: the save fails, the bytes already written stay, and the
// download is not started over.
func TestSaveAttachmentPartialWrite(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 64<<10)
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("payload requested after a partial write")
		http.NotFound(w, r)
	})
	mux.HandleFunc("/users/me/messages/m1/attachments/a1", func(w http.ResponseWriter, r *http.Request) {
		// The client stops reading on the write error, so the response may not
		// be delivered in full; that is the point of the test.
		_, _ = io.WriteString(w, fmt.Sprintf(`{"size":%d,"data":%q}`,
			len(content), base64.RawURLEncoding.EncodeToString(content)))
	})
	svc, _ := newTestService(t, mux)

	writeErr := errors.New("disk full")
	out := &failAfterWriter{limit: 8 << 10, err: writeErr}
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", out, nil); !errors.Is(err, writeErr) {
		t.Fatalf("partial save returned %v, want %v", err, writeErr)
	}
	if out.n == 0 {
		t.Fatal("nothing was written before the failure")
	}
}

// TestSaveAttachmentRetriesUnauthorized pins that a rejected token is refreshed
// once before the body is read, so the target still receives exactly one copy.
func TestSaveAttachmentRetriesUnauthorized(t *testing.T) {
	const content = "retried content"
	body := fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fresh-token","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(tokenSrv.Close)

	var served atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("payload requested for a blob attachment")
		http.NotFound(w, r)
	})
	mux.HandleFunc("/users/me/messages/m1/attachments/a1", func(w http.ResponseWriter, r *http.Request) {
		if !served.Swap(true) {
			writeErrorJSON(t, w, http.StatusUnauthorized,
				`{"error":{"code":401,"message":"invalid credentials"}}`)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh-token" {
			t.Errorf("retry sent %q, want the refreshed token", got)
		}
		_, _ = io.WriteString(w, body)
	})

	svc, _ := newTestService(t, mux, func(o *options) { o.tokenURL = tokenSrv.URL })
	var out countingWriter
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", &out, nil); err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if got := out.String(); got != content {
		t.Fatalf("saved %q, want one copy of %q", got, content)
	}
}

func TestSaveAttachmentHonoursContext(t *testing.T) {
	svc, _ := newTestService(t, blobHandler(t, `{"size":5,"data":"aGVsbG8"}`))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.SaveAttachment(ctx, "m1", "a1", io.Discard, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save returned %v, want context.Canceled", err)
	}
}

// TestSaveAttachmentLogs pins what the DEBUG lines say: one line per save, and on
// failure a stage that tells a missing attachment from a broken download or a
// target that refused the bytes.
func TestSaveAttachmentLogs(t *testing.T) {
	buf := captureLogs(t, slog.LevelDebug)
	const content = "log me"

	svc, _ := newTestService(t, blobHandler(t,
		fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content))))
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", io.Discard, nil); err != nil {
		t.Fatalf("save attachment: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "gmail attachment saved") || strings.Contains(got, "source=") {
		t.Fatalf("success line should be the only one and carry no source:\n%s", got)
	}

	buf.Reset()
	if err := svc.SaveAttachment(context.Background(), "m1", "", io.Discard, nil); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("empty attachment ID returned %v, want ErrNotFound", err)
	}
	if got := buf.String(); !strings.Contains(got, "gmail attachment save failed") || !strings.Contains(got, `stage="no blob"`) {
		t.Fatalf("log does not mention the missing blob ID:\n%s", got)
	}

	buf.Reset()
	svc, _ = newTestService(t, blobHandler(t, `{"size":0}`))
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", io.Discard, nil); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("empty blob returned %v, want ErrNotFound", err)
	}
	if got := buf.String(); !strings.Contains(got, "gmail attachment save failed") || !strings.Contains(got, "stage=empty") {
		t.Fatalf("log does not mention the empty blob:\n%s", got)
	}

	buf.Reset()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/messages/m1/attachments/a1", func(w http.ResponseWriter, r *http.Request) {
		writeErrorJSON(t, w, http.StatusNotFound, `{"error":{"code":404,"message":"not found"}}`)
	})
	svc, _ = newTestService(t, mux)
	if err := svc.SaveAttachment(context.Background(), "m1", "a1", io.Discard, nil); !errors.Is(err, mail.ErrNotFound) {
		t.Fatalf("deleted blob returned %v, want ErrNotFound", err)
	}
	if got := buf.String(); !strings.Contains(got, "gmail attachment save failed") || !strings.Contains(got, "stage=download") {
		t.Fatalf("log does not mention the failed download:\n%s", got)
	}

	buf.Reset()
	svc, _ = newTestService(t, blobHandler(t,
		fmt.Sprintf(`{"size":%d,"data":%q}`, len(content), b64(content))))
	if err := svc.SaveAttachment(context.Background(), "m1", "a1",
		failingWriter{err: errors.New("disk full")}, nil); err == nil {
		t.Fatal("failing writer saved the attachment")
	}
	if got := buf.String(); !strings.Contains(got, "gmail attachment save failed") || !strings.Contains(got, "stage=write") {
		t.Fatalf("log does not mention the write failure:\n%s", got)
	}
}
