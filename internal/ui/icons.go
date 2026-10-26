package ui

import (
	_ "embed"
	"image/color"
)

// The icon set is a subset of Material Symbols Rounded (Apache-2.0, see
// assets/LICENSE-Apache-2.0.txt). Only the glyphs listed in this file are
// kept, which brings the shipped font down to a few kilobytes. Regenerate the
// bundled fonts after adding a glyph with:
//
//	python3 scripts/gen_icon_fonts.py
//
// The script parses the codepoints below, instantiates the upstream variable
// font at FILL=0, GRAD=0, opsz=24, wght=400 and subsets it; the FILL=1
// instance only needs U+F09A. icons_test.go fails when a glyph is missing.
// The application icon is separate artwork: assets/icon*.png are rendered from
// the SVG masters by scripts/gen_app_icons.py, see appicon.go.
//
//go:embed assets/material-symbols-rounded.ttf
var iconFontData []byte

// The filled instance is only needed for the solid star, which shares its
// codepoint with the outlined glyph and differs solely in the FILL axis.
//
//go:embed assets/material-symbols-rounded-fill.ttf
var iconFontFillData []byte

// iconGlyph is a single codepoint of the icon font.
type iconGlyph rune

// icon is a glyph together with the font instance and palette color it is
// drawn with.
type icon struct {
	glyph iconGlyph
	fill  bool
	col   color.NRGBA
}

// Codepoints as published in the Material Symbols codepoint table.
const (
	glyphMenu         iconGlyph = 0xe5d2
	glyphSearch       iconGlyph = 0xef7a
	glyphRefresh      iconGlyph = 0xe5d5
	glyphCompose      iconGlyph = 0xf88d // edit_square
	glyphArchive      iconGlyph = 0xe149
	glyphDelete       iconGlyph = 0xe92e
	glyphReply        iconGlyph = 0xe15e
	glyphForward      iconGlyph = 0xf57a
	glyphAttach       iconGlyph = 0xe226 // attach_file
	glyphSend         iconGlyph = 0xe163
	glyphClose        iconGlyph = 0xe5cd
	glyphChevronLeft  iconGlyph = 0xe5cb
	glyphChevronRight iconGlyph = 0xe5cc
	glyphBack         iconGlyph = 0xe5c4 // arrow_back
	glyphExpandMore   iconGlyph = 0xe5cf
	glyphExpandLess   iconGlyph = 0xe5ce
	glyphInbox        iconGlyph = 0xe156
	glyphMail         iconGlyph = 0xe159
	glyphMarkUnread   iconGlyph = 0xf18a
	glyphMarkRead     iconGlyph = 0xf18c
	glyphPlus         iconGlyph = 0xe145
	glyphFile         iconGlyph = 0xe873 // description
	glyphCheck        iconGlyph = 0xe668
	glyphAlert        iconGlyph = 0xf083 // warning
	glyphSettings     iconGlyph = 0xe8b8
	glyphOpenInNew    iconGlyph = 0xe89e
	glyphOpenInFull   iconGlyph = 0xf1ce
	glyphCloseFull    iconGlyph = 0xf1cf // close_fullscreen
	glyphMoreVert     iconGlyph = 0xe5d4
	glyphMinimize     iconGlyph = 0xe15b // remove
	glyphStar         iconGlyph = 0xf09a
)

func mutedIcon(g iconGlyph) icon {
	return icon{glyph: g, col: colMuted}
}

var (
	iconMenu   = mutedIcon(glyphMenu)
	iconSearch = mutedIcon(glyphSearch)

	iconRefresh     = mutedIcon(glyphRefresh)
	iconComposeBlue = icon{glyph: glyphCompose, col: colPrimary}

	iconArchive = mutedIcon(glyphArchive)
	iconDelete  = mutedIcon(glyphDelete)
	iconTrash   = mutedIcon(glyphDelete)

	iconReply   = mutedIcon(glyphReply)
	iconForward = mutedIcon(glyphForward)
	iconAttach  = mutedIcon(glyphAttach)

	iconSendWhite = icon{glyph: glyphSend, col: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}}

	iconClose        = mutedIcon(glyphClose)
	iconChevronLeft  = mutedIcon(glyphChevronLeft)
	iconChevronRight = mutedIcon(glyphChevronRight)
	iconBack         = mutedIcon(glyphBack)

	iconExpandMore = mutedIcon(glyphExpandMore)
	iconExpandLess = mutedIcon(glyphExpandLess)

	iconInbox      = mutedIcon(glyphInbox)
	iconMail       = mutedIcon(glyphMail)
	iconSent       = mutedIcon(glyphSend)
	iconMarkRead   = mutedIcon(glyphMarkRead)
	iconMarkUnread = mutedIcon(glyphMarkUnread)

	// The spam folder reuses the warning triangle: Gmail's octagon glyph is not
	// in the bundled subset, and regenerating the font costs more than it adds.
	iconSpam = mutedIcon(glyphAlert)

	iconPlus = mutedIcon(glyphPlus)
	iconFile = mutedIcon(glyphFile)

	iconCheck        = icon{glyph: glyphCheck, col: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}}
	iconCheckPrimary = icon{glyph: glyphCheck, col: colPrimary}
	iconAlert        = icon{glyph: glyphAlert, col: color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}}

	iconSettings  = mutedIcon(glyphSettings)
	iconOpenInNew = mutedIcon(glyphOpenInNew)
	// The composer's expand control and its way back: the arrows point out of
	// the corner and back into it.
	iconOpenInFull = mutedIcon(glyphOpenInFull)
	iconCloseFull  = mutedIcon(glyphCloseFull)
	iconMoreVert   = mutedIcon(glyphMoreVert)
	iconMinimize   = mutedIcon(glyphMinimize)

	// iconWarning marks an action that gives up a protection, and iconSource
	// the view of the unstyled message source.
	iconWarning = mutedIcon(glyphAlert)
	iconSource  = mutedIcon(glyphFile)

	iconStarOutline = icon{glyph: glyphStar, col: colStarIdle}
	iconStarFilled  = icon{glyph: glyphStar, fill: true, col: colAmber}
)
