// Package uihost puts the UI of internal/ui into Gio windows. It is the only
// package that imports gioui.org/app, so the UI itself compiles without cgo;
// nothing inside the UI may import this package, the dependency goes one way.
package uihost

import (
	"os"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/ui"
)

// onWayland reports whether Gio talks to a Wayland compositor. Only there does
// the decoration mode belong to a negotiation the app can restart; every other
// platform decorates on its own terms.
var onWayland = os.Getenv("WAYLAND_DISPLAY") != ""

// Main hands the main thread to Gio and runs the UI until the process quits.
// args are the arguments of this launch: a mailto URI among them opens a
// composer once the app exists.
func Main(args []string) {
	// The compositor identifies the window by application ID; keep it in sync
	// with the bus name that guards single-instance startup. The guard itself,
	// and the activation of the running instance by a second launch, live in
	// ui.Run.
	app.ID = singleinstance.Name
	// app.Main has to own the main thread on some platforms, so the window
	// event loop runs in a goroutine. The arguments travel untouched.
	go ui.Run(args, driver{})
	app.Main()
}

// driver builds the windows of one run.
type driver struct{}

// NewWindow creates a window with the options the app opens with.
func (driver) NewWindow() ui.Window {
	w := new(app.Window)
	w.Option(
		app.Size(unit.Dp(1200), unit.Dp(840)),
		app.MinSize(unit.Dp(560), unit.Dp(420)),
		// Leave decoration to the app so Config.Decorated tells the truth. The
		// title bar is drawn by the app in windowcontrols.go and topbar.go;
		// platforms that decorate regardless (X11, Windows, macOS) keep their
		// own frame and the app draws nothing. On Wayland Gio turns this
		// option into the decoration mode it requests from the compositor.
		app.Decorated(false),
	)
	// The explorer belongs to the window: a new window gets a new one, and the
	// app only holds the picker of the window it draws into.
	return &window{win: w, explorer: explorer.NewExplorer(w)}
}

// window is one Gio window, driven through the ui.Window interface.
type window struct {
	win      *app.Window
	explorer *explorer.Explorer
	// askedClientSide records that the window already answered the compositor's
	// server-side verdict with a client-side request. See requestClientSide.
	askedClientSide bool
}

// Events pumps the window until it is destroyed.
func (w *window) Events(frame func(layout.Context) ui.WindowActions, config func(ui.WindowConfig)) {
	var ops op.Ops
	for {
		e := w.win.Event()
		// The explorer needs every event, including the ones handled below:
		// its file dialogs answer through them.
		w.explorer.ListenEvents(e)
		switch e := e.(type) {
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			acts := frame(gtx)
			e.Frame(gtx.Ops)
			w.perform(acts)
		case app.ConfigEvent:
			// Config.Decorated reports whether the platform decorates the
			// window; without that the application draws its own title bar.
			w.requestClientSide(e.Config.Decorated)
			config(ui.WindowConfig{
				Decorated: e.Config.Decorated,
				Maximized: e.Config.Mode == app.Maximized,
			})
			w.win.Invalidate()
		case app.DestroyEvent:
			return
		}
	}
}

// requestClientSide answers a compositor that decorated the window on its own
// with a client-side request of its own.
//
// A Wayland window starts out with Config.Decorated false: Gio only presumes
// server-side decorations where the compositor speaks xdg-decoration, and its
// guard for that is evaluated before the window's decoration object exists, so
// the Decorated(false) the window opens with changes nothing and Gio sends no
// set_mode. Compositors that decorate by default (KWin) then answer
// server_side, and the app stops drawing its title bar while Gio disables the
// resize edges that key off Config.Decorated.
//
// Gio sends set_mode only when Configure sees Config.Decorated change, so the
// request has to follow the compositor's answer: repeating Decorated(false) now
// that Config.Decorated reads true turns into set_mode(client_side). The
// server-side title bar is visible for the moment in between, which is what
// makes this a workaround: drop it once Gio presumes server-side decorations
// from the protocol object and sends set_mode before the first commit itself.
//
// Compositors without the protocol never decorate and report Decorated false
// throughout, so they never reach the request.
func (w *window) requestClientSide(decorated bool) {
	if !onWayland || !decorated || w.askedClientSide {
		return
	}
	w.askedClientSide = true
	w.win.Option(app.Decorated(false))
}

// perform applies the requests the title bar collected during a frame. The mode
// reset after a minimize works around the Gio Wayland behaviour documented at
// windowSteps: the window keeps its title bar and gets its maximize and drag
// gestures back once it is restored.
func (w *window) perform(acts ui.WindowActions) {
	if acts.Actions != 0 {
		w.win.Perform(acts.Actions)
	}
	if acts.ResetMode {
		w.win.Option(app.Windowed.Option())
	}
}

func (w *window) Invalidate() {
	w.win.Invalidate()
}

func (w *window) Perform(actions system.Action) {
	w.win.Perform(actions)
}

func (w *window) SetTitle(title string) {
	w.win.Option(app.Title(title))
}

func (w *window) Picker() ui.FilePicker {
	return picker{exp: w.explorer}
}
