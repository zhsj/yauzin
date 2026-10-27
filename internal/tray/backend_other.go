//go:build !linux

package tray

import "errors"

// startBackend has no non-Linux implementation yet, so Start fails and the
// caller keeps its "closing the window quits" path.
func startBackend(startState, Handlers, Icons) (backend, error) {
	return nil, errors.New("notification area not supported on this platform")
}
