package ui

import (
	"log/slog"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

const (
	iconTypeface     = font.Typeface("Material Symbols Rounded")
	iconFillTypeface = font.Typeface("Material Symbols Rounded Fill")
)

// themeFonts is the parsed font collection shared by every view.
type themeFonts struct {
	shaper       *text.Shaper
	face         font.Typeface
	iconFace     font.Typeface
	iconFillFace font.Typeface
}

// loadFonts assembles the shaper's font collection. Since Gio v0.2.0 the
// shaper loads system fonts on its own (fontconfig-aware, including the
// /run/host/fonts mount of a Flatpak sandbox), so CJK and every other script
// the OS provides needs no handling here. The collection only adds the
// bundled icon fonts plus the Go faces as a last-resort Latin fallback for
// systems without any installed fonts; the generic sans-serif family
// resolves to the system default UI font.
func loadFonts() *themeFonts {
	faces := gofont.Collection()
	faces = append(faces, loadIconFaces()...)
	return &themeFonts{
		shaper:       text.NewShaper(text.WithCollection(faces)),
		face:         font.Typeface("sans-serif"),
		iconFace:     iconTypeface,
		iconFillFace: iconFillTypeface,
	}
}

func loadIconFaces() []text.FontFace {
	var out []text.FontFace
	for _, spec := range []struct {
		data     []byte
		typeface font.Typeface
	}{
		{iconFontData, iconTypeface},
		{iconFontFillData, iconFillTypeface},
	} {
		parsed, err := opentype.ParseCollection(spec.data)
		if err != nil {
			slog.Error("parse icon font", "typeface", spec.typeface, "err", err)
			continue
		}
		for _, f := range parsed {
			f.Font.Typeface = spec.typeface
			f.Font.Style = font.Regular
			f.Font.Weight = font.Normal
			out = append(out, f)
		}
	}
	return out
}
