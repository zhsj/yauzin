package ui

import (
	"bytes"
	_ "embed"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"sync"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// The application icon is authored as SVG and shipped as PNG: the masters
// assets/icon-gray.svg (grey wax seal) and assets/icon-red.svg (red wax seal)
// are rasterised by scripts/gen_app_icons.py, because the binary carries no SVG
// renderer. Only the small variants are embedded; assets/icon-gray-512.png
// exists for desktop integration and stays out of the executable.
//
//go:embed assets/icon-gray-44.png
var iconGrayPNG []byte

//go:embed assets/icon-red-44.png
var iconRedPNG []byte

// The title-bar mark is the red-seal master embedded at several sizes: Gio only
// filters linearly, so drawing a single 128px bitmap into a 36dp cell looks
// muddy. appLogo draws the smallest variant that still covers the device
// pixels, and 36dp lands on an exact variant at 1x / 1.25x / 1.5x / 2x / 3x
// (36 / 45 / 54 / 72 / 108px), where the scale factor is 1 and nothing is
// interpolated. The 32 / 40 / 48 / 64 / 96 / 128px variants are the leftovers
// of the earlier 32dp mark; they still win at odd scale factors such as 1.1x
// or 1.75x, where they cover the cell more exactly than the next size up.
//
//go:embed assets/icon-red-32.png
var logo32PNG []byte

//go:embed assets/icon-red-36.png
var logo36PNG []byte

//go:embed assets/icon-red-40.png
var logo40PNG []byte

//go:embed assets/icon-red-45.png
var logo45PNG []byte

//go:embed assets/icon-red-48.png
var logo48PNG []byte

//go:embed assets/icon-red-54.png
var logo54PNG []byte

//go:embed assets/icon-red-64.png
var logo64PNG []byte

//go:embed assets/icon-red-72.png
var logo72PNG []byte

//go:embed assets/icon-red-96.png
var logo96PNG []byte

//go:embed assets/icon-red-108.png
var logo108PNG []byte

//go:embed assets/icon-red-128.png
var logo128PNG []byte

// trayIconSize is the pixmap size handed to the tray host. Notification-area
// hosts downscale it to the panel height.
const trayIconSize = 44

// logoPNGs lists the embedded title-bar mark (red seal), smallest first.
var logoPNGs = []struct {
	size int
	data []byte
}{
	{32, logo32PNG},
	{36, logo36PNG},
	{40, logo40PNG},
	{45, logo45PNG},
	{48, logo48PNG},
	{54, logo54PNG},
	{64, logo64PNG},
	{72, logo72PNG},
	{96, logo96PNG},
	{108, logo108PNG},
	{128, logo128PNG},
}

var (
	logoMu  sync.Mutex
	logoOps = map[int]paint.ImageOp{}
)

// trayIdlePNG returns the notification-area icon for a mailbox without unread
// mail: the envelope with its grey wax seal.
func trayIdlePNG() []byte { return iconGrayPNG }

// trayUnreadPNG returns the notification-area icon for unread mail: the same
// envelope with a red seal of the same size.
func trayUnreadPNG() []byte { return iconRedPNG }

// appLogo draws the title-bar brand mark (the red-seal envelope) inside a
// square cell of the given size.
func (t *theme) appLogo(gtx layout.Context, cell unit.Dp) layout.Dimensions {
	s := gtx.Dp(cell)
	if s <= 0 {
		return layout.Dimensions{}
	}
	img, edge := appLogoOp(s)
	if edge <= 0 {
		return layout.Dimensions{Size: image.Pt(s, s)}
	}
	scale := float32(s) / float32(edge)
	defer clip.Rect{Max: image.Pt(s, s)}.Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(s, s)}
}

// appLogoOp returns the decoded variant for a cell of px pixels: the smallest
// one that still covers it, so the scale factor stays close to one. Decoded
// variants are cached, since the title bar draws every frame.
func appLogoOp(px int) (paint.ImageOp, int) {
	choice := logoPNGs[len(logoPNGs)-1]
	for _, variant := range logoPNGs {
		if variant.size >= px {
			choice = variant
			break
		}
	}

	logoMu.Lock()
	defer logoMu.Unlock()
	if img, ok := logoOps[choice.size]; ok {
		return img, choice.size
	}
	src, err := png.Decode(bytes.NewReader(choice.data))
	if err != nil {
		slog.Error("decode app logo", "size", choice.size, "err", err)
		return paint.ImageOp{}, 0
	}
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	img := paint.NewImageOp(rgba)
	img.Filter = paint.FilterLinear
	logoOps[choice.size] = img
	return img, choice.size
}
