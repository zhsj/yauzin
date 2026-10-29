package ui

import "gioui.org/unit"

// The interface draws text in one ladder of six sizes. Every label, editor and
// input field takes its size from this list instead of a literal: that is what
// keeps the sidebar labels, the message list and the reader body on the same
// scale, and it lets typography_test.go reject stray literals.
//
// The steps are one source pixel apart at 100% scaling, so each step is a small
// but visible change and the hierarchy stays readable at any window size.
const (
	// textTitle is the largest text: the subject of the open conversation and
	// the brand name in the title bar.
	textTitle unit.Sp = 22
	// textHeading titles a page or a dialog.
	textHeading unit.Sp = 18
	// textMain is the reading size: body copy, sidebar labels, message list
	// rows, buttons and input fields.
	textMain unit.Sp = 14
	// textSecondary annotates a main line in place: conversation counts, list
	// timestamps and toolbar status.
	textSecondary unit.Sp = 13
	// textMeta is the smallest information text: addresses, full timestamps
	// and settings descriptions.
	textMeta unit.Sp = 12
	// textTiny fits text inside a badge: label chips and the performance
	// overlay.
	textTiny unit.Sp = 11
)
