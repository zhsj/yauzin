package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// uploadPayload is a message whose bytes are produced on demand. Size has to
// be exactly the number of bytes WriteTo produces: a resumable session declares
// it up front, and the server uses it to know when the message is complete.
type uploadPayload interface {
	Size() int64
	WriteTo(io.Writer) (int64, error)
}

const (
	// statusResumeIncomplete is the 308 the resumable protocol uses while the
	// upload still needs more bytes; net/http names 308 Permanent Redirect.
	statusResumeIncomplete = 308
	// uploadAttempts bounds how often one chunk or the session start is tried
	// again after a retryable failure.
	uploadAttempts = 3
)

var (
	// uploadChunkSize is one PUT body. The resumable protocol requires every
	// chunk but the last to be a multiple of 256 KiB, and this is large enough
	// to keep the request count low without raising the memory cost. Tests
	// shrink it to exercise chunking without moving megabytes.
	uploadChunkSize = 8 << 20
	// uploadRetryDelay is the pause between two attempts. Tests shorten it.
	uploadRetryDelay = time.Second
)

// doResumableUpload sends media with a resumable session: the message is
// rendered once through a pipe and streamed in fixed size chunks, so neither
// the attachments nor the encoded message are ever held in memory as a whole.
func (c *client) doResumableUpload(ctx context.Context, op, path, contentType string, media uploadPayload, out any) error {
	total := media.Size()
	if total < 0 {
		return fmt.Errorf("%w: negative message size", mail.ErrInvalidArgument)
	}
	// The whole session is one send as far as the quota is concerned: the
	// chunked PUTs and the offset queries are not separate method calls, so the
	// cost is reserved once here rather than per request.
	if err := c.reserveQuota(ctx, op, methodUnits(op, "")); err != nil {
		return err
	}
	session, err := c.startUpload(ctx, op, path, contentType, total)
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	rendered := make(chan error, 1)
	go func() {
		_, err := media.WriteTo(pw)
		_ = pw.CloseWithError(err)
		rendered <- err
		close(rendered)
	}()
	// The renderer exits as soon as the pipe is closed, so a failed or
	// cancelled upload never keeps reading a large attachment.
	claimed := false
	defer func() {
		_ = pr.Close()
		if !claimed {
			// Wait for the renderer to leave; the error it reports has
			// already travelled through the pipe.
			for range rendered {
			}
		}
	}()

	buf := make([]byte, uploadChunkSize)
	var sent int64
	for sent < total {
		want := int64(len(buf))
		if rest := total - sent; rest < want {
			want = rest
		}
		if _, err := io.ReadFull(pr, buf[:want]); err != nil {
			return fmt.Errorf("gmail %s: read message: %w", op, err)
		}
		done, resp, err := c.sendChunk(ctx, op, session, contentType, buf[:want], sent, total)
		if err != nil {
			return err
		}
		if done {
			claimed = true
			renderErr := <-rendered
			if renderErr != nil {
				if resp != nil {
					_ = resp.Body.Close()
				}
				return fmt.Errorf("gmail %s: render message: %w", op, renderErr)
			}
			if resp == nil {
				return nil
			}
			defer func() { _ = resp.Body.Close() }()
			if out == nil {
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("gmail %s: decode response: %w", op, err)
			}
			return nil
		}
		sent += want
	}
	return fmt.Errorf("gmail %s: upload ended without a completion response", op)
}

// startUpload opens a resumable session and returns its URI. The whole message
// size is declared, so the server can reject a payload over its limit before a
// single byte of it arrives.
func (c *client) startUpload(ctx context.Context, op, path, contentType string, total int64) (string, error) {
	u := c.uploadURL + path + "?" + url.Values{"uploadType": {"resumable"}}.Encode()
	var lastErr error
	for attempt := 0; attempt <= uploadAttempts; attempt++ {
		headers := make(http.Header)
		headers.Set("X-Upload-Content-Type", contentType)
		headers.Set("X-Upload-Content-Length", strconv.FormatInt(total, 10))

		resp, err := c.sendHeaders(ctx, op, http.MethodPost, u, headers, nil)
		if err != nil {
			lastErr = err
		} else {
			status := resp.StatusCode
			if status == http.StatusOK {
				loc := resp.Header.Get("Location")
				_ = resp.Body.Close()
				if loc == "" {
					return "", fmt.Errorf("gmail %s: resumable session without a Location", op)
				}
				base, parseErr := url.Parse(u)
				if parseErr != nil {
					return "", fmt.Errorf("gmail %s: session URI: %w", op, parseErr)
				}
				ref, parseErr := url.Parse(loc)
				if parseErr != nil {
					return "", fmt.Errorf("gmail %s: session URI: %w", op, parseErr)
				}
				return base.ResolveReference(ref).String(), nil
			}
			if status == http.StatusTooManyRequests || status >= 500 {
				_ = resp.Body.Close()
				lastErr = fmt.Errorf("gmail %s: start upload: %s", op, resp.Status)
			} else {
				apiErr := apiError(op, resp)
				_ = resp.Body.Close()
				return "", apiErr
			}
		}
		if attempt < uploadAttempts {
			if err := sleepContext(ctx, uploadRetryDelay); err != nil {
				return "", err
			}
		}
	}
	return "", lastErr
}

// sendChunk uploads data, which starts at start in the message. It resends the
// part the server has not confirmed yet, and reports done with the completion
// response when the whole message has been accepted.
func (c *client) sendChunk(ctx context.Context, op, session, contentType string, data []byte, start, total int64) (bool, *http.Response, error) {
	for attempt := 0; ; {
		resp, err := c.putChunk(ctx, op, session, contentType, data, start, total)
		if err == nil {
			switch {
			case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
				return true, resp, nil
			case resp.StatusCode == statusResumeIncomplete:
				end := start + int64(len(data))
				offset, rangeErr := rangeOffset(resp.Header.Get("Range"))
				_ = resp.Body.Close()
				if rangeErr != nil {
					return false, nil, fmt.Errorf("gmail %s: %w", op, rangeErr)
				}
				if offset <= start {
					// No progress: treat it like a failed attempt instead of
					// resending the same bytes forever.
					err = fmt.Errorf("gmail %s: upload made no progress", op)
					break
				}
				var stored bool
				data, start, stored, err = shiftChunk(data, start, offset, end)
				if err != nil {
					return false, nil, fmt.Errorf("gmail %s: %w", op, err)
				}
				if stored {
					return false, nil, nil
				}
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
				_ = resp.Body.Close()
				err = fmt.Errorf("gmail %s: upload chunk: %s", op, resp.Status)
			default:
				return false, nil, apiError(op, resp)
			}
		}
		if err == nil {
			continue
		}

		attempt++
		if attempt > uploadAttempts {
			return false, nil, err
		}
		if sleepErr := sleepContext(ctx, uploadRetryDelay); sleepErr != nil {
			return false, nil, sleepErr
		}
		offset, statusErr := c.uploadOffset(ctx, op, session, total)
		if statusErr != nil {
			// The status query failed as well; try the chunk again from where
			// it was, the session may still be alive.
			continue
		}
		if offset >= total {
			return true, nil, nil
		}
		end := start + int64(len(data))
		var stored bool
		data, start, stored, err = shiftChunk(data, start, offset, end)
		if err != nil {
			return false, nil, fmt.Errorf("gmail %s: %w", op, err)
		}
		if stored {
			return false, nil, nil
		}
	}
}

// shiftChunk aligns the remaining bytes of a chunk with the offset the server
// has confirmed. It reports stored when the whole chunk is already accepted.
func shiftChunk(data []byte, start, offset, end int64) ([]byte, int64, bool, error) {
	if offset >= end {
		return nil, end, true, nil
	}
	if offset > start {
		return data[offset-start:], offset, false, nil
	}
	if offset == start {
		return data, start, false, nil
	}
	return nil, start, false, fmt.Errorf("server confirmed offset %d before chunk start %d", offset, start)
}

// putChunk sends one range of the message. A non-nil error means the request
// did not complete; the response is nil then.
func (c *client) putChunk(ctx context.Context, op, session, contentType string, data []byte, start, total int64) (*http.Response, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", contentType)
	headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+int64(len(data))-1, total))
	return c.sendHeaders(ctx, op, http.MethodPut, session, headers, data)
}

// uploadOffset asks the session how many bytes it has stored, using the status
// query the resumable protocol defines for an interrupted upload.
func (c *client) uploadOffset(ctx context.Context, op, session string, total int64) (int64, error) {
	headers := make(http.Header)
	headers.Set("Content-Range", fmt.Sprintf("bytes */%d", total))
	resp, err := c.sendHeaders(ctx, op, http.MethodPut, session, headers, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case statusResumeIncomplete:
		return rangeOffset(resp.Header.Get("Range"))
	case http.StatusOK, http.StatusCreated:
		return total, nil
	default:
		return 0, apiError(op, resp)
	}
}

// rangeOffset reads the upper bound of a Range header ("bytes=0-524287") and
// returns the first byte the server still needs.
func rangeOffset(header string) (int64, error) {
	value, ok := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !ok || value == "" {
		return 0, fmt.Errorf("invalid Range header %q", header)
	}
	_, end, ok := strings.Cut(value, "-")
	if !ok {
		return 0, fmt.Errorf("invalid Range header %q", header)
	}
	last, err := strconv.ParseInt(strings.TrimSpace(end), 10, 64)
	if err != nil || last < 0 {
		return 0, fmt.Errorf("invalid Range header %q", header)
	}
	return last + 1, nil
}

// sleepContext waits for d unless the context ends first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
