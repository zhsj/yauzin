// Package notify sends desktop notifications.
//
// Only Linux is implemented: the notification is handed to the session bus
// service org.freedesktop.Notifications. On other platforms Start returns nil
// and callers simply do not notify.
package notify

// Notice is one desktop notification.
type Notice struct {
	// AppName is the application the notification belongs to.
	AppName string
	// Summary is the title line.
	Summary string
	// Body is the text below the title.
	Body string
	// Action labels the default button. Empty hides the button.
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
