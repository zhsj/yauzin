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

// batchLimit is the maximum number of calls one batch request may carry. Gmail
// allows 100 embedded calls; a longer list is split into several batches.
const batchLimit = 100

// labelDetailFields trims a labels.get response to what the sidebar renders.
// labels.list only returns id, name and type, so these fields have to come from
// a separate call per label.
const labelDetailFields = "messagesUnread,color/backgroundColor"

// labelDetails fetches the detail of every label in ids.
//
// Gmail's labels.list returns partial label resources: the unread counters and
// the label colours are missing, which is why the sidebar showed no numbers for
// a real account. One labels.get per label fills them in, and one batch request
// keeps that at a single round trip (plus one more per 100 labels).
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
	var failed int
	var firstErr error
	for start := 0; start < len(ids); start += batchLimit {
		end := min(start+batchLimit, len(ids))
		errs, fatal := c.batchLabelDetails(ctx, ids[start:end], out)
		if fatal != nil {
			// The batch itself never got an answer: reporting zeros would hide
			// a broken backend, which is exactly the bug this code fixes.
			return out, fatal
		}
		failed += len(errs)
		if firstErr == nil && len(errs) > 0 {
			firstErr = errs[0]
		}
	}
	if failed == len(ids) {
		return out, firstErr
	}
	return out, nil
}

// batchLabelDetails sends one batch of labels.get calls and merges the decoded
// labels into out. It returns one error per call that did not answer with 200,
// and a fatal error when the batch request or its envelope failed as a whole.
func (c *client) batchLabelDetails(ctx context.Context, ids []string, out map[string]*Label) (errs []error, fatal error) {
	body, boundary, err := batchBody(c.apiPath, ids)
	if err != nil {
		return nil, fmt.Errorf("gmail labels.get batch: %w", err)
	}
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.sendBatch(ctx, body, boundary, token)
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
		resp, err = c.sendBatch(ctx, body, boundary, token)
		if err != nil {
			return nil, err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("labels.get batch", resp)
	}
	return parseBatch(resp, ids, out)
}

// batchBody encodes one multipart/mixed request with one embedded labels.get
// per id. The Content-ID carries the position in ids instead of the label ID:
// a label ID would have to be escaped for a header, and the response index maps
// straight back to the request slice.
func batchBody(apiPath string, ids []string) (body []byte, boundary string, err error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, id := range ids {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", "application/http")
		header.Set("Content-ID", fmt.Sprintf("<label-%d>", i))
		part, err := mw.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		q := url.Values{}
		q.Set("fields", labelDetailFields)
		target := apiPath + "/users/me/labels/" + url.PathEscape(id) + "?" + q.Encode()
		if _, err := fmt.Fprintf(part, "GET %s HTTP/1.1\r\n\r\n", target); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.Boundary(), nil
}

// sendBatch performs one authorized batch POST.
func (c *client) sendBatch(ctx context.Context, body []byte, boundary, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.batchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "multipart/mixed; boundary="+boundary)
	req.Header.Set("Accept", "multipart/mixed")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmail labels.get batch: %w", err)
	}
	return resp, nil
}

// parseBatch decodes the embedded responses of a batch into out, keyed by the
// label ID the corresponding request asked for. Responses come back in no
// guaranteed order.
func parseBatch(resp *http.Response, ids []string, out map[string]*Label) (errs []error, fatal error) {
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" || params["boundary"] == "" {
		return nil, fmt.Errorf("gmail labels.get batch: response is %q, want multipart/mixed", resp.Header.Get("Content-Type"))
	}
	mr := multipart.NewReader(resp.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("gmail labels.get batch: read part: %w", err))
			break
		}
		idx, ok := partIndex(part.Header.Get("Content-ID"))
		if !ok || idx >= len(ids) {
			errs = append(errs, fmt.Errorf("gmail labels.get batch: unexpected Content-ID %q", part.Header.Get("Content-ID")))
			continue
		}
		id := ids[idx]
		inner, err := http.ReadResponse(bufio.NewReader(part), &http.Request{Method: http.MethodGet})
		if err != nil {
			errs = append(errs, fmt.Errorf("gmail labels.get %s: parse response: %w", id, err))
			continue
		}
		if inner.StatusCode != http.StatusOK {
			detail, _ := io.ReadAll(io.LimitReader(inner.Body, 4<<10))
			_ = inner.Body.Close()
			// A label deleted between the list and the batch lands here. The
			// other labels of the folder list stay usable.
			slog.Warn("gmail label detail failed", "label", id, "status", inner.StatusCode)
			errs = append(errs, fmt.Errorf("gmail labels.get %s: %s: %s", id, inner.Status, strings.TrimSpace(string(detail))))
			continue
		}
		var label Label
		err = json.NewDecoder(inner.Body).Decode(&label)
		_ = inner.Body.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("gmail labels.get %s: decode response: %w", id, err))
			continue
		}
		out[id] = &label
	}
	return errs, nil
}

// partIndex reads the index out of a response Content-ID such as
// "<response-label-3>".
func partIndex(contentID string) (int, bool) {
	s := strings.Trim(strings.TrimSpace(contentID), "<>")
	s = strings.TrimPrefix(s, "response-")
	s = strings.TrimPrefix(s, "label-")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}
