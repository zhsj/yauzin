package gmail

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/netproxy"
)

const (
	// modifyScope covers everything Service does except permanent deletion:
	// reading, and adding or removing labels, which is how the app stars,
	// archives, marks read and moves a message to the trash. The wider
	// https://mail.google.com/ is deliberately not requested: only permanent
	// deletion needs it, and Delete moves mail to the trash instead.
	modifyScope = "https://www.googleapis.com/auth/gmail.modify"
	// profileScope is what makes the Google account name available. It is the
	// only addition: no ID token is needed, so openid stays out of the grant and
	// the OAuth 2.0 userinfo endpoint returns the name for this scope alone.
	profileScope = "https://www.googleapis.com/auth/userinfo.profile"
)

// defaultUserInfoURL is the OAuth 2.0 profile endpoint of Google.
const defaultUserInfoURL = "https://www.googleapis.com/oauth2/v3/userinfo"

// requestedScopes is the grant of a sign-in. Growing this list makes Google
// show the consent screen again, so accounts authorized with an older list have
// to sign in once more: a token granted gmail.readonly alone can still read the
// mailbox but every write answers 403 insufficientPermissions, which the client
// reports as mail.ErrUnauthorized.
func requestedScopes() []string { return []string{modifyScope, profileScope} }

// googleEndpoint is Google's installed-application OAuth endpoint. The client
// credentials travel in the request body (AuthStyleInParams), which is what
// Google's "Desktop app" client type expects.
var googleEndpoint = oauth2.Endpoint{
	AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:  "https://oauth2.googleapis.com/token",
	AuthStyle: oauth2.AuthStyleInParams,
}

var (
	// ErrAuthDenied reports that the user rejected the consent screen.
	ErrAuthDenied = errors.New("gmail: authorization denied")
	// ErrAuthTimeout reports that the browser never called back in time.
	ErrAuthTimeout = errors.New("gmail: authorization timed out")
)

// SignInOptions configures one browser sign-in.
type SignInOptions struct {
	ClientID     string
	ClientSecret string
	// HTTPClient carries the token exchange and the profile request. A client
	// with a proxy is how an account that opts into the shared proxy signs in.
	HTTPClient *http.Client
	// OnAuthURL receives the consent URL as soon as the loopback listener is
	// ready. The UI shows it so the user can copy it or open it with a button;
	// it is called on the sign-in goroutine and must not block.
	OnAuthURL func(url string)
	// OpenBrowser hands the authorization URL to the system browser. Optional:
	// a caller that presents the URL itself leaves it nil, and then OnAuthURL
	// has to be set instead.
	OpenBrowser func(url string) error
	// Timeout bounds the wait for the browser callback. Zero means defaultTimeout.
	Timeout time.Duration
}

// SignInResult is the outcome of a successful authorization.
type SignInResult struct {
	// Email is the address the token belongs to, from users.getProfile.
	Email string
	// Name is the Google account name. It is empty when the grant carries no
	// profileScope (an account authorized before it was requested) or the
	// profile request failed.
	Name  string
	Token accounts.Token
}

// defaultAuthTimeout is how long the loopback server waits for the callback.
const defaultAuthTimeout = 5 * time.Minute

// errStateMismatch reports a callback that did not echo our state value.
var errStateMismatch = errors.New("gmail: OAuth state mismatch")

// flow collects the pieces a test replaces: the endpoints, the listener and
// the browser. Production uses defaultFlow.
type flow struct {
	authURL     string
	tokenURL    string
	apiBaseURL  string
	userInfoURL string
	client      *http.Client
	openBrowser func(string) error
	listen      func(network, addr string) (net.Listener, error)
}

func defaultFlow() flow {
	return flow{
		authURL:     googleEndpoint.AuthURL,
		tokenURL:    googleEndpoint.TokenURL,
		apiBaseURL:  defaultBaseURL,
		userInfoURL: defaultUserInfoURL,
		client:      http.DefaultClient,
		listen:      net.Listen,
	}
}

// SignIn runs the authorization code flow with PKCE: it opens the system
// browser at Google's consent page, waits for the redirect to a loopback
// listener, exchanges the code and reads the account address.
//
// The loopback listener binds 127.0.0.1 only and never goes through the proxy;
// the token exchange and the profile request use opts.HTTPClient.
func SignIn(ctx context.Context, opts SignInOptions) (SignInResult, error) {
	return signIn(ctx, defaultFlow(), opts)
}

func signIn(ctx context.Context, f flow, opts SignInOptions) (SignInResult, error) {
	clientID := strings.TrimSpace(opts.ClientID)
	if clientID == "" {
		return SignInResult{}, fmt.Errorf("%w: client ID is empty", mail.ErrInvalidArgument)
	}
	if opts.HTTPClient != nil {
		f.client = opts.HTTPClient
	}
	if f.client == nil {
		f.client = http.DefaultClient
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultAuthTimeout
	}
	if f.listen == nil {
		f.listen = net.Listen
	}

	ln, err := f.listen("tcp", "127.0.0.1:0")
	if err != nil {
		return SignInResult{}, fmt.Errorf("gmail: listen for OAuth callback: %w", err)
	}
	defer func() { _ = ln.Close() }()

	redirectURL := "http://" + ln.Addr().String() + "/oauth2/callback"
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: strings.TrimSpace(opts.ClientSecret),
		RedirectURL:  redirectURL,
		Scopes:       requestedScopes(),
		Endpoint: oauth2.Endpoint{
			AuthURL:   f.authURL,
			TokenURL:  f.tokenURL,
			AuthStyle: googleEndpoint.AuthStyle,
		},
	}

	state, err := randomState()
	if err != nil {
		return SignInResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "consent"),
	)

	codeCh := make(chan string, 1)
	deniedCh := make(chan struct{}, 1)
	srv := &http.Server{
		Handler: callbackHandler(state, codeCh, deniedCh),
		// No ReadTimeout: the browser may take minutes to arrive.
	}
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("oauth callback server shutdown", "err", err)
		}
	}()

	if opts.OpenBrowser != nil {
		f.openBrowser = opts.OpenBrowser
	}
	if opts.OnAuthURL == nil && f.openBrowser == nil {
		// Without a way to reach the consent page the flow would wait for a
		// callback nobody can trigger.
		return SignInResult{}, fmt.Errorf("%w: no browser opener or authorization URL handler", mail.ErrInvalidArgument)
	}
	if opts.OnAuthURL != nil {
		opts.OnAuthURL(authURL)
	}
	if f.openBrowser != nil {
		if err := f.openBrowser(authURL); err != nil {
			return SignInResult{}, fmt.Errorf("gmail: open browser: %w", err)
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var code string
	select {
	case code = <-codeCh:
	case <-deniedCh:
		return SignInResult{}, ErrAuthDenied
	case err := <-serveErr:
		return SignInResult{}, fmt.Errorf("gmail: serve OAuth callback: %w", err)
	case <-waitCtx.Done():
		if ctx.Err() != nil {
			return SignInResult{}, ctx.Err()
		}
		return SignInResult{}, ErrAuthTimeout
	}

	exchangeCtx := context.WithValue(waitCtx, oauth2.HTTPClient, f.client)
	tok, err := cfg.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return SignInResult{}, authError("exchange authorization code", err)
	}
	if tok.RefreshToken == "" {
		// Without a refresh token the account would die with the first access
		// token; prompt=consent makes Google issue one for a fresh grant, so an
		// empty value points at a misconfigured client.
		return SignInResult{}, fmt.Errorf("%w: token has no refresh token; check that the OAuth client is a desktop app", mail.ErrUnauthorized)
	}

	email, err := profileEmail(exchangeCtx, f, tok)
	if err != nil {
		return SignInResult{}, err
	}
	return SignInResult{Email: email, Name: profileName(exchangeCtx, f, tok), Token: tokenFromOAuth(tok)}, nil
}

// callbackHandler serves the single redirect Google makes after consent.
// Wrong paths and wrong states are rejected without touching the channels, so
// a stray request cannot finish the flow.
func callbackHandler(state string, codeCh chan<- string, deniedCh chan<- struct{}) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if errCode := q.Get("error"); errCode != "" {
			writeCallbackPage(w, "Authorization was cancelled. You can close this window.")
			select {
			case deniedCh <- struct{}{}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		select {
		case codeCh <- code:
		default:
		}
		writeCallbackPage(w, "Authorization complete. You can close this window and return to Yauzin.")
	})
	return mux
}

func writeCallbackPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Yauzin</title></head>`+
		`<body style="font-family:sans-serif;padding:3em;color:#202124"><p>`+message+`</p></body></html>`)
}

// randomState returns the CSRF nonce of the authorization request.
func randomState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("gmail: generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// authError turns an oauth2 exchange or refresh failure into the category the
// UI can translate, while keeping the server's own explanation as detail.
func authError(op string, err error) error {
	var retr *oauth2.RetrieveError
	if errors.As(err, &retr) {
		switch retr.ErrorCode {
		case "invalid_grant", "invalid_client", "unauthorized_client":
			return fmt.Errorf("%w: %s: %s", mail.ErrUnauthorized, op, retr.ErrorCode)
		}
	}
	return fmt.Errorf("gmail: %s: %w", op, err)
}

// profileEmail reads the address of the authorized account.
func profileEmail(ctx context.Context, f flow, tok *oauth2.Token) (string, error) {
	client := oauth2.NewClient(withHTTPClient(ctx, f.client), oauth2.StaticTokenSource(tok))
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := getJSON(ctx, client, strings.TrimRight(f.apiBaseURL, "/")+"/users/me/profile", &profile); err != nil {
		return "", fmt.Errorf("gmail: read profile: %w", err)
	}
	if profile.EmailAddress == "" {
		return "", fmt.Errorf("%w: profile has no email address", mail.ErrUnauthorized)
	}
	return profile.EmailAddress, nil
}

// profileName reads the display name of the authorized account. It is best
// effort: the profile endpoint rejects a grant without profileScope, which is
// exactly what an account authorized before the scope existed carries, so the
// caller falls back to the address instead of failing the sign-in.
func profileName(ctx context.Context, f flow, tok *oauth2.Token) string {
	client := oauth2.NewClient(withHTTPClient(ctx, f.client), oauth2.StaticTokenSource(tok))
	var profile struct {
		Name string `json:"name"`
	}
	if err := getJSON(ctx, client, f.userInfoURL, &profile); err != nil {
		slog.Warn("gmail: read profile name", "err", err)
		return ""
	}
	return strings.TrimSpace(profile.Name)
}

// getJSON performs one authorized GET and decodes a JSON object.
func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// withHTTPClient puts client into ctx the way oauth2 expects.
func withHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

// tokenFromOAuth converts the library token into the stored shape.
func tokenFromOAuth(t *oauth2.Token) accounts.Token {
	return accounts.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		ExpiresAt:    t.Expiry,
	}
}

// oauthToken converts the stored token back for oauth2.
func oauthToken(t accounts.Token) *oauth2.Token {
	tok := &oauth2.Token{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.ExpiresAt,
	}
	if tok.TokenType == "" {
		tok.TokenType = "Bearer"
	}
	return tok
}

// ParseProxy validates a proxy address from the settings and returns it in the
// form http.Transport expects. The rules live in netproxy, which the SMTP
// sender shares, so both transports accept exactly the same addresses.
func ParseProxy(raw string) (*url.URL, error) {
	return netproxy.Parse(raw)
}

// HTTPClient returns a client that sends its requests through the proxy in
// raw, or http.DefaultClient when raw is empty. The sign-in flow needs it
// before a Service exists.
func HTTPClient(raw string) (*http.Client, error) {
	tr, err := proxyTransport(raw)
	if err != nil {
		return nil, err
	}
	if tr == nil {
		return http.DefaultClient, nil
	}
	return &http.Client{Transport: tr}, nil
}

// proxyTransport returns a transport that sends its requests through raw, or
// nil when raw is empty. Tests read the Proxy field to assert the choice.
func proxyTransport(raw string) (*http.Transport, error) {
	u, err := ParseProxy(raw)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(u)
	return tr, nil
}
