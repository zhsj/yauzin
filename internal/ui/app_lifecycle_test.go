package ui

import (
	"testing"
	"time"
)

// TestWaitForWindowDrainsWithoutOpeningAWindow is the regression for a window
// that reopened itself: a posted callback must run, but leave the loop waiting
// for the tray instead of falling through to a new window.
func TestWaitForWindowDrainsWithoutOpeningAWindow(t *testing.T) {
	a := testApp(t)

	ran := make(chan struct{})
	a.post(func() { close(ran) })

	done := make(chan struct{})
	go func() {
		a.waitForWindow()
		close(done)
	}()

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("the posted callback never ran while the window was closed")
	}
	select {
	case <-done:
		t.Fatal("a posted callback reopened the window")
	case <-time.After(100 * time.Millisecond):
	}

	// Only the tray asking for the window ends the wait.
	a.openCh <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the open request did not end the wait")
	}
}

// TestWaitForWindowLeavesOnQuit covers the other way out of the wait.
func TestWaitForWindowLeavesOnQuit(t *testing.T) {
	a := testApp(t)
	done := make(chan struct{})
	go func() {
		a.waitForWindow()
		close(done)
	}()

	a.quitCh <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the quit request did not end the wait")
	}
}
