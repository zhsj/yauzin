package gmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/mail"
)

// defaultBaseURL is the Gmail REST root. Tests point it at an httptest server.
const defaultBaseURL = "https://gmail.googleapis.com/gmail/v1"

// refreshSkew refreshes an access token this long before it actually expires,
// so a request is not sent with a token that dies in flight.
const refreshSkew = 60 * time.Second

// Store is the part of accounts.Store the backend needs. It is an interface so
// tests can hand in a fake without touching the disk.
type Store interface {
	// Credentials returns the OAuth client of the given backend, which for
	// this package is always mail.KindGmail.
	Credentials(kind mail.Kind) (accounts.OAuth, bool)
	Token(accountID string) (accounts.Token, bool)
	SaveToken(accountID string, t accounts.Token) error
}

// client performs the authorized REST calls of one account. It serializes
// token refreshes so a burst of requests refreshes once.
type client struct {
	accountID string
	baseURL   string
	// apiPath is the path part of baseURL, and batchURL is the batch endpoint of
	// the same API: Gmail accepts "origin/batch/apiPath" for multipart calls.
	apiPath  string
	batchURL string
	// uploadURL is the media upload root of the same API. Google serves media
	// uploads of every API under /upload, so a raw message goes there while the
	// metadata calls stay on apiPath.
	uploadURL string
	tokenURL  string
	store     Store
	http      *http.Client
	// ownTransport is the transport this client built for the account (the
	// proxy one), nil when the client shares another one. The logging wrapper
	// and the shared default transport must not be closed, so close works off
	// this field rather than off the http.Client it ended up with.
	ownTransport *http.Transport
	now          func() time.Time

	refreshMu sync.Mutex

	// reqSem holds one token while a Gmail API request is in flight. Every
	// request, single and batch alike, passes through it, so one account never
	// sends two Gmail calls at once: Gmail runs the calls embedded in a batch
	// concurrently and counts them against a per-user limit, and the sidebar
	// and the page used to overlap their batches and trip it.
	reqSem chan struct{}
}

// options collects the injected dependencies of Service.
type options struct {
	baseURL  string
	tokenURL string
	client   *http.Client
	proxyURL string
	now      func() time.Time
}

// Option customizes a Service.
type Option func(*options)

// WithBaseURL points the client at another API root.
func WithBaseURL(raw string) Option {
	return func(o *options) {
		if raw != "" {
			o.baseURL = strings.TrimRight(raw, "/")
		}
	}
}

// WithHTTPClient replaces the transport the client builds.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.client = c }
}

// WithProxyURL sends the account's requests through raw, which ParseProxy has
// already validated. Empty means a direct connection.
func WithProxyURL(raw string) Option {
	return func(o *options) { o.proxyURL = raw }
}

// WithNow replaces the clock used for the token expiry check.
func WithNow(now func() time.Time) Option {
	return func(o *options) {
		if now != nil {
			o.now = now
		}
	}
}

// newClient builds the REST client of one account.
func newClient(accountID string, store Store, opts []Option) (*client, error) {
	o := options{baseURL: defaultBaseURL, tokenURL: googleEndpoint.TokenURL, now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	base, err := url.Parse(o.baseURL)
	if err != nil {
		return nil, fmt.Errorf("gmail: parse base URL: %w", err)
	}
	apiPath := strings.TrimSuffix(base.Path, "/")
	c := &client{
		accountID: accountID,
		baseURL:   o.baseURL,
		apiPath:   apiPath,
		batchURL:  base.Scheme + "://" + base.Host + "/batch" + apiPath,
		uploadURL: base.Scheme + "://" + base.Host + "/upload" + apiPath,
		tokenURL:  o.tokenURL,
		store:     store,
		http:      o.client,
		now:       o.now,
		reqSem:    make(chan struct{}, 1),
	}
	if c.http == nil {
		tr, err := proxyTransport(o.proxyURL)
		if err != nil {
			return nil, err
		}
		if tr != nil {
			c.http = &http.Client{Transport: tr}
			c.ownTransport = tr
		} else {
			c.http = http.DefaultClient
		}
	}
	return c, nil
}

// close releases the idle connections of a transport this client owns.
func (c *client) close() {
	if c.ownTransport != nil {
		c.ownTransport.CloseIdleConnections()
	}
}

// acquireReq waits for this account's request slot. A cancelled request leaves
// the queue immediately instead of waiting for the request ahead of it.
func (c *client) acquireReq(ctx context.Context) error {
	select {
	case c.reqSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseReq gives the request slot back.
func (c *client) releaseReq() { <-c.reqSem }

// do performs one authorized JSON request without a body.
func (c *client) do(ctx context.Context, op, method, path string, query url.Values, out any) error {
	return c.doBody(ctx, op, method, path, query, nil, out)
}

// postJSON performs one authorized JSON POST with in as its body and decodes
// the answer into out when out is not nil.
func (c *client) postJSON(ctx context.Context, op, path string, in, out any) error {
	var body []byte
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("gmail %s: encode request: %w", op, err)
		}
		body = encoded
	}
	return c.doBody(ctx, op, http.MethodPost, path, nil, body, out)
}

// doBody performs one authorized JSON request. A 204 No Content answer is a
// success with no payload: Gmail answers a label-only messages.batchModify that
// way instead of returning the touched messages.
func (c *client) doBody(ctx context.Context, op, method, path string, query url.Values, body []byte, out any) error {
	return c.doRequest(ctx, op, method, c.baseURL, path, query, "application/json", body, out)
}

// doMediaUpload performs one authorized request whose body is the resource
// itself rather than JSON, such as the raw source of a message. The body goes to
// the media upload service, which avoids the base64 field of the JSON form and
// its size limit.
func (c *client) doMediaUpload(ctx context.Context, op, path, contentType string, body []byte, out any) error {
	return c.doRequest(ctx, op, http.MethodPost, c.uploadURL, path,
		url.Values{"uploadType": {"media"}}, contentType, body, out)
}

// streamDrain bounds how much of a response is read after a streamed handler
// has taken what it needs. What is left is the tail of the JSON document, so a
// full buffer is already more than enough to finish the transfer.
const streamDrain = 64 << 10

// doStream performs one authorized JSON GET and hands the response body to
// handle as it arrives, instead of decoding the whole document into a value.
// The body is counted for progress while handle reads it. Once handle is done
// the rest of the document is drained, so the transfer completes and the
// connection can be reused.
func (c *client) doStream(ctx context.Context, op, path string, query url.Values, progress func(mail.Progress), handle func(io.Reader) error) error {
	return c.doRequestHandle(ctx, op, http.MethodGet, c.baseURL, path, query, "application/json", nil, func(resp *http.Response) error {
		src := io.Reader(resp.Body)
		var counter *progressReader
		if progress != nil {
			counter = &progressReader{r: resp.Body, total: resp.ContentLength, report: progress}
			src = counter
		}
		if err := handle(src); err != nil {
			// A failed transfer is closed rather than drained: the rest of the
			// body cannot repair it.
			return err
		}
		_, _ = io.CopyN(io.Discard, src, streamDrain)
		if counter != nil {
			counter.finish()
		}
		return nil
	})
}

// doRequest performs one authorized request against one of the API roots. It
// refreshes the token once when the server rejects it.
func (c *client) doRequest(ctx context.Context, op, method, base, path string, query url.Values, contentType string, body []byte, out any) error {
	return c.doRequestProgress(ctx, op, method, base, path, query, contentType, body, out, nil)
}

// doRequestProgress is doRequest with an optional progress callback for the
// response body. A retried request starts its count from zero, so a caller that
// reports progress must keep it monotonic.
func (c *client) doRequestProgress(ctx context.Context, op, method, base, path string, query url.Values, contentType string, body []byte, out any, progress func(mail.Progress)) error {
	return c.doRequestHandle(ctx, op, method, base, path, query, contentType, body, func(resp *http.Response) error {
		if out == nil || resp.StatusCode == http.StatusNoContent {
			return nil
		}
		src := io.Reader(resp.Body)
		var counter *progressReader
		if progress != nil {
			counter = &progressReader{r: resp.Body, total: resp.ContentLength, report: progress}
			src = counter
		}
		if err := json.NewDecoder(src).Decode(out); err != nil {
			return fmt.Errorf("gmail %s: decode response: %w", op, err)
		}
		if counter != nil {
			counter.finish()
		}
		return nil
	})
}

// doRequestHandle performs one authorized request against one of the API roots
// and hands a successful response to handle. It refreshes the token once when
// the server rejects it and always closes the response body.
func (c *client) doRequestHandle(ctx context.Context, op, method, base, path string, query url.Values, contentType string, body []byte, handle func(*http.Response) error) error {
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, method, base, path, query, contentType, body, token)
	if err != nil {
		return fmt.Errorf("gmail %s: %w", op, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// The server rejected a token the clock still considered valid. Drop it
		// and try exactly once with a refreshed one.
		_ = resp.Body.Close()
		token, err = c.accessToken(ctx, true)
		if err != nil {
			return err
		}
		resp, err = c.send(ctx, method, base, path, query, contentType, body, token)
		if err != nil {
			return fmt.Errorf("gmail %s: %w", op, err)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return apiError(op, resp)
	}
	if handle == nil {
		return nil
	}
	return handle(resp)
}

// progressReportStep bounds how much a transfer without a known total may read
// between two progress callbacks.
const progressReportStep = 64 << 10

// progressReader counts the bytes of a response body and reports them at most
// once per percent (or per progressReportStep when the length is unknown), so a
// large attachment yields about a hundred callbacks instead of thousands.
type progressReader struct {
	r      io.Reader
	done   int64
	total  int64
	last   int64
	report func(mail.Progress)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.wantsReport() {
		p.report(mail.Progress{Done: p.done, Total: p.total})
		p.last = p.done
	}
	return n, err
}

func (p *progressReader) wantsReport() bool {
	if p.done == p.last {
		return false
	}
	if p.total <= 0 {
		return p.done-p.last >= progressReportStep
	}
	return p.done*100/p.total != p.last*100/p.total
}

// finish reports the final state once the payload has been decoded. The body
// is complete even when the last Read stopped short of the advertised length.
func (p *progressReader) finish() {
	done := p.done
	if p.total > 0 {
		done = p.total
	}
	if done != p.last {
		p.report(mail.Progress{Done: done, Total: p.total})
		p.last = done
	}
}

// send builds and performs one request with the given bearer token. A non-nil
// body is sent with contentType. It holds the account's request slot for the
// HTTP round trip, so single and batch requests never overlap.
func (c *client) send(ctx context.Context, method, base, path string, query url.Values, contentType string, body []byte, token string) (*http.Response, error) {
	if err := c.acquireReq(ctx); err != nil {
		return nil, err
	}
	defer c.releaseReq()

	u := base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	logRequest(c.accountID, method, req.URL.Path, resp, err)
	return resp, err
}

// logRequest reports one Gmail API call at DEBUG. endpoint is the API path
// without host or query: the query carries search terms and must not be
// logged. A failed round trip reports err instead of a status.
func logRequest(account, method, endpoint string, resp *http.Response, err error) {
	if err != nil {
		slog.Debug("gmail request",
			"account", logAccount(account), "method", method, "endpoint", endpoint, "err", err)
		return
	}
	slog.Debug("gmail request",
		"account", logAccount(account), "method", method, "endpoint", endpoint, "status", resp.StatusCode)
}

// logAccount is the account label of a log line. The backend prefix repeats
// what the message already says, so only the address is kept; an empty ID
// (no stored account yet) is shown as "-".
func logAccount(id string) string {
	if id == "" {
		return "-"
	}
	return strings.TrimPrefix(id, string(mail.KindGmail)+":")
}

// accessToken returns a usable access token, refreshing and persisting it when
// it is close to expiry or force is set.
func (c *client) accessToken(ctx context.Context, force bool) (string, error) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()

	stored, ok := c.store.Token(c.accountID)
	if !ok {
		return "", fmt.Errorf("%w: account %s has no token", mail.ErrUnauthorized, c.accountID)
	}
	tok := oauthToken(stored)
	// A token without an expiry is used as is: oauth2 treats a zero expiry as
	// "never expires", and Google always sends expires_in, so this only
	// affects hand-edited files.
	if !force && tok.Valid() && (tok.Expiry.IsZero() || c.now().Before(tok.Expiry.Add(-refreshSkew))) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", fmt.Errorf("%w: account %s has no refresh token", mail.ErrUnauthorized, c.accountID)
	}
	client, ok := c.store.Credentials(mail.KindGmail)
	if !ok || client.ClientID == "" {
		return "", fmt.Errorf("%w: OAuth client credentials are missing", mail.ErrUnauthorized)
	}
	cfg := &oauth2.Config{
		ClientID:     client.ClientID,
		ClientSecret: client.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL:  c.tokenURL,
			AuthStyle: googleEndpoint.AuthStyle,
		},
	}
	// A forced refresh needs a token oauth2 considers expired, or the token
	// source returns the rejected one unchanged. A zero expiry means "never
	// expires" to oauth2, so use a time in the past.
	if force {
		tok.Expiry = time.Now().Add(-time.Hour)
	}
	ctx = withHTTPClient(ctx, c.http)
	fresh, err := cfg.TokenSource(ctx, tok).Token()
	if err != nil {
		return "", authError("refresh access token", err)
	}
	if err := c.store.SaveToken(c.accountID, tokenFromOAuth(fresh)); err != nil {
		// The token works for this request; only the next start loses it.
		slog.Warn("persist refreshed token", "account", logAccount(c.accountID), "err", err)
	}
	return fresh.AccessToken, nil
}

// apiError maps a non-200 response onto the mail error categories. The body of
// a Google error carries a human readable message, which is kept as detail for
// anything that is not a category.
func apiError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var payload struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	detail := payload.Error.Message
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", mail.ErrNotFound, detail)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", mail.ErrInvalidArgument, detail)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", mail.ErrUnauthorized, detail)
	case http.StatusForbidden:
		// A token granted before a scope was added answers
		// 403 insufficientPermissions: the credential still works, the grant
		// does not, and only a new sign-in widens it. Every other 403
		// (rate limiting, a disabled account) stays uncategorised so the UI
		// does not send the user through a pointless re-authorization.
		for _, e := range payload.Error.Errors {
			if e.Reason == "insufficientPermissions" {
				return fmt.Errorf("%w: %s", mail.ErrUnauthorized, detail)
			}
		}
	}
	if detail == "" {
		return fmt.Errorf("gmail %s: %s", op, resp.Status)
	}
	return fmt.Errorf("gmail %s: %s: %s", op, resp.Status, detail)
}
