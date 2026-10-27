//go:build linux

package tray

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	// The object paths follow the desktop convention so tools that assume them
	// keep working. The host locates the item through the registration call,
	// not through a well-known bus name.
	itemPath = dbus.ObjectPath("/StatusNotifierItem")
	menuPath = dbus.ObjectPath("/StatusNotifierItem/menu")

	watcherName  = "org.kde.StatusNotifierWatcher"
	watcherPath  = dbus.ObjectPath("/StatusNotifierWatcher")
	registerCall = watcherName + ".RegisterStatusNotifierItem"

	// busTimeout bounds the round trips made while mounting and unmounting, so
	// a stalled bus cannot hold up the window.
	busTimeout = 2 * time.Second
)

// emitter sends one signal on the bus. It is a function rather than a *dbus.Conn
// so the protocol objects can be tested without a bus.
type emitter func(path dbus.ObjectPath, iface, member string, args ...any)

// backendLinux mounts the icon on the process-wide session bus. The connection
// is shared with the single-instance guard and the notifier, so it is never
// closed here: it belongs to the process.
type backendLinux struct {
	conn *dbus.Conn
	item *item
	menu *menu

	signals chan *dbus.Signal
	stop    chan struct{}
	matched bool
	once    sync.Once
}

// startBackend publishes the icon and registers it with the notification area.
// Every failure leaves the bus as it was found.
func startBackend(state startState, h Handlers, icons Icons) (backend, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the session bus: %w", err)
	}

	b := &backendLinux{
		conn:    conn,
		signals: make(chan *dbus.Signal, 8),
		stop:    make(chan struct{}),
	}
	b.item = newItem(b.emit, state, icons, h.OnOpen)
	b.menu = newMenu(b.emit, state, h)
	if err := b.export(); err != nil {
		b.unexport()
		return nil, err
	}
	if err := b.register(); err != nil {
		b.unexport()
		return nil, err
	}
	b.watch()
	return b, nil
}

// emit sends a signal. A host that went away is not a reason to disturb the
// user, so failures are logged at debug level.
func (b *backendLinux) emit(path dbus.ObjectPath, iface, member string, args ...any) {
	if err := b.conn.Emit(path, iface+"."+member, args...); err != nil {
		slog.Debug("tray: emit signal", "signal", iface+"."+member, "err", err)
	}
}

// export publishes the item and the menu together with their properties
// interfaces.
func (b *backendLinux) export() error {
	exports := []struct {
		value any
		path  dbus.ObjectPath
		iface string
	}{
		{b.item, itemPath, sniIface},
		{newProperties(sniIface, b.item.properties), itemPath, propertiesIface},
		{b.menu, menuPath, menuIface},
		{newProperties(menuIface, b.menu.properties), menuPath, propertiesIface},
	}
	for _, e := range exports {
		if err := b.conn.Export(e.value, e.path, e.iface); err != nil {
			return fmt.Errorf("export %s at %s: %w", e.iface, e.path, err)
		}
	}
	return nil
}

// unexport withdraws whatever export managed to register. It is called on the
// failure paths of startBackend and from close.
func (b *backendLinux) unexport() {
	for _, object := range []struct {
		path   dbus.ObjectPath
		ifaces []string
	}{
		{itemPath, []string{sniIface, propertiesIface}},
		{menuPath, []string{menuIface, propertiesIface}},
	} {
		for _, iface := range object.ifaces {
			if err := b.conn.Export(nil, object.path, iface); err != nil {
				slog.Debug("tray: withdraw interface", "path", object.path, "interface", iface, "err", err)
			}
		}
	}
}

// register hands the item path to the watcher. The watcher locates the item by
// the sender of this message plus the path, which is why no well-known bus name
// is requested: that name would only fail under a sandboxed bus.
func (b *backendLinux) register() error {
	ctx, cancel := context.WithTimeout(context.Background(), busTimeout)
	defer cancel()
	err := b.conn.Object(watcherName, watcherPath).
		CallWithContext(ctx, registerCall, 0, string(itemPath)).Err
	if err == nil {
		return nil
	}
	if unavailable(err) {
		return fmt.Errorf("no notification area on the session bus: %w", err)
	}
	return fmt.Errorf("register with the notification area: %w", err)
}

// unavailable reports whether err says that nobody serves the watcher name.
// A missing tray and a sandboxed bus without the talk permission look the same
// from here.
func unavailable(err error) bool {
	var derr dbus.Error
	if !errors.As(err, &derr) {
		return false
	}
	switch derr.Name {
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner":
		return true
	}
	return false
}

// watch subscribes to the watcher's owner changes so the icon comes back when
// the desktop shell restarts. Losing the match rule only costs that recovery.
func (b *backendLinux) watch() {
	ctx, cancel := context.WithTimeout(context.Background(), busTimeout)
	defer cancel()
	if err := b.conn.AddMatchSignalContext(ctx, watcherMatch()...); err != nil {
		slog.Debug("tray: cannot watch the notification area", "err", err)
		return
	}
	b.matched = true
	b.conn.Signal(b.signals)
	go b.listen()
}

// watcherMatch is the rule that delivers NameOwnerChanged for the watcher name.
// Add and Remove have to use exactly the same options.
func watcherMatch() []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, watcherName),
	}
}

func (b *backendLinux) listen() {
	for {
		select {
		case <-b.stop:
			return
		case sig, ok := <-b.signals:
			if !ok {
				return
			}
			b.handleSignal(sig)
		}
	}
}

func (b *backendLinux) handleSignal(sig *dbus.Signal) {
	if sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) < 3 {
		return
	}
	name, _ := sig.Body[0].(string)
	owner, _ := sig.Body[2].(string)
	if name != watcherName || owner == "" {
		return
	}
	if err := b.register(); err != nil {
		slog.Debug("tray: re-register with the notification area", "err", err)
	}
}

// close removes the match rule, the signal channel and both exports, then stops
// the listener. The connection stays open: it is shared with the rest of the
// process.
func (b *backendLinux) close() {
	b.once.Do(func() {
		close(b.stop)
		if b.matched {
			ctx, cancel := context.WithTimeout(context.Background(), busTimeout)
			err := b.conn.RemoveMatchSignalContext(ctx, watcherMatch()...)
			cancel()
			if err != nil {
				slog.Debug("tray: stop watching the notification area", "err", err)
			}
			b.conn.RemoveSignal(b.signals)
		}
		b.unexport()
	})
}

func (b *backendLinux) setActive(active bool) { b.item.setActive(active) }

func (b *backendLinux) setTitle(title string) { b.item.setTitle(title) }

func (b *backendLinux) setTooltip(text string) { b.item.setTooltip(text) }

func (b *backendLinux) setMenuLabels(open, quit string) { b.menu.setLabels(open, quit) }
