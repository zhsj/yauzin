package ui

import (
	"errors"
	"io"

	"gioui.org/io/system"
	"gioui.org/layout"
)

// Driver creates the platform windows of one run. It is the only seam between
// the UI and the cgo window layer, which lives in internal/uihost: that package
// is the only one importing gioui.org/app, so everything on this side of the
// interface compiles and is tested without a graphics stack.
type Driver interface {
	// NewWindow creates a window with the options the app opens with. The
	// caller sets the cursor theme first: Gio reads it while the window is
	// created.
	NewWindow() Window
}

// Window is one platform window. Every method is safe to call from any
// goroutine.
type Window interface {
	// Events pumps the window until it is destroyed. frame runs once per frame
	// and returns the requests the title bar collected during it; they are
	// applied after the frame, so a close cannot tear the window down in the
	// middle of a layout. config runs whenever the platform reports new
	// decorations or a new mode.
	Events(frame func(gtx layout.Context) WindowActions, config func(WindowConfig))
	// Invalidate requests a redraw.
	Invalidate()
	// Perform runs window actions, such as the close request of the tray.
	Perform(actions system.Action)
	// SetTitle changes the window title.
	SetTitle(title string)
	// Picker returns the file chooser of this window, or nil when the platform
	// offers none.
	Picker() FilePicker
}

// WindowConfig is the platform state a window reports. The app mirrors it to
// adapt the title bar: Decorated decides whether the app draws its own.
type WindowConfig struct {
	Decorated bool
	Maximized bool
}

// WindowActions is the title bar requests of one frame, split by where they are
// applied: Actions go to the window, and ResetMode restores a windowed window
// after a minimize. See windowSteps for why the second part exists.
type WindowActions struct {
	Actions   system.Action
	ResetMode bool
}

// FilePicker is the desktop file chooser of the current window.
type FilePicker interface {
	// ChooseFile opens an existing file for reading.
	ChooseFile() (io.ReadCloser, error)
	// CreateFile asks for a destination and opens it for writing.
	CreateFile(name string) (io.WriteCloser, error)
}

// Errors the file picker reports. They are the two outcomes the views
// distinguish; internal/uihost maps the platform errors onto them. A declined
// dialog is not a failure: nothing was opened, so no message is shown.
var (
	ErrPickerUnavailable = errors.New("file picker unavailable")
	ErrPickerDeclined    = errors.New("file picker declined")
)
