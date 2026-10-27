//go:build !linux

package notify

// start reports notifications as unavailable: there is no channel for this
// platform yet.
func start(func()) Notifier { return nil }
