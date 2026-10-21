// Package singleinstance guarantees that only one yauzin process runs per
// desktop session. Ownership is claimed on the D-Bus session bus, which is
// where the tray and the notification area already live: the first instance
// takes the well-known name and serves an Open method on it, a second one
// sees the name taken, calls Open to bring the window back and exits.
//
// The well-known name doubles as the application ID reported to the
// compositor (gioui.org/app.ID). A Flatpak build needs --own-name and
// --talk-name for this name in its manifest.
package singleinstance

// Name is the well-known D-Bus bus name and the Gio application ID.
const Name = "me.zhsj.yauzin"
