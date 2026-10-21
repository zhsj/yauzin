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
		t.Fatalf("page = %d, want 0 after clicking the interface segment", s.page)
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
