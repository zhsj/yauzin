package ui

import (
	"log/slog"
	"strings"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"

	"github.com/zhsj/yauzin/internal/i18n"
)

const (
	iconTypeface     = font.Typeface("Material Symbols Rounded")
	iconFillTypeface = font.Typeface("Material Symbols Rounded Fill")

	// latinTypeface draws Latin text. The Noto Sans CJK families cover Latin
	// as well, so this family has to stay in front of them: listing a CJK
	// variant first would render Latin glyphs in its Source Sans shapes.
	latinTypeface = "Noto Sans"

	// The Han glyph forms differ between Chinese, Japanese and Korean, so each
	// language gets its own family. See cjkTypeface.
	cjkSCTypeface = font.Typeface("Noto Sans CJK SC")
	cjkTCTypeface = font.Typeface("Noto Sans CJK TC")
	cjkJPTypeface = font.Typeface("Noto Sans CJK JP")
	cjkKRTypeface = font.Typeface("Noto Sans CJK KR")
)

// textTypeface is the CSS-style family list shared by every text widget. Four
// stages, and the order carries the meaning:
//
//  1. latinTypeface draws Latin text.
//  2. A CJK family draws Han text. Naming it explicitly is required because
//     the shaper cannot pick one by language: fontscan only knows the script,
//     and go-text maps the Han script to no language at all (language.go's
//     ScriptToLang documents that no language represents Chinese, Japanese
//     and Korean Han forms at once). Language-aware substitution is therefore
//     disabled for Han and the script fallback lands on the Japanese family,
//     which draws Japanese glyph forms for Chinese text.
//  3. "sans-serif" is the last resort when neither named family is installed,
//     keeping the fallback to the system default UI font.
//  4. "emoji" catches what the first three cannot draw. It has to stay last:
//     emoji fonts also cover digits, '#' and '*' and would render them at full
//     emoji width, and naming it late keeps emoji colour even though the
//     shaper folds their Common script into the preceding Latin or CJK run
//     (that run would otherwise resolve to a monochrome symbol font).
func textTypeface() font.Typeface {
	return textTypefaceFor(i18n.SystemLocale(), i18n.Language())
}

// textTypefaceFor assembles the family list for the given locales, in order of
// preference: the first one naming a CJK language decides the Han family.
func textTypefaceFor(locales ...string) font.Typeface {
	return font.Typeface(latinTypeface + ", " + string(cjkTypeface(locales...)) + ", sans-serif, emoji")
}

// cjkTypeface returns the Noto Sans CJK family matching the first locale that
// names a CJK language, and Simplified Chinese when none does. The Chinese
// variants share one code point range but draw different glyph forms, which is
// why the variant follows the reader's language rather than the script.
func cjkTypeface(locales ...string) font.Typeface {
	for _, locale := range locales {
		if face := cjkTypefaceOf(locale); face != "" {
			return face
		}
	}
	return cjkSCTypeface
}

// cjkTypefaceOf maps one locale to its CJK family, or "" when the locale is
// not a CJK one. Traditional Chinese is spelled either as a script subtag or
// as a region, and POSIX locales use both forms ("zh_Hant", "zh_TW").
func cjkTypefaceOf(locale string) font.Typeface {
	tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
	lang, rest, _ := strings.Cut(tag, "-")
	switch lang {
	case "ja":
		return cjkJPTypeface
	case "ko":
		return cjkKRTypeface
	case "zh":
		for _, part := range strings.Split(rest, "-") {
			switch part {
			case "hant", "tw", "hk", "mo":
				return cjkTCTypeface
			}
		}
		return cjkSCTypeface
	default:
		return ""
	}
}

// themeFonts is the parsed font collection shared by every view.
type themeFonts struct {
	shaper       *text.Shaper
	face         font.Typeface
	iconFace     font.Typeface
	iconFillFace font.Typeface
}

// loadFonts assembles the shaper's font collection. Since Gio v0.2.0 the
// shaper loads system fonts on its own (fontconfig-aware, including the
// /run/host/fonts mount of a Flatpak sandbox), so every other script the OS
// provides needs no handling here. The collection only adds the bundled icon
// fonts plus the Go faces as a last-resort Latin fallback for systems without
// any installed fonts; textTypeface names the system families to use and falls
// back to a colour emoji font for emoji.
func loadFonts() *themeFonts {
	faces := gofont.Collection()
	faces = append(faces, loadIconFaces()...)
	return &themeFonts{
		shaper:       text.NewShaper(text.WithCollection(faces)),
		face:         textTypeface(),
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
