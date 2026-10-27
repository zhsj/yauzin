//go:build linux

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/godbus/dbus/v5"
)

func noopEmitter(dbus.ObjectPath, string, string, ...any) {}

// iconPNG encodes a two-pixel image whose first pixel has the given colour.
func iconPNG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, c)
	img.SetNRGBA(1, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode icon: %v", err)
	}
	return buf.Bytes()
}

func TestPixmapsARGBOrder(t *testing.T) {
	got := pixmaps(iconPNG(t, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x44}))
	if len(got) != 1 {
		t.Fatalf("pixmaps = %d, want 1", len(got))
	}
	p := got[0]
	if p.Width != 2 || p.Height != 1 {
		t.Fatalf("size = %dx%d, want 2x1", p.Width, p.Height)
	}
	if len(p.Data) != 8 {
		t.Fatalf("data = %d bytes, want 8", len(p.Data))
	}
	want := []byte{0x44, 0x11, 0x22, 0x33}
	if !bytes.Equal(p.Data[:4], want) {
		t.Fatalf("first pixel = % x, want % x", p.Data[:4], want)
	}
}

func TestPixmapsWithoutIcon(t *testing.T) {
	if got := pixmaps(nil); len(got) != 0 {
		t.Fatalf("pixmaps(nil) = %v, want empty", got)
	}
}

func TestPixmapsRejectsGarbage(t *testing.T) {
	if got := pixmaps([]byte("not a png")); len(got) != 0 {
		t.Fatalf("pixmaps(garbage) = %v, want empty", got)
	}
}

func TestItemProperties(t *testing.T) {
	i := newItem(noopEmitter,
		startState{title: "Yauzin", tooltip: "Yauzin - 1 unread"},
		Icons{Idle: iconPNG(t, color.NRGBA{A: 0xff}), Active: iconPNG(t, color.NRGBA{A: 0xff})},
		nil,
	)
	props := i.properties()
	if got := props["Id"].Value(); got != appID {
		t.Fatalf("Id = %v, want %v", got, appID)
	}
	if got := props["ItemIsMenu"].Value(); got != false {
		t.Fatalf("ItemIsMenu = %v, want false", got)
	}
	if got := props["Menu"].Value(); got != menuPath {
		t.Fatalf("Menu = %v, want %v", got, menuPath)
	}
	var tt tooltip
	if err := props["ToolTip"].Store(&tt); err != nil {
		t.Fatalf("store tooltip: %v", err)
	}
	if tt.Title != "Yauzin - 1 unread" {
		t.Fatalf("tooltip title = %q, want %q", tt.Title, "Yauzin - 1 unread")
	}
	if len(tt.IconPixmap) != 0 {
		t.Fatalf("tooltip pixmaps = %d, want none", len(tt.IconPixmap))
	}
}

func TestSetActiveEmitsOnlyOnChange(t *testing.T) {
	var signals []string
	emit := func(_ dbus.ObjectPath, iface, member string, _ ...any) {
		signals = append(signals, iface+"."+member)
	}
	i := newItem(emit, startState{},
		Icons{Idle: iconPNG(t, color.NRGBA{A: 0xff}), Active: iconPNG(t, color.NRGBA{A: 0xff})},
		nil,
	)

	i.setActive(false)
	if len(signals) != 0 {
		t.Fatalf("signals after unchanged state = %v, want none", signals)
	}
	i.setActive(true)
	if len(signals) != 2 {
		t.Fatalf("signals after change = %v, want two", signals)
	}
	if signals[0] != sniIface+".NewIcon" || signals[1] != propertiesIface+".PropertiesChanged" {
		t.Fatalf("signals = %v, want NewIcon then PropertiesChanged", signals)
	}
	i.setActive(true)
	if len(signals) != 2 {
		t.Fatalf("signals after repeated state = %v, want the two from before", signals)
	}
}

func TestSetActiveIgnoredWithoutActiveIcon(t *testing.T) {
	var signals int
	emit := func(dbus.ObjectPath, string, string, ...any) { signals++ }
	i := newItem(emit, startState{}, Icons{Idle: iconPNG(t, color.NRGBA{A: 0xff})}, nil)

	i.setActive(true)
	if signals != 0 {
		t.Fatalf("signals = %d, want none without an unread icon", signals)
	}
}
