package ui

import (
	"strings"

	"gioui.org/app"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/gmail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// updateSettings reads the stored settings, lets mutate change them and writes
// them back. It reports false when the read or the write failed and the user
// was already told, so the caller leaves the running state alone: a choice only
// takes effect once it is stored.
func (a *App) updateSettings(mutate func(*prefs.Settings)) bool {
	// Read before mutating: writing the zero value back after a failed read
	// would drop the settings the user did not touch.
	settings, err := prefs.LoadOrZero()
	if err != nil {
		logErr("read settings", err)
		a.notify(err.Error(), true)
		return false
	}
	mutate(&settings)
	if err := prefs.Save(settings); err != nil {
		logErr("save settings", err)
		a.notify(err.Error(), true)
		return false
	}
	return true
}

// rebuildSettingsPanel swaps in a fresh panel so its copy and check marks
// follow the new choice, and schedules the redraw that shows it.
func (a *App) rebuildSettingsPanel() {
	a.settings = newSettingsView(a)
	a.overlay = a.settings
	if w := a.window(); w != nil {
		w.Invalidate()
	}
}

// setLanguage switches the interface language and applies it to the running
// window: the views that build their labels once are rebuilt, the window title
// and the tray follow, and the choice is stored for the next start.
func (a *App) setLanguage(tag string) {
	// The switch happens inside mutate so it precedes the save, which keeps the
	// old behaviour: a failed save leaves the new language active but unrecorded.
	if !a.updateSettings(func(s *prefs.Settings) {
		s.Language = i18n.SetLanguage(tag)
	}) {
		return
	}

	// The sidebar and the message cards cache their labels; every other view
	// translates while laying out.
	a.side = newSidebar(a)
	a.side.reload()
	a.reader.relabel()
	brand := i18n.T("app.brand")
	if w := a.window(); w != nil {
		w.Option(app.Title(brand))
	}
	a.tray.Relabel(brand)

	a.notify(i18n.T("toast.languageChanged"), false)
	// Rebuild the panel last, so its copy and the check mark use the new
	// language.
	a.rebuildSettingsPanel()
}

// setClientDecorations switches between the system decoration and the title bar
// drawn by the app. It applies on the next frame: the window was already opened
// with decoration left to the app, so only the layout changes.
func (a *App) setClientDecorations(on bool) {
	if a.csd == on {
		return
	}
	if !a.updateSettings(func(s *prefs.Settings) { s.ClientDecorations = on }) {
		return
	}
	a.csd = on
	a.rebuildSettingsPanel()
}

// setPerfMonitor shows or hides the performance readout in the top bar and
// persists the choice. The sampler only runs while the readout is visible.
func (a *App) setPerfMonitor(on bool) {
	if a.perf.isVisible() == on {
		return
	}
	if !a.updateSettings(func(s *prefs.Settings) { s.ShowPerfMonitor = on }) {
		return
	}
	a.perf.setVisible(on)
	a.rebuildSettingsPanel()
}

// setProxy validates and stores the shared proxy address, then replaces the
// Gmail backends that opt into it. A returned error is meant for the settings
// form; a storage failure has already been reported by updateSettings.
func (a *App) setProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if _, err := gmail.ParseProxy(raw); err != nil {
		return err
	}
	if !a.updateSettings(func(s *prefs.Settings) { s.Proxy = raw }) {
		return nil
	}
	a.proxy = raw
	if a.factory != nil {
		a.factory.proxy = raw
	}
	a.rebuildProxiedServices()
	a.notify(i18n.T("toast.proxySaved"), false)
	return nil
}

// rebuildProxiedServices swaps the backend of every Gmail account that opts
// into the proxy, so a new address is used without a restart. In-flight
// requests are cancelled first: they belong to the clients being replaced.
func (a *App) rebuildProxiedServices() {
	if a.factory == nil || a.accountStore == nil {
		return
	}
	var targets []mail.Account
	for _, acc := range a.accounts {
		if acc.ID == demoAccountID {
			continue
		}
		if entry, ok := a.accountStore.Entry(acc.ID); ok && entry.UseProxy {
			targets = append(targets, acc)
		}
	}
	if len(targets) == 0 {
		return
	}
	a.cancelRequests()
	for _, acc := range targets {
		svc := a.factory.newService(acc)
		if svc == nil {
			continue
		}
		if old := a.services[acc.ID]; old != nil {
			_ = old.Close()
		}
		a.services[acc.ID] = svc
	}
	a.reloadFolders()
	a.refreshThreads()
}
