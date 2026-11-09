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

	// latinMonoTypeface is the monospace counterpart, used by the mail bodies
	// (see monoTypeface). It follows the same ordering rule as latinTypeface:
	// the mono CJK families cover Latin too.
	latinMonoTypeface = "Noto Sans Mono"

	// The Han glyph forms differ between Chinese, Japanese and Korean, so each
	// language gets its own family. See cjkTypeface.
	cjkSCTypeface = font.Typeface("Noto Sans CJK SC")
	cjkTCTypeface = font.Typeface("Noto Sans CJK TC")
	cjkJPTypeface = font.Typeface("Noto Sans CJK JP")
	cjkKRTypeface = font.Typeface("Noto Sans CJK KR")

	// The monospace variants draw Han in the mail bodies, so a Chinese body
	// keeps its glyph forms there as well. See cjkMonoTypeface.
	cjkSCMonoTypeface = font.Typeface("Noto Sans Mono CJK SC")
	cjkTCMonoTypeface = font.Typeface("Noto Sans Mono CJK TC")
	cjkJPMonoTypeface = font.Typeface("Noto Sans Mono CJK JP")
	cjkKRMonoTypeface = font.Typeface("Noto Sans Mono CJK KR")
)

// textTypeface is the CSS-style family list shared by the interface text
// widgets. Four stages, and the order carries the meaning:
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

// monoTypeface is the family list of the mail bodies: the text/plain body of
// the reader and the body editor of the composer. Mail is often laid out in
// columns — quoted replies, tables, ASCII art — and only a monospace family
// keeps them aligned, while the interface around the body keeps textTypeface.
// The stages mirror textTypeface, with "monospace" as the generic fallback:
//
//  1. latinMonoTypeface draws Latin.
//  2. A monospace CJK family draws Han in the reader's variant. Naming it is
//     required for the same reason as in textTypeface: the shaper cannot pick
//     a Han variant by language and would fall back to the Japanese forms.
//  3. "monospace" keeps the body monospaced on systems with neither Noto
//     family installed.
//  4. "emoji" stays last, or it would widen digits and symbols, exactly as in
//     textTypeface.
func monoTypeface() font.Typeface {
	return monoTypefaceFor(i18n.SystemLocale(), i18n.Language())
}

// monoTypefaceFor assembles the monospace family list for the given locales.
func monoTypefaceFor(locales ...string) font.Typeface {
	return font.Typeface(latinMonoTypeface + ", " + string(cjkMonoTypeface(locales...)) + ", monospace, emoji")
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

// cjkMonoTypeface is cjkTypeface for the monospace families.
func cjkMonoTypeface(locales ...string) font.Typeface {
	for _, locale := range locales {
		if face := cjkMonoTypefaceOf(locale); face != "" {
			return face
		}
	}
	return cjkSCMonoTypeface
}

// cjkTypefaceOf maps one locale to its CJK family, or "" when the locale is
// not a CJK one.
func cjkTypefaceOf(locale string) font.Typeface {
	switch cjkVariantOf(locale) {
	case "TC":
		return cjkTCTypeface
	case "JP":
		return cjkJPTypeface
	case "KR":
		return cjkKRTypeface
	case "SC":
		return cjkSCTypeface
	default:
		return ""
	}
}

// cjkMonoTypefaceOf is cjkTypefaceOf for the monospace families.
func cjkMonoTypefaceOf(locale string) font.Typeface {
	switch cjkVariantOf(locale) {
	case "TC":
		return cjkTCMonoTypeface
	case "JP":
		return cjkJPMonoTypeface
	case "KR":
		return cjkKRMonoTypeface
	case "SC":
		return cjkSCMonoTypeface
	default:
		return ""
	}
}

// cjkVariantOf returns the Noto Sans CJK variant one locale asks for ("SC",
// "TC", "JP" or "KR"), or "" when the locale names no CJK language. Both the
// proportional and the monospace families are named after this variant, so the
// language decision lives here once. Traditional Chinese is spelled either as
// a script subtag or as a region, and POSIX locales use both forms ("zh_Hant",
// "zh_TW").
func cjkVariantOf(locale string) string {
	tag := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
	lang, rest, _ := strings.Cut(tag, "-")
	switch lang {
	case "ja":
		return "JP"
	case "ko":
		return "KR"
	case "zh":
		for _, part := range strings.Split(rest, "-") {
			switch part {
			case "hant", "tw", "hk", "mo":
				return "TC"
			}
		}
		return "SC"
	default:
		return ""
	}
}

// themeFonts is the parsed font collection shared by every view.
type themeFonts struct {
	shaper       *text.Shaper
	face         font.Typeface
	monoFace     font.Typeface
	iconFace     font.Typeface
	iconFillFace font.Typeface
}

// loadFonts assembles the shaper's font collection. Since Gio v0.2.0 the
// shaper loads system fonts on its own (fontconfig-aware, including the
// /run/host/fonts mount of a Flatpak sandbox), so every other script the OS
// provides needs no handling here. The collection only adds the bundled icon
// fonts plus the Go faces as a last-resort Latin fallback for systems without
// any installed fonts; textTypeface and monoTypeface name the system families
// to use and fall back to a colour emoji font for emoji.
func loadFonts() *themeFonts {
	faces := gofont.Collection()
	faces = append(faces, loadIconFaces()...)
	return &themeFonts{
		shaper:       text.NewShaper(text.WithCollection(faces)),
		face:         textTypeface(),
		monoFace:     monoTypeface(),
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
