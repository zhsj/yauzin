package ui

import (
	"log/slog"
	"os/exec"
	"runtime"
)

// openPath hands a file or URL to the platform's default handler without
// blocking the UI goroutine.
func openPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// A handler that starts and then fails (no browser registered, xdg-open
	// missing its desktop file) can only be reported here: the UI already moved
	// on by now.
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Warn("open handler failed", "program", cmd.Path, "err", err)
		}
	}()
	return nil
}
