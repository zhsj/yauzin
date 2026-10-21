package ui

import (
	"image/color"

	"gioui.org/unit"
)

// Gmail Web palette.
var (
	colPageBG      = color.NRGBA{R: 0xF6, G: 0xF8, B: 0xFC, A: 0xFF}
	colSurface     = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	colSurface2    = color.NRGBA{R: 0xF2, G: 0xF6, B: 0xFC, A: 0xFF}
	colSearchBG    = color.NRGBA{R: 0xEA, G: 0xF1, B: 0xFB, A: 0xFF}
	colPrimary     = color.NRGBA{R: 0x0B, G: 0x57, B: 0xD0, A: 0xFF}
	colPrimaryDark = color.NRGBA{R: 0x08, G: 0x42, B: 0xA0, A: 0xFF}
	colPrimarySoft = color.NRGBA{R: 0xE8, G: 0xF0, B: 0xFE, A: 0xFF}
	colNavActive   = color.NRGBA{R: 0xD3, G: 0xE3, B: 0xFD, A: 0xFF}
	colRowSelected = color.NRGBA{R: 0xC2, G: 0xDB, B: 0xFF, A: 0xFF}
	colRowRead     = color.NRGBA{R: 0xF2, G: 0xF6, B: 0xFC, A: 0xFF}
	colRowHover    = color.NRGBA{R: 0xEE, G: 0xF3, B: 0xFA, A: 0xFF}
	colText        = color.NRGBA{R: 0x20, G: 0x21, B: 0x24, A: 0xFF}
	colMuted       = color.NRGBA{R: 0x5F, G: 0x63, B: 0x68, A: 0xFF}
	colFaint       = color.NRGBA{R: 0x9A, G: 0xA0, B: 0xA6, A: 0xFF}
	colBorder      = color.NRGBA{R: 0xE0, G: 0xE3, B: 0xE7, A: 0xFF}
	colHover       = color.NRGBA{R: 0xE8, G: 0xF0, B: 0xFE, A: 0xFF}
	colDanger      = color.NRGBA{R: 0xD9, G: 0x30, B: 0x25, A: 0xFF}
	colAmber       = color.NRGBA{R: 0xF4, G: 0xB4, B: 0x00, A: 0xFF}
	colStarIdle    = color.NRGBA{R: 0xB8, G: 0xBD, B: 0xC4, A: 0xFF}
	colSelection   = color.NRGBA{R: 0x0B, G: 0x57, B: 0xD0, A: 0x33}
	colOverlay     = color.NRGBA{R: 0x1F, G: 0x1F, B: 0x1F, A: 0x3D}
)

// colOnDeco is the glyph colour on the red close-button hover, the only place
// that still needs white on a saturated background.
var colOnDeco = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

const (
	sidebarWidth = unit.Dp(256)
	// topBarHeight is the Gmail header height used while the platform draws the
	// window decorations; topBarHeightCSD is the height of the combined title
	// bar, which carries the window buttons and the header controls in one row.
	topBarHeight    = unit.Dp(64)
	topBarHeightCSD = unit.Dp(48)
	listRowHeight   = unit.Dp(40)
	// listCursorWidth is the accent bar on the left edge of the row under the
	// keyboard cursor; it is what tells the cursor apart from a checked row.
	listCursorWidth = unit.Dp(3)
)
