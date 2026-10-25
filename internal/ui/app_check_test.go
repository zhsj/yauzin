package ui

import (
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/tray"
)

// TestConcurrentChecksShareOneRequest pins the single-flight rule: a burst of
// reload intents while a check is in flight must not turn into a burst of
// users.getProfile calls.
func TestConcurrentChecksShareOneRequest(t *testing.T) {
	a := testApp(t)
	gate := make(chan struct{})
	svc := &fakeService{checkGate: gate}
	useService(a, svc)

	a.reloadChecked()
	waitFor(t, a, "the check in flight", func() bool { return svc.countCalls("check") == 1 })

	a.reloadChecked()
	close(gate)
	waitFor(t, a, "the page", func() bool { return svc.countCalls("list") == 1 })

	if got := svc.countCalls("check"); got != 1 {
		t.Fatalf("checks = %d, want the in-flight one reused", got)
	}
}

// TestAutomaticReloadReusesARecentCheck covers the cooldown: a window reattach
// right after a check loads from the backend again but does not ask Gmail
// whether anything moved. The loop stands for the storm that used to happen.
func TestAutomaticReloadReusesARecentCheck(t *testing.T) {
	a := testApp(t)
	a.checkCooldown = time.Hour
	svc := &fakeService{}
	useService(a, svc)

	a.reloadChecked()
	waitFor(t, a, "the first page", func() bool { return svc.countCalls("list") == 1 })

	for i := 0; i < 20; i++ {
		a.reloadChecked()
	}
	waitFor(t, a, "the reloaded page", func() bool { return svc.countCalls("list") > 1 })

	if got := svc.countCalls("check"); got != 1 {
		t.Fatalf("checks = %d, want the recent answer reused", got)
	}
}

// TestManualRefreshAlwaysChecks pins the other half of the cooldown: a request
// the user made never waits for it.
func TestManualRefreshAlwaysChecks(t *testing.T) {
	a := testApp(t)
	a.checkCooldown = time.Hour
	svc := &fakeService{}
	useService(a, svc)

	a.reloadChecked()
	waitFor(t, a, "the first page", func() bool { return svc.countCalls("list") == 1 })

	a.reloadCheckedNow()
	waitFor(t, a, "the refreshed page", func() bool { return svc.countCalls("list") == 2 })

	if got := svc.countCalls("check"); got != 2 {
		t.Fatalf("checks = %d, want the manual refresh to check again", got)
	}
}

// TestStartupChecksOnceWithATray keeps the unread baseline from costing a
// second users.getProfile: the startup check hands its answer to the poll.
func TestStartupChecksOnceWithATray(t *testing.T) {
	a := testApp(t)
	a.tray = &tray.Tray{}
	svc := &fakeService{}
	useService(a, svc)

	a.startInitialLoad()
	waitFor(t, a, "the inbox baseline", func() bool { return svc.countCalls("search") == 1 })

	if got := svc.countCalls("check"); got != 1 {
		t.Fatalf("checks = %d at startup, want 1", got)
	}
}

// TestChangedReloadIsNotCancelledByANewerOne pins the reason a change has to
// reach Gmail: the sidebar load a completed check started must survive the next
// refresh, which used to cancel it before the request was even sent.
func TestChangedReloadIsNotCancelledByANewerOne(t *testing.T) {
	a := testApp(t)
	gate := make(chan struct{})
	svc := &fakeService{checkChanged: true, foldersGate: gate}
	useService(a, svc)

	a.reloadChecked()
	waitFor(t, a, "the first sidebar load", func() bool { return svc.countCalls("folders") == 1 })

	a.reloadCheckedNow()
	waitFor(t, a, "the second sidebar load", func() bool { return svc.countCalls("folders") == 2 })

	close(gate)
	waitFor(t, a, "the page", func() bool { return svc.countCalls("list") >= 1 })

	if got := svc.cancelledFolders(); got != 0 {
		t.Fatalf("cancelled sidebar loads = %d, want none", got)
	}
}
