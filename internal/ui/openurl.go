package ui

import (
	"log/slog"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// webURL returns raw as an http(s) address when the whole string is one, and
// "" otherwise. The scheme is restricted on purpose: the selection comes from
// a message body, and handing a file: or javascript: address to the platform
// opener would act on this machine instead of opening a page.
func webURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return raw
	default:
		return ""
	}
}

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
