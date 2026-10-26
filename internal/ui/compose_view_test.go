package ui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
)

// TestComposePanelStates pins the card geometry of the three sizes the header
// offers: the floating window, the maximized one and the collapsed header row.
// The expanded card lives in composeArea, so its size is the window minus the
// header, the sidebar and the margin on every side.
func TestComposePanelStates(t *testing.T) {
	a, _, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")

	const (
		bar    = 64 // topBarHeight: the test app leaves the decorations to the platform
		side   = 256
		margin = 24
	)
	tests := []struct {
		name      string
		window    image.Point
		collapsed bool
		maximize  bool
		minimize  bool
		size      image.Point
		origin    image.Point
	}{
		{
			name:   "floating sits at the bottom right",
			window: testWindow,
			size:   image.Pt(480, 540),
			origin: image.Pt(testWindow.X-480-24, testWindow.Y-540-24),
		},
		{
			name:     "maximized fills the content area",
			window:   testWindow,
			maximize: true,
			size:     image.Pt(testWindow.X-side-2*margin, testWindow.Y-bar-2*margin),
			origin:   image.Pt(side+margin, bar+margin),
		},
		{
			name:      "maximized reclaims the folded sidebar",
			window:    testWindow,
			collapsed: true,
			maximize:  true,
			size:      image.Pt(testWindow.X-2*margin, testWindow.Y-bar-2*margin),
			origin:    image.Pt(margin, bar+margin),
		},
		{
			name:     "maximized follows a small window",
			window:   smallWindow,
			maximize: true,
			size:     image.Pt(smallWindow.X-side-2*margin, smallWindow.Y-bar-2*margin),
			origin:   image.Pt(side+margin, bar+margin),
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
			a.sidebarCollapsed = tc.collapsed
			c.maximized, c.minimized = tc.maximize, tc.minimize
			var ops op.Ops
			gtx := testGtx(&ops, tc.window)
			size, origin := c.panel(gtx, a)
			if size != tc.size || origin != tc.origin {
				t.Fatalf("panel = %v at %v, want %v at %v", size, origin, tc.size, tc.origin)
			}
			if !tc.maximize || tc.minimize {
				return
			}
			// The card must clear the header, or the title bar painted after
			// the overlay hides its own buttons, and the folder list.
			area := composeArea(gtx, a)
			if origin.Y < gtx.Dp(a.headerHeight()) || origin.X < area.Min.X {
				t.Fatalf("expanded card at %v overlaps the header or the sidebar of %v", origin, area)
			}
			got := image.Rectangle{Min: origin, Max: origin.Add(size)}
			if !got.In(area) {
				t.Fatalf("expanded card %v leaves the content area %v", got, area)
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

// TestComposeFocusFollowsTheWritingPosition pins where the keyboard lands: a
// reply is written below the quote, so the body owns it; a forward still starts
// with the recipient and leaves the caret on the blank line above the quote.
func TestComposeFocusFollowsTheWritingPosition(t *testing.T) {
	a, _, _ := composeTestApp(t)
	msg := &mail.Message{
		MessageSummary: mail.MessageSummary{
			From:    mail.Address{Name: "Bob", Email: "bob@example.com"},
			Subject: "subject",
		},
		TextBody: "hello",
	}

	router, ops, gtx := newRouterFrame(testWindow)
	frame := func() { runFrame(a, router, ops, gtx) }

	a.openCompose(composeReply, msg)
	reply := a.compose
	frame()
	frame()
	if !gtx.Focused(&reply.body) {
		t.Fatal("the reply opened without the keyboard in the body")
	}
	if gtx.Focused(&reply.to) {
		t.Fatal("the recipient field kept the keyboard in a reply")
	}

	a.openCompose(composeForward, msg)
	fwd := a.compose
	if start, end := fwd.body.Selection(); start != 0 || end != 0 {
		t.Fatalf("forward caret = %d..%d, want the top of the body", start, end)
	}
	frame()
	frame()
	if !gtx.Focused(&fwd.to) {
		t.Fatal("the forward opened without the keyboard in the recipient")
	}
}
