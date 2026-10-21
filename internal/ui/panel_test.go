package ui

import (
	"image"
	"testing"

	"gioui.org/op"
)

// TestPanelSize pins the clamp every floating panel relies on: the panel keeps
// its wanted size, shrinks with the viewport down to its minimum, and never
// grows wider than the window minus the margins.
func TestPanelSize(t *testing.T) {
	tests := []struct {
		name    string
		window  image.Point
		want    image.Point
		minSize image.Point
		out     image.Point
	}{
		{
			name:    "roomy window keeps the wanted size",
			window:  testWindow,
			want:    image.Pt(480, 540),
			minSize: image.Pt(320, 160),
			out:     image.Pt(480, 540),
		},
		{
			name:    "narrow window shrinks down to the margins",
			window:  image.Pt(500, 400),
			want:    image.Pt(480, 540),
			minSize: image.Pt(320, 160),
			out:     image.Pt(452, 352),
		},
		{
			name:    "window below the minimum keeps the margins",
			window:  image.Pt(300, 120),
			want:    image.Pt(480, 540),
			minSize: image.Pt(320, 160),
			out:     image.Pt(252, 72),
		},
		{
			name:    "minimized compose collapses to the header row",
			window:  testWindow,
			want:    image.Pt(480, 48),
			minSize: image.Pt(320, 48),
			out:     image.Pt(480, 48),
		},
		{
			name:    "degenerate window never yields a negative size",
			window:  image.Point{},
			want:    image.Pt(480, 540),
			minSize: image.Pt(320, 160),
			out:     image.Pt(1, 1),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ops op.Ops
			gtx := testGtx(&ops, tc.window)
			if got := panelSize(gtx, tc.want, tc.minSize); got != tc.out {
				t.Fatalf("panelSize(%v, %v, %v) = %v, want %v",
					tc.window, tc.want, tc.minSize, got, tc.out)
			}
		})
	}
}

// TestPlaceCardConstrainsToSize checks the second half of the panel template:
// the returned context is pinned to the card size and the offset is popped by
// the caller.
func TestPlaceCardConstrainsToSize(t *testing.T) {
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	size := image.Pt(320, 200)

	cardGtx, pop := placeCard(gtx, size, image.Pt(24, 40))
	if cardGtx.Constraints.Max != size || cardGtx.Constraints.Min != size {
		t.Fatalf("card constraints = %v..%v, want exactly %v",
			cardGtx.Constraints.Min, cardGtx.Constraints.Max, size)
	}
	if gtx.Constraints.Max != testWindow {
		t.Fatalf("placeCard mutated the caller's constraints: %v", gtx.Constraints.Max)
	}
	pop()
}
