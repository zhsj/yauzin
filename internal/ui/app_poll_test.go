package ui

import (
	"errors"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// TestInitialLoadChecksBeforeLoading pins the order every full load follows: the
// check records the baseline first, so the data that follows cannot predate it.
func TestInitialLoadChecksBeforeLoading(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)

	a.startInitialLoad()
	waitFor(t, a, "the first page", func() bool { return svc.countCalls("list") == 1 })

	calls := svc.callLog()
	if len(calls) == 0 || calls[0] != "check" {
		t.Fatalf("calls = %v, want the check first", calls)
	}
	if got := svc.countCalls("check"); got != 1 {
		t.Fatalf("checks = %d, want 1", got)
	}
}

// TestLoadContinuesAfterAFailedCheck keeps startup usable offline: the check is
// a refresher, not a gate on the mailbox.
func TestLoadContinuesAfterAFailedCheck(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{checkErr: errors.New("offline")}
	useService(a, svc)

	a.reloadChecked()
	waitFor(t, a, "the page despite the failed check", func() bool { return svc.countCalls("list") == 1 })
}

// TestRefreshButtonChecksFirst covers the manual refresh: it goes through the
// same helper as startup, so it too establishes the baseline before loading.
func TestRefreshButtonChecksFirst(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)

	a.toolbar.refresh.onTap()
	waitFor(t, a, "the refreshed page", func() bool { return svc.countCalls("list") == 1 })

	if calls := svc.callLog(); len(calls) == 0 || calls[0] != "check" {
		t.Fatalf("calls = %v, want the check first", calls)
	}
}

// TestSwitchAccountChecksFirst pins the same order across accounts: the new
// mailbox is checked before anything is loaded from it.
func TestSwitchAccountChecksFirst(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts:   []mail.Account{{ID: "one"}, {ID: "two"}},
		newService: func(mail.Account) mail.Service { return &fakeService{} },
	})
	two := a.services["two"].(*fakeService)

	a.switchAccount("two")
	waitFor(t, a, "the new account's page", func() bool { return two.countCalls("list") == 1 })

	if calls := two.callLog(); len(calls) == 0 || calls[0] != "check" {
		t.Fatalf("calls on the new account = %v, want the check first", calls)
	}
}

// TestPollIgnoresAnUnchangedMailbox keeps a quiet mailbox from costing anything
// beyond the check itself.
func TestPollIgnoresAnUnchangedMailbox(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{}
	useService(a, svc)

	a.pollChanges()
	waitFor(t, a, "the check", func() bool { return svc.countCalls("check") == 1 })

	if got := svc.callLog(); len(got) != 1 {
		t.Fatalf("calls = %v, want only the check", got)
	}
}

// TestPollInTheTrayReloadsTheSidebarOnly covers the windowless process: the
// badge and the label counters are refreshed, the list is left to attach.
func TestPollInTheTrayReloadsTheSidebarOnly(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{checkChanged: true}
	useService(a, svc)

	a.pollChanges()
	waitFor(t, a, "the sidebar reload", func() bool { return svc.countCalls("folders") == 1 })

	if got := svc.countCalls("list"); got != 0 {
		t.Fatalf("threads loads = %d without a window, want 0", got)
	}
}

// TestPollLeavesABusyListAlone pins the guard around a request the user just
// started: replacing it would cancel their folder, page or query change.
func TestPollLeavesABusyListAlone(t *testing.T) {
	a := testApp(t)
	a.toolbar.setLoading(true)
	if a.canRefreshList() {
		t.Fatal("the poll would replace a list request in flight")
	}
	a.toolbar.setLoading(false)
	if a.canRefreshList() {
		t.Fatal("the poll would touch the list without a window")
	}
}

// TestPostWakesTheLifecycleLoop covers the tray path of the queue: with no
// window, the wake signal is what gets a posted callback drained.
func TestPostWakesTheLifecycleLoop(t *testing.T) {
	a := testApp(t)

	ran := false
	a.post(func() { ran = true })
	select {
	case <-a.wake:
	default:
		t.Fatal("post did not wake the lifecycle loop")
	}
	a.drain()
	if !ran {
		t.Fatal("the posted callback did not run")
	}
}

// TestInboxUnread covers the badge decision, which only reads the inbox entry.
func TestInboxUnread(t *testing.T) {
	for _, tc := range []struct {
		name    string
		folders []mail.Folder
		want    int
	}{
		{"no folders", nil, 0},
		{"no inbox", []mail.Folder{{ID: "work", Kind: mail.FolderLabel, Unread: 3}}, 0},
		{"empty inbox", []mail.Folder{{ID: "inbox", Kind: mail.FolderInbox}}, 0},
		{"inbox among labels", []mail.Folder{
			{ID: "work", Kind: mail.FolderLabel, Unread: 9},
			{ID: "inbox", Kind: mail.FolderInbox, Unread: 4},
		}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inboxUnread(tc.folders); got != tc.want {
				t.Fatalf("inboxUnread = %d, want %d", got, tc.want)
			}
		})
	}
}
