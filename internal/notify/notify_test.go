//go:build linux

package notify

import (
	"testing"

	"github.com/godbus/dbus/v5"
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
			n := &notifier{lastID: tt.lastID}
			if got := n.handleSignal(tt.sig); got != tt.want {
				t.Fatalf("handleSignal = %v, want %v", got, tt.want)
			}
			if n.lastID != tt.wantLastID {
				t.Fatalf("lastID = %d, want %d", n.lastID, tt.wantLastID)
			}
		})
	}
}

// TestHandleSignalActivationAfterClose guards the reason for tracking closes:
// once the notification is gone, a late click must not raise the window and
// the next notice has to be published as a new one.
func TestHandleSignalActivationAfterClose(t *testing.T) {
	n := &notifier{lastID: 4}
	n.handleSignal(notificationSignal("NotificationClosed", uint32(4), uint32(1)))
	if n.handleSignal(notificationSignal("ActionInvoked", uint32(4), actionKey)) {
		t.Fatal("activation of a closed notification must be ignored")
	}
}
