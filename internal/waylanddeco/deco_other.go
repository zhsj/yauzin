//go:build !linux || !cgo

// Package waylanddeco carves out client-side decorations on Wayland. Only the
// Linux cgo build links the shim; everywhere else the package is inert, which
// keeps the import in the command unconditional.
package waylanddeco
