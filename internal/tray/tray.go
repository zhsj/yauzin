// Package tray keeps an icon in the desktop notification area and exposes its
// menu actions.
//
// On Linux the icon is published by the package itself: the backend speaks
// org.kde.StatusNotifierItem and com.canonical.dbusmenu over the session bus
// and registers with org.kde.StatusNotifierWatcher. The bus connection is the
// process-wide shared one, so Close only withdraws what the backend exported
// and subscribed; the connection itself belongs to the process. Other platforms
// have no backend yet: Start reports the notification area as unavailable and
// the caller falls back to quitting with its window.
package tray

import (
	"sync"

	"github.com/zhsj/yauzin/internal/i18n"
)

// Handlers are run on goroutines owned by the backend, so callers must
// marshal them onto their own UI thread.
type Handlers struct {
	// OnOpen fires when the icon is activated or the "Open" entry is selected.
	OnOpen func()
	// OnQuit fires when the "Quit" entry is selected.
	OnQuit func()
}

// Icons holds the two PNG encoded variants of the tray icon: Idle while there
// is nothing unread, Active while unread mail is waiting.
type Icons struct {
	Idle   []byte
	Active []byte
}

// Tray is a registered notification-area icon. A nil *Tray is a no-op.
type Tray struct {
	once    sync.Once
	backend backend
}

// backend is the platform side of the icon. Its methods are called without any
// lock held and must be safe for concurrent use: the UI thread changes state
// while D-Bus goroutines read it. Each setter drops an update that does not
// change the published state.
type backend interface {
	setActive(active bool)
	setTitle(title string)
	setTooltip(text string)
	setMenuLabels(open, quit string)
	close()
}

// startState is the copy a backend publishes while it mounts the icon.
type startState struct {
	title   string
	tooltip string
	open    string
	quit    string
}

// Start publishes the tray icon and menu. An error means the notification area
// is unavailable and the caller has to fall back to quitting when its window
// closes.
func Start(title string, h Handlers, icons Icons) (*Tray, error) {
	b, err := startBackend(startState{
		title:   title,
		tooltip: i18n.T("tray.tooltip", map[string]string{"Name": title}),
		open:    i18n.T("tray.open"),
		quit:    i18n.T("tray.quit"),
	}, h, icons)
	if err != nil {
		return nil, err
	}
	return &Tray{backend: b}, nil
}

// SetActive switches between the idle and the unread-mail icon. The backend
// drops the update when the state did not change, so callers can call this
// whenever the mailbox refreshes.
func (t *Tray) SetActive(active bool) {
	if t == nil || t.backend == nil {
		return
	}
	t.backend.setActive(active)
}

// Relabel updates the application name in the title, the tooltip and the menu
// entries, so a language change shows up in the notification area without a
// restart. A nil *Tray is safe to call.
func (t *Tray) Relabel(title string) {
	if t == nil || t.backend == nil {
		return
	}
	t.backend.setTitle(title)
	t.backend.setTooltip(i18n.T("tray.tooltip", map[string]string{"Name": title}))
	t.backend.setMenuLabels(i18n.T("tray.open"), i18n.T("tray.quit"))
}

// Close withdraws the icon. The session bus connection is shared with the rest
// of the process, so the backend only removes its exports and match rules; the
// connection stays open. A nil *Tray is safe to call and repeated calls are
// ignored.
func (t *Tray) Close() {
	if t == nil || t.backend == nil {
		return
	}
	t.once.Do(func() {
		t.backend.close()
	})
}
