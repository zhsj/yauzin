package ui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/system"
	"gioui.org/layout"
)

// TestClientSide covers the decoration decision: the platform decides when the
// user follows the system, while the setting forces a title bar on Wayland only.
func TestClientSide(t *testing.T) {
	tests := []struct {
		name      string
		decorated bool
		csd       bool
		wayland   bool
		want      bool
	}{
		{"platform undecorated", false, false, false, true},
		{"platform decorated, follow system", true, false, true, false},
		{"platform decorated, client title bar on wayland", true, true, true, true},
		{"platform decorated, client title bar on x11", true, true, false, false},
		{"platform undecorated, follow system on wayland", false, false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wayland {
				t.Setenv("WAYLAND_DISPLAY", "wayland-0")
			} else {
				t.Setenv("WAYLAND_DISPLAY", "")
			}
			a := &App{decorated: tt.decorated, csd: tt.csd}
			if got := a.clientSide(); got != tt.want {
				t.Fatalf("clientSide() = %v, want %v", got, tt.want)
			}
			wantHeight := topBarHeight
			if tt.want {
				wantHeight = topBarHeightCSD
			}
			if got := a.headerHeight(); got != wantHeight {
				t.Fatalf("headerHeight() = %v, want %v", got, wantHeight)
			}
		})
	}
}

// TestWindowStepsResetAfterMinimize pins which title bar requests need the
// backend mode reset that keeps Gio's latched minimized state from swallowing
// every later mode change.
func TestWindowStepsResetAfterMinimize(t *testing.T) {
	tests := []struct {
		name      string
		acts      system.Action
		wantReset bool
	}{
		{"minimize", system.ActionMinimize, true},
		{"minimize and close", system.ActionMinimize | system.ActionClose, true},
		{"maximize", system.ActionMaximize, false},
		{"unmaximize", system.ActionUnmaximize, false},
		{"close", system.ActionClose, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perform, reset := windowSteps(tt.acts)
			if reset != tt.wantReset {
				t.Fatalf("windowSteps(%v) reset = %v, want %v", tt.acts, reset, tt.wantReset)
			}
			if perform != tt.acts {
				t.Fatalf("windowSteps(%v) performed %v, want every action untouched", tt.acts, perform)
			}
		})
	}
}

// frameWithRouter lays out one frame of a and returns the input router that
// knows its hit areas, so tests can ask what the platform would do on a press.
func frameWithRouter(t *testing.T, a *App, size image.Point) (*input.Router, layout.Context) {
	t.Helper()
	router, ops, gtx := newRouterFrame(size)
	runFrame(a, router, ops, gtx)
	return router, gtx
}

// TestTitleBarDragHandles pins the client-side title bar pointer areas: the
// brand block and the blank strips beside the search pill move the window,
// while the search pill, the header buttons and the window buttons keep the
// pointer for themselves. A press over a move area is swallowed by the
// compositor's window drag and would never reach the widget underneath.
func TestTitleBarDragHandles(t *testing.T) {
	a := testApp(t)
	a.decorated = false // the platform reports no decorations: the app draws the bar
	if !a.clientSide() {
		t.Fatal("expected a client-side title bar")
	}

	router, gtx := frameWithRouter(t, a, image.Pt(1200, 840))
	y := gtx.Dp(a.headerHeight()) / 2
	actionAt := func(x, y int) (system.Action, bool) {
		return router.ActionAt(f32.Pt(float32(x), float32(y)))
	}

	// Sweep the middle of the bar: the drag handles show up as spans separated
	// by the search pill.
	var spans [][2]int
	for x := 0; x < gtx.Constraints.Max.X; x++ {
		if act, ok := actionAt(x, y); !ok || act != system.ActionMove {
			continue
		}
		if n := len(spans); n > 0 && spans[n-1][1] == x {
			spans[n-1][1] = x + 1
			continue
		}
		spans = append(spans, [2]int{x, x + 1})
	}
	if len(spans) < 2 {
		t.Fatalf("title bar exposes %d drag handles, want the brand plus both sides of the search pill: %v",
			len(spans), spans)
	}
	if gap := spans[1][0] - spans[0][1]; gap < gtx.Dp(searchMinWidth) {
		t.Fatalf("the %dpx gap between the drag handles is narrower than the %dpx search pill",
			gap, gtx.Dp(searchMinWidth))
	}

	// None of these may sit under a drag handle.
	avatar := a.top.avatarRect
	settingsX := avatar.Min.X - gtx.Dp(8) - gtx.Dp(20)
	closeX := gtx.Constraints.Max.X - gtx.Dp(titleBarInset) - gtx.Dp(windowButtonSize)/2
	menuX := gtx.Dp(titleBarInset) + gtx.Dp(20)
	for _, widget := range []struct {
		name string
		x, y int
	}{
		{"sidebar button", menuX, y},
		{"search pill", spans[0][1] + gtx.Dp(searchMinWidth)/2, y},
		{"settings button", settingsX, y},
		{"account avatar", avatar.Min.X + avatar.Dx()/2, y},
		{"close button", closeX, y},
	} {
		if act, ok := actionAt(widget.x, widget.y); ok {
			t.Fatalf("%s is covered by a window drag handle (%v)", widget.name, act)
		}
	}

	// The server-side header keeps its old behaviour: no drag areas at all.
	a.decorated = true
	router, gtx = frameWithRouter(t, a, image.Pt(1200, 840))
	y = gtx.Dp(a.headerHeight()) / 2
	for x := 0; x < gtx.Constraints.Max.X; x++ {
		if act, ok := router.ActionAt(f32.Pt(float32(x), float32(y))); ok {
			t.Fatalf("the server-side header exposes a %v area at x=%d", act, x)
		}
	}
}
