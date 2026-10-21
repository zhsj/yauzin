package ui

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

func allGlyphs() []iconGlyph {
	return []iconGlyph{
		glyphMenu, glyphSearch, glyphRefresh, glyphCompose, glyphArchive,
		glyphDelete, glyphReply, glyphForward, glyphAttach, glyphSend,
		glyphClose, glyphChevronLeft, glyphChevronRight, glyphBack,
		glyphExpandMore, glyphExpandLess, glyphInbox, glyphMail,
		glyphMarkUnread, glyphMarkRead, glyphPlus, glyphFile, glyphCheck,
		glyphAlert, glyphSettings, glyphOpenInNew, glyphMoreVert,
		glyphMinimize, glyphStar,
	}
}

func TestIconFontContainsEveryGlyph(t *testing.T) {
	f, err := sfnt.Parse(iconFontData)
	if err != nil {
		t.Fatalf("parse icon font: %v", err)
	}
	var buf sfnt.Buffer
	for _, g := range allGlyphs() {
		idx, err := f.GlyphIndex(&buf, rune(g))
		if err != nil {
			t.Errorf("glyph U+%04X: %v", rune(g), err)
			continue
		}
		if idx == 0 {
			t.Errorf("glyph U+%04X missing from regular icon font", rune(g))
		}
	}
}

func TestFillFontContainsStar(t *testing.T) {
	f, err := sfnt.Parse(iconFontFillData)
	if err != nil {
		t.Fatalf("parse fill font: %v", err)
	}
	var buf sfnt.Buffer
	idx, err := f.GlyphIndex(&buf, rune(glyphStar))
	if err != nil {
		t.Fatalf("glyph star: %v", err)
	}
	if idx == 0 {
		t.Fatalf("glyph U+%04X missing from fill icon font", rune(glyphStar))
	}
}
