package ui

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// theme bundles the shared text shaper, font faces and the small Gmail-like
// palette used by every view. All drawing helpers are methods so views do not
// have to plumb the shaper through every call.
type theme struct {
	shaper *text.Shaper
	mat    *material.Theme

	face         font.Typeface
	iconFace     font.Typeface
	iconFillFace font.Typeface

	// glyphSizes caches the natural line box of icon glyphs, keyed by size and
	// scale, so centring them does not shape the same glyph every frame.
	glyphSizes map[glyphKey]image.Point

	// pointer is the pointer position in window coordinates, captured by the
	// App's full-window pass-through area. Widgets combine it with the
	// position their own area reports to locate themselves in the window.
	pointer image.Point

	// tips collects the hover labels recorded while laying out the frame. The
	// App replays them after the whole window is laid out, anchored next to
	// their buttons, so the views drawn later cannot paint over them.
	tips []tipOp
}

// tipOp is one queued hover label: the recorded draw operations, the label
// size and the window coordinates of its top left corner.
type tipOp struct {
	call   op.CallOp
	size   image.Point
	anchor image.Point
}

// glyphKey identifies a cached icon line box.
type glyphKey struct {
	fill    bool
	size    unit.Sp
	pxPerSp float32
}

func newTheme(t *themeFonts) *theme {
	mat := material.NewTheme()
	mat.Shaper = t.shaper
	mat.TextSize = 14
	mat.Face = t.face
	mat.Fg = colText
	mat.Bg = colSurface
	mat.ContrastBg = colPrimary
	mat.ContrastFg = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	return &theme{
		shaper:       t.shaper,
		mat:          mat,
		face:         t.face,
		iconFace:     t.iconFace,
		iconFillFace: t.iconFillFace,
	}
}

// text draws a single run of text, truncating with an ellipsis when maxLines is
// positive and the text does not fit.
func (t *theme) text(gtx layout.Context, txt string, size unit.Sp, col color.NRGBA, weight font.Weight, maxLines int) layout.Dimensions {
	l := material.Label(t.mat, size, txt)
	l.Font = font.Font{Typeface: t.face, Weight: weight}
	l.Color = col
	l.MaxLines = maxLines
	if maxLines > 0 {
		l.Truncator = "…"
		l.WrapPolicy = text.WrapHeuristically
	}
	return labelLayout(gtx, l)
}

// labelLayout keeps a single-line label at its natural line height instead of
// letting it fill a fixed-size cell. A Flex hands every child the row height as
// its minimum constraint, and the label would then report that height while
// still painting the text at the top of it, which looks like the text sits too
// high. The cell width is preserved so the main-axis arithmetic of the parent
// is unaffected.
func labelLayout(gtx layout.Context, l material.LabelStyle) layout.Dimensions {
	cell := gtx.Constraints.Min
	if l.MaxLines > 0 {
		gtx.Constraints.Min = image.Point{}
	}
	dims := l.Layout(gtx)
	if dims.Size.X < cell.X {
		dims.Size.X = cell.X
	}
	return dims
}

// wrappedText draws body text that may span multiple lines.
func (t *theme) wrappedText(gtx layout.Context, txt string, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	return t.text(gtx, txt, size, col, weight, 0)
}

// glyph draws one Material Symbols codepoint.
// selectableText draws wrapped text that can be dragged over and copied with
// Ctrl+C. The Selectable must be kept by the caller so the selection survives
// redraws and can be reset when the text changes.
func (t *theme) selectableText(gtx layout.Context, sel *widget.Selectable, txt string, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	l := material.Label(t.mat, size, txt)
	l.Font = font.Font{Typeface: t.face, Weight: weight}
	l.Color = col
	l.State = sel
	l.SelectionColor = colSelection
	return l.Layout(gtx)
}

func (t *theme) glyph(gtx layout.Context, ic icon, size unit.Sp) layout.Dimensions {
	face := t.iconFace
	if ic.fill {
		face = t.iconFillFace
	}
	l := material.Label(t.mat, size, string(rune(ic.glyph)))
	l.Font = font.Font{Typeface: face}
	l.Color = ic.col
	l.MaxLines = 1
	return labelLayout(gtx, l)
}

// glyphMetrics returns the natural, unconstrained line box of ic drawn at size.
func (t *theme) glyphMetrics(gtx layout.Context, ic icon, size unit.Sp) image.Point {
	key := glyphKey{fill: ic.fill, size: size, pxPerSp: gtx.Metric.PxPerSp}
	if m, ok := t.glyphSizes[key]; ok {
		return m
	}
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
	macro := op.Record(gtx.Ops)
	dims := t.glyph(measure, ic, size)
	macro.Stop()
	if t.glyphSizes == nil {
		t.glyphSizes = map[glyphKey]image.Point{}
	}
	t.glyphSizes[key] = dims.Size
	return dims.Size
}

// glyphIn draws ic optically centred in a square cell of cell dp. The label
// line box is taller than the icon size, so a smaller cell would clamp the
// metrics and paint the glyph low; the natural box is used to centre it instead.
func (t *theme) glyphIn(gtx layout.Context, ic icon, size unit.Sp, cell unit.Dp) layout.Dimensions {
	s := gtx.Dp(cell)
	box := t.glyphMetrics(gtx, ic, size)
	gtx.Constraints = layout.Exact(image.Pt(s, s))
	off := op.Offset(glyphOffset(image.Pt(s, s), box)).Push(gtx.Ops)
	t.glyph(gtx, ic, size)
	off.Pop()
	return layout.Dimensions{Size: image.Pt(s, s)}
}

// glyphOffset centres a natural glyph box inside a cell.
func glyphOffset(cell, box image.Point) image.Point {
	return image.Pt((cell.X-box.X)/2, (cell.Y-box.Y)/2)
}

// recordTip queues the hover label of a hovered button. The label is recorded
// now and replayed at the end of the frame at anchor (window coordinates), so
// the views laid out after the button cannot paint over it and the label sits
// at a fixed spot next to its button.
func (t *theme) recordTip(gtx layout.Context, text string, anchor image.Point) {
	macro := op.Record(gtx.Ops)
	dims := t.tooltip(gtx, text)
	t.tips = append(t.tips, tipOp{call: macro.Stop(), size: dims.Size, anchor: anchor})
}

// tooltip draws a small dark hover label at the current origin: a rounded
// rectangle that hugs a single line of white text.
func (t *theme) tooltip(gtx layout.Context, text string) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
	content := layout.Inset{Top: 5, Bottom: 5, Left: 10, Right: 10}.Layout(measure, func(gtx layout.Context) layout.Dimensions {
		return t.text(gtx, text, 12, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, font.Normal, 1)
	})
	call := macro.Stop()

	size := content.Size
	rr := clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(4)).Push(gtx.Ops)
	paint.Fill(gtx.Ops, colStatusBG)
	rr.Pop()
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: size}
}

func avatarColor(seed string) color.NRGBA {
	palette := []color.NRGBA{
		{R: 0x0B, G: 0x57, B: 0xD0, A: 0xFF},
		{R: 0x1E, G: 0x8E, B: 0x3E, A: 0xFF},
		{R: 0xB3, G: 0x26, B: 0x1E, A: 0xFF},
		{R: 0x84, G: 0x30, B: 0xCE, A: 0xFF},
		{R: 0x00, G: 0x74, B: 0x86, A: 0xFF},
		{R: 0xC5, G: 0x22, B: 0x1F, A: 0xFF},
	}
	var sum int
	for _, r := range seed {
		sum += int(r)
	}
	if sum < 0 {
		sum = -sum
	}
	return palette[sum%len(palette)]
}

func initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	runes := []rune(parts[0])
	if len(parts) == 1 {
		if len(runes) > 2 {
			return string(runes[:2])
		}
		return string(runes)
	}
	second := []rune(parts[1])
	if len(second) == 0 {
		return string(runes[0:1])
	}
	return string(runes[0:1]) + string(second[0:1])
}

// sectionLabel is the small uppercase-ish heading used in the sidebar.
func (t *theme) sectionLabel(gtx layout.Context, txt string) layout.Dimensions {
	return t.text(gtx, txt, 12, colMuted, font.Medium, 1)
}

// emptyState renders a centred icon, title and hint.
func (t *theme) emptyState(gtx layout.Context, ic icon, title, hint string) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return t.glyph(gtx, mutedIcon(ic.glyph), 42)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return t.text(gtx, title, 16, colText, font.Medium, 0)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 4) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return t.text(gtx, hint, 13, colMuted, font.Normal, 0)
			}),
		)
	})
}
