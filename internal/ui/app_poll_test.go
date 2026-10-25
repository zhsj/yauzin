package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
	"github.com/zhsj/yauzin/internal/tray"
)

// pollThread builds one unread conversation for the poll tests. ids are
// ordered oldest first.
func pollThread(id, subject string, unread int, ids ...string) mail.ThreadSummary {
	return mail.ThreadSummary{ID: id, Subject: subject, UnreadCount: unread, MessageIDs: ids}
}

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
// beyond the check itself. The first poll builds the baseline, which needs one
// search even when the mailbox reports no change.
func TestPollIgnoresAnUnchangedMailbox(t *testing.T) {
	a := testApp(t)
	a.tray = &tray.Tray{}
	svc := &fakeService{}
	useService(a, svc)

	a.pollChanges()
	waitFor(t, a, "the baseline", func() bool { return svc.countCalls("search") == 1 })
	before := len(svc.callLog())

	a.pollChanges()
	waitFor(t, a, "the quiet check", func() bool { return svc.countCalls("check") == 2 })

	if got := svc.callLog(); len(got) != before+1 {
		t.Fatalf("calls = %v, want only one more check", got)
	}
}

// TestPollOnlyRefreshesTheTray covers the process-level poll: a changed
// mailbox reads the unread conversations of the inbox and touches nothing the
// window shows.
func TestPollOnlyRefreshesTheTray(t *testing.T) {
	a := testApp(t)
	a.tray = &tray.Tray{}
	svc := &fakeService{
		checkChanged: true,
		searchPage: mail.ThreadPage{
			Threads: []mail.ThreadSummary{pollThread("t1", "First", 1, "m1")},
			Total:   1,
		},
	}
	useService(a, svc)

	a.pollChanges()
	waitFor(t, a, "the inbox search", func() bool { return svc.countCalls("search") == 1 })

	if got := svc.countCalls("folders"); got != 0 {
		t.Fatalf("folder reloads = %d, want 0 from the poll", got)
	}
	if got := svc.countCalls("list"); got != 0 {
		t.Fatalf("threads loads = %d, want 0 from the poll", got)
	}
	requests := svc.searchRequests()
	if len(requests) != 1 {
		t.Fatalf("searches = %d, want 1", len(requests))
	}
	if got := requests[0]; got.Query != "label:inbox is:unread" || got.Limit != 3 {
		t.Fatalf("search = %+v, want the unread inbox query limited to 3", got)
	}
}

// TestPollNotifiesANewConversation checks the whole path: the first poll seeds
// the baseline silently, a second one with a new unread conversation publishes
// one notification.
func TestPollNotifiesANewConversation(t *testing.T) {
	a := testApp(t)
	a.tray = &tray.Tray{}
	notifier := &fakeNotifier{}
	a.notifier = notifier
	svc := &fakeService{
		checkChanged: true,
		searchPage: mail.ThreadPage{
			Threads: []mail.ThreadSummary{pollThread("t1", "First", 1, "m1")},
			Total:   1,
		},
	}
	useService(a, svc)

	a.pollChanges()
	waitFor(t, a, "the baseline search", func() bool { return svc.countCalls("search") == 1 })
	if sent := notifier.sent(); len(sent) != 0 {
		t.Fatalf("notices = %v, want none while seeding the baseline", sent)
	}

	svc.mu.Lock()
	svc.searchPage = mail.ThreadPage{
		Threads: []mail.ThreadSummary{
			pollThread("t2", "Second subject", 1, "m2"),
			pollThread("t1", "First", 1, "m1"),
		},
		Total: 4,
	}
	svc.mu.Unlock()
	a.pollChanges()
	waitFor(t, a, "the notification", func() bool { return len(notifier.sent()) == 1 })

	notice := notifier.sent()[0]
	if !strings.Contains(notice.Body, "Second subject") || !strings.Contains(notice.Body, "4 unread conversations") {
		t.Fatalf("body = %q, want the new subject and the total", notice.Body)
	}
}

// TestPollStaysQuietWithoutATrayOrNotifier keeps a backend without a
// destination from being polled at all.
func TestPollStaysQuietWithoutATrayOrNotifier(t *testing.T) {
	a := testApp(t)
	svc := &fakeService{checkChanged: true}
	useService(a, svc)

	a.pollChanges()

	if got := svc.countCalls("check"); got != 0 {
		t.Fatalf("checks = %d without a tray or notifier, want 0", got)
	}
	if got := svc.countCalls("search"); got != 0 {
		t.Fatalf("searches = %d without a tray or notifier, want 0", got)
	}
}

// TestNotificationClickOpensInbox checks the posted callback: without a window
// it brings the state back to the inbox and asks for one.
func TestNotificationClickOpensInbox(t *testing.T) {
	a := testApp(t)
	a.activeFolder = "sent"
	a.query = "report"

	a.openInboxFromNotification()
	a.drain()

	if a.activeFolder != "inbox" || a.query != "" {
		t.Fatalf("folder/query = %q/%q, want inbox with no search", a.activeFolder, a.query)
	}
	select {
	case <-a.openCh:
	default:
		t.Fatal("the notification click did not ask for a window")
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
