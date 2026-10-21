package ui

import "testing"

func TestNotifyKeepsOnlyTheLatestMessage(t *testing.T) {
	a := testApp(t)
	t.Cleanup(a.status.stopTimer)

	a.notify("first", false)
	first := a.status.timer
	if first == nil {
		t.Fatal("the first notification did not arm a dismissal timer")
	}

	a.notify("second", true)

	if a.status.msg != "second" || !a.status.isErr {
		t.Fatalf("status = %q/%v, want the second notification", a.status.msg, a.status.isErr)
	}
	if a.status.timer == first {
		t.Fatal("the second notification reused the dismissal timer of the first")
	}
	if first.Stop() {
		t.Fatal("the first notification's dismissal timer was still armed")
	}
}

func TestClearStopsDismissalTimer(t *testing.T) {
	a := testApp(t)
	a.notify("saved", false)
	timer := a.status.timer

	a.status.clear()

	if timer.Stop() {
		t.Fatal("clear left the dismissal timer armed")
	}
	if a.status.visible() {
		t.Fatal("clear left the message visible")
	}
}

func TestStaleExpiryKeepsNewerMessage(t *testing.T) {
	a := testApp(t)
	t.Cleanup(a.status.stopTimer)

	a.notify("first", false)
	stale := a.status.gen
	a.notify("second", false)

	a.expireStatus(stale)

	if a.status.msg != "second" {
		t.Fatalf("status = %q after a stale expiry, want the newer message", a.status.msg)
	}
}

func TestExpiryClearsCurrentMessage(t *testing.T) {
	a := testApp(t)
	a.notify("done", false)

	a.expireStatus(a.status.gen)

	if a.status.visible() {
		t.Fatal("expiry left the current message visible")
	}
	if a.status.timer != nil {
		t.Fatal("expiry left the dismissal timer behind")
	}
}
