package main

import (
	"os"

	"gioui.org/app"

	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/ui"

	// Imported for its link-time side effect: it makes Gio ask the Wayland
	// compositor for client-side decorations before the window is mapped.
	_ "github.com/zhsj/yauzin/internal/waylanddeco"
)

func main() {
	// The compositor identifies the window by application ID; keep it in sync
	// with the bus name that guards single-instance startup. The guard
	// itself, and the activation of the running instance by a second launch,
	// live in ui.Run.
	app.ID = singleinstance.Name

	// app.Main has to own the main thread on some platforms, so the window
	// event loop runs in a goroutine. The arguments travel untouched: a mailto
	// URI among them is what opens a composer on the running instance.
	go ui.Run(os.Args[1:])
	app.Main()
}
