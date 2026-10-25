package ui

import (
	"image"
	"testing"

	"gioui.org/op"
)

// TestMenuAlignsCardWithTheAnchorEdge pins the horizontal alignment: a menu
// opened from a control near the left grows rightwards, one opened from the
// right edge grows leftwards. The toolbar's overflow button once opened its
// card 200dp to the left after the reader menu was fixed for the other side.
func TestMenuAlignsCardWithTheAnchorEdge(t *testing.T) {
	a := testApp(t)
	one := []menuItem{{Label: "One"}}
	for _, tc := range []struct {
		name   string
		anchor image.Rectangle
		align  menuAlign
	}{
		{"start", image.Rect(24, 56, 56, 88), menuAlignStart},
		{"end", image.Rect(1100, 40, 1132, 72), menuAlignEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMenuOverlay(a, tc.anchor, tc.align, one)
			size, origin := m.geometry(testGtx(new(op.Ops), testWindow))
			want := tc.anchor.Min.X
			if tc.align == menuAlignEnd {
				want = tc.anchor.Max.X - size.X
			}
			if origin.X != want {
				t.Fatalf("card x = %d, want %d", origin.X, want)
			}
			if origin.Y < tc.anchor.Max.Y {
				t.Fatalf("card y = %d overlaps the anchor ending at %d", origin.Y, tc.anchor.Max.Y)
			}
		})
	}
}

// TestMenuClampsBothAlignsIntoTheWindow covers anchors too close to the
// opposite edge: whichever edge the card lines up with, it still has to fit.
func TestMenuClampsBothAlignsIntoTheWindow(t *testing.T) {
	a := testApp(t)
	one := []menuItem{{Label: "One"}}
	for _, tc := range []struct {
		name   string
		anchor image.Rectangle
		align  menuAlign
	}{
		{"start near the right edge", image.Rect(500, 8, 540, 40), menuAlignStart},
		{"end near the left edge", image.Rect(8, 8, 48, 40), menuAlignEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMenuOverlay(a, tc.anchor, tc.align, one)
			size, origin := m.geometry(testGtx(new(op.Ops), smallWindow))
			if origin.X < 8 || origin.X+size.X > smallWindow.X-8 {
				t.Fatalf("card %v at x = %d is outside the %dpx window", size, origin.X, smallWindow.X)
			}
		})
	}
}

// TestMenuOpensUpwardsWithoutRoomBelow keeps the vertical fallback: an anchor
// near the bottom flips the card above itself instead of pushing it off screen.
func TestMenuOpensUpwardsWithoutRoomBelow(t *testing.T) {
	a := testApp(t)
	anchor := image.Rect(24, 780, 56, 812)
	m := newMenuOverlay(a, anchor, menuAlignStart, []menuItem{{Label: "One"}, {Label: "Two"}})
	size, origin := m.geometry(testGtx(new(op.Ops), testWindow))
	if origin.Y+size.Y > anchor.Min.Y {
		t.Fatalf("card %v at y = %d overlaps the anchor at %d", size, origin.Y, anchor.Min.Y)
	}
}
