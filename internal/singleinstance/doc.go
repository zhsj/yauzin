// Package singleinstance guarantees that only one yauzin process runs per
// desktop session. Ownership is claimed on the D-Bus session bus, which is
// where the tray and the notification area already live: the first instance
// takes the well-known name and serves the Open and OpenURI methods on it, a
// second one sees the name taken, calls one of them and exits.
//
// OpenURI carries the mailto URI of the launch, so a link handed to a second
// process still opens the composer in the one that owns the mailbox. A caller
// whose OpenURI is refused falls back to Open: the window opens without the
// draft instead of the activation being lost.
//
// The well-known name doubles as the application ID reported to the
// compositor (gioui.org/app.ID). Flatpak's default session bus policy already
// lets an application own and talk to its own application ID, so a manifest
// needs no --own-name or --talk-name for it.
package singleinstance

// Name is the well-known D-Bus bus name and the Gio application ID.
const Name = "me.zhsj.yauzin"
