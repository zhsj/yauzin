package ui

import (
	"reflect"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/mock"
	"github.com/zhsj/yauzin/internal/prefs"
)

// TestNewAppWithUsesTheInjectedBackend pins the assembly point: every account
// gets exactly one service from newService, in account order, and the first
// account starts active in the inbox.
func TestNewAppWithUsesTheInjectedBackend(t *testing.T) {
	accounts := []mail.Account{{ID: "one", Email: "one@example.com"}, {ID: "two", Email: "two@example.com"}}
	var built []string
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: accounts,
		newService: func(acc mail.Account) mail.Service {
			built = append(built, acc.ID)
			return &fakeService{}
		},
	})

	if want := []string{"one", "two"}; !reflect.DeepEqual(built, want) {
		t.Fatalf("services built for %v, want %v", built, want)
	}
	if len(a.services) != len(accounts) {
		t.Fatalf("services = %d, want %d", len(a.services), len(accounts))
	}
	if a.activeAcc != accounts[0].ID {
		t.Fatalf("active account = %q, want %q", a.activeAcc, accounts[0].ID)
	}
	if a.activeFolder != "inbox" {
		t.Fatalf("active folder = %q, want inbox", a.activeFolder)
	}
	if a.service() == nil {
		t.Fatal("the active account has no backend")
	}
}

// TestNewAppWithRestoresTheStoredAccount covers the choice saved by the
// previous run: that mailbox is active again and starts in its inbox.
func TestNewAppWithRestoresTheStoredAccount(t *testing.T) {
	a := newAppWith(prefs.Settings{ActiveAccount: "two"}, appOptions{
		accounts: []mail.Account{
			{ID: "one", Email: "one@example.com"},
			{ID: "two", Email: "two@example.com"},
		},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
	})

	if a.activeAcc != "two" {
		t.Fatalf("active account = %q, want the stored two", a.activeAcc)
	}
	if a.activeFolder != "inbox" {
		t.Fatalf("active folder = %q, want inbox", a.activeFolder)
	}
}

// TestNewAppWithFallsBackFromAStaleChoice covers the ID a removed account
// leaves behind: the stored mailbox is gone, so the first one opens instead.
func TestNewAppWithFallsBackFromAStaleChoice(t *testing.T) {
	a := newAppWith(prefs.Settings{ActiveAccount: "gone"}, appOptions{
		accounts: []mail.Account{
			{ID: "one", Email: "one@example.com"},
			{ID: "two", Email: "two@example.com"},
		},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
	})

	if a.activeAcc != "one" {
		t.Fatalf("active account = %q, want the first account", a.activeAcc)
	}
}

// TestNewAppWithFallsBackFromABackendlessChoice keeps the restore honest: an
// account whose backend could not be built must not be reopened.
func TestNewAppWithFallsBackFromABackendlessChoice(t *testing.T) {
	a := newAppWith(prefs.Settings{ActiveAccount: "two"}, appOptions{
		accounts: []mail.Account{
			{ID: "one", Email: "one@example.com"},
			{ID: "two", Email: "two@example.com"},
		},
		newService: func(acc mail.Account) mail.Service {
			if acc.ID == "two" {
				return nil
			}
			return &fakeService{}
		},
	})

	if a.activeAcc != "one" {
		t.Fatalf("active account = %q, want the first account", a.activeAcc)
	}
}

// TestNewAppWithDefaultsToNoAccounts keeps the release default honest: without
// stored accounts and without the development mode the window opens on an empty
// list instead of shipped demo data.
func TestNewAppWithDefaultsToNoAccounts(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{})

	if len(a.accounts) != 0 {
		t.Fatalf("accounts = %+v, want none", a.accounts)
	}
	if a.activeAcc != "" {
		t.Fatalf("active account = %q, want none", a.activeAcc)
	}
	if svc := a.service(); svc != nil {
		t.Fatalf("service() = %T, want nil", svc)
	}
}

// TestDevModeAddsTheDemoAccount pins what the development mode is for: the
// demo mailbox, served by the mock backend, is the only account of an otherwise
// unconfigured app.
func TestDevModeAddsTheDemoAccount(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{dev: true})

	if len(a.accounts) != 1 || a.accounts[0].ID != demoAccountID {
		t.Fatalf("accounts = %+v, want only the demo account", a.accounts)
	}
	if a.activeAcc != demoAccountID {
		t.Fatalf("active account = %q, want %q", a.activeAcc, demoAccountID)
	}
	if _, ok := a.services[a.activeAcc].(*mock.Service); !ok {
		t.Fatalf("demo backend = %T, want *mock.Service", a.services[a.activeAcc])
	}
}

// TestDevModeAppendsTheDemoAccount keeps the stored mailboxes first: the demo
// one is an extra for development, never a replacement, and listing it twice
// must not duplicate it.
func TestDevModeAppendsTheDemoAccount(t *testing.T) {
	real := mail.Account{ID: "one", Email: "one@example.com"}
	for _, tc := range []struct {
		name     string
		accounts []mail.Account
	}{
		{name: "without demo", accounts: []mail.Account{real}},
		{name: "with demo", accounts: []mail.Account{real, demoAccounts()[0]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAppWith(prefs.Settings{}, appOptions{accounts: tc.accounts, dev: true})

			if len(a.accounts) != 2 || a.accounts[0].ID != real.ID || a.accounts[1].ID != demoAccountID {
				t.Fatalf("accounts = %+v, want the real account then the demo one", a.accounts)
			}
			if a.activeAcc != real.ID {
				t.Fatalf("active account = %q, want %q", a.activeAcc, real.ID)
			}
		})
	}
}

// TestNewAppWithSkipsAccountsWithoutABackend documents the nil convention used
// by testApp: an account listed without a service stays selectable but has no
// backend to call.
func TestNewAppWithSkipsAccountsWithoutABackend(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: []mail.Account{{ID: "demo"}},
		newService: func(mail.Account) mail.Service {
			return nil
		},
	})

	if len(a.services) != 0 {
		t.Fatalf("services = %+v, want none", a.services)
	}
	if a.activeAcc != "demo" {
		t.Fatalf("active account = %q, want demo", a.activeAcc)
	}
}
