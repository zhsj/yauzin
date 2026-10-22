package gmail

import (
	"log/slog"
	"net/http"
)

// loggingTransport prints one DEBUG line per request: the endpoint, the account
// it belongs to and the HTTP status. Every backend request passes through it
// (REST calls, the label batch, token refreshes and the sign-in exchange), so
// it is the single place the network traffic of an account is reported.
//
// The line deliberately carries no query, header or body: search terms and
// credentials travel there and do not belong in a log.
type loggingTransport struct {
	base    http.RoundTripper
	account string
}

func (t loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		slog.Debug("gmail request",
			"account", t.account, "method", req.Method, "endpoint", endpoint(req), "err", err)
		return nil, err
	}
	slog.Debug("gmail request",
		"account", t.account, "method", req.Method, "endpoint", endpoint(req), "status", resp.StatusCode)
	return resp, nil
}

// endpoint is the host and path of the request, without the query string.
func endpoint(req *http.Request) string {
	if req.URL == nil {
		return ""
	}
	return req.URL.Host + req.URL.Path
}

// withRequestLogging returns a copy of c that sends its requests through the
// logging transport labelled with account. The copy keeps every other field
// (timeout, cookie jar, redirect policy), and the client handed in is left
// untouched: http.DefaultClient and the client a test injects must not be
// rewritten behind their back. An empty account (adding a new account, which
// has no stored ID yet) is shown as "-".
func withRequestLogging(c *http.Client, account string) *http.Client {
	if account == "" {
		account = "-"
	}
	base := http.RoundTripper(http.DefaultTransport)
	if c == nil {
		c = http.DefaultClient
	} else if c.Transport != nil {
		base = c.Transport
	}
	cp := *c
	cp.Transport = loggingTransport{base: base, account: account}
	return &cp
}
