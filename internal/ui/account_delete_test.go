package ui

import (
	"testing"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// closeSpyService records that the backend of a removed account was released.
type closeSpyService struct {
	fakeService
	closed bool
}

func (s *closeSpyService) Close() error {
	s.closed = true
	return nil
}

// newDeleteTestApp assembles an app whose accounts are also in a real store, so
// the settings page can offer the delete control. The demo mailbox is left out
// of the store, exactly like a configured install.
func newDeleteTestApp(t *testing.T, accs []mail.Account) (*App, *accounts.Store) {
	t.Helper()
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts:   accs,
		newService: func(mail.Account) mail.Service { return &closeSpyService{} },
	})
	store, err := accounts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, acc := range accs {
		if acc.ID == demoAccountID {
			continue
		}
		entry := accounts.Entry{ID: acc.ID, Kind: acc.Kind, Email: acc.Email, Name: acc.Name}
		entry.Token = accounts.Token{AccessToken: "access", RefreshToken: "refresh"}
		if err := store.Upsert(entry); err != nil {
			t.Fatalf("seed store: %v", err)
		}
	}
	a.accountStore = store
	return a, store
}

// TestRemoveAccountDropsEntryAndBackend covers the account that is not on
// screen: the file loses the entry and the token, the backend is closed, and
// the mailbox stays where it was.
func TestRemoveAccountDropsEntryAndBackend(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, store := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	svc, ok := a.services[target.ID].(*closeSpyService)
	if !ok {
		t.Fatalf("backend = %T, want the spy", a.services[target.ID])
	}
	active := a.activeAcc

	a.removeAccount(target)

	if _, ok := store.Entry(target.ID); ok {
		t.Fatal("the removed account is still in accounts.json")
	}
	if _, ok := a.services[target.ID]; ok {
		t.Fatal("the removed account kept its backend")
	}
	if !svc.closed {
		t.Fatal("the backend of the removed account was not closed")
	}
	if a.activeAcc != active {
		t.Fatalf("active account = %q, want %q kept", a.activeAcc, active)
	}
	for _, acc := range a.accounts {
		if acc.ID == target.ID {
			t.Fatal("the removed account is still in the account list")
		}
	}
	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T, want the settings panel", a.overlay)
	}
	if panel.page != settingsTabAccounts {
		t.Fatalf("page = %d, want the accounts tab", panel.page)
	}
	if got := len(panel.accRows); got != 2 {
		t.Fatalf("account rows = %d, want the demo row and the add row", got)
	}
}

// TestRemoveActiveAccountLeavesEmptyMailbox pins the empty state: the mailbox
// shown by the removed account is cleared instead of jumping to another
// account, and picking one from the account menu works afterwards.
func TestRemoveActiveAccountLeavesEmptyMailbox(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, _ := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	a.activeAcc = target.ID
	a.folders = []mail.Folder{{ID: "inbox", Kind: mail.FolderInbox}}
	a.side.reload()
	a.selectedThread = "t1"
	a.thread = mail.ThreadSummary{ID: "t1"}
	a.msgs = []*mail.Message{{MessageSummary: mail.MessageSummary{ID: "m1"}}}
	a.list.setThreads([]mail.ThreadSummary{{ID: "t1"}})
	a.page, a.tokens = 2, []string{"", "cursor"}
	a.toolbar.setLoading(true)

	a.removeAccount(target)

	if a.activeAcc != "" {
		t.Fatalf("active account = %q after removing it, want none", a.activeAcc)
	}
	if a.service() != nil {
		t.Fatal("an account without a selection still has a backend")
	}
	if a.folders != nil {
		t.Fatalf("folders = %+v after the removal, want them cleared", a.folders)
	}
	if a.selectedThread != "" || a.thread.ID != "" || a.msgs != nil {
		t.Fatalf("thread = %q / %q / %d, want the reader cleared", a.selectedThread, a.thread.ID, len(a.msgs))
	}
	if got := a.accountName(); got != i18n.T("app.brand") {
		t.Fatalf("account name = %q, want the brand", got)
	}
	if a.page != 1 || len(a.tokens) != 1 {
		t.Fatalf("page = %d with %d cursors, want the first page", a.page, len(a.tokens))
	}
	if a.toolbar.loading {
		t.Fatal("the toolbar is still loading after the removal")
	}

	a.switchAccount(demoAccountID)
	if a.activeAcc != demoAccountID {
		t.Fatalf("active account = %q after switching, want %q", a.activeAcc, demoAccountID)
	}
}
