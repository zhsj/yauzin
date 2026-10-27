// Package notify sends desktop notifications.
//
// Two channels exist on Linux, and both implement the same contract:
// org.freedesktop.Notifications on the session bus, and the XDG desktop portal
// (org.freedesktop.portal.Notification). The channel is picked once, when the
// first notice is due, by asking the bus whether the notification server owns
// its name; the portal is the normal answer inside a Flatpak sandbox that is
// not granted that name.
//
// The contract is the intersection of what both channels can express:
//
//   - a title, a body and a single action the click runs;
//   - at most one notification per application, a new one replacing the
//     previous;
//   - the notification disappears once the user clicked it;
//   - the application identity (name, icon, history, per-application settings)
//     comes from the desktop file me.zhsj.yauzin, not from the call.
//
// Whatever only one channel has stays inside that channel: the replacement id,
// the close signal and the explicit expire timeout belong to the notification
// server, so a popup stays up for 30 seconds there and for whatever the desktop
// is configured for otherwise.
//
// On other platforms Start returns nil and callers simply do not notify.
package notify

// Notice is one desktop notification.
type Notice struct {
	// Summary is the title line.
	Summary string
	// Body is the text below the title.
	Body string
	// Action labels the default button. Empty hides the button and makes the
	// plain body click the only activation.
	Action string
}

// Notifier publishes desktop notifications. Implementations must be safe for
// concurrent use; Show returns before the notification service answers.
type Notifier interface {
	Show(Notice)
	Close()
}

// Start begins listening for notification activations and returns the
// notifier. A nil result means desktop notifications are unavailable; the
// caller keeps working without them. onActivate runs on the transport's own
// goroutine and must marshal itself onto the UI thread.
func Start(onActivate func()) Notifier {
	return start(onActivate)
}
