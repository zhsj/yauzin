package main

import (
	"os"

	"github.com/zhsj/yauzin/internal/uihost"
)

func main() {
	// The window layer owns the process: it identifies the application to the
	// compositor, runs the UI and hands the main thread to Gio. See
	// internal/uihost.
	uihost.Main(os.Args[1:])
}
