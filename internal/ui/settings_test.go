package ui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/logging"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// TestSettingsPagesFrame lays out every page of the settings panel: none may
// panic, and clicking a segment must switch pages.
func TestSettingsPagesFrame(t *testing.T) {
	a := testApp(t)
	s := newSettingsView(a)

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.tabs[1].click.Click()
	s.Layout(gtx, a)
	if s.page != 1 {
		t.Fatalf("page = %d, want 1 after clicking the accounts segment", s.page)
	}
	ops.Reset()

	s.tabs[2].click.Click()
	s.Layout(gtx, a)
	if s.page != 2 {
		t.Fatalf("page = %d, want 2 after clicking the shortcuts segment", s.page)
	}
	ops.Reset()

	s.tabs[0].click.Click()
	s.Layout(gtx, a)
	if s.page != 0 {
		t.Fatalf("page = %d, want 0 after clicking the general segment", s.page)
	}
}

// TestSettingsAccountRowDoesNotSwitch keeps the account list informational:
// clicking a row must neither switch the mailbox nor close the panel.
func TestSettingsAccountRowDoesNotSwitch(t *testing.T) {
	a := testApp(t)
	s := newSettingsView(a)
	a.showOverlay(s)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	before := a.activeAcc
	s.accRows[0].click.Click()
	s.Layout(gtx, a)

	if a.activeAcc != before {
		t.Fatalf("active account = %q after clicking its row, want %q", a.activeAcc, before)
	}
	if _, ok := a.overlay.(*settingsView); !ok {
		t.Fatalf("overlay = %T after clicking an account row, want the settings panel", a.overlay)
	}
}

// probeAccRow returns the accounts-page row with the given title, or nil.
func probeAccRow(s *settingsView, title string) *settingsRow {
	for _, row := range s.accRows {
		if row.title == title {
			return row
		}
	}
	return nil
}

// findAccRow is probeAccRow for tests that require the row to exist.
func findAccRow(t *testing.T, s *settingsView, title string) *settingsRow {
	t.Helper()
	row := probeAccRow(s, title)
	if row == nil {
		t.Fatalf("no row titled %q among %d rows", title, len(s.accRows))
	}
	return row
}

// accountDetailsOf returns the details block of an open account row.
func accountDetailsOf(t *testing.T, s *settingsView, title string) *accountDetails {
	t.Helper()
	row := findAccRow(t, s, title)
	if row.details == nil {
		t.Fatalf("the account %q is collapsed", title)
	}
	details, ok := row.details.(*accountDetails)
	if !ok {
		t.Fatalf("the account %q expanded into %T, want *accountDetails", title, row.details)
	}
	return details
}

// TestSettingsAccountRowExpands covers the accordion: a stored Gmail account
// only shows its details while it is open, and those details live inside the
// row's own card item instead of becoming sibling rows.
func TestSettingsAccountRowExpands(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, _ := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	s := newSettingsView(a)
	s.page = settingsTabAccounts

	if got := len(s.accRows); got != 3 {
		t.Fatalf("rows = %d, want the two accounts and the add row", got)
	}
	if s.accRows[0].onTap != nil || s.accRows[0].valueIcon != 0 {
		t.Fatal("the demo account is expandable")
	}
	if s.accRows[1].valueIcon != glyphExpandMore {
		t.Fatalf("collapsed account arrow = %v, want the expand-more glyph", s.accRows[1].valueIcon)
	}
	if s.accRows[1].details != nil {
		t.Fatal("the collapsed account already has a details block")
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)

	if s.expanded != target.ID {
		t.Fatalf("expanded = %q, want %q", s.expanded, target.ID)
	}
	if got := len(s.accRows); got != 3 {
		t.Fatalf("rows = %d, want the details to stay inside the account row", got)
	}
	if s.accRows[1].valueIcon != glyphExpandLess {
		t.Fatalf("open account arrow = %v, want the expand-less glyph", s.accRows[1].valueIcon)
	}
	d := accountDetailsOf(t, s, target.DisplayName())
	if d.proxy == nil || d.relogin == nil || d.remove == nil || d.cancel == nil || d.confirm == nil {
		t.Fatalf("details block = %+v, want every control", d)
	}
	if d.armed {
		t.Fatal("a freshly opened account is already confirming a delete")
	}

	// A second click collapses the account again.
	ops.Reset()
	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	if s.expanded != "" {
		t.Fatalf("expanded = %q after the second click, want it closed", s.expanded)
	}
	if s.accRows[1].details != nil {
		t.Fatal("the collapsed account kept its details block")
	}
}

// TestSettingsAccountsAccordion keeps one account open at a time: expanding
// another one collapses the previous.
func TestSettingsAccountsAccordion(t *testing.T) {
	first := mail.Account{ID: "gmail:first@example.com", Kind: mail.KindGmail, Email: "first@example.com"}
	second := mail.Account{ID: "gmail:second@example.com", Kind: mail.KindGmail, Email: "second@example.com"}
	a, _ := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], first, second})
	s := newSettingsView(a)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	ops.Reset()

	findAccRow(t, s, second.DisplayName()).click.Click()
	s.Layout(gtx, a)

	if s.expanded != second.ID {
		t.Fatalf("expanded = %q, want %q", s.expanded, second.ID)
	}
	closed := findAccRow(t, s, first.DisplayName())
	if closed.valueIcon != glyphExpandMore || closed.details != nil {
		t.Fatalf("first account = arrow %v, details %v; want it collapsed", closed.valueIcon, closed.details)
	}
	if findAccRow(t, s, second.DisplayName()).details == nil {
		t.Fatal("the second account has no details block")
	}
	if got := len(s.accRows); got != 4 {
		t.Fatalf("rows = %d, want the two accounts, the demo row and the add row", got)
	}
}

// TestManageAccountsOpensAccountsPage pins the account menu entry: managing
// accounts lands on the accounts tab, not on the general page.
func TestManageAccountsOpensAccountsPage(t *testing.T) {
	a := testApp(t)
	a.showAccountMenu()
	m, ok := a.overlay.(*menuOverlay)
	if !ok {
		t.Fatalf("overlay = %T after the account menu, want *menuOverlay", a.overlay)
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	m.Layout(gtx, a)
	ops.Reset()

	manage := m.rows[len(m.rows)-1]
	manage.click.Click()
	m.Layout(gtx, a)

	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T after manage accounts, want *settingsView", a.overlay)
	}
	if panel.page != settingsTabAccounts {
		t.Fatalf("page = %d after manage accounts, want the accounts tab", panel.page)
	}
}

// TestSettingsRowDisabledIgnoresClicks covers rows that are listed but have no
// implementation behind them: they must not react to a click.
func TestSettingsRowDisabledIgnoresClicks(t *testing.T) {
	a := testApp(t)
	taps := 0
	row := &settingsRow{
		title:    "Disabled row",
		disabled: true,
		onTap:    func() { taps++ },
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	gtx.Constraints = layout.Constraints{Min: image.Pt(400, 46), Max: image.Pt(400, 46)}
	row.click.Click()
	row.Layout(gtx, a.th)
	if taps != 0 {
		t.Fatalf("taps = %d on a disabled row, want 0", taps)
	}
}

// TestShortcutSections checks that the page groups every table row exactly
// once, in one section per group.
func TestShortcutSections(t *testing.T) {
	sections := shortcutSections()
	if len(sections) != 3 {
		t.Fatalf("sections = %d, want 3", len(sections))
	}
	seen := map[shortcutGroup]bool{}
	total := 0
	for i, sec := range sections {
		if i > 0 && sec.group <= sections[i-1].group {
			t.Errorf("section %d repeats or reorders group %d", i, sec.group)
		}
		if seen[sec.group] {
			t.Errorf("group %d appears twice", sec.group)
		}
		seen[sec.group] = true
		if len(sec.rows) == 0 {
			t.Errorf("group %d has no rows", sec.group)
		}
		total += len(sec.rows)
	}
	if total != len(shortcutTable) {
		t.Fatalf("sections cover %d rows, table has %d", total, len(shortcutTable))
	}
}

// TestSettingsLanguageDropdown pins the in-panel language choice: the
// candidates stay hidden until the selector row is clicked, and picking one
// switches the language and rebuilds the panel with the list collapsed again.
// It must not go through App.showMenu: that would close the settings panel.
func TestSettingsLanguageDropdown(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.DefaultLanguage) })
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())

	a := testApp(t)
	a.settings = newSettingsView(a)
	a.overlay = a.settings
	s := a.settings

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	if s.langOpen {
		t.Fatal("language candidates are open before the selector row is clicked")
	}
	ops.Reset()

	s.langRow.click.Click()
	s.Layout(gtx, a)
	if !s.langOpen {
		t.Fatal("language candidates did not expand on the selector row click")
	}
	ops.Reset()

	// Pick the language that is not active right now.
	var pick *settingsRow
	wantTag := ""
	for _, loc := range i18n.Languages() {
		if loc.Tag == i18n.Language() {
			continue
		}
		wantTag = loc.Tag
		for _, row := range s.langRows {
			if row.title == loc.Name {
				pick = row
			}
		}
	}
	if pick == nil || wantTag == "" {
		t.Fatal("no inactive language row to pick")
	}
	pick.click.Click()
	s.Layout(gtx, a)

	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T after picking a language, want the settings panel", a.overlay)
	}
	if panel == s {
		t.Fatal("the settings panel was not rebuilt after the language switch")
	}
	if panel.langOpen {
		t.Fatal("language candidates stayed open after the rebuild")
	}
	if got := i18n.Language(); got != wantTag {
		t.Fatalf("language after picking %q = %q", wantTag, got)
	}
}

// TestSettingsLogLevelDropdown pins the in-panel log level choice: the
// candidates stay hidden until the selector row is clicked, and a pick applies
// the level in place, collapses the list and stores it. The panel is not
// rebuilt, so its other state survives the choice.
func TestSettingsLogLevelDropdown(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	t.Cleanup(func() { logging.SetLevel(logging.LevelInfo) })

	a := testApp(t)
	a.settings = newSettingsView(a)
	a.overlay = a.settings
	s := a.settings

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	if s.logOpen {
		t.Fatal("log level candidates are open before the selector row is clicked")
	}
	ops.Reset()

	s.logRow.click.Click()
	s.Layout(gtx, a)
	if !s.logOpen {
		t.Fatal("log level candidates did not expand on the selector row click")
	}
	ops.Reset()

	debug := s.logRows[len(s.logRows)-1]
	debug.click.Click()
	s.Layout(gtx, a)

	if s.logOpen {
		t.Fatal("log level candidates stayed open after a pick")
	}
	if a.logLevel != logging.LevelDebug {
		t.Fatalf("log level = %q, want %q", a.logLevel, logging.LevelDebug)
	}
	if got, want := s.logRow.value, i18n.T("settings.logLevelDebug"); got != want {
		t.Fatalf("selector shows %q, want %q", got, want)
	}
	if !s.logRows[len(s.logRows)-1].active {
		t.Fatal("the picked level is not checked")
	}
	if s.logRows[0].active {
		t.Fatal("the old level is still checked")
	}
	if a.overlay != s {
		t.Fatalf("overlay = %T after a pick, want the same settings panel", a.overlay)
	}

	stored, err := prefs.LoadOrZero()
	if err != nil {
		t.Fatalf("LoadOrZero: %v", err)
	}
	if stored.LogLevel != logging.LevelDebug {
		t.Fatalf("stored log level = %q, want %q", stored.LogLevel, logging.LevelDebug)
	}
}

// TestSettingsSwitchKeepsLanguageListOpen pins the panel state across a switch
// toggle: flipping a boolean setting used to rebuild the whole panel, which
// closed the expanded language list and dropped the proxy draft.
func TestSettingsSwitchKeepsLanguageListOpen(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())

	a := testApp(t)
	a.settings = newSettingsView(a)
	a.overlay = a.settings
	s := a.settings

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.langRow.click.Click()
	s.Layout(gtx, a)
	if !s.langOpen {
		t.Fatal("language candidates did not expand")
	}
	ops.Reset()

	s.proxy.SetText("http://127.0.0.1:9")
	want := !a.perf.isVisible()
	s.perfRows[0].click.Click()
	s.Layout(gtx, a)

	if got := a.perf.isVisible(); got != want {
		t.Fatalf("performance monitor = %v after the click, want %v", got, want)
	}
	if !s.langOpen {
		t.Fatal("toggling a switch closed the language candidates")
	}
	if a.overlay != s {
		t.Fatalf("overlay = %T after the toggle, want the same settings panel", a.overlay)
	}
	if got := s.perfRows[0].switchCtl.on; got != a.perf.isVisible() {
		t.Fatalf("switch shows %v, want the state that took effect: %v", got, a.perf.isVisible())
	}
	if got := s.proxy.Text(); got != "http://127.0.0.1:9" {
		t.Fatalf("proxy draft = %q after the toggle, want it untouched", got)
	}
}

// TestSettingsAccountRowsDoNotMarkTheActiveOne keeps the panel neutral: which
// mailbox is in use is answered by the check mark of the top bar's account
// menu, not by a bold title or a check mark in this list.
func TestSettingsAccountRowsDoNotMarkTheActiveOne(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, _ := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	s := newSettingsView(a)

	for i, row := range s.accRows {
		if row.active {
			t.Fatalf("row %d is marked as the current account", i)
		}
	}
}

// TestSettingsAccountDeleteNeedsConfirmation pins the two-step delete behind
// the expander: the first click only arms the block, the cancel link backs out,
// and the confirmation removes the account. Neither step adds a row.
func TestSettingsAccountDeleteNeedsConfirmation(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, store := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	s := newSettingsView(a)
	a.showOverlay(s)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	ops.Reset()

	accountDetailsOf(t, s, target.DisplayName()).remove.click.Click()
	s.Layout(gtx, a)
	// Lay the confirmation out once at the real width: its bordered buttons are
	// wider than the links they replaced, so this is where an overflow would
	// show up.
	ops.Reset()
	s.Layout(gtx, a)

	if _, ok := store.Entry(target.ID); !ok {
		t.Fatal("the first click deleted the account")
	}
	if s.confirmDelete != target.ID {
		t.Fatalf("confirmDelete = %q, want %q", s.confirmDelete, target.ID)
	}
	if got := len(s.accRows); got != 3 {
		t.Fatalf("rows = %d, want the confirmation to replace the action line", got)
	}
	if d := accountDetailsOf(t, s, target.DisplayName()); !d.armed {
		t.Fatal("the block does not show the delete confirmation")
	}

	// Cancel backs out and leaves the account open.
	ops.Reset()
	accountDetailsOf(t, s, target.DisplayName()).cancel.click.Click()
	s.Layout(gtx, a)

	if s.confirmDelete != "" {
		t.Fatalf("confirmDelete = %q after cancelling", s.confirmDelete)
	}
	if s.expanded != target.ID {
		t.Fatalf("expanded = %q after cancelling, want the account still open", s.expanded)
	}
	if _, ok := store.Entry(target.ID); !ok {
		t.Fatal("cancelling deleted the account")
	}
	if d := accountDetailsOf(t, s, target.DisplayName()); d.armed {
		t.Fatal("the block stayed armed after cancelling")
	}

	ops.Reset()
	accountDetailsOf(t, s, target.DisplayName()).remove.click.Click()
	s.Layout(gtx, a)
	ops.Reset()
	accountDetailsOf(t, s, target.DisplayName()).confirm.click.Click()
	s.Layout(gtx, a)

	if _, ok := store.Entry(target.ID); ok {
		t.Fatal("the account survived the confirmation")
	}
	panel, ok := a.overlay.(*settingsView)
	if !ok {
		t.Fatalf("overlay = %T after the delete, want the settings panel", a.overlay)
	}
	if panel.page != settingsTabAccounts {
		t.Fatalf("page = %d after the delete, want the accounts tab", panel.page)
	}
	if got := len(panel.accRows); got != 2 {
		t.Fatalf("account rows = %d after the delete, want the demo row and the add row", got)
	}
}

// TestSettingsDisarmsDeleteOnTabSwitch makes sure a pending confirmation does
// not survive a page change: a row must never stay one click away from being
// deleted while the user is looking at a different page.
func TestSettingsDisarmsDeleteOnTabSwitch(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, store := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	s := newSettingsView(a)
	a.showOverlay(s)
	s.page = settingsTabAccounts

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	ops.Reset()

	accountDetailsOf(t, s, target.DisplayName()).remove.click.Click()
	s.Layout(gtx, a)
	if s.confirmDelete != target.ID {
		t.Fatalf("confirmDelete = %q, want the armed account", s.confirmDelete)
	}
	ops.Reset()

	s.tabs[settingsTabGeneral].click.Click()
	s.Layout(gtx, a)

	if s.confirmDelete != "" {
		t.Fatalf("confirmDelete = %q after leaving the accounts page", s.confirmDelete)
	}
	if d := accountDetailsOf(t, s, target.DisplayName()); d.armed {
		t.Fatal("the block stayed armed after leaving the accounts page")
	}
	if _, ok := store.Entry(target.ID); !ok {
		t.Fatal("switching pages deleted the account")
	}
}

// TestSettingsRowSubtitleFitsCell keeps a wrapped explanation inside an exact
// row cell: a caller that hands the row a fixed height must get that height
// back, never more.
func TestSettingsRowSubtitleFitsCell(t *testing.T) {
	a := testApp(t)
	row := &settingsRow{
		title:    "Row with explanation",
		subtitle: "An explanation long enough to wrap onto a second line inside the cell it was given.",
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	gtx.Constraints = layout.Constraints{Min: image.Pt(400, 46), Max: image.Pt(400, 46)}
	dims := row.Layout(gtx, a.th)
	if dims.Size.Y > 46 {
		t.Fatalf("row height = %d, want at most the 46dp cell", dims.Size.Y)
	}
}

// TestSettingsSwitchRowToggles checks that a switch row flips through the row
// click: the row is the hit target, so the switch must not consume a second
// toggle for the same tap.
func TestSettingsSwitchRowToggles(t *testing.T) {
	a := testApp(t)
	flips := 0
	row := &settingsRow{
		title:     "Switch row",
		switchCtl: newToggleSwitch(false, nil),
		onTap:     func() { flips++ },
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	gtx.Constraints = layout.Constraints{Min: image.Pt(400, 46), Max: image.Pt(400, 46)}
	row.click.Click()
	row.Layout(gtx, a.th)
	if flips != 1 {
		t.Fatalf("flips = %d, want 1 after one row click", flips)
	}
	if row.switchCtl.on {
		t.Fatal("display-only switch must not flip on its own clicks")
	}
}

// TestToggleSwitchOnChange covers the standalone mode: with onChange set the
// switch handles its own clicks.
func TestToggleSwitchOnChange(t *testing.T) {
	a := testApp(t)
	var got bool
	sw := newToggleSwitch(false, func(on bool) { got = on })

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	sw.click.Click()
	sw.Layout(gtx, a.th)
	if !sw.on || !got {
		t.Fatalf("on = %v, onChange saw %v; want both true", sw.on, got)
	}

	ops.Reset()
	sw.click.Click()
	sw.Layout(gtx, a.th)
	if sw.on || got {
		t.Fatalf("on = %v, onChange saw %v; want both false after a second click", sw.on, got)
	}
}

// TestToggleSwitchHitArea feeds a real pointer press and release to the
// standalone switch. A programmatic Click cannot catch a switch that never
// registers a hit area, which is how the proxy switch of the add-account
// dialog ignored the mouse.
func TestToggleSwitchHitArea(t *testing.T) {
	a := testApp(t)
	var got bool
	sw := newToggleSwitch(false, func(on bool) { got = on })

	ops := new(op.Ops)
	router := new(input.Router)
	gtx := testGtx(ops, image.Pt(200, 100))
	gtx.Source = router.Source()
	frame := func() {
		ops.Reset()
		sw.Layout(gtx, a.th)
		router.Frame(ops)
	}

	frame() // register the hit area
	at := f32.Pt(18, 10)
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: at, Buttons: pointer.ButtonPrimary})
	router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	frame() // route the events to the switch
	frame() // the switch drains them

	if !sw.on || !got {
		t.Fatalf("on = %v, onChange saw %v after a pointer click; want both true", sw.on, got)
	}
}

// TestSetAccountProxyStoresTheChoice pins the per-account switch: only that
// account is written to and rebuilt, its token survives the rewrite, and the
// other backends keep running.
func TestSetAccountProxyStoresTheChoice(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, store := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	a.proxy = "127.0.0.1:1080"
	demo := a.services[demoAccountID]
	old, ok := a.services[target.ID].(*closeSpyService)
	if !ok {
		t.Fatalf("backend = %T, want the spy", a.services[target.ID])
	}

	a.setAccountProxy(target, true)

	entry, ok := store.Entry(target.ID)
	if !ok {
		t.Fatal("the account disappeared from accounts.json")
	}
	if !entry.UseProxy {
		t.Fatal("the choice was not stored")
	}
	if entry.Token.RefreshToken != "refresh" || entry.Kind != mail.KindGmail || entry.Email != target.Email {
		t.Fatalf("stored entry = %+v, want the account fields kept", entry)
	}
	if !old.closed {
		t.Fatal("the proxied account kept its old backend")
	}
	next, ok := a.services[target.ID].(*closeSpyService)
	if !ok || next == old {
		t.Fatalf("backend = %v, want it replaced", a.services[target.ID])
	}
	if a.services[demoAccountID] != demo {
		t.Fatal("switching the proxy replaced the backend of another account")
	}

	a.setAccountProxy(target, false)

	entry, ok = store.Entry(target.ID)
	if !ok || entry.UseProxy {
		t.Fatalf("stored entry = %+v after switching the proxy off", entry)
	}
	if !next.closed {
		t.Fatal("the proxied backend was not closed when the proxy was switched off")
	}
	if got := a.services[target.ID]; got == next || got == old {
		t.Fatal("switching the proxy off did not rebuild the backend")
	}
}

// TestSettingsAccountProxyRow covers the switch inside the details block of an
// expanded account: it is hidden while collapsed, mirrors the store, stays
// inert without an address and flips the account when clicked.
func TestSettingsAccountProxyRow(t *testing.T) {
	target := mail.Account{ID: "gmail:user@example.com", Kind: mail.KindGmail, Email: "user@example.com"}
	a, store := newDeleteTestApp(t, []mail.Account{demoAccounts()[0], target})
	s := newSettingsView(a)
	s.page = settingsTabAccounts

	if s.accRows[1].details != nil {
		t.Fatal("the proxy switch is visible while the account is collapsed")
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	s.Layout(gtx, a)
	ops.Reset()

	s.accRows[1].click.Click()
	s.Layout(gtx, a)
	// The details block is built during that frame, so its switch mirrors the
	// app on the following one.
	ops.Reset()
	s.Layout(gtx, a)

	row := accountDetailsOf(t, s, target.DisplayName()).proxy
	if row.switchCtl == nil {
		t.Fatal("the proxy row has no switch")
	}
	if s.accRows[0].details != nil {
		t.Fatal("the demo account got a details block")
	}
	if !row.disabled {
		t.Fatal("the proxy row is switchable without a configured address")
	}
	ops.Reset()
	row.click.Click()
	s.Layout(gtx, a)
	if entry, _ := store.Entry(target.ID); entry.UseProxy {
		t.Fatal("a disabled proxy row wrote the choice")
	}

	a.proxy = "127.0.0.1:1080"
	ops.Reset()
	s.Layout(gtx, a)
	if row.disabled {
		t.Fatal("the proxy row stayed disabled after the address was configured")
	}
	if row.switchCtl.on {
		t.Fatal("the proxy switch is on although the account is direct")
	}

	row.click.Click()
	s.Layout(gtx, a)
	entry, ok := store.Entry(target.ID)
	if !ok || !entry.UseProxy {
		t.Fatalf("stored entry = %+v after clicking the row", entry)
	}

	// The row mirrors the store on the next frame, which is also what makes a
	// failed write fall back to the choice that actually took effect.
	ops.Reset()
	s.Layout(gtx, a)
	if !row.switchCtl.on {
		t.Fatal("the switch does not mirror the stored choice")
	}
	if row.subtitle != a.proxy {
		t.Fatalf("subtitle = %q, want the proxy address", row.subtitle)
	}
}
