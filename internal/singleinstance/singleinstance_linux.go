//go:build linux

package singleinstance

import (
	"github.com/godbus/dbus/v5"
)

const (
	objectPath  = "/me/zhsj/yauzin"
	methodIface = "me.zhsj.yauzin"
)

// hold keeps the session bus connection alive for the whole process, so the
// requested name and the exported Open method never disappear while the app
// runs.
var hold *dbus.Conn

// activator serves the Open method on the bus connection of the first
// instance; later launches call it to ask for the window.
type activator struct {
	open func()
}

// Open shows the primary instance's window. It runs on a D-Bus goroutine; the
// callback is responsible for marshalling onto the UI thread.
func (a *activator) Open() {
	if a.open != nil {
		a.open()
	}
}

// Acquire claims the well-known bus name. When it succeeds, onActivate is
// served as the Open method so later launches can bring the window back. It
// reports false when another instance already owns the name; the caller is
// then expected to activate it and exit. An error is returned when the session
// bus cannot be reached: the caller decides whether that is fatal (here it is
// not, the tray degrades the same way).
func Acquire(name string, onActivate func()) (bool, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false, err
	}
	ret, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		conn.Close()
		return false, err
	}
	if ret == dbus.RequestNameReplyExists {
		conn.Close()
		return false, nil
	}
	// ExportAll takes methods of any signature; plain Export drops methods
	// whose last return value is not *dbus.Error, which Open is not.
	if err := conn.ExportAll(&activator{open: onActivate}, objectPath, methodIface); err != nil {
		conn.Close()
		return false, err
	}
	hold = conn
	return true, nil
}

// Activate asks the instance that owns name to show its window. It is meant to
// be called by a second launch right before exiting.
func Activate(name string) error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object(name, objectPath).Call(methodIface+".Open", 0).Err
}
