package ui

import (
	"io"
	"testing"

	"gioui.org/io/system"
	"gioui.org/layout"

	"github.com/zhsj/yauzin/internal/i18n"
)

// fakeWindow records what the app asks of a platform window and keeps the
// callbacks it was handed, so a test can pump frames and configurations itself.
type fakeWindow struct {
	frame  func(layout.Context) WindowActions
	config func(WindowConfig)

	invalidated int
	performed   system.Action
	title       string
	picker      FilePicker
}

func (w *fakeWindow) Events(frame func(layout.Context) WindowActions, config func(WindowConfig)) {
	w.frame, w.config = frame, config
}

func (w *fakeWindow) Invalidate() { w.invalidated++ }

func (w *fakeWindow) Perform(actions system.Action) { w.performed |= actions }

func (w *fakeWindow) SetTitle(title string) { w.title = title }

func (w *fakeWindow) Picker() FilePicker { return w.picker }

// fakePicker stands in for the desktop file chooser: the tests only need the
// window to hand one out, never a dialog.
type fakePicker struct {
	reader  io.ReadCloser
	writer  io.WriteCloser
	err     error
	created string
}

func (p *fakePicker) ChooseFile() (io.ReadCloser, error) { return p.reader, p.err }

func (p *fakePicker) CreateFile(name string) (io.WriteCloser, error) {
	p.created = name
	return p.writer, p.err
}

// TestAttachDetachBindsWindow pins the window binding: attach hands the app the
// window it draws into and titles it, and detach drops it so background work
// stops invalidating a destroyed window.
func TestAttachDetachBindsWindow(t *testing.T) {
	a := testApp(t)
	picker := &fakePicker{}
	w := &fakeWindow{picker: picker}

	a.attach(w)
	if got := a.window(); got != w {
		t.Fatalf("window() after attach = %v, want the attached window", got)
	}
	if got, want := w.title, i18n.T("app.brand"); got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	if !a.decorated || a.maximized {
		t.Fatalf("fresh window state = decorated %v maximized %v, want decorated true and not maximized", a.decorated, a.maximized)
	}
	if got := a.windowPicker(); got != FilePicker(picker) {
		t.Fatalf("windowPicker() = %v, want the window's picker", got)
	}

	a.invalidate()
	if w.invalidated != 1 {
		t.Fatalf("invalidate requests = %d, want 1", w.invalidated)
	}
	// A close request from the tray goes straight to the window.
	a.requestQuit()
	if w.performed&system.ActionClose == 0 {
		t.Fatalf("performed = %v, want a close request", w.performed)
	}
	a.quitting.Store(false)

	a.detach()
	if got := a.window(); got != nil {
		t.Fatalf("window() after detach = %v, want nil", got)
	}
	if got := a.windowPicker(); got != nil {
		t.Fatalf("windowPicker() after detach = %v, want nil", got)
	}
	// Without a window a redraw request is dropped: the next window draws the
	// state as it is by then.
	a.invalidate()
	if w.invalidated != 1 {
		t.Fatalf("invalidations after detach = %d, want the detached window untouched", w.invalidated)
	}
}

// TestWindowConfigMirrorsPlatformState covers the only platform state the title
// bar adapts to.
func TestWindowConfigMirrorsPlatformState(t *testing.T) {
	a := testApp(t)
	w := &fakeWindow{}
	a.attach(w)
	a.eventLoop(w)
	if w.config == nil {
		t.Fatal("eventLoop did not hand the configuration callback to the window")
	}

	w.config(WindowConfig{Decorated: false, Maximized: true})
	if a.decorated || !a.maximized {
		t.Fatalf("state = decorated %v maximized %v, want false and true", a.decorated, a.maximized)
	}
	if !a.clientSide() {
		t.Fatal("an undecorated window must draw its own title bar")
	}

	w.config(WindowConfig{Decorated: true})
	if !a.decorated || a.maximized {
		t.Fatalf("state = decorated %v maximized %v, want true and false", a.decorated, a.maximized)
	}
}

// TestFrameReturnsWindowActions pins the frame contract with the window layer:
// the title bar requests leave the frame instead of running inside it, and a
// minimize drags the mode reset along.
func TestFrameReturnsWindowActions(t *testing.T) {
	a := testApp(t)
	w := &fakeWindow{}
	a.attach(w)
	a.eventLoop(w)
	if w.frame == nil {
		t.Fatal("eventLoop did not hand the frame callback to the window")
	}

	_, ops, gtx := newRouterFrame(testWindow)
	a.windowActions = system.ActionClose
	acts := w.frame(gtx)
	if acts.Actions != system.ActionClose {
		t.Fatalf("actions = %v, want %v", acts.Actions, system.ActionClose)
	}
	if acts.ResetMode {
		t.Fatal("a close must not reset the window mode")
	}
	if a.windowActions != 0 {
		t.Fatalf("windowActions = %v after the frame, want them consumed", a.windowActions)
	}

	ops.Reset()
	a.windowActions = system.ActionMinimize
	acts = w.frame(gtx)
	if acts.Actions != system.ActionMinimize || !acts.ResetMode {
		t.Fatalf("actions = %+v, want a minimize with the mode reset", acts)
	}
}
