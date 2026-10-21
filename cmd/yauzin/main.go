package main

import (
	"gioui.org/app"

	"github.com/zhsj/yauzin/internal/singleinstance"
	"github.com/zhsj/yauzin/internal/ui"
)

func main() {
	// The compositor identifies the window by application ID; keep it in sync
	// with the bus name that guards single-instance startup. The guard
	// itself, and the activation of the running instance by a second launch,
	// live in ui.Run.
	app.ID = singleinstance.Name

	// app.Main has to own the main thread on some platforms, so the window
	// event loop runs in a goroutine.
	go ui.Run()
	app.Main()
}
