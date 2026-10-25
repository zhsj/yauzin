package gmail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// oauthServer serves the token endpoint, the profile endpoint and the userinfo
// endpoint of a sign-in.
type oauthServer struct {
	token    *httptest.Server
	profile  *httptest.Server
	userinfo *httptest.Server
	form     url.Values
	auths    []string
	// userinfoStatus and userinfoBody shape the name answer; zero values mean a
	// successful request for a known name.
	userinfoStatus int
	userinfoBody   string
}

func newOAuthServer(t *testing.T, tokenBody string, tokenStatus int) *oauthServer {
	t.Helper()
	o := &oauthServer{}
	o.token = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		o.form, _ = url.ParseQuery(string(body))
		writeErrorJSON(t, w, tokenStatus, tokenBody)
	}))
	t.Cleanup(o.token.Close)
	o.profile = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.auths = append(o.auths, r.Header.Get("Authorization"))
		if r.URL.Path != "/users/me/profile" {
			t.Errorf("profile path = %q", r.URL.Path)
		}
		writeJSON(t, w, `{"emailAddress":"user@example.com"}`)
	}))
	t.Cleanup(o.profile.Close)
	o.userinfo = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/userinfo" {
			t.Errorf("userinfo path = %q", r.URL.Path)
		}
		if o.userinfoStatus != 0 && o.userinfoStatus != http.StatusOK {
			writeErrorJSON(t, w, o.userinfoStatus, `{"error":{"code":403,"message":"insufficient scope"}}`)
			return
		}
		body := o.userinfoBody
		if body == "" {
			body = `{"name":"Ada Chen","email":"user@example.com"}`
		}
		writeJSON(t, w, body)
	}))
	t.Cleanup(o.userinfo.Close)
	return o
}

const tokenOK = `{"access_token":"access","token_type":"Bearer","refresh_token":"refresh","expires_in":3600}`

// browse launches a goroutine that plays the browser: it reads the
// authorization URL and calls the loopback redirect with the given query.
func browse(t *testing.T, extra url.Values) func(string) error {
	t.Helper()
	return func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			t.Errorf("authorization URL: %v", err)
			return nil
		}
		q := u.Query()
		if got := strings.Fields(q.Get("scope")); len(got) != 2 || got[0] != modifyScope || got[1] != profileScope {
			t.Errorf("scope = %q, want %q and %q", q.Get("scope"), modifyScope, profileScope)
		}
		if got := q.Get("code_challenge_method"); got != "S256" {
			t.Errorf("code_challenge_method = %q, want S256", got)
		}
		if q.Get("code_challenge") == "" {
			t.Error("authorization URL has no code_challenge")
		}
		if got := q.Get("access_type"); got != "offline" {
			t.Errorf("access_type = %q, want offline", got)
		}
		redirect := q.Get("redirect_uri")
		state := q.Get("state")
		params := url.Values{}
		for k, v := range extra {
			params[k] = v
		}
		params.Set("state", state)
		go func() {
			resp, err := http.Get(redirect + "?" + params.Encode())
			if err != nil {
				t.Logf("callback request: %v", err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
		}()
		return nil
	}
}

func testFlow(o *oauthServer) flow {
	return flow{
		authURL:     "https://auth.example/authorize",
		tokenURL:    o.token.URL,
		apiBaseURL:  o.profile.URL,
		userInfoURL: o.userinfo.URL + "/userinfo",
		client:      http.DefaultClient,
		openBrowser: func(string) error { return nil },
	}
}

func TestSignInCompletesThePKCEFlow(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)
	f.openBrowser = browse(t, url.Values{"code": {"test-code"}})

	res, err := signIn(context.Background(), f, SignInOptions{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		HTTPClient:   http.DefaultClient,
		OpenBrowser:  f.openBrowser,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("signIn: %v", err)
	}
	if res.Email != "user@example.com" {
		t.Fatalf("email = %q", res.Email)
	}
	if res.Name != "Ada Chen" {
		t.Fatalf("name = %q, want the Google profile name", res.Name)
	}
	if res.Token.AccessToken != "access" || res.Token.RefreshToken != "refresh" {
		t.Fatalf("token = %+v", res.Token)
	}
	if res.Token.ExpiresAt.IsZero() || !res.Token.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiry = %v, want in the future", res.Token.ExpiresAt)
	}
	if got := o.form.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q", got)
	}
	if got := o.form.Get("code"); got != "test-code" {
		t.Fatalf("code = %q", got)
	}
	if o.form.Get("code_verifier") == "" {
		t.Fatal("token request has no code_verifier")
	}
	if got := o.form.Get("client_id"); got != "client-id" {
		t.Fatalf("client_id = %q", got)
	}
	if got := o.form.Get("client_secret"); got != "client-secret" {
		t.Fatalf("client_secret = %q", got)
	}
	if got := o.form.Get("redirect_uri"); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Fatalf("redirect_uri = %q, want a loopback address", got)
	}
	if len(o.auths) != 1 || o.auths[0] != "Bearer access" {
		t.Fatalf("profile authorizations = %v", o.auths)
	}
}

func TestSignInWithoutProfileScopeStillSucceeds(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	// An account authorized before profileScope existed carries a grant the
	// userinfo endpoint rejects; the sign-in must still go through.
	o.userinfoStatus = http.StatusForbidden
	f := testFlow(o)
	f.openBrowser = browse(t, url.Values{"code": {"test-code"}})

	res, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		OpenBrowser: f.openBrowser,
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("signIn: %v", err)
	}
	if res.Name != "" {
		t.Fatalf("name = %q, want empty without profile scope", res.Name)
	}
	if res.Email != "user@example.com" {
		t.Fatalf("email = %q", res.Email)
	}
}

func TestSignInReportsTheAuthorizationURL(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)
	// No browser: the caller shows the URL and opens it on demand.
	f.openBrowser = nil

	var got string
	res, err := signIn(context.Background(), f, SignInOptions{
		ClientID: "client-id",
		OnAuthURL: func(raw string) {
			got = raw
			if err := browse(t, url.Values{"code": {"test-code"}})(raw); err != nil {
				t.Errorf("browse: %v", err)
			}
		},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("signIn: %v", err)
	}
	if res.Email != "user@example.com" {
		t.Fatalf("email = %q", res.Email)
	}
	if !strings.HasPrefix(got, "https://auth.example/authorize?") || !strings.Contains(got, "code_challenge=") {
		t.Fatalf("authorization URL = %q", got)
	}
}

func TestSignInNeedsAwayToReachTheConsentPage(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)
	f.openBrowser = nil

	_, err := signIn(context.Background(), f, SignInOptions{ClientID: "client-id", Timeout: time.Second})
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestSignInReportsDeniedConsent(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)
	f.openBrowser = browse(t, url.Values{"error": {"access_denied"}})

	_, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		OpenBrowser: f.openBrowser,
		Timeout:     2 * time.Second,
	})
	if !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("error = %v, want ErrAuthDenied", err)
	}
}

func TestSignInIgnoresCallbacksWithWrongState(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)
	f.openBrowser = func(raw string) error {
		u, _ := url.Parse(raw)
		redirect := u.Query().Get("redirect_uri")
		go func() {
			resp, err := http.Get(redirect + "?code=test-code&state=wrong")
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	// The mismatching callback is rejected, so the flow waits until its own
	// deadline instead of exchanging the code.
	_, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		OpenBrowser: f.openBrowser,
		Timeout:     150 * time.Millisecond,
	})
	if !errors.Is(err, ErrAuthTimeout) {
		t.Fatalf("error = %v, want ErrAuthTimeout", err)
	}
}

func TestSignInRefreshTokenRequired(t *testing.T) {
	o := newOAuthServer(t, `{"access_token":"access","token_type":"Bearer","expires_in":3600}`, http.StatusOK)
	f := testFlow(o)
	f.openBrowser = browse(t, url.Values{"code": {"test-code"}})

	_, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		OpenBrowser: f.openBrowser,
		Timeout:     2 * time.Second,
	})
	if !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestSignInMapsInvalidGrant(t *testing.T) {
	o := newOAuthServer(t, `{"error":"invalid_grant","error_description":"bad code"}`, http.StatusBadRequest)
	f := testFlow(o)
	f.openBrowser = browse(t, url.Values{"code": {"test-code"}})

	_, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		OpenBrowser: f.openBrowser,
		Timeout:     2 * time.Second,
	})
	if !errors.Is(err, mail.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestSignInUsesTheInjectedHTTPClient(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	f := testFlow(o)
	f.client = client
	f.openBrowser = browse(t, url.Values{"code": {"test-code"}})

	if _, err := signIn(context.Background(), f, SignInOptions{
		ClientID:    "client-id",
		HTTPClient:  client,
		OpenBrowser: f.openBrowser,
		Timeout:     2 * time.Second,
	}); err != nil {
		t.Fatalf("signIn: %v", err)
	}
	// Token exchange, address and name, all through the same client.
	if got := requests.Load(); got < 3 {
		t.Fatalf("client requests = %d, want at least 3", got)
	}
}

func TestSignInRequiresClientID(t *testing.T) {
	_, err := SignIn(context.Background(), SignInOptions{})
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestSignInBrowserFailure(t *testing.T) {
	o := newOAuthServer(t, tokenOK, http.StatusOK)
	f := testFlow(o)

	_, err := signIn(context.Background(), f, SignInOptions{
		ClientID: "client-id",
		OpenBrowser: func(string) error {
			return errors.New("no browser")
		},
		Timeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "no browser") {
		t.Fatalf("error = %v, want the browser failure", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
