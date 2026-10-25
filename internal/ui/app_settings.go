package ui

import (
	"errors"
	"strings"

	"gioui.org/app"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/logging"
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
// with decoration left to the app, so only the layout changes. The settings
// panel is left alone rather than rebuilt: its switch row mirrors the new
// state and the layout reads a.csd every frame, so rebuilding would only throw
// away view state such as the expanded language list.
func (a *App) setClientDecorations(on bool) {
	if a.csd == on {
		return
	}
	if !a.updateSettings(func(s *prefs.Settings) { s.ClientDecorations = on }) {
		return
	}
	a.csd = on
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
}

// setLogLevel changes the console log level and persists the choice. The
// logger is process wide, so the new level also covers the backends that are
// already running; nothing has to be rebuilt.
func (a *App) setLogLevel(name string) {
	name = logging.Normalize(name)
	if a.logLevel == name {
		return
	}
	if !a.updateSettings(func(s *prefs.Settings) { s.LogLevel = name }) {
		return
	}
	a.logLevel = logging.SetLevel(name)
}

// rememberActiveAccount stores id as the mailbox to reopen on the next start.
// The switch has already happened, so a failed read or write is only logged:
// the user asked for another mailbox and cannot act on a settings failure.
func (a *App) rememberActiveAccount(id string) {
	if a.saveSettings == nil || id == "" {
		return
	}
	settings, err := prefs.LoadOrZero()
	if err != nil {
		logErr("read settings", err)
		return
	}
	if settings.ActiveAccount == id {
		return
	}
	settings.ActiveAccount = id
	if err := a.saveSettings(settings); err != nil {
		logErr("save settings", err)
	}
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

// setAccountProxy switches one account between the shared proxy and a direct
// connection. The choice is stored first, so a failed write leaves the running
// backends on the setting the user already had and the row simply snaps back.
func (a *App) setAccountProxy(acc mail.Account, on bool) {
	if a.accountStore == nil {
		return
	}
	entry, ok := a.accountStore.Entry(acc.ID)
	if !ok {
		return
	}
	// Upsert replaces the whole entry: it needs to carry the token and the
	// display name of the account it is rewriting.
	if entry.UseProxy == on {
		return
	}
	entry.UseProxy = on
	if err := a.accountStore.Upsert(entry); err != nil {
		logErr("save account proxy", err)
		a.notify(err.Error(), true)
		return
	}
	if acc.ID == a.activeAcc {
		// In-flight callbacks belong to the backend that is about to be closed.
		a.cancelRequests()
	}
	a.swapService(acc)
	if acc.ID == a.activeAcc {
		a.reloadCheckedNow()
	}
}

// swapService replaces the backend of one account with a freshly built one. The
// old backend is closed before the new one is constructed: requests already in
// flight were issued with the previous proxy and must not outlive the change.
func (a *App) swapService(acc mail.Account) {
	if a.newService == nil {
		return
	}
	if old := a.services[acc.ID]; old != nil {
		_ = old.Close()
		delete(a.services, acc.ID)
	}
	// A backend that fails to build leaves the account without one instead of
	// keeping the closed client in the map, where every request would fail.
	if svc := a.newService(acc); svc != nil {
		a.services[acc.ID] = svc
	}
}

// removeSMTPEntry deletes one sending account. Nothing else holds a copy: no
// backend reads through it, and a draft that picked it as sender fails at send
// time like any other missing server.
func (a *App) removeSMTPEntry(entry accounts.Entry) {
	if a.accountStore != nil {
		if err := a.accountStore.Remove(entry.ID); err != nil && !errors.Is(err, accounts.ErrNotExist) {
			logErr("remove sending account", err)
			a.notify(i18n.T("toast.smtpRemoveFailed"), true)
			return
		}
	}
	name := entry.Name
	if name == "" {
		name = entry.Email
	}
	a.notify(i18n.T("toast.smtpRemoved", map[string]string{"Name": name}), false)
	// Reopen the panel on the same page: the deleted row has to disappear
	// without the user leaving and reopening the settings.
	a.showSettingsPage(settingsTabAccounts)
}

// removeAccount deletes an account and its stored token, then drops it from the
// running app. Removing the active account leaves the mailbox empty instead of
// picking another one: the next mailbox is the user's choice in the account
// menu. A failed write leaves both the list and the backends untouched.
func (a *App) removeAccount(acc mail.Account) {
	if a.accountStore != nil {
		// ErrNotExist means the file no longer holds the account, which is the
		// state the caller asked for; the running copy still has to go.
		if err := a.accountStore.Remove(acc.ID); err != nil && !errors.Is(err, accounts.ErrNotExist) {
			logErr("remove account", err)
			a.notify(i18n.T("toast.accountRemoveFailed"), true)
			return
		}
	}
	// In-flight callbacks belong to the backend that is about to be closed.
	a.cancelRequests()
	if svc := a.services[acc.ID]; svc != nil {
		_ = svc.Close()
		delete(a.services, acc.ID)
	}
	kept := make([]mail.Account, 0, len(a.accounts))
	for _, other := range a.accounts {
		if other.ID != acc.ID {
			kept = append(kept, other)
		}
	}
	a.accounts = kept
	if a.activeAcc == acc.ID {
		a.clearAccountView()
	}
	a.notify(i18n.T("toast.accountRemoved", map[string]string{"Name": acc.DisplayName()}), false)
	// Reopen the panel on the same page: the deleted row has to disappear
	// without the user leaving and reopening the settings.
	a.showSettingsPage(settingsTabAccounts)
}

// clearAccountView empties the mailbox after the account showing it was
// removed. The folder goes back to the inbox, so switching to another account
// from the menu lands where it always does: a label of the account that just
// went away must not be carried over.
func (a *App) clearAccountView() {
	a.activeAcc = ""
	a.activeFolder = "inbox"
	a.cancelRequests()
	a.folders = nil
	a.side.reload()
	a.resetInboxState()
	a.query = ""
	a.top.clearSearch()
	a.page, a.tokens = 1, []string{""}
	a.resetSelection()
	a.clearList()
	a.toolbar.setLoading(false)
}

// rebuildProxiedServices swaps the backend of every Gmail account that opts
// into the proxy, so a new address is used without a restart. In-flight
// requests are cancelled first: they belong to the clients being replaced.
func (a *App) rebuildProxiedServices() {
	if a.newService == nil || a.accountStore == nil {
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
		a.swapService(acc)
	}
	a.reloadCheckedNow()
}
