//go:build !linux

package tray

// checkNotificationArea assumes the native notification area is available.
// There is no equivalent of the Linux watcher service to probe on macOS or
// Windows, and the systray backend fails loudly enough there.
func checkNotificationArea() error { return nil }
