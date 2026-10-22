package ui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
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
