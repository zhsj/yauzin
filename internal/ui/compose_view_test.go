package ui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/op"
)

// TestComposePanelStates pins the card geometry of the three sizes the header
// offers: the floating window, the maximized one and the collapsed header row.
func TestComposePanelStates(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	tests := []struct {
		name     string
		window   image.Point
		maximize bool
		minimize bool
		size     image.Point
		origin   image.Point
	}{
		{
			name:   "floating sits at the bottom right",
			window: testWindow,
			size:   image.Pt(480, 540),
			origin: image.Pt(testWindow.X-480-24, testWindow.Y-540-24),
		},
		{
			name:     "maximized fills the window up to the margin",
			window:   testWindow,
			maximize: true,
			size:     image.Pt(testWindow.X-48, testWindow.Y-48),
			origin:   image.Pt(24, 24),
		},
		{
			name:     "maximized follows a small window",
			window:   smallWindow,
			maximize: true,
			size:     image.Pt(smallWindow.X-48, smallWindow.Y-48),
			origin:   image.Pt(24, 24),
		},
		{
			name:     "minimized keeps the width and the header height",
			window:   testWindow,
			minimize: true,
			size:     image.Pt(480, 48),
			origin:   image.Pt(testWindow.X-480-24, testWindow.Y-48-24),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c.maximized, c.minimized = tc.maximize, tc.minimize
			var ops op.Ops
			gtx := testGtx(&ops, tc.window)
			size, origin := c.panel(gtx)
			if size != tc.size || origin != tc.origin {
				t.Fatalf("panel = %v at %v, want %v at %v", size, origin, tc.size, tc.origin)
			}
		})
	}
}

// TestComposeMaximizeAndMinimizeAreExclusive walks the four transitions of the
// two header buttons: whichever state was entered last is the one that holds.
func TestComposeMaximizeAndMinimizeAreExclusive(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	check := func(wantMax, wantMin bool) {
		t.Helper()
		if c.maximized != wantMax || c.minimized != wantMin {
			t.Fatalf("expanded = %v, collapsed = %v; want %v/%v",
				c.maximized, c.minimized, wantMax, wantMin)
		}
	}

	c.toggleMaximized()
	check(true, false)
	c.toggleMinimized()
	check(false, true)
	c.toggleMaximized()
	check(true, false)
	c.toggleMaximized()
	check(false, false)
}

// TestComposeFieldLabelKeepsItsColumn pins the fixed label column: the label
// reports exactly that width even when a long translation does not fit, so the
// editors of From, To, Cc, Bcc and Subject all start at the same x.
func TestComposeFieldLabelKeepsItsColumn(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	want := gtx.Dp(composeLabelWidth)
	for _, label := range []string{"To", "Cc", "Bcc", "Subject", strings.Repeat("Wide", 20)} {
		if dims := c.fieldLabel(gtx, label); dims.Size.X != want {
			t.Fatalf("label %q took %v, want the %v column", label, dims.Size, want)
		}
	}
}

// TestComposeLayoutFrames draws the composer in every state the header offers,
// with a filled header and an open From list, and checks the frame never
// reports more than the window.
func TestComposeLayoutFrames(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")
	c.focusInit = true
	c.to.SetText("friend@example.com, another@example.com")
	c.cc.SetText("cc@example.com")
	c.bcc.SetText("bcc@example.com")
	c.subject.SetText("A subject long enough to be cut in a narrow window")

	states := []struct {
		name     string
		maximize bool
		minimize bool
		fromOpen bool
	}{
		{name: "floating"},
		{name: "expanded", maximize: true},
		{name: "expanded with the From list open", maximize: true, fromOpen: true},
		{name: "collapsed", minimize: true},
	}
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			c.maximized, c.minimized, c.fromOpen = st.maximize, st.minimize, st.fromOpen
			var ops op.Ops
			gtx := testGtx(&ops, testWindow)
			if dims := c.Layout(gtx, a); dims.Size != testWindow {
				t.Fatalf("frame = %v, want the window %v", dims.Size, testWindow)
			}
		})
	}
}
