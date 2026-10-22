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
	if s.page != settingsTabAccounts {
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

// TestAccountDialogCancelReturnsToSettings pins the way out of the wizard:
// cancel closes the dialog and shows the settings panel again on its accounts
// page instead of dropping the user into the mailbox.
func TestAccountDialogCancelReturnsToSettings(t *testing.T) {
	a := testApp(t)
	s := newSettingsView(a)
	a.showOverlay(s)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.accRows[len(s.accRows)-1].click.Click()
	s.Layout(gtx, a)

	d, ok := a.overlay.(*accountDialog)
	if !ok {
		t.Fatalf("overlay = %T after clicking add account, want *accountDialog", a.overlay)
	}
	ops.Reset()
	d.Layout(gtx, a)
	ops.Reset()

	d.cancel.click.Click()
	d.Layout(gtx, a)

	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T after cancel, want *settingsView", a.overlay)
	}
	if panel.page != settingsTabAccounts {
		t.Fatalf("page = %d after cancel, want the accounts tab", panel.page)
	}
	if a.accountAdd != nil {
		t.Fatal("the wizard is still reachable after cancel")
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

// TestAccountDialogWizardSteps covers the two-step flow: the dialog opens on
// the type list, a disabled type stays put and Gmail can be entered and left
// again.
func TestAccountDialogWizardSteps(t *testing.T) {
	a := testApp(t)
	d := newAccountDialog(a)
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)

	if d.step != accountStepType {
		t.Fatalf("step = %v, want the type list", d.step)
	}
	if len(d.typeRows) != 2 {
		t.Fatalf("type rows = %d, want Gmail and IMAP/SMTP", len(d.typeRows))
	}
	if !d.typeRows[1].disabled {
		t.Fatal("the IMAP/SMTP row is not disabled")
	}
	d.Layout(gtx, a)
	ops.Reset()

	d.typeRows[1].click.Click()
	d.Layout(gtx, a)
	if d.step != accountStepType {
		t.Fatalf("step = %v after clicking a disabled type, want the type list", d.step)
	}
	ops.Reset()

	d.typeRows[0].click.Click()
	d.Layout(gtx, a)
	if d.step != accountStepGmail {
		t.Fatalf("step = %v after clicking Gmail, want the form", d.step)
	}
	ops.Reset()

	d.back.click.Click()
	d.Layout(gtx, a)
	if d.step != accountStepType {
		t.Fatalf("step = %v after the back link, want the type list", d.step)
	}
}

// TestAccountDialogRequiresClientID keeps the sign-in button disabled until
// the OAuth client ID is typed.
func TestAccountDialogRequiresClientID(t *testing.T) {
	a := testApp(t)
	d := newAccountDialog(a)
	d.step = accountStepGmail
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
		Name:  "Ada Chen",
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
			if acc.Email != "User@Example.com" || acc.Name != "Ada Chen" {
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
	if entry.Kind != mail.KindGmail || !entry.UseProxy || entry.Token.RefreshToken != "refresh" || entry.Name != "Ada Chen" {
		t.Fatalf("stored entry = %+v", entry)
	}
	if o, ok := store.Credentials(mail.KindGmail); !ok || o.ClientID != "client-id" || o.ClientSecret != "client-secret" {
		t.Fatalf("stored credentials = %+v/%v", o, ok)
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

// TestAccountDialogAuthStepFrames lays out the authorization panel in both
// states: waiting without a link, and with the link that enables the copy and
// browser buttons.
func TestAccountDialogAuthStepFrames(t *testing.T) {
	a := testApp(t)
	d := newAccountDialog(a)
	d.step = accountStepAuth

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	d.Layout(gtx, a)
	if !d.copyLink.disabled || !d.openLink.disabled {
		t.Fatal("the copy/open buttons are enabled before the URL exists")
	}
	ops.Reset()

	d.authURL = "https://accounts.example/authorize?client_id=test"
	d.authField.SetText(d.authURL)
	d.Layout(gtx, a)
	if d.copyLink.disabled || d.openLink.disabled {
		t.Fatal("the copy/open buttons are disabled although the URL is shown")
	}

	// The copy click is carried to the frame that writes the clipboard.
	d.copyLink.click.Click()
	d.Layout(gtx, a)
	if d.copyPending {
		t.Fatal("the copy click was not consumed by the frame")
	}

	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Name: "Grace Hopper", Email: "user@example.com"}
	d.relogin = &target
	ops.Reset()
	d.Layout(gtx, a)
	if got := d.titleText(); got != i18n.T("settings.accountRelauthTitle") {
		t.Fatalf("title = %q, want the re-login heading", got)
	}
}

// TestRelauthRejectsAnotherMailbox keeps a "sign in again" click from writing a
// different Google account: the tokens of a row belong to that row's address,
// so nothing may be stored when the user picks another mailbox.
func TestRelauthRejectsAnotherMailbox(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	a := testApp(t)
	store, err := accounts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	a.accountStore = store

	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	d := newAccountDialog(a)
	d.relogin = &target
	d.finish(gmail.SignInResult{
		Email: "other@example.com",
		Token: accounts.Token{RefreshToken: "other"},
	}, "client-id", "client-secret")

	if d.err == "" {
		t.Fatal("signing in another address was accepted")
	}
	if _, ok := store.Entry(target.ID); ok {
		t.Fatal("the target account was written")
	}
	if _, ok := store.Entry("gmail:other@example.com"); ok {
		t.Fatal("another account was added")
	}
	if o, ok := store.Credentials(mail.KindGmail); ok {
		t.Fatalf("credentials = %+v, want untouched", o)
	}
	if a.accountAdd != nil {
		t.Fatal("the dialog closed although the re-login was rejected")
	}
}

// TestRelauthUpdatesTheAccountInPlace checks the successful re-login: the
// target row gains the name and the new token, the mailbox on screen does not
// switch, and the dialog returns to the accounts page.
func TestRelauthUpdatesTheAccountInPlace(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts:   []mail.Account{demoAccounts()[0], target},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
	})
	store, err := accounts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	entry := accounts.Entry{
		ID:    target.ID,
		Kind:  mail.KindGmail,
		Email: target.Email,
		Name:  "Old Name",
		Token: accounts.Token{RefreshToken: "old"},
	}
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	a.accountStore = store
	a.showSettingsPage(settingsTabAccounts)
	active := a.activeAcc

	d := newAccountDialog(a)
	d.relogin = &target
	d.finish(gmail.SignInResult{
		Email: "User@Example.com",
		Name:  "Grace Hopper",
		Token: accounts.Token{AccessToken: "new", RefreshToken: "new-refresh"},
	}, "client-id", "client-secret")

	if d.err != "" {
		t.Fatalf("finish: %s", d.err)
	}
	if a.activeAcc != active {
		t.Fatalf("active account switched to %q", a.activeAcc)
	}
	stored, ok := store.Entry(target.ID)
	if !ok {
		t.Fatal("the refreshed account vanished from accounts.json")
	}
	if stored.Name != "Grace Hopper" || stored.Token.RefreshToken != "new-refresh" {
		t.Fatalf("stored entry = %+v", stored)
	}
	if got := len(store.List()); got != 1 {
		t.Fatalf("stored accounts = %d, want 1", got)
	}
	for _, acc := range a.accounts {
		if acc.ID == target.ID && acc.Name != "Grace Hopper" {
			t.Fatalf("account = %+v, want the profile name", acc)
		}
	}
	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T after a re-login, want the settings panel", a.overlay)
	}
	if panel.page != settingsTabAccounts {
		t.Fatalf("page = %d, want the accounts page", panel.page)
	}
}

// TestRelauthRowOpensTheAuthorizationStep wires the refresh button to the
// dialog: it opens on the URL step with the target account remembered, so the
// link and the account name are shown without asking for the client again.
func TestRelauthRowOpensTheAuthorizationStep(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts:   []mail.Account{demoAccounts()[0], target},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
	})
	store, err := accounts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.SetCredentials(accounts.OAuth{Provider: mail.KindGmail, ClientID: "client-id", ClientSecret: "client-secret"}); err != nil {
		t.Fatalf("store credentials: %v", err)
	}
	if err := store.Upsert(accounts.Entry{ID: target.ID, Kind: mail.KindGmail, Email: target.Email}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	a.accountStore = store

	s := newSettingsView(a)
	a.showOverlay(s)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	ops.Reset()

	accountDetailsOf(t, s, target.DisplayName()).relogin.click.Click()
	s.Layout(gtx, a)

	d, ok := a.overlay.(*accountDialog)
	if !ok {
		t.Fatalf("overlay = %T after the refresh button, want *accountDialog", a.overlay)
	}
	if d.relogin == nil || d.relogin.ID != target.ID {
		t.Fatalf("re-login target = %+v, want %q", d.relogin, target.ID)
	}
	if d.step != accountStepAuth {
		t.Fatalf("step = %v, want the authorization step", d.step)
	}
	a.hideOverlay()
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
