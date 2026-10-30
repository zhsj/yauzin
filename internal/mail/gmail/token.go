package gmail

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/oauth2"

	"github.com/zhsj/yauzin/internal/mail"
)

// refreshSkew refreshes an access token this long before it actually expires,
// so a request is not sent with a token that dies in flight.
const refreshSkew = 60 * time.Second

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
	if !force && tok.Valid() && (tok.Expiry.IsZero() || time.Now().Before(tok.Expiry.Add(-refreshSkew))) {
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
