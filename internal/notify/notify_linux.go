//go:build linux

package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/zhsj/yauzin/internal/singleinstance"
)

const (
	notifyInterface = "org.freedesktop.Notifications"
	notifyPath      = "/org/freedesktop/Notifications"
	// actionKey is the reserved key of the action the notification body runs
	// when the user clicks it.
	actionKey = "default"
	// callTimeout bounds one round trip to either channel. The calls run off
	// the caller's goroutine, so a stalled service cannot hold up the app.
	callTimeout = 3 * time.Second
	// matchTimeout bounds the bus match calls, which run on the startup and
	// exit paths and must not hang there.
	matchTimeout = 2 * time.Second
	// noticeTimeout is how long the notification server keeps the popup on
	// screen. It is stated explicitly instead of leaving the choice to the
	// server: Plasma's own default is about five seconds, which is too short
	// for a body of three subjects and a count. Per-application settings
	// still override it. The portal has no such option, so it stays a
	// difference between the channels.
	noticeTimeout = 30 * time.Second

	// The portal is the fallback channel. It is meant for sandboxed
	// applications, and inside a Flatpak build it is the only channel: the
	// session bus proxy answers ServiceUnknown for a name the manifest does
	// not grant, which looks exactly like a missing notification server.
	portalDest      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	portalInterface = "org.freedesktop.portal.Notification"
	// portalNoticeID is the one notification the portal keeps for this app.
	// AddNotification with the same id updates it in place, standing in for
	// replaces_id, which the portal does not have.
	portalNoticeID = "unread"
)

// channel is the contract both delivery channels implement: what the package
// offers, and nothing else. Whatever only one of them has stays inside it.
type channel interface {
	// iface is the bus interface this channel reports its activations on.
	iface() string
	// show publishes the notice, replacing the notification published before.
	show(ctx context.Context, notice Notice) error
	// activated reports whether sig is the user clicking our notification.
	activated(sig *dbus.Signal) bool
	// closed handles the service reporting that the notification is gone.
	closed(sig *dbus.Signal)
	// dismiss removes the notification the user just clicked.
	dismiss()
}

// serverChannel publishes to the notification server on the session bus.
type serverChannel struct {
	conn *dbus.Conn

	// mu guards lastID, the identifier of the notification the server still
	// shows. While it is set the next notice replaces it, which keeps a burst
	// of new mail to one notification; the close signal clears it.
	mu     sync.Mutex
	lastID uint32
}

// iface reports the notification server interface.
func (c *serverChannel) iface() string { return notifyInterface }

// show publishes one notification. The application name is left empty on
// purpose: the server resolves it from the desktop-entry hint, the same source
// the portal uses, so both channels display the same name and it no longer
// follows the interface language.
func (c *serverChannel) show(ctx context.Context, notice Notice) error {
	c.mu.Lock()
	replaces := c.lastID
	c.mu.Unlock()

	actions := []string{}
	if notice.Action != "" {
		actions = []string{actionKey, notice.Action}
	}

	var id uint32
	err := c.conn.Object(notifyInterface, notifyPath).CallWithContext(ctx,
		notifyInterface+".Notify", 0,
		"", // no application name: desktop-entry decides it
		replaces,
		"", // no icon: the notification server uses the application icon
		notice.Summary,
		notice.Body,
		actions,
		notificationHints(),
		int32(noticeTimeout/time.Millisecond),
	).Store(&id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.lastID = id
	c.mu.Unlock()
	return nil
}

// activated reports whether sig is a click on the notification this channel
// published. The match rule also sees other applications' notifications, so
// the id is checked first.
func (c *serverChannel) activated(sig *dbus.Signal) bool {
	if sig == nil || sig.Name != notifyInterface+".ActionInvoked" {
		return false
	}
	if len(sig.Body) < 2 {
		return false
	}
	if id, ok := sig.Body[0].(uint32); !ok || !c.isOurs(id) {
		return false
	}
	action, ok := sig.Body[1].(string)
	return ok && action == actionKey
}

// closed forgets the notification the server has closed. The next show then
// creates a new notification instead of updating one that is gone: KDE Plasma
// only refreshes the tray entry, and keeps the single history entry, when
// replaces_id names a notification that already timed out.
func (c *serverChannel) closed(sig *dbus.Signal) {
	if sig == nil || sig.Name != notifyInterface+".NotificationClosed" {
		return
	}
	if len(sig.Body) < 1 {
		return
	}
	id, ok := sig.Body[0].(uint32)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == c.lastID {
		c.lastID = 0
	}
}

// dismiss is empty: the notification server closes the notification itself
// once the user activates one of its actions.
func (c *serverChannel) dismiss() {}

// isOurs reports whether id is the notification this channel last published.
func (c *serverChannel) isOurs(id uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastID != 0 && id == c.lastID
}

// portalChannel publishes through the XDG desktop portal.
type portalChannel struct {
	conn *dbus.Conn
}

// iface reports the portal notification interface.
func (c *portalChannel) iface() string { return portalInterface }

// show publishes or updates the one notification this application has. The
// portal takes the application name and icon from the application id.
func (c *portalChannel) show(ctx context.Context, notice Notice) error {
	return c.conn.Object(portalDest, portalPath).CallWithContext(ctx,
		portalInterface+".AddNotification", 0,
		portalNoticeID, portalNotification(notice),
	).Err
}

// activated reports whether sig is a click on the portal notification this
// application published.
func (c *portalChannel) activated(sig *dbus.Signal) bool {
	return portalActivation(sig)
}

// closed is empty: the portal never reports that a notification is gone.
func (c *portalChannel) closed(*dbus.Signal) {}

// dismiss drops the portal notification the user just clicked. The portal has
// no close signal, so the app cannot tell a dismissal from a timeout; the
// click is the one moment it knows the notification is done.
func (c *portalChannel) dismiss() {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := c.conn.Object(portalDest, portalPath).CallWithContext(ctx,
		portalInterface+".RemoveNotification", 0, portalNoticeID,
	).Err; err != nil {
		slog.Debug("remove portal notification", "err", err)
	}
}

// portalActivation reports whether sig is the default action of the portal
// notification this application publishes. The portal signals the notification
// id, the action and the parameters of the action target, which no notice
// uses.
func portalActivation(sig *dbus.Signal) bool {
	if sig == nil || sig.Name != portalInterface+".ActionInvoked" {
		return false
	}
	if len(sig.Body) < 2 {
		return false
	}
	id, ok := sig.Body[0].(string)
	if !ok || id != portalNoticeID {
		return false
	}
	action, ok := sig.Body[1].(string)
	return ok && action == actionKey
}

// notifier publishes notifications over the session bus and turns the
// activation signal back into the caller's callback.
type notifier struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
	done    chan struct{}

	once       sync.Once
	onActivate func()

	// mu guards ch, the channel notices go out on.
	mu sync.Mutex
	// ch is chosen by the first notice and never changes afterwards. It stays
	// nil until then, because the probe would answer too early at startup: a
	// shell that takes the notification name later in the session would be
	// missed.
	ch channel
}

// start connects the notifier to the session bus and returns it. Any failure
// leaves the app without desktop notifications, which is not fatal. Nothing is
// subscribed here: the interface to watch is only known once the first notice
// picks a channel.
func start(onActivate func()) Notifier {
	conn, err := dbus.SessionBus()
	if err != nil {
		slog.Debug("desktop notifications unavailable", "err", err)
		return nil
	}
	n := &notifier{
		conn:       conn,
		signals:    make(chan *dbus.Signal, 8),
		done:       make(chan struct{}),
		onActivate: onActivate,
	}
	conn.Signal(n.signals)
	go n.listen()
	return n
}

// listen waits for the notification signals. The activation callback runs
// here, on a D-Bus goroutine, so the caller has to marshal it onto its own
// thread.
func (n *notifier) listen() {
	for {
		select {
		case <-n.done:
			return
		case sig := <-n.signals:
			n.handleSignal(sig)
		}
	}
}

// handleSignal routes one signal through the channel the notices go out on.
func (n *notifier) handleSignal(sig *dbus.Signal) {
	ch := n.selected()
	if ch == nil {
		// Nothing has been published, so no signal can be ours, and an
		// unrelated notification must not decide the channel.
		return
	}
	if !ch.activated(sig) {
		ch.closed(sig)
		return
	}
	if n.onActivate != nil {
		n.onActivate()
	}
	// The removal only cleans up the popup, so it runs aside: the window has
	// to come back even if the service stalls.
	go ch.dismiss()
}

// channel returns the channel notices go out on, probing the bus on the first
// notice. The probe runs under the lock, so concurrent notices wait for it
// instead of querying the bus each.
func (n *notifier) channel() channel {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ch == nil {
		ch := channelFor(n.conn)
		ctx, cancel := context.WithTimeout(context.Background(), matchTimeout)
		defer cancel()
		// Only the chosen channel is watched, which is why this happens here
		// rather than in start: the interface that carries no notice never
		// costs a match rule.
		if err := watchSignals(ctx, n.conn, ch.iface()); err != nil {
			slog.Debug("subscribe to notification signals", "interface", ch.iface(), "err", err)
		}
		n.ch = ch
	}
	return n.ch
}

// selected returns the channel the first notice chose, or nil while no notice
// has been published yet.
func (n *notifier) selected() channel {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// probeServer reports whether the notification server is on the session bus.
// The bus proxy of a Flatpak sandbox answers a name the manifest does not grant
// with NameHasNoOwner, so one call covers a missing server and a filtered name
// alike. It is a variable so tests can answer without a bus.
var probeServer = func(ctx context.Context, conn *dbus.Conn) error {
	var owner string
	return conn.BusObject().CallWithContext(ctx,
		"org.freedesktop.DBus.GetNameOwner", 0, notifyInterface).Store(&owner)
}

// watchSignals subscribes the connection to the activations of one interface.
// Like probeServer it is a variable, so tests can follow the choice without a
// session bus.
var watchSignals = func(ctx context.Context, conn *dbus.Conn, iface string) error {
	return conn.AddMatchSignalContext(ctx, dbus.WithMatchInterface(iface))
}

// channelFor picks the channel once per process: the notification server when
// the probe finds it, the portal otherwise.
func channelFor(conn *dbus.Conn) channel {
	ctx, cancel := context.WithTimeout(context.Background(), matchTimeout)
	defer cancel()
	err := probeServer(ctx, conn)
	if err == nil {
		return &serverChannel{conn: conn}
	}
	slog.Info("desktop notification server unavailable, using the desktop portal", "err", err)
	return &portalChannel{conn: conn}
}

// notificationHints builds the hints sent with every notification. The only
// one is desktop-entry, the desktop file id the notification server attributes
// the notification to: an id it cannot resolve still shows the popup, but keeps
// the notification out of the history and out of the per-application settings.
// It is the Gio application id and the single-instance bus name as well, and
// the portal resolves the very same file from its application id.
func notificationHints() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant(singleinstance.Name),
	}
}

// portalNotification renders a notice as the dictionary AddNotification takes.
// The portal has no replacement id, no expire timeout and no close signal, so
// only the text and the action travel here. Each button is a dictionary of its
// own, which is why the portal types the list as aa{sv}.
func portalNotification(notice Notice) map[string]dbus.Variant {
	notification := map[string]dbus.Variant{
		"title": dbus.MakeVariant(notice.Summary),
		"body":  dbus.MakeVariant(notice.Body),
	}
	if notice.Action != "" {
		// Both keys are set: the body click is the action on every portal, and
		// the button spells it out where the portal renders buttons.
		notification["default-action"] = dbus.MakeVariant(actionKey)
		notification["buttons"] = dbus.MakeVariant([]map[string]dbus.Variant{{
			"label":  dbus.MakeVariant(notice.Action),
			"action": dbus.MakeVariant(actionKey),
		}})
	}
	return notification
}

// Show publishes the notification in the background.
func (n *notifier) Show(notice Notice) {
	go n.show(notice)
}

func (n *notifier) show(notice Notice) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if err := n.channel().show(ctx, notice); err != nil {
		slog.Debug("desktop notification failed", "err", err)
	}
}

// Close stops listening. The session bus connection is shared with the rest of
// the process, so it is left open, and the portal notification is left in the
// history the same way the notification server keeps its own after an exit.
func (n *notifier) Close() {
	n.once.Do(func() {
		n.conn.RemoveSignal(n.signals)
		ctx, cancel := context.WithTimeout(context.Background(), matchTimeout)
		defer cancel()
		// Only the chosen channel was watched, and only when a notice was
		// published at all.
		if ch := n.selected(); ch != nil {
			if err := n.conn.RemoveMatchSignalContext(ctx,
				dbus.WithMatchInterface(ch.iface()),
			); err != nil {
				slog.Debug("unsubscribe from notification signals", "interface", ch.iface(), "err", err)
			}
		}
		close(n.done)
	})
}
