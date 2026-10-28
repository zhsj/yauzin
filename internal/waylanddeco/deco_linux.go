//go:build linux && cgo

// Package waylanddeco makes Gio's Wayland backend ask the compositor for
// client-side decorations.
//
// Gio creates a zxdg_toplevel_decoration_v1 object and installs its configure
// listener, but it never sends set_mode, so the compositor picks a mode on its
// own. KWin answers server-side for windows its "no titlebar and frame" rule
// covers, and Gio then reports Config.Decorated as true: the application stops
// drawing its title bar and Gio disables its resize edges, which key off
// Config.Decorated. Nothing in the public API can change that, because the
// Wayland backend only writes Config.Decorated from the decoration configure.
//
// The shim fixes that from outside the dependency instead of editing it. The
// generated zxdg_toplevel_decoration_v1_add_listener helper is a static inline
// that ends in wl_proxy_add_listener, so linking with --wrap routes every
// listener registration through deco_linux.c, which asks for client-side
// decorations right after the decoration listener is installed — still before
// the first wl_surface.commit, which is when the protocol expects the mode.
//
// Import the package for its side effect; it has no API.
package waylanddeco

// The link flag has to live in a Go file, and cgo hands the comment group
// directly above import "C" to the C compiler, so the wrapper stays in C and
// only the directive is here.

// #cgo LDFLAGS: -Wl,--wrap=wl_proxy_add_listener
import "C"
