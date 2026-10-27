//go:build linux

package tray

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestMenuLayoutHasBothEntries(t *testing.T) {
	m := newMenu(noopEmitter, startState{open: "Open", quit: "Quit"}, Handlers{})
	revision, root, err := m.GetLayout(menuRoot, -1, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if revision == 0 {
		t.Fatal("revision must not be zero")
	}
	if len(root.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(root.Children))
	}
	for idx, want := range []struct {
		id    int32
		label string
	}{{menuOpen, "Open"}, {menuQuit, "Quit"}} {
		var child layoutItem
		if err := root.Children[idx].Store(&child); err != nil {
			t.Fatalf("store child %d: %v", idx, err)
		}
		if child.ID != want.id {
			t.Fatalf("child %d id = %d, want %d", idx, child.ID, want.id)
		}
		var label string
		if err := child.Props["label"].Store(&label); err != nil {
			t.Fatalf("store label %d: %v", idx, err)
		}
		if label != want.label {
			t.Fatalf("child %d label = %q, want %q", idx, label, want.label)
		}
	}
}

func TestMenuDepthZeroHasNoChildren(t *testing.T) {
	m := newMenu(noopEmitter, startState{open: "Open", quit: "Quit"}, Handlers{})
	_, root, err := m.GetLayout(menuRoot, 0, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if len(root.Children) != 0 {
		t.Fatalf("children at depth 0 = %d, want 0", len(root.Children))
	}
}

func TestMenuLabelsBumpRevision(t *testing.T) {
	m := newMenu(noopEmitter, startState{open: "Open", quit: "Quit"}, Handlers{})
	before, _, err := m.GetLayout(menuRoot, -1, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}

	m.setLabels("Open", "Quit")
	unchanged, _, err := m.GetLayout(menuRoot, -1, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if unchanged != before {
		t.Fatalf("revision after identical labels = %d, want %d", unchanged, before)
	}

	m.setLabels("打开", "退出")
	after, _, err := m.GetLayout(menuRoot, -1, nil)
	if err != nil {
		t.Fatalf("GetLayout: %v", err)
	}
	if after != before+1 {
		t.Fatalf("revision after relabel = %d, want %d", after, before+1)
	}
}

func TestMenuEventDispatchesClicks(t *testing.T) {
	var opened, quit int
	m := newMenu(noopEmitter, startState{}, Handlers{
		OnOpen: func() { opened++ },
		OnQuit: func() { quit++ },
	})
	if err := m.Event(menuOpen, "clicked", dbus.Variant{}, 0); err != nil {
		t.Fatalf("open event: %v", err)
	}
	if err := m.Event(menuQuit, "clicked", dbus.Variant{}, 0); err != nil {
		t.Fatalf("quit event: %v", err)
	}
	if err := m.Event(menuQuit, "hovered", dbus.Variant{}, 0); err != nil {
		t.Fatalf("hover event: %v", err)
	}
	if opened != 1 || quit != 1 {
		t.Fatalf("callbacks = %d open, %d quit; want 1 each", opened, quit)
	}
}

func TestMenuGetProperty(t *testing.T) {
	m := newMenu(noopEmitter, startState{open: "Open"}, Handlers{})
	v, err := m.GetProperty(menuOpen, "label")
	if err != nil {
		t.Fatalf("GetProperty: %v", err)
	}
	var label string
	if err := v.Store(&label); err != nil {
		t.Fatalf("store label: %v", err)
	}
	if label != "Open" {
		t.Fatalf("label = %q, want Open", label)
	}
	if _, err := m.GetProperty(99, "label"); err == nil {
		t.Fatal("unknown id did not fail")
	}
}
