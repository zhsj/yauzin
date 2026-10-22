package ui

import (
	"image"
	"testing"

	"gioui.org/op"
)

// TestOutlineButtonFrames pins the three states of the compact bordered action:
// each one keeps the fixed height of an action line, the label widens the
// button beyond its padding, and a disabled button stays inert.
func TestOutlineButtonFrames(t *testing.T) {
	a := testApp(t)
	neutral := newOutlineButton("relogin", nil)
	danger := newOutlineButton("delete", nil)
	danger.danger = true
	disabled := newOutlineButton("off", func() { t.Error("the disabled button fired") })
	disabled.disabled = true

	cases := []struct {
		name string
		b    *outlineButton
	}{
		{"neutral", neutral},
		{"danger", danger},
		{"disabled", disabled},
	}
	for _, tc := range cases {
		var ops op.Ops
		gtx := testGtx(&ops, image.Pt(400, 100))
		dims := tc.b.Layout(gtx, a.th)
		if want := gtx.Dp(32); dims.Size.Y != want {
			t.Fatalf("%s height = %d, want %d", tc.name, dims.Size.Y, want)
		}
		if pad := 2 * gtx.Dp(14); dims.Size.X <= pad {
			t.Fatalf("%s width = %d, want the label to widen the %d of padding", tc.name, dims.Size.X, pad)
		}
	}

	var ops op.Ops
	gtx := testGtx(&ops, image.Pt(400, 100))
	disabled.click.Click()
	disabled.Layout(gtx, a.th)
}
