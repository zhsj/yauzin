//go:build linux

package notify

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/zhsj/yauzin/internal/singleinstance"
)

// notificationSignal builds one signal of the notification interface.
func notificationSignal(member string, body ...any) *dbus.Signal {
	return &dbus.Signal{Name: notifyInterface + "." + member, Body: body}
}

func TestHandleSignal(t *testing.T) {
	tests := []struct {
		name       string
		sig        *dbus.Signal
		lastID     uint32
		want       bool
		wantLastID uint32
	}{
		{
			name:       "default action of our notification",
			sig:        notificationSignal("ActionInvoked", uint32(7), actionKey),
			lastID:     7,
			want:       true,
			wantLastID: 7,
		},
		{
			name:       "action of another application",
			sig:        notificationSignal("ActionInvoked", uint32(9), actionKey),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "action that is not the default one",
			sig:        notificationSignal("ActionInvoked", uint32(7), "other"),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "action without an id",
			sig:        notificationSignal("ActionInvoked"),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "action with a foreign body",
			sig:        notificationSignal("ActionInvoked", "7", actionKey),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "close of our notification",
			sig:        notificationSignal("NotificationClosed", uint32(7), uint32(1)),
			lastID:     7,
			wantLastID: 0,
		},
		{
			name:       "close of another notification",
			sig:        notificationSignal("NotificationClosed", uint32(9), uint32(1)),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "close without an id",
			sig:        notificationSignal("NotificationClosed"),
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "signal of another interface",
			sig:        &dbus.Signal{Name: "org.example.Other.Changed", Body: []any{uint32(7)}},
			lastID:     7,
			wantLastID: 7,
		},
		{
			name:       "nil signal",
			lastID:     7,
			wantLastID: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &serverChannel{lastID: tt.lastID}
			got := c.activated(tt.sig)
			if !got {
				c.closed(tt.sig)
			}
			if got != tt.want {
				t.Fatalf("activated = %v, want %v", got, tt.want)
			}
			if c.lastID != tt.wantLastID {
				t.Fatalf("lastID = %d, want %d", c.lastID, tt.wantLastID)
			}
		})
	}
}

// TestHandleSignalActivationAfterClose guards the reason for tracking closes:
// once the notification is gone, a late click must not raise the window and
// the next notice has to be published as a new one.
func TestHandleSignalActivationAfterClose(t *testing.T) {
	c := &serverChannel{lastID: 4}
	c.closed(notificationSignal("NotificationClosed", uint32(4), uint32(1)))
	if c.activated(notificationSignal("ActionInvoked", uint32(4), actionKey)) {
		t.Fatal("activation of a closed notification must be ignored")
	}
}

// TestNotificationHints guards the hint the notification server needs to
// attribute the notification to this application: its value has to stay the
// desktop file id, which is the Gio application id as well.
func TestNotificationHints(t *testing.T) {
	hints := notificationHints()
	if len(hints) != 1 {
		t.Fatalf("hints = %v, want desktop-entry only", hints)
	}
	variant, ok := hints["desktop-entry"]
	if !ok {
		t.Fatal("desktop-entry hint is missing")
	}
	if sig := variant.Signature().String(); sig != "s" {
		t.Fatalf("desktop-entry signature = %q, want %q", sig, "s")
	}
	value, ok := variant.Value().(string)
	if !ok {
		t.Fatalf("desktop-entry value = %T, want a string", variant.Value())
	}
	if value != singleinstance.Name {
		t.Fatalf("desktop-entry = %q, want %q", value, singleinstance.Name)
	}

	hints["other"] = dbus.MakeVariant("x")
	if _, ok := notificationHints()["other"]; ok {
		t.Fatal("notificationHints must hand out a fresh map")
	}
}

// TestChannelFor guards the one decision the notifier makes: the notification
// server where the bus knows the name, the portal everywhere else. The bus
// proxy of a Flatpak sandbox reports a name the manifest does not grant as
// NameHasNoOwner, so a rejected probe is the normal case there.
func TestChannelFor(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		server bool
	}{
		{
			name:   "notification server present",
			server: true,
		},
		{
			name: "name filtered by the bus proxy",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner"},
		},
		{
			name: "no notification server at all",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreProbe(t)
			probeServer = func(context.Context, *dbus.Conn) error { return tt.err }
			ch := channelFor(nil)
			if _, ok := ch.(*serverChannel); ok != tt.server {
				t.Fatalf("channelFor with %v = %T, want the server: %v", tt.err, ch, tt.server)
			}
		})
	}
}

// TestChannelWatchesChosenInterface guards that only the interface of the
// channel the probe picked is subscribed, and that it happens on the first
// notice rather than at start.
func TestChannelWatchesChosenInterface(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "notification server present",
			want: notifyInterface,
		},
		{
			name: "name filtered by the bus proxy",
			err:  dbus.Error{Name: "org.freedesktop.DBus.Error.NameHasNoOwner"},
			want: portalInterface,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreProbe(t)
			probeServer = func(context.Context, *dbus.Conn) error { return tt.err }
			watched := stubWatch(t)
			(&notifier{}).channel()
			if len(*watched) != 1 || (*watched)[0] != tt.want {
				t.Fatalf("watched = %v, want only %q", *watched, tt.want)
			}
		})
	}
}

// TestChannelProbedOnce guards the lazily chosen channel: the first call probes
// the bus and subscribes once, every later call reuses what it picked.
func TestChannelProbedOnce(t *testing.T) {
	restoreProbe(t)
	probes := 0
	probeServer = func(context.Context, *dbus.Conn) error {
		probes++
		return nil
	}
	watched := stubWatch(t)
	n := &notifier{}
	if n.selected() != nil {
		t.Fatal("no channel may be chosen before the first notice")
	}
	first := n.channel()
	if second := n.channel(); second != first {
		t.Fatalf("channel changed from %T to %T, want one channel per process", first, second)
	}
	if probes != 1 {
		t.Fatalf("probes = %d, want 1", probes)
	}
	if len(*watched) != 1 {
		t.Fatalf("watched = %v, want a single match rule", *watched)
	}
}

// restoreProbe puts the real probe back after a test replaced it.
func restoreProbe(t *testing.T) {
	t.Helper()
	original := probeServer
	t.Cleanup(func() { probeServer = original })
}

// stubWatch replaces the match call and returns the interfaces it was asked to
// watch.
func stubWatch(t *testing.T) *[]string {
	t.Helper()
	original := watchSignals
	watched := &[]string{}
	watchSignals = func(_ context.Context, _ *dbus.Conn, iface string) error {
		*watched = append(*watched, iface)
		return nil
	}
	t.Cleanup(func() { watchSignals = original })
	return watched
}

// TestPortalNotification guards the dictionary AddNotification takes: the
// portal has no replacement id and no expire timeout, so the text and the
// action are all that travels, and the action has to be spelled as the default
// action as well as a button. The signatures matter as much as the values: the
// portal rejects the whole notification when one key carries the wrong type.
func TestPortalNotification(t *testing.T) {
	notice := Notice{
		Summary: "3 new messages",
		Body:    "First subject",
		Action:  "Open inbox",
	}
	notification := portalNotification(notice)
	if got := stringOf(t, notification, "title"); got != notice.Summary {
		t.Fatalf("title = %q, want %q", got, notice.Summary)
	}
	if got := stringOf(t, notification, "body"); got != notice.Body {
		t.Fatalf("body = %q, want %q", got, notice.Body)
	}
	if got := stringOf(t, notification, "default-action"); got != actionKey {
		t.Fatalf("default-action = %q, want %q", got, actionKey)
	}
	for key, want := range map[string]string{
		"title":          "s",
		"body":           "s",
		"default-action": "s",
		"buttons":        "aa{sv}",
	} {
		if got := signatureOf(t, notification, key); got != want {
			t.Fatalf("%s signature = %q, want %q", key, got, want)
		}
	}
	for _, key := range []string{"replaces_id", "expire_timeout"} {
		if _, ok := notification[key]; ok {
			t.Fatalf("%s is set, but the portal does not have it", key)
		}
	}

	list, ok := notification["buttons"].Value().([]map[string]dbus.Variant)
	if !ok || len(list) != 1 {
		t.Fatalf("buttons = %v, want one button dictionary", notification["buttons"].Value())
	}
	if got, _ := list[0]["label"].Value().(string); got != notice.Action {
		t.Fatalf("button label = %q, want %q", got, notice.Action)
	}
	if got, _ := list[0]["action"].Value().(string); got != actionKey {
		t.Fatalf("button action = %v, want %q", list[0]["action"], actionKey)
	}

	bare := portalNotification(Notice{Summary: "3 new messages", Body: "First subject"})
	if _, ok := bare["buttons"]; ok {
		t.Fatal("a notice without an action must not carry buttons")
	}
	if _, ok := bare["default-action"]; ok {
		t.Fatal("a notice without an action must not carry a default action")
	}
}

// signatureOf returns the D-Bus signature a notification dictionary holds under
// key.
func signatureOf(t *testing.T, notification map[string]dbus.Variant, key string) string {
	t.Helper()
	value, ok := notification[key]
	if !ok {
		t.Fatalf("%s is missing", key)
	}
	return value.Signature().String()
}

// stringOf returns the string a notification dictionary holds under key.
func stringOf(t *testing.T, notification map[string]dbus.Variant, key string) string {
	t.Helper()
	value, ok := notification[key]
	if !ok {
		t.Fatalf("%s is missing", key)
	}
	s, ok := value.Value().(string)
	if !ok {
		t.Fatalf("%s = %T, want a string", key, value.Value())
	}
	return s
}

// TestPortalActivation guards the click callback on the portal channel: the
// portal sends the notification id as a string and the action as a separate
// argument, and only this application's own notification counts.
func TestPortalActivation(t *testing.T) {
	signal := func(member string, body ...any) *dbus.Signal {
		return &dbus.Signal{Name: portalInterface + "." + member, Body: body}
	}
	tests := []struct {
		name string
		sig  *dbus.Signal
		want bool
	}{
		{
			name: "default action of our notification",
			sig:  signal("ActionInvoked", portalNoticeID, actionKey, []dbus.Variant{}),
			want: true,
		},
		{
			name: "action of another application",
			sig:  signal("ActionInvoked", "elsewhere", actionKey, []dbus.Variant{}),
		},
		{
			name: "action that is not the default one",
			sig:  signal("ActionInvoked", portalNoticeID, "other", []dbus.Variant{}),
		},
		{
			name: "short body",
			sig:  signal("ActionInvoked", portalNoticeID),
		},
		{
			name: "id of the notification server",
			sig:  signal("ActionInvoked", uint32(7), actionKey),
		},
		{
			name: "signal of another interface",
			sig:  &dbus.Signal{Name: notifyInterface + ".ActionInvoked", Body: []any{portalNoticeID, actionKey}},
		},
		{
			name: "nil signal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := portalActivation(tt.sig); got != tt.want {
				t.Fatalf("portalActivation = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHandleSignalWithoutAChannel keeps an unrelated notification from choosing
// the channel: before the first notice the notifier has not published anything,
// so no signal can be ours.
func TestHandleSignalWithoutAChannel(t *testing.T) {
	restoreProbe(t)
	probed := false
	probeServer = func(context.Context, *dbus.Conn) error {
		probed = true
		return nil
	}
	n := &notifier{}
	n.handleSignal(notificationSignal("ActionInvoked", uint32(7), actionKey))
	if probed {
		t.Fatal("a signal must not probe the bus")
	}
	if n.selected() != nil {
		t.Fatal("a signal must not choose the channel")
	}
}
