//go:build linux

package tray

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png" // decodes the icon bytes
	"log/slog"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/zhsj/yauzin/internal/singleinstance"
)

const (
	sniIface        = "org.kde.StatusNotifierItem"
	propertiesIface = "org.freedesktop.DBus.Properties"

	// appID identifies the item to the host. It is the application id rather
	// than the window title, so a language change does not alter it.
	appID = singleinstance.Name
)

// pixmap is one icon variant. The field order is the wire order of (iiay).
type pixmap struct {
	Width  int32
	Height int32
	Data   []byte // ARGB32, network byte order
}

// tooltip is the hover text. The field order is the wire order of (sa(iiay)ss).
type tooltip struct {
	IconName   string
	IconPixmap []pixmap
	Title      string
	SubTitle   string
}

// properties serves org.freedesktop.DBus.Properties on one object. Every value
// comes from the owner's snapshot, so the object and its properties cannot
// disagree.
type properties struct {
	iface    string
	snapshot func() map[string]dbus.Variant
}

func newProperties(iface string, snapshot func() map[string]dbus.Variant) *properties {
	return &properties{iface: iface, snapshot: snapshot}
}

// Get returns one property. An empty interface name is tolerated because hosts
// occasionally ask without one.
func (p *properties) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface != "" && iface != p.iface {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
	}
	v, ok := p.snapshot()[name]
	if !ok {
		return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
	}
	return v, nil
}

// GetAll returns every property of the interface in one snapshot.
func (p *properties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != "" && iface != p.iface {
		return map[string]dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownInterface", []any{iface})
	}
	return p.snapshot(), nil
}

// Set always fails: every property this package exports is read-only, and
// refusing here keeps a wrong type from ever reaching the state.
func (p *properties) Set(iface, name string, value dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name})
}

// item is the StatusNotifierItem object. The state is guarded by mu and is the
// only truth the properties, the signals and the tooltip read from.
type item struct {
	emit emitter

	mu      sync.Mutex
	title   string
	tooltip string
	idle    []pixmap
	unread  []pixmap
	active  bool
	onOpen  func()
}

func newItem(emit emitter, state startState, icons Icons, onOpen func()) *item {
	return &item{
		emit:    emit,
		title:   state.title,
		tooltip: state.tooltip,
		idle:    pixmaps(icons.Idle),
		unread:  pixmaps(icons.Active),
		onOpen:  onOpen,
	}
}

// pixmaps converts one PNG to the ARGB32 pixmap the protocol carries: four
// bytes per pixel in network order, alpha first. The image is not scaled; the
// host picks the size it needs.
func pixmaps(data []byte) []pixmap {
	if len(data) == 0 {
		return []pixmap{}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		slog.Warn("tray: cannot decode the icon", "err", err)
		return []pixmap{}
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	argb := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			at := (y*w + x) * 4
			argb[at+0] = c.A
			argb[at+1] = c.R
			argb[at+2] = c.G
			argb[at+3] = c.B
		}
	}
	return []pixmap{{Width: int32(w), Height: int32(h), Data: argb}}
}

// properties snapshots the item for the Properties interface. The host reads it
// whenever it likes, so the copy is taken under the lock and the returned map
// is never mutated afterwards.
func (i *item) properties() map[string]dbus.Variant {
	i.mu.Lock()
	defer i.mu.Unlock()
	pix := i.idle
	if i.active && len(i.unread) > 0 {
		pix = i.unread
	}
	if pix == nil {
		pix = []pixmap{}
	}
	empty := []pixmap{}
	return map[string]dbus.Variant{
		"Id":                  dbus.MakeVariant(appID),
		"Category":            dbus.MakeVariant("ApplicationStatus"),
		"Status":              dbus.MakeVariant("Active"),
		"WindowId":            dbus.MakeVariant(uint32(0)),
		"IconName":            dbus.MakeVariant(""),
		"IconPixmap":          dbus.MakeVariant(pix),
		"OverlayIconName":     dbus.MakeVariant(""),
		"OverlayIconPixmap":   dbus.MakeVariant(empty),
		"AttentionIconName":   dbus.MakeVariant(""),
		"AttentionIconPixmap": dbus.MakeVariant(empty),
		"AttentionMovieName":  dbus.MakeVariant(""),
		"ToolTip":             dbus.MakeVariant(tooltip{Title: i.tooltip}),
		"ItemIsMenu":          dbus.MakeVariant(false),
		"Menu":                dbus.MakeVariant(menuPath),
	}
}

// Activate is the left click: the host asks for the window. The callback runs on
// the D-Bus goroutine, which is the contract the UI layer already expects.
func (i *item) Activate(x, y int32) *dbus.Error {
	if i.onOpen != nil {
		i.onOpen()
	}
	return nil
}

// ContextMenu is the right click. The menu lives at the path the Menu property
// names, so the host opens it from there and nothing further is needed.
func (i *item) ContextMenu(x, y int32) *dbus.Error { return nil }

// SecondaryActivate is the middle click, which this icon does not use.
func (i *item) SecondaryActivate(x, y int32) *dbus.Error { return nil }

// Scroll is the wheel gesture, which this icon does not use.
func (i *item) Scroll(delta int32, orientation string) *dbus.Error { return nil }

func (i *item) setActive(active bool) {
	if len(i.unread) == 0 {
		active = false
	}
	i.mu.Lock()
	changed := i.active != active
	i.active = active
	i.mu.Unlock()
	if changed {
		i.emitChanged("NewIcon", "IconPixmap")
	}
}

func (i *item) setTitle(title string) {
	i.mu.Lock()
	changed := i.title != title
	i.title = title
	i.mu.Unlock()
	if changed {
		i.emitChanged("NewTitle", "Title")
	}
}

func (i *item) setTooltip(text string) {
	i.mu.Lock()
	changed := i.tooltip != text
	i.tooltip = text
	i.mu.Unlock()
	if changed {
		i.emitChanged("NewToolTip", "ToolTip")
	}
}

// emitChanged sends the item signal hosts of this protocol listen to, plus
// PropertiesChanged for the properties that moved, so hosts following either
// convention stay in sync.
func (i *item) emitChanged(signal string, changed ...string) {
	i.emit(itemPath, sniIface, signal)
	props := i.properties()
	updates := make(map[string]dbus.Variant, len(changed))
	for _, name := range changed {
		if v, ok := props[name]; ok {
			updates[name] = v
		}
	}
	if len(updates) == 0 {
		return
	}
	i.emit(itemPath, propertiesIface, "PropertiesChanged", sniIface, updates, []string{})
}
