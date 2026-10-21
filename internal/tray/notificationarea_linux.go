//go:build linux

package tray

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// watcherProbeTimeout bounds the pre-flight D-Bus round trip so a stalled
// session bus cannot keep the window from opening.
const watcherProbeTimeout = 2 * time.Second

// checkNotificationArea reports whether a StatusNotifierWatcher is present.
// fyne.io/systray only logs a missing watcher, which would leave the process
// alive with neither a window nor an icon, so the caller is told instead.
func checkNotificationArea() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("connect to session bus: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), watcherProbeTimeout)
	defer cancel()
	var watched bool
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&watched); err != nil {
		return fmt.Errorf("query StatusNotifierWatcher: %w", err)
	}
	if !watched {
		return errors.New("no StatusNotifierWatcher on the session bus")
	}
	return nil
}
