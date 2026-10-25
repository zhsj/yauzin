//go:build linux

package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	notifyInterface = "org.freedesktop.Notifications"
	notifyPath      = "/org/freedesktop/Notifications"
	// actionKey is the reserved key of the action the notification body runs
	// when the user clicks it.
	actionKey = "default"
	// callTimeout bounds one Notify round trip. The call runs off the caller's
	// goroutine, so a stalled notification server cannot hold up the app.
	callTimeout = 3 * time.Second
	// matchTimeout bounds the bus match calls, which run on the startup and
	// exit paths and must not hang there.
	matchTimeout = 2 * time.Second
)

// notifier publishes notifications over the session bus and turns the
// activation signal back into the caller's callback.
type notifier struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
	done    chan struct{}

	once       sync.Once
	onActivate func()

	// mu guards lastID, the identifier of the notification that the next one
	// replaces. Replacing keeps a burst of new mail to one notification.
	mu     sync.Mutex
	lastID uint32
}

// start subscribes to ActionInvoked and returns the notifier. Any failure
// leaves the app without desktop notifications, which is not fatal.
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
	ctx, cancel := context.WithTimeout(context.Background(), matchTimeout)
	defer cancel()
	if err := conn.AddMatchSignalContext(ctx,
		dbus.WithMatchInterface(notifyInterface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		slog.Debug("subscribe to notification actions", "err", err)
		return nil
	}
	conn.Signal(n.signals)
	go n.listen()
	return n
}

// listen waits for the activation signal. The callback runs here, on a D-Bus
// goroutine, so the caller has to marshal it onto its own thread.
func (n *notifier) listen() {
	for {
		select {
		case <-n.done:
			return
		case sig := <-n.signals:
			if sig == nil || sig.Name != notifyInterface+".ActionInvoked" || len(sig.Body) < 2 {
				continue
			}
			// The match rule also sees other applications' notifications:
			// only the id this notifier published may raise its window.
			if id, ok := sig.Body[0].(uint32); !ok || !n.isOurs(id) {
				continue
			}
			if action, ok := sig.Body[1].(string); ok && action == actionKey && n.onActivate != nil {
				n.onActivate()
			}
		}
	}
}

// isOurs reports whether id is the notification this notifier last published.
func (n *notifier) isOurs(id uint32) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastID != 0 && id == n.lastID
}

// Show publishes the notification in the background.
func (n *notifier) Show(notice Notice) {
	go n.show(notice)
}

func (n *notifier) show(notice Notice) {
	n.mu.Lock()
	replaces := n.lastID
	n.mu.Unlock()

	actions := []string{}
	if notice.Action != "" {
		actions = []string{actionKey, notice.Action}
	}
	appName := notice.AppName
	if appName == "" {
		appName = "yauzin"
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var id uint32
	err := n.conn.Object(notifyInterface, notifyPath).CallWithContext(ctx,
		notifyInterface+".Notify", 0,
		appName,
		replaces,
		"", // no icon: the notification server uses the application icon
		notice.Summary,
		notice.Body,
		actions,
		map[string]dbus.Variant{},
		int32(-1), // server default timeout
	).Store(&id)
	if err != nil {
		slog.Debug("desktop notification failed", "err", err)
		return
	}
	n.mu.Lock()
	n.lastID = id
	n.mu.Unlock()
}

// Close stops listening. The session bus connection is shared with the rest of
// the process, so it is left open.
func (n *notifier) Close() {
	n.once.Do(func() {
		n.conn.RemoveSignal(n.signals)
		ctx, cancel := context.WithTimeout(context.Background(), matchTimeout)
		defer cancel()
		if err := n.conn.RemoveMatchSignalContext(ctx,
			dbus.WithMatchInterface(notifyInterface),
			dbus.WithMatchMember("ActionInvoked"),
		); err != nil {
			slog.Debug("unsubscribe from notification actions", "err", err)
		}
		close(n.done)
	})
}
