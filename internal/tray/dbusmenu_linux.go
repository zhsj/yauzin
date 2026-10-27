//go:build linux

package tray

import (
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	menuIface = "com.canonical.dbusmenu"

	// The menu is static: a root with the two entries. Fixed ids let Event
	// dispatch without a lookup table.
	menuRoot int32 = 0
	menuOpen int32 = 1
	menuQuit int32 = 2
)

// layoutItem is one node of the menu layout. The field order is the wire order
// of (ia{sv}av).
type layoutItem struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

// propertyGroup is one entry of GetGroupProperties: (ia{sv}).
type propertyGroup struct {
	ID    int32
	Props map[string]dbus.Variant
}

// menu is the com.canonical.dbusmenu object behind the right click. The labels
// follow the interface language; revision grows with every change so the host
// knows to fetch the layout again.
type menu struct {
	emit emitter

	mu       sync.Mutex
	open     string
	quit     string
	revision uint32
	onOpen   func()
	onQuit   func()
}

func newMenu(emit emitter, state startState, h Handlers) *menu {
	return &menu{
		emit:     emit,
		open:     state.open,
		quit:     state.quit,
		revision: 1,
		onOpen:   h.OnOpen,
		onQuit:   h.OnQuit,
	}
}

// properties returns the menu-level properties. They do not change, so no lock
// is needed.
func (m *menu) properties() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"Version":       dbus.MakeVariant(uint32(3)),
		"TextDirection": dbus.MakeVariant("ltr"),
		"Status":        dbus.MakeVariant("normal"),
		"IconThemePath": dbus.MakeVariant(""),
	}
}

func (m *menu) setLabels(open, quit string) {
	m.mu.Lock()
	if m.open == open && m.quit == quit {
		m.mu.Unlock()
		return
	}
	m.open, m.quit = open, quit
	m.revision++
	revision := m.revision
	m.mu.Unlock()

	m.emit(menuPath, menuIface, "LayoutUpdated", revision, menuRoot)
}

// GetLayout returns the node whose id is parentID. The revision is the same
// value for every node, as the protocol asks.
func (m *menu) GetLayout(parentID, depth int32, names []string) (uint32, layoutItem, *dbus.Error) {
	m.mu.Lock()
	open, quit, revision := m.open, m.quit, m.revision
	m.mu.Unlock()

	entry := func(id int32, label string) layoutItem {
		return layoutItem{ID: id, Props: entryLabels(label), Children: []dbus.Variant{}}
	}
	switch parentID {
	case menuOpen:
		return revision, entry(menuOpen, open), nil
	case menuQuit:
		return revision, entry(menuQuit, quit), nil
	case menuRoot:
		root := layoutItem{ID: menuRoot, Props: rootLabels(), Children: []dbus.Variant{}}
		if depth != 0 {
			root.Children = []dbus.Variant{
				dbus.MakeVariant(entry(menuOpen, open)),
				dbus.MakeVariant(entry(menuQuit, quit)),
			}
		}
		return revision, root, nil
	default:
		return revision, layoutItem{ID: parentID, Props: map[string]dbus.Variant{}, Children: []dbus.Variant{}}, nil
	}
}

// GetGroupProperties returns the properties of the requested ids, or of every
// node when ids is empty.
func (m *menu) GetGroupProperties(ids []int32, names []string) ([]propertyGroup, *dbus.Error) {
	m.mu.Lock()
	open, quit := m.open, m.quit
	m.mu.Unlock()

	all := []propertyGroup{
		{ID: menuRoot, Props: rootLabels()},
		{ID: menuOpen, Props: entryLabels(open)},
		{ID: menuQuit, Props: entryLabels(quit)},
	}
	if len(ids) == 0 {
		return all, nil
	}
	want := make(map[int32]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make([]propertyGroup, 0, len(ids))
	for _, group := range all {
		if want[group.ID] {
			out = append(out, group)
		}
	}
	return out, nil
}

// GetProperty returns one property of one node.
func (m *menu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	m.mu.Lock()
	open, quit := m.open, m.quit
	m.mu.Unlock()

	var props map[string]dbus.Variant
	switch id {
	case menuRoot:
		props = rootLabels()
	case menuOpen:
		props = entryLabels(open)
	case menuQuit:
		props = entryLabels(quit)
	default:
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", []any{id})
	}
	v, ok := props[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
	}
	return v, nil
}

// Event runs one menu event. Only a click on an entry matters here; the
// callbacks run on the D-Bus goroutine, as the UI layer expects.
func (m *menu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	switch id {
	case menuOpen:
		if m.onOpen != nil {
			m.onOpen()
		}
	case menuQuit:
		if m.onQuit != nil {
			m.onQuit()
		}
	}
	return nil
}

// AboutToShow is the chance to refresh dynamic entries before the host asks for
// the layout. Nothing in this menu is dynamic.
func (m *menu) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

// AboutToShowGroup has no updates to report and no ids it failed on.
func (m *menu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}

// rootLabels marks the root as a submenu parent.
func rootLabels() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"children-display": dbus.MakeVariant("submenu"),
	}
}

// entryLabels builds the properties of one clickable entry.
func entryLabels(label string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(label),
		"enabled": dbus.MakeVariant(true),
		"visible": dbus.MakeVariant(true),
	}
}
