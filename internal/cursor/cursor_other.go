//go:build !linux

// Package cursor resolves the cursor theme configured in the desktop
// environment and exports it through the environment variables the Gio
// backends read.
//
// Only the Linux backends read XCURSOR_THEME and XCURSOR_SIZE; macOS and
// Windows ask the system for the cursor, so Apply does nothing there.
package cursor

import "context"

// Apply does nothing outside Linux: the platform backends do not read the
// Xcursor environment variables.
func Apply(ctx context.Context) {}
