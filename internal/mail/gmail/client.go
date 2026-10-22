package gmail

import (
	"context"
	"encoding/json"
	"errors"
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
	Credentials() (clientID, clientSecret string)
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
	tokenURL string
	store    Store
	http     *http.Client
	now      func() time.Time

	refreshMu sync.Mutex
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
		tokenURL:  o.tokenURL,
		store:     store,
		http:      o.client,
		now:       o.now,
	}
	if c.http == nil {
		tr, err := proxyTransport(o.proxyURL)
		if err != nil {
			return nil, err
		}
		if tr != nil {
			c.http = &http.Client{Transport: tr}
		} else {
			c.http = http.DefaultClient
		}
	}
	return c, nil
}

// close releases the idle connections of a transport this client owns.
func (c *client) close() {
	if c.http == nil || c.http == http.DefaultClient {
		return
	}
	if tr, ok := c.http.Transport.(*http.Transport); ok && tr != nil {
		tr.CloseIdleConnections()
	}
}

// do performs one authorized JSON request. force is unused: the retry logic
// below asks for it explicitly.
func (c *client) do(ctx context.Context, op, method, path string, query url.Values, out any) error {
	token, err := c.accessToken(ctx, false)
	if err != nil {
		return err
	}
	resp, err := c.send(ctx, method, path, query, token)
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
		resp, err = c.send(ctx, method, path, query, token)
		if err != nil {
			return fmt.Errorf("gmail %s: %w", op, err)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return apiError(op, resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gmail %s: decode response: %w", op, err)
	}
	return nil
}

// send builds and performs one request with the given bearer token.
func (c *client) send(ctx context.Context, method, path string, query url.Values, token string) (*http.Response, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
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
	clientID, clientSecret := c.store.Credentials()
	if clientID == "" {
		return "", fmt.Errorf("%w: OAuth client credentials are missing", mail.ErrUnauthorized)
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
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
		slog.Warn("persist refreshed token", "account", c.accountID, "err", err)
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
	}
	if detail == "" {
		return fmt.Errorf("gmail %s: %s", op, resp.Status)
	}
	return fmt.Errorf("gmail %s: %s: %s", op, resp.Status, detail)
}

// isNotFound reports the 404 category wrapped by apiError.
func isNotFound(err error) bool { return errors.Is(err, mail.ErrNotFound) }
