// Package uihost puts the UI of internal/ui into Gio windows. It is the only
// package that imports gioui.org/app, so the UI itself compiles without cgo;
// nothing inside the UI may import this package, the dependency goes one way.
package uihost

import (
	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/ui"

	// Imported for its link-time side effect: it makes Gio ask the Wayland
	// compositor for client-side decorations before the window is mapped.
	_ "github.com/zhsj/yauzin/internal/waylanddeco"
)

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
		// own frame and the app draws nothing. On Wayland the compositor
		// answers client-side because internal/waylanddeco requests it.
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
