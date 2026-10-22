package gmail

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

// batchLimit is the number of calls one batch request carries. Gmail accepts
// 100 embedded calls, but it also runs them concurrently and counts them
// against a per-user limit, so 100 calls (plus the label batch of the sidebar)
// tripped "Too many concurrent requests for user". 25 keeps that peak low; lower
// it if the limit is hit again.
const batchLimit = 25

// labelDetailFields trims a labels.get response to what the sidebar renders.
// labels.list only returns id, name and type, so these fields have to come from
// a separate call per label.
const labelDetailFields = "messagesUnread,color/backgroundColor"

// batchTarget is one GET embedded in a batch request.
type batchTarget struct {
	// path is the API path of the call, such as "/users/me/threads/t1".
	path string
	// query is the query string of the call. Repeated keys are kept: the
	// metadata calls send one metadataHeaders per header.
	query url.Values
}

// batchFetch sends every target as an embedded call of one batch request per
// batchLimit targets, and calls handle with the index of the target and the
// response of its embedded call. handle owns resp.Body for the call; the
// framework closes it afterwards.
//
// A fatal error (a request, envelope or part that failed as a whole) is
// returned immediately and the remaining chunks are not sent. The first error
// handle reports is returned after all chunks have run, without stopping the
// remaining ones.
func (c *client) batchFetch(ctx context.Context, op string, targets []batchTarget, handle func(i int, resp *http.Response) error) error {
	var firstErr error
	for start := 0; start < len(targets); start += batchLimit {
		end := min(start+batchLimit, len(targets))
		chunkErr, fatal := c.batchChunk(ctx, op, targets[start:end], func(i int, resp *http.Response) error {
			return handle(start+i, resp)
		})
		if fatal != nil {
			return fatal
		}
		if chunkErr != nil && firstErr == nil {
			firstErr = chunkErr
		}
	}
	return firstErr
}

// batchChunk performs one batch request for targets and drives handle with its
// embedded responses. It returns the first error handle reported, which does
// not stop the remaining chunks, and a fatal error, which does.
func (c *client) batchChunk(ctx context.Context, op string, targets []batchTarget, handle func(i int, resp *http.Response) error) (firstErr error, fatal error) {
	body, boundary, err := batchBody(c.apiPath, targets)
	if err != nil {
		return nil, fmt.Errorf("gmail %s batch: %w", op, err)
	}
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.sendBatch(ctx, op, body, boundary, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// The server rejected a token the clock still considered valid: drop it
		// and retry once, like a single call does.
		_ = resp.Body.Close()
		token, err = c.accessToken(ctx, true)
		if err != nil {
			return nil, err
		}
		resp, err = c.sendBatch(ctx, op, body, boundary, token)
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(op+" batch", resp)
	}
	return parseBatch(resp, len(targets), op, handle)
}

// batchBody encodes one multipart/mixed request with one embedded GET per
// target. The Content-ID carries the index in targets instead of an ID: an ID
// would have to be escaped for a header, and the response index maps straight
// back to the request slice.
func batchBody(apiPath string, targets []batchTarget) (body []byte, boundary string, err error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, t := range targets {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", "application/http")
		header.Set("Content-ID", fmt.Sprintf("<call-%d>", i))
		part, err := mw.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		target := apiPath + t.path
		if len(t.query) > 0 {
			target += "?" + t.query.Encode()
		}
		if _, err := fmt.Fprintf(part, "GET %s HTTP/1.1\r\n\r\n", target); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.Boundary(), nil
}

// sendBatch performs one authorized batch POST. Like send, it holds the
// account's request slot for the HTTP round trip.
func (c *client) sendBatch(ctx context.Context, op string, body []byte, boundary, token string) (*http.Response, error) {
	if err := c.acquireReq(ctx); err != nil {
		return nil, err
	}
	defer c.releaseReq()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.batchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "multipart/mixed; boundary="+boundary)
	req.Header.Set("Accept", "multipart/mixed")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmail %s batch: %w", op, err)
	}
	return resp, nil
}

// parseBatch decodes the embedded responses of a batch and drives handle with
// each one, using the index the corresponding request asked for. Responses come
// back in no guaranteed order. A broken envelope or part is fatal; the first
// error handle reports is returned so batchFetch can record it without stopping
// the remaining chunks.
func parseBatch(resp *http.Response, n int, op string, handle func(i int, resp *http.Response) error) (firstErr error, fatal error) {
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" || params["boundary"] == "" {
		return nil, fmt.Errorf("gmail %s batch: response is %q, want multipart/mixed", op, resp.Header.Get("Content-Type"))
	}
	mr := multipart.NewReader(resp.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gmail %s batch: read part: %w", op, err)
		}
		idx, ok := partIndex(part.Header.Get("Content-ID"))
		if !ok || idx < 0 || idx >= n {
			return nil, fmt.Errorf("gmail %s batch: unexpected Content-ID %q", op, part.Header.Get("Content-ID"))
		}
		inner, err := http.ReadResponse(bufio.NewReader(part), &http.Request{Method: http.MethodGet})
		if err != nil {
			return nil, fmt.Errorf("gmail %s %d: parse response: %w", op, idx, err)
		}
		// handle owns the body for the call; the framework closes it afterwards.
		if err := handle(idx, inner); err != nil && firstErr == nil {
			firstErr = err
		}
		_ = inner.Body.Close()
	}
	return firstErr, nil
}

// partIndex reads the request index out of a response Content-ID such as
// "<response-call-3>" or "<response-label-3>". Only the tail after the last
// dash matters, so the prefix a server echoes back is not significant.
func partIndex(contentID string) (int, bool) {
	s := strings.Trim(strings.TrimSpace(contentID), "<>")
	if i := strings.LastIndexByte(s, '-'); i >= 0 {
		s = s[i+1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// labelDetails fetches the detail of every label in ids.
//
// Gmail's labels.list returns partial label resources: the unread counters and
// the label colours are missing, which is why the sidebar showed no numbers for
// a real account. One labels.get per label fills them in, and one batch request
// keeps that at a single round trip (plus one more per batchLimit labels).
//
// A label that disappeared between the list and the batch is logged and left
// out of the result: the caller then renders it without a count. Only when every
// call failed is the first error returned, so the caller still reports a broken
// backend instead of silently showing zeros.
func (c *client) labelDetails(ctx context.Context, ids []string) (map[string]*Label, error) {
	out := make(map[string]*Label, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	targets := make([]batchTarget, len(ids))
	for i, id := range ids {
		q := url.Values{}
		q.Set("fields", labelDetailFields)
		targets[i] = batchTarget{path: "/users/me/labels/" + url.PathEscape(id), query: q}
	}

	// A label that disappeared between the list and the batch is logged and
	// left out of the result; only when every call failed is the first error
	// returned, so the caller still reports a broken backend instead of
	// silently showing zeros.
	failed := 0
	var firstErr error
	err := c.batchFetch(ctx, "labels.get", targets, func(i int, resp *http.Response) error {
		id := ids[i]
		if resp.StatusCode != http.StatusOK {
			detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
			slog.Warn("gmail label detail failed", "label", id, "status", resp.StatusCode)
			if firstErr == nil {
				firstErr = fmt.Errorf("gmail labels.get %s: %s: %s", id, http.StatusText(resp.StatusCode), strings.TrimSpace(string(detail)))
			}
			failed++
			return nil
		}
		var label Label
		if err := json.NewDecoder(resp.Body).Decode(&label); err != nil {
			slog.Warn("gmail label detail decode failed", "label", id, "err", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("gmail labels.get %s: decode response: %w", id, err)
			}
			failed++
			return nil
		}
		out[id] = &label
		return nil
	})
	if err != nil {
		// The batch itself never got an answer: reporting zeros would hide a
		// broken backend, which is exactly the bug this code fixes.
		return out, err
	}
	if failed == len(ids) {
		return out, firstErr
	}
	return out, nil
}
