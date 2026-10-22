// Package logging owns the process logger. It writes the same text format to
// stderr as the slog default did, and it exposes the level the settings page
// changes: the console destination is fixed, so there is no log file, no
// rotation and no path to configure.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Level names as they are stored in settings.json and shown in the settings
// panel.
const (
	LevelError = "error"
	LevelWarn  = "warn"
	LevelInfo  = "info"
	LevelDebug = "debug"
)

// level is shared by every handler Handler builds. A change takes effect on the
// logger that is already installed, so a level picked in the settings panel
// applies to the requests that follow it.
var level slog.LevelVar

// Init installs the default logger. It runs once while the application starts,
// before anything that could log.
func Init() {
	slog.SetDefault(slog.New(Handler(os.Stderr)))
}

// Handler returns the console handler the application logs through. It is
// exported so a test can assert the level filtering on a buffer instead of
// touching the default logger.
func Handler(w io.Writer) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{Level: &level})
}

// Normalize returns the level name to apply for name: the four known names,
// case and surrounding space ignored, and info for anything else, including an
// empty value (first run, hand-edited file).
func Normalize(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case LevelDebug:
		return LevelDebug
	case LevelError:
		return LevelError
	case LevelWarn:
		return LevelWarn
	default:
		return LevelInfo
	}
}

// SetLevel applies name to the running logger and returns the normalized name,
// so the caller can store and display exactly what took effect.
func SetLevel(name string) string {
	name = Normalize(name)
	switch name {
	case LevelDebug:
		level.Set(slog.LevelDebug)
	case LevelError:
		level.Set(slog.LevelError)
	case LevelWarn:
		level.Set(slog.LevelWarn)
	default:
		level.Set(slog.LevelInfo)
	}
	return name
}
