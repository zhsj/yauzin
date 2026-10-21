// Package tray keeps an icon in the desktop notification area and exposes its
// menu actions.
//
// All platform work is delegated to fyne.io/systray: on Linux its backend
// speaks org.kde.StatusNotifierItem and com.canonical.dbusmenu over D-Bus, on
// macOS and Windows it uses the native APIs. The only platform specific piece
// here is the pre-flight check that the notification area actually exists,
// which lives in notificationarea_linux.go and notificationarea_other.go.
package tray

import (
	"log/slog"
	"sync"

	"fyne.io/systray"

	"github.com/zhsj/yauzin/internal/i18n"
)

// Handlers are run on goroutines owned by the tray library, so callers must
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
	mu     sync.Mutex
	once   sync.Once
	icons  Icons
	active bool
	// title is the application name shown by the host, and the one the
	// tooltip format is filled with.
	title string
	// open and quit are the menu entries, kept so they can be relabelled
	// after a language change.
	open, quit *systray.MenuItem
	// live is set once the host has been handed the item, so state changes
	// arriving earlier are applied when the icon is published.
	live bool
}

// Start publishes the tray icon and menu. An error means the notification area
// is unavailable and the caller has to fall back to quitting when its window
// closes.
func Start(title string, h Handlers, icons Icons) (*Tray, error) {
	if err := checkNotificationArea(); err != nil {
		return nil, err
	}
	t := &Tray{icons: icons, title: title}
	start, _ := systray.RunWithExternalLoop(func() { t.setup(title, h) }, nil)
	start()

	// The item is exported while the ready callback runs, so re-apply the icon
	// to make sure the host got the variant matching the current state.
	t.mu.Lock()
	t.live = true
	t.mu.Unlock()
	t.applyIcon()
	return t, nil
}

// SetActive switches between the idle and the unread-mail icon. The icon is
// only handed to the host when the state actually changes, so callers can call
// this whenever the mailbox refreshes.
func (t *Tray) SetActive(active bool) {
	if t == nil {
		return
	}
	if len(t.icons.Active) == 0 {
		active = false
	}
	t.mu.Lock()
	changed := t.active != active
	t.active = active
	live := t.live
	t.mu.Unlock()
	if changed && live {
		t.applyIcon()
	}
}

// Relabel updates the application name in the title, the tooltip and the menu
// entries, so a language change shows up in the notification area without a
// restart. It is a no-op when the name did not change or the host never got the
// item, and a nil *Tray is safe to call.
func (t *Tray) Relabel(title string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	changed := t.title != title
	t.title = title
	live := t.live
	open, quit := t.open, t.quit
	t.mu.Unlock()
	if !changed || !live {
		return
	}
	systray.SetTitle(title)
	systray.SetTooltip(i18n.T("tray.tooltip", map[string]string{"Name": title}))
	if open != nil {
		open.SetTitle(i18n.T("tray.open"))
	}
	if quit != nil {
		quit.SetTitle(i18n.T("tray.quit"))
	}
}

// Close removes the icon on a best-effort basis. It deliberately skips the
// library's native teardown, which closes a connection it may not have opened.
func (t *Tray) Close() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("tray shutdown panicked", "recover", r)
			}
		}()
		systray.Quit()
	})
}

// setup runs from the library's ready callback, before the item is exported.
func (t *Tray) setup(title string, h Handlers) {
	t.applyIcon()
	systray.SetTitle(title)
	systray.SetTooltip(i18n.T("tray.tooltip", map[string]string{"Name": title}))

	if h.OnOpen != nil {
		// Setting the left-click handler clears ItemIsMenu, so the host
		// activates the item instead of opening the menu on a left click.
		systray.SetOnTapped(h.OnOpen)
	}
	open := systray.AddMenuItem(i18n.T("tray.open"), "")
	quit := systray.AddMenuItem(i18n.T("tray.quit"), "")
	t.mu.Lock()
	t.title = title
	t.open, t.quit = open, quit
	t.mu.Unlock()
	go func() {
		for {
			select {
			case <-open.ClickedCh:
				if h.OnOpen != nil {
					h.OnOpen()
				}
			case <-quit.ClickedCh:
				if h.OnQuit != nil {
					h.OnQuit()
				}
				return
			}
		}
	}()
}

func (t *Tray) applyIcon() {
	t.mu.Lock()
	icon := t.icons.Idle
	if t.active && len(t.icons.Active) > 0 {
		icon = t.icons.Active
	}
	t.mu.Unlock()
	systray.SetIcon(icon)
}
