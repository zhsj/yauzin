package ui

import (
	"image"
	"testing"

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

// TestSettingsAccountRowsOfferRelauth adds the refresh button to real Gmail
// accounts only: the demo mailbox has no OAuth grant behind it, and the add row
// is a separate action.
func TestSettingsAccountRowsOfferRelauth(t *testing.T) {
	a := testApp(t)
	a.accounts = append(a.accounts, mail.Account{
		ID:    "gmail:user@example.com",
		Kind:  mail.KindGmail,
		Email: "user@example.com",
	})
	s := newSettingsView(a)

	if len(s.accRows) != 3 {
		t.Fatalf("account rows = %d, want two accounts and the add row", len(s.accRows))
	}
	if s.accRows[0].action != nil {
		t.Fatal("the demo account got a refresh button")
	}
	if s.accRows[1].action == nil {
		t.Fatal("the Gmail account has no refresh button")
	}
	if s.accRows[2].action != nil {
		t.Fatal("the add-account row got a refresh button")
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
// toggle: turning client-side decorations on used to rebuild the whole panel,
// which closed the expanded language list and dropped the proxy draft.
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
	want := !a.csd
	s.decoRow.click.Click()
	s.Layout(gtx, a)

	if a.csd != want {
		t.Fatalf("client-side decorations = %v after the click, want %v", a.csd, want)
	}
	if !s.langOpen {
		t.Fatal("toggling a switch closed the language candidates")
	}
	if a.overlay != s {
		t.Fatalf("overlay = %T after the toggle, want the same settings panel", a.overlay)
	}
	if got := s.decoRow.switchCtl.on; got != a.csd {
		t.Fatalf("switch shows %v, want the state that took effect: %v", got, a.csd)
	}
	if got := s.proxy.Text(); got != "http://127.0.0.1:9" {
		t.Fatalf("proxy draft = %q after the toggle, want it untouched", got)
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
