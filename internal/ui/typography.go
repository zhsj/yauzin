package ui

import "gioui.org/unit"

// The interface draws text in one ladder of six sizes. Every label, editor and
// input field takes its size from this list instead of a literal: that is what
// keeps the sidebar labels, the message list and the reader body on the same
// scale, and it lets typography_test.go reject stray literals.
//
// The two largest steps carry the hierarchy; the rest are one source pixel
// apart at 100% scaling, so a step down still reads as clearly smaller while
// small text stays legible.
const (
	// textTitle is the largest text: the subject of the open conversation and
	// the brand name in the title bar.
	textTitle unit.Sp = 24
	// textHeading titles a page or a dialog.
	textHeading unit.Sp = 20
	// textMain is the reading size: body copy, sidebar labels, message list
	// rows, buttons and input fields. Those surfaces are deliberately equal.
	textMain unit.Sp = 16
	// textSecondary annotates a main line in place: conversation counts, list
	// timestamps and toolbar status.
	textSecondary unit.Sp = 14
	// textMeta is the smallest information text: addresses, full timestamps
	// and settings descriptions.
	textMeta unit.Sp = 13
	// textTiny fits text inside a badge: label chips and the performance
	// overlay.
	textTiny unit.Sp = 12
)
