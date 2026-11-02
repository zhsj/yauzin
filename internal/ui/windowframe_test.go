package ui

import (
	"image"
	"testing"
)

// TestFrameStrips pins the geometry of the window outline: the four strips
// cover exactly the ring of the window and nothing else, so the content behind
// the outline is never painted over, and the left and right strips do not
// repeat the pixels the top and bottom ones already own.
func TestFrameStrips(t *testing.T) {
	tests := []struct {
		name string
		size image.Point
		w    int
	}{
		{"1x", image.Pt(300, 200), 1},
		{"2x", image.Pt(301, 201), 2},
		{"odd window", image.Pt(9, 7), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strips := frameStrips(tt.size, tt.w)

			// Top and bottom span the window width and are w tall; left and
			// right are w wide and stop between them.
			want := [4]image.Rectangle{
				image.Rect(0, 0, tt.size.X, tt.w),
				image.Rect(0, tt.size.Y-tt.w, tt.size.X, tt.size.Y),
				image.Rect(0, tt.w, tt.w, tt.size.Y-tt.w),
				image.Rect(tt.size.X-tt.w, tt.w, tt.size.X, tt.size.Y-tt.w),
			}
			for i, r := range strips {
				if r != want[i] {
					t.Fatalf("strip %d = %v, want %v", i, r, want[i])
				}
			}

			w := tt.w
			for y := 0; y < tt.size.Y; y++ {
				for x := 0; x < tt.size.X; x++ {
					pt := image.Pt(x, y)
					hits := 0
					for _, r := range strips {
						if pt.In(r) {
							hits++
						}
					}
					ring := x < w || y < w || x >= tt.size.X-w || y >= tt.size.Y-w
					switch {
					case ring && hits != 1:
						t.Fatalf("%v is on the outline ring but covered by %d strips", pt, hits)
					case !ring && hits != 0:
						t.Fatalf("%v is inside the window but covered by %d strips", pt, hits)
					}
				}
			}
		})
	}
}

// TestFrameStripsDegenerate pins the sizes the outline must leave alone: no
// width, an empty window, or a window too narrow to hold two strips plus an
// interior. Drawing any of them would cover the content with the border colour.
func TestFrameStripsDegenerate(t *testing.T) {
	tests := []struct {
		name string
		size image.Point
		w    int
	}{
		{"no width", image.Pt(100, 100), 0},
		{"negative width", image.Pt(100, 100), -1},
		{"empty window", image.Point{}, 1},
		{"one strip wide", image.Pt(1, 40), 1},
		{"one strip tall", image.Pt(40, 1), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, r := range frameStrips(tt.size, tt.w) {
				if !r.Empty() {
					t.Fatalf("frameStrips(%v, %d) drew %v, want no strips", tt.size, tt.w, r)
				}
			}
		})
	}
}
