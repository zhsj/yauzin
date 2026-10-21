package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// TestTrayIconsFillCanvas checks that both notification-area variants decode
// and that the envelope fills the pixmap instead of sitting in the master's
// margins.
func TestTrayIconsFillCanvas(t *testing.T) {
	for name, data := range map[string][]byte{"idle": trayIdlePNG(), "unread": trayUnreadPNG()} {
		img := decodePNG(t, data)
		if got := img.Bounds().Size(); got != image.Pt(trayIconSize, trayIconSize) {
			t.Fatalf("%s: size = %v, want %v", name, got, image.Pt(trayIconSize, trayIconSize))
		}
		ink, cover := inkBox(img)
		if ink.Dx() < trayIconSize*17/20 || ink.Dy() < trayIconSize/2 {
			t.Fatalf("%s: ink box %v does not fill a %dpx icon", name, ink, trayIconSize)
		}
		want := image.Pt(trayIconSize/2, trayIconSize/2)
		if cx, cy := ink.Min.X+ink.Dx()/2, ink.Min.Y+ink.Dy()/2; absInt(cx-want.X) > 2 || absInt(cy-want.Y) > 2 {
			t.Fatalf("%s: ink centre (%d,%d) is not centred on %v", name, cx, cy, want)
		}
		if cover < 0.2 || cover > 0.8 {
			t.Fatalf("%s: ink covers %.0f%% of the canvas, expected 20-80%%", name, cover*100)
		}
	}
}

// TestTrayIconsDifferOnlyInSeal checks the unread state: the two icons may only
// differ in the wax seal, which keeps its size and position and only switches
// from grey to red.
func TestTrayIconsDifferOnlyInSeal(t *testing.T) {
	idle := decodePNG(t, trayIdlePNG())
	unread := decodePNG(t, trayUnreadPNG())

	if hasRed(idle) {
		t.Fatal("idle icon carries the unread red")
	}
	if !hasRed(unread) {
		t.Fatal("unread icon lost the red seal")
	}

	box, ok := diffBox(idle, unread)
	if !ok {
		t.Fatal("idle and unread icons are identical")
	}
	if absInt(box.Dx()-box.Dy()) > 2 {
		t.Fatalf("seal diff %v is not round", box)
	}
	r := float64(box.Dx()) / 2
	// At 44px the seal is about 6px in radius; much smaller disappears in the
	// panel, much larger swamps the envelope.
	if r < 4.5 || r > 8 {
		t.Fatalf("seal radius %.1fpx is outside the readable range at %dpx", r, trayIconSize)
	}
	cx, cy := box.Min.X+box.Dx()/2, box.Min.Y+box.Dy()/2
	if absInt(cx-trayIconSize/2) > 3 || absInt(cy-trayIconSize/2) > 4 {
		t.Fatalf("seal sits at (%d,%d), too far from the canvas centre", cx, cy)
	}

	idleSeal := idle.At(cx, cy)
	unreadSeal := unread.At(cx, cy)
	if !isGrey(idleSeal) {
		t.Fatalf("idle seal is %v, want grey", idleSeal)
	}
	if !isRed(unreadSeal) {
		t.Fatalf("unread seal is %v, want red", unreadSeal)
	}
	// The grey has to separate from the pale envelope body, or the idle state
	// reads as a blank icon.
	if r, _, _, _ := idleSeal.RGBA(); r>>8 > 190 {
		t.Fatalf("idle seal red channel %d is too close to the body fill", r>>8)
	}
}

// TestAppLogoVariants checks that every embedded title-bar size decodes and
// that a cell picks the smallest variant that still covers its pixels.
func TestAppLogoVariants(t *testing.T) {
	for _, variant := range logoPNGs {
		img := decodePNG(t, variant.data)
		if got := img.Bounds().Dx(); got != variant.size {
			t.Fatalf("logo variant %d decodes at %dpx", variant.size, got)
		}
	}

	for _, tc := range []struct{ px, want int }{
		{1, 32},
		{28, 32},
		{32, 32},
		{33, 36},
		{36, 36},
		{37, 40},
		{41, 45},
		{45, 45},
		{46, 48},
		{50, 54},
		{54, 54},
		{60, 64},
		{70, 72},
		{80, 96},
		{100, 108},
		{110, 128},
		{200, 128},
	} {
		if _, edge := appLogoOp(tc.px); edge != tc.want {
			t.Fatalf("appLogoOp(%d) = %dpx, want %dpx", tc.px, edge, tc.want)
		}
	}

	// 36dp — the title-bar cell — must hit a variant exactly at every common
	// device scale, or the mark gets filtered.
	for _, px := range []int{36, 45, 54, 72, 108} {
		if _, edge := appLogoOp(px); edge != px {
			t.Fatalf("appLogoOp(%d) = %dpx, want the 1:1 variant", px, edge)
		}
	}
}

// TestAppLogoCarriesRedSeal keeps the title-bar mark on the red master: the
// brand reads as a red dot, so a regeneration that silently picks the grey
// master has to fail here. The smallest variant matters most, since that is the
// one a 1x window draws.
func TestAppLogoCarriesRedSeal(t *testing.T) {
	for _, variant := range logoPNGs {
		if !hasRed(decodePNG(t, variant.data)) {
			t.Fatalf("logo variant %d lost the red seal", variant.size)
		}
	}
}

// diffBox returns the bounding box of the pixels two images disagree on.
func diffBox(a, b image.Image) (image.Rectangle, bool) {
	box := image.Rectangle{}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			if samePixel(a.At(x, y), b.At(x, y)) {
				continue
			}
			p := image.Pt(x, y)
			if box.Empty() {
				box = image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}
				continue
			}
			box = box.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
		}
	}
	return box, !box.Empty()
}

// samePixel reports whether two colors are identical in 8-bit channels.
func samePixel(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// hasRed reports whether the image contains the unread seal red.
func hasRed(img image.Image) bool {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if isRed(img.At(x, y)) {
				return true
			}
		}
	}
	return false
}

// isRed matches the wax-seal red and its anti-aliased interior.
func isRed(c color.Color) bool {
	r, g, b, a := c.RGBA()
	return a>>8 > 128 && r>>8 > 150 && g>>8 < 130 && b>>8 < 130 && r>>8-g>>8 > 60
}

// isGrey matches the idle seal, a desaturated mid tone.
func isGrey(c color.Color) bool {
	r, g, b, a := c.RGBA()
	if a>>8 < 128 || r>>8 < 100 || r>>8 > 200 {
		return false
	}
	return absDiff(r>>8, g>>8) < 40 && absDiff(g>>8, b>>8) < 40
}

// decodePNG decodes icon bytes, failing the test on malformed data.
func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("icon PNG is empty")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode icon: %v", err)
	}
	return img
}

// inkBox returns the bounding box of the icon ink and the fraction of the
// canvas it covers.
func inkBox(img image.Image) (image.Rectangle, float64) {
	box := image.Rectangle{}
	hit := 0
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a == 0 {
				continue
			}
			hit++
			p := image.Pt(x, y)
			if box.Empty() {
				box = image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}
				continue
			}
			box = box.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
		}
	}
	total := bounds.Dx() * bounds.Dy()
	return box, float64(hit) / float64(total)
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
