package ui

import (
	"errors"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/gmail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// TestAddAccountRowOpensTheDialog pins the entry point: the last row of the
// Accounts page opens the sign-in card instead of a placeholder toast.
func TestAddAccountRowOpensTheDialog(t *testing.T) {
	a := testApp(t)
	s := newSettingsView(a)
	a.showOverlay(s)

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.tabs[1].click.Click()
	s.Layout(gtx, a)
	if s.page != 1 {
		t.Fatalf("page = %d, want the accounts page", s.page)
	}
	ops.Reset()

	last := s.accRows[len(s.accRows)-1]
	last.click.Click()
	s.Layout(gtx, a)

	if _, ok := a.overlay.(*accountDialog); !ok {
		t.Fatalf("overlay = %T after clicking add account, want *accountDialog", a.overlay)
	}
	if a.accountAdd == nil {
		t.Fatal("the open dialog is not reachable from App")
	}
}

// TestAccountDialogFollowsProxySetting checks that an unset proxy hides the
// switch and a configured one turns it on by default.
func TestAccountDialogFollowsProxySetting(t *testing.T) {
	a := testApp(t)
	d := newAccountDialog(a)
	if d.hasProxy || d.useProxy || d.proxySwitch != nil {
		t.Fatalf("without a proxy: hasProxy=%v useProxy=%v switch=%v", d.hasProxy, d.useProxy, d.proxySwitch)
	}

	a.proxy = "socks5://127.0.0.1:1080"
	d = newAccountDialog(a)
	if !d.hasProxy || !d.useProxy || d.proxySwitch == nil {
		t.Fatalf("with a proxy: hasProxy=%v useProxy=%v switch=%v", d.hasProxy, d.useProxy, d.proxySwitch)
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	d.Layout(gtx, a)
}

// TestAccountDialogRequiresClientID keeps the sign-in button disabled until
// the OAuth client ID is typed.
func TestAccountDialogRequiresClientID(t *testing.T) {
	a := testApp(t)
	d := newAccountDialog(a)
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)

	d.Layout(gtx, a)
	if !d.signIn.disabled {
		t.Fatal("sign-in is enabled without a client ID")
	}

	d.clientID.SetText("client-id")
	ops.Reset()
	d.Layout(gtx, a)
	if d.signIn.disabled {
		t.Fatal("sign-in is still disabled with a client ID")
	}
}

// TestAccountDialogStoresTheAccount covers the post-authorization step: the
// credentials, the token and the per-account proxy choice end up on disk and
// the new mailbox becomes active.
func TestAccountDialogStoresTheAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)

	a := testApp(t)
	store, err := accounts.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	a.accountStore = store
	a.newService = func(mail.Account) mail.Service { return &fakeService{} }

	d := newAccountDialog(a)
	d.useProxy = true
	d.finish(gmail.SignInResult{
		Email: "User@Example.com",
		Token: accounts.Token{AccessToken: "access", RefreshToken: "refresh", TokenType: "Bearer"},
	}, "client-id", "client-secret")

	id := "gmail:user@example.com"
	if a.activeAcc != id {
		t.Fatalf("active account = %q, want %q", a.activeAcc, id)
	}
	if _, ok := a.services[id]; !ok {
		t.Fatal("the new account has no backend")
	}
	var found bool
	for _, acc := range a.accounts {
		if acc.ID == id {
			found = true
			if acc.Email != "User@Example.com" {
				t.Fatalf("account = %+v", acc)
			}
		}
	}
	if !found {
		t.Fatalf("accounts = %+v, want the new one", a.accounts)
	}

	entry, ok := store.Entry(id)
	if !ok {
		t.Fatal("the account is missing from accounts.json")
	}
	if !entry.UseProxy || entry.Token.RefreshToken != "refresh" {
		t.Fatalf("stored entry = %+v", entry)
	}
	if clientID, secret := store.Credentials(); clientID != "client-id" || secret != "client-secret" {
		t.Fatalf("stored credentials = %q/%q", clientID, secret)
	}
}

// TestAccountDialogReusesExistingAccount checks the sign-in of a mailbox that
// is already configured: the entry is replaced, not duplicated.
func TestAccountDialogReusesExistingAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)
	a := testApp(t)
	store, err := accounts.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	a.accountStore = store
	a.newService = func(mail.Account) mail.Service { return &fakeService{} }

	res := gmail.SignInResult{
		Email: "user@example.com",
		Token: accounts.Token{AccessToken: "access", RefreshToken: "refresh"},
	}
	d := newAccountDialog(a)
	d.finish(res, "client-id", "")
	d.finish(res, "client-id", "")

	var count int
	for _, acc := range a.accounts {
		if acc.ID == "gmail:user@example.com" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the account appears %d times, want 1", count)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("stored accounts = %d, want 1", got)
	}
}

// TestSettingsProxyValidationAndSave covers the network section: a malformed
// address is rejected in place, a valid one is stored in settings.json.
func TestSettingsProxyValidationAndSave(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	a := testApp(t)
	s := newSettingsView(a)
	a.showOverlay(s)

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)

	s.proxy.SetText("127.0.0.1:8080")
	s.proxySave.click.Click()
	s.Layout(gtx, a)
	if s.proxyErr == "" {
		t.Fatal("a proxy address without a scheme was accepted")
	}
	if a.proxy != "" {
		t.Fatalf("proxy = %q after a rejected address, want empty", a.proxy)
	}
	ops.Reset()

	s.proxy.SetText("http://127.0.0.1:8080")
	s.proxySave.click.Click()
	s.Layout(gtx, a)
	if s.proxyErr != "" {
		t.Fatalf("valid address reported %q", s.proxyErr)
	}
	if a.proxy != "http://127.0.0.1:8080" {
		t.Fatalf("proxy = %q", a.proxy)
	}
	pref, err := prefs.LoadOrZero()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if pref.Proxy != "http://127.0.0.1:8080" {
		t.Fatalf("stored proxy = %q", pref.Proxy)
	}
}

func TestSignInErrorText(t *testing.T) {
	previous := i18n.Language()
	defer i18n.SetLanguage(previous)
	i18n.SetLanguage("en")

	for _, tc := range []struct {
		err  error
		want string
	}{
		{gmail.ErrAuthDenied, i18n.T("error.authDenied")},
		{gmail.ErrAuthTimeout, i18n.T("error.authTimeout")},
		{mail.ErrUnauthorized, i18n.T("error.unauthorized")},
		{errors.New("dial tcp: refused"), "dial tcp: refused"},
	} {
		if got := signInErrorText(tc.err); got != tc.want {
			t.Fatalf("signInErrorText(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
