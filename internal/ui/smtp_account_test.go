package ui

import (
	"testing"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestSMTPDialogRejectsIncompleteForm checks that nothing reaches the store
// until the form describes a server it could actually talk to.
func TestSMTPDialogRejectsIncompleteForm(t *testing.T) {
	a, store := smtpTestApp(t)
	d := newSMTPDialog(a, nil)

	d.email.SetText("me@example.com")
	d.submit()
	if d.err == "" {
		t.Fatal("an empty host was accepted")
	}

	d.host.SetText("smtp.example.com")
	d.port.SetText("70000")
	d.submit()
	if d.err == "" {
		t.Fatal("an out-of-range port was accepted")
	}

	d.port.SetText("587")
	d.email.SetText("not an address")
	d.submit()
	if d.err == "" {
		t.Fatal("an invalid sender address was accepted")
	}

	if entries := store.List(); len(entries) != 0 {
		t.Fatalf("store holds %+v, want nothing", entries)
	}
}

// TestSMTPDialogStoresAccount covers the happy path: the form writes one
// sending entry and returns to the settings panel it came from.
func TestSMTPDialogStoresAccount(t *testing.T) {
	a, store := smtpTestApp(t)
	d := newSMTPDialog(a, nil)
	d.host.SetText("smtp.example.com")
	d.port.SetText("465")
	d.username.SetText("me@example.com")
	d.password.SetText("secret")
	d.name.SetText("Ada Chen")
	d.email.SetText("Ada@Example.com")
	d.setSecurity("tls")
	d.proxySwitch.sw.on = false
	d.submit()

	if d.err != "" {
		t.Fatalf("submit failed: %s", d.err)
	}
	entry, ok := store.Entry("smtp:ada@example.com")
	if !ok {
		t.Fatalf("stored entries = %+v, want the sending account", store.List())
	}
	if entry.Kind != mail.KindSMTP || entry.Email != "Ada@Example.com" || entry.Name != "Ada Chen" {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.UseProxy {
		t.Fatal("the proxy switch was stored as on")
	}
	cfg := entry.SMTP
	if cfg == nil {
		t.Fatal("the entry has no server")
	}
	if cfg.Host != "smtp.example.com" || cfg.Port != 465 || cfg.Security != "tls" {
		t.Fatalf("server = %+v", cfg)
	}
	if cfg.Username != "me@example.com" || cfg.Password != "secret" {
		t.Fatalf("credentials = %q/%q", cfg.Username, cfg.Password)
	}
	if !cfg.BccToAccount {
		t.Fatal("the default blind copy was lost")
	}
	if _, ok := a.overlay.(*settingsView); !ok {
		t.Fatalf("overlay = %T after saving, want the settings panel", a.overlay)
	}
}

// TestSMTPDialogRenamesOnAddressChange keeps one entry per sender: editing the
// address moves the entry instead of leaving the old one behind.
func TestSMTPDialogRenamesOnAddressChange(t *testing.T) {
	a, store := smtpTestApp(t)
	old := accounts.Entry{
		ID:    "smtp:old@example.com",
		Kind:  mail.KindSMTP,
		Email: "old@example.com",
		Name:  "Old",
		SMTP:  &accounts.SMTPConfig{Host: "smtp.example.com", Port: 587, Security: "starttls"},
	}
	if err := store.Upsert(old); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	d := newSMTPDialog(a, &old)
	d.email.SetText("new@example.com")
	d.submit()

	if d.err != "" {
		t.Fatalf("submit failed: %s", d.err)
	}
	if _, ok := store.Entry(old.ID); ok {
		t.Fatal("the entry kept its old ID")
	}
	entry, ok := store.Entry("smtp:new@example.com")
	if !ok || entry.Email != "new@example.com" {
		t.Fatalf("renamed entry = %+v/%v", entry, ok)
	}
}

// TestSMTPDialogPrefillsFromEntry checks that editing starts from the stored
// server, not from the defaults of a new one.
func TestSMTPDialogPrefillsFromEntry(t *testing.T) {
	a, _ := smtpTestApp(t)
	entry := accounts.Entry{
		ID:       "smtp:me@example.com",
		Kind:     mail.KindSMTP,
		Email:    "me@example.com",
		Name:     "Me",
		UseProxy: true,
		SMTP: &accounts.SMTPConfig{
			Host:         "smtp.example.com",
			Port:         2525,
			Username:     "me",
			Password:     "secret",
			Security:     "none",
			BccToAccount: false,
		},
	}
	d := newSMTPDialog(a, &entry)

	if got := d.host.Text(); got != "smtp.example.com" {
		t.Fatalf("host = %q", got)
	}
	if got := d.port.Text(); got != "2525" {
		t.Fatalf("port = %q, want the stored one", got)
	}
	if d.security != "none" {
		t.Fatalf("security = %q", d.security)
	}
	if got := d.username.Text(); got != "me" {
		t.Fatalf("username = %q", got)
	}
	if d.bccSwitch.on() {
		t.Fatal("the blind copy switch ignores the stored choice")
	}
	if !d.proxySwitch.on() {
		t.Fatal("the proxy switch ignores the stored choice")
	}
}

// TestSMTPDialogSecurityDrivesPort checks the port follows the encryption until
// the user types one of their own.
func TestSMTPDialogSecurityDrivesPort(t *testing.T) {
	a, _ := smtpTestApp(t)
	d := newSMTPDialog(a, nil)
	if got := d.port.Text(); got != "587" {
		t.Fatalf("default port = %q, want 587 for STARTTLS", got)
	}
	d.setSecurity("tls")
	if got := d.port.Text(); got != "465" {
		t.Fatalf("port = %q, want 465 after switching to TLS", got)
	}
	d.port.SetText("2525")
	d.setSecurity("none")
	if got := d.port.Text(); got != "2525" {
		t.Fatalf("port = %q, want the typed port kept", got)
	}
	d.port.SetText("25")
	d.setSecurity("starttls")
	if got := d.port.Text(); got != "587" {
		t.Fatalf("port = %q, want 587 after leaving the conventional 25", got)
	}
}

// TestSMTPDialogKeepProxySwitchHonest hides the switch when no proxy is
// configured instead of letting the form store a useless "use proxy".
func TestSMTPDialogKeepProxySwitchHonest(t *testing.T) {
	a, store := smtpTestApp(t)
	a.proxy = ""
	d := newSMTPDialog(a, nil)
	if !d.proxySwitch.disabled {
		t.Fatal("the proxy switch is enabled without a proxy")
	}
	if d.proxySwitch.on() {
		t.Fatal("the proxy switch defaults to on without a proxy")
	}
	d.host.SetText("smtp.example.com")
	d.email.SetText("me@example.com")
	d.submit()
	entry, ok := store.Entry("smtp:me@example.com")
	if !ok {
		t.Fatalf("stored entries = %+v", store.List())
	}
	if entry.UseProxy {
		t.Fatal("an entry without a proxy was stored with the switch on")
	}
}

// smtpTestApp returns an App with an empty account store in a temporary
// configuration directory.
func smtpTestApp(t *testing.T) (*App, *accounts.Store) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)
	a := testApp(t)
	store, err := accounts.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	a.accountStore = store
	a.proxy = "socks5://127.0.0.1:1080"
	return a, store
}
