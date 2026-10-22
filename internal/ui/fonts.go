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

// textTypeface is the CSS-style family list shared by every text widget. The
// order matters. The shaper resolves each code point against these families
// before any script fallback, so "sans-serif" (the system UI font) must stay
// first: emoji fonts also cover digits, '#' and '*' and would render them at
// full emoji width. The generic "emoji" family second only catches what
// sans-serif cannot draw, which keeps emoji colour even though the shaper
// folds their Common script into the preceding Latin or CJK run; that run
// would otherwise resolve to a monochrome symbol font such as Symbola.
const textTypeface = font.Typeface("sans-serif, emoji")

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
// systems without any installed fonts; textTypeface resolves to the system
// default UI font and falls back to a colour emoji font for emoji.
func loadFonts() *themeFonts {
	faces := gofont.Collection()
	faces = append(faces, loadIconFaces()...)
	return &themeFonts{
		shaper:       text.NewShaper(text.WithCollection(faces)),
		face:         textTypeface,
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
