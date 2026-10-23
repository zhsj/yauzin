package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/op"
)

// TestOutlineButtonFrames pins the three states of the compact bordered action:
// each one keeps the fixed height of an action line, the label widens the
// button beyond its padding, and a disabled button stays inert.
func TestOutlineButtonFrames(t *testing.T) {
	a := testApp(t)
	neutral := newOutlineButton("relogin", nil)
	danger := newOutlineButton("delete", nil)
	danger.danger = true
	disabled := newOutlineButton("off", func() { t.Error("the disabled button fired") })
	disabled.disabled = true

	cases := []struct {
		name string
		b    *outlineButton
	}{
		{"neutral", neutral},
		{"danger", danger},
		{"disabled", disabled},
	}
	for _, tc := range cases {
		var ops op.Ops
		gtx := testGtx(&ops, image.Pt(400, 100))
		dims := tc.b.Layout(gtx, a.th)
		if want := gtx.Dp(32); dims.Size.Y != want {
			t.Fatalf("%s height = %d, want %d", tc.name, dims.Size.Y, want)
		}
		if pad := 2 * gtx.Dp(14); dims.Size.X <= pad {
			t.Fatalf("%s width = %d, want the label to widen the %d of padding", tc.name, dims.Size.X, pad)
		}
	}

	var ops op.Ops
	gtx := testGtx(&ops, image.Pt(400, 100))
	disabled.click.Click()
	disabled.Layout(gtx, a.th)
}

// TestSpinningButtonIgnoresClicks keeps a second tap from superseding the load
// the first one started, and pins that the rotation restarts upright.
func TestSpinningButtonIgnoresClicks(t *testing.T) {
	a := testApp(t)
	tapped := false
	b := newIconButton(iconRefresh, 32, func() { tapped = true })
	b.setSpinning(true)

	var ops op.Ops
	b.click.Click()
	b.Layout(testCell(&ops, image.Pt(32, 32)), a.th)
	if tapped {
		t.Fatal("a spinning button reported a tap")
	}
	if b.spinStart.IsZero() {
		t.Fatal("the spinning button did not anchor its rotation")
	}

	b.setSpinning(false)
	b.click.Click()
	b.Layout(testCell(&ops, image.Pt(32, 32)), a.th)
	if !tapped {
		t.Fatal("the button did not respond once it stopped spinning")
	}
	if !b.spinStart.IsZero() {
		t.Fatal("the rotation start survived the end of the spin")
	}
}

// TestLoadingDrivesTheRefreshSpinner pins the single switch for the spinner:
// the toolbar's loading flag.
func TestLoadingDrivesTheRefreshSpinner(t *testing.T) {
	a := testApp(t)
	a.toolbar.setLoading(true)
	if !a.toolbar.refresh.spinning {
		t.Fatal("the refresh button does not spin while the list loads")
	}
	a.toolbar.setLoading(false)
	if a.toolbar.refresh.spinning {
		t.Fatal("the refresh button kept spinning after the load ended")
	}
}

// TestLoadingWatchdogClearsAStuckSpinner is the safety net: a load path that
// forgets to clear the flag costs one watchdog period, not a window that never
// stops redrawing.
func TestLoadingWatchdogClearsAStuckSpinner(t *testing.T) {
	a := testApp(t)
	a.toolbar.setLoading(true)
	a.toolbar.loadingUntil = time.Now().Add(-time.Second)

	var ops op.Ops
	a.toolbar.Layout(testGtx(&ops, image.Pt(600, 48)))

	if a.toolbar.loading {
		t.Fatal("the loading flag survived the watchdog")
	}
	if a.toolbar.refresh.spinning {
		t.Fatal("the refresh button kept spinning after the watchdog")
	}
}
