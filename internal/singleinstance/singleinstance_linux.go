//go:build linux

package singleinstance

import (
	"github.com/godbus/dbus/v5"
)

const (
	objectPath  = "/me/zhsj/yauzin"
	methodIface = "me.zhsj.yauzin"
)

// activator serves the activation methods on the bus connection of the first
// instance; later launches call them to ask for the window, with a mailto URI
// when the launch carried one.
type activator struct {
	open func(uri string)
}

// Open shows the primary instance's window. It runs on a D-Bus goroutine; the
// callback is responsible for marshalling onto the UI thread.
func (a *activator) Open() {
	a.activate("")
}

// OpenURI shows the primary instance's window and asks it to open a composer
// for the mailto URI. It is a method of its own rather than an argument on
// Open, so an instance started before this method existed still answers the
// plain activation.
func (a *activator) OpenURI(uri string) {
	a.activate(uri)
}

// activate hands uri to the callback, using the empty string for a plain
// activation. A nil callback is tolerated for the same reason Acquire's may be.
func (a *activator) activate(uri string) {
	if a.open != nil {
		a.open(uri)
	}
}

// Acquire claims the well-known bus name. When it succeeds, onActivate is
// served as the Open and OpenURI methods so later launches can bring the
// window back, the latter carrying a mailto URI. The callback receives an empty
// URI for a plain activation. It reports false when another instance already
// owns the name; the caller is then expected to activate it and exit. An error
// is returned when the session bus cannot be reached: the caller decides
// whether that is fatal (here it is not, the tray degrades the same way).
func Acquire(name string, onActivate func(uri string)) (bool, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, err
	}
	ret, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		_ = conn.Close()
		return false, err
	}
	if ret == dbus.RequestNameReplyExists {
		_ = conn.Close()
		return false, nil
	}
	// ExportAll takes methods of any signature; plain Export drops methods
	// whose last return value is not *dbus.Error, which Open and OpenURI are
	// not.
	if err := conn.ExportAll(&activator{open: onActivate}, objectPath, methodIface); err != nil {
		_ = conn.Close()
		return false, err
	}
	return true, nil
}

// Activate asks the instance that owns name to show its window. It is meant to
// be called by a second launch right before exiting.
func Activate(name string) error {
	return call(name, methodIface+".Open")
}

// ActivateURI hands uri to the instance that owns name, which shows its window
// and opens a composer for the mailto URI. An instance started before OpenURI
// existed answers UnknownMethod, so the plain activation is tried instead: the
// window then opens without the draft, and the error is still returned so the
// lost URI is not silent.
func ActivateURI(name, uri string) error {
	err := call(name, methodIface+".OpenURI", uri)
	if err == nil {
		return nil
	}
	if fallback := Activate(name); fallback != nil {
		return fallback
	}
	return err
}

// call invokes one method on the instance that owns name. Every call opens its
// own connection: a second launch makes exactly one and exits.
func call(name, method string, args ...any) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return conn.Object(name, objectPath).Call(method, 0, args...).Err
}
