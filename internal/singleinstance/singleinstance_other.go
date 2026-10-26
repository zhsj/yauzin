//go:build !linux

package singleinstance

// Acquire is a no-op outside Linux: there is no session bus to claim a name
// on, so every instance is allowed to run.
func Acquire(name string, onActivate func(uri string)) (bool, error) {
	return true, nil
}

// Activate is a no-op outside Linux, matching Acquire.
func Activate(name string) error { return nil }

// ActivateURI is a no-op outside Linux, matching Acquire.
func ActivateURI(name, uri string) error { return nil }
