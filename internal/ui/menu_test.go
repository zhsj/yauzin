package ui

import (
	"image"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
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

// TestAccountMenuShowsTheAddress covers two mailboxes that share a display
// name: the address under the name is what tells them apart. Every account
// entry carries its address, the manage entry carries none.
func TestAccountMenuShowsTheAddress(t *testing.T) {
	a := newAppWith(prefs.Settings{}, appOptions{
		accounts: []mail.Account{
			{ID: "work", Kind: mail.KindGmail, Name: "Work", Email: "me@example.com"},
			{ID: "other", Kind: mail.KindGmail, Name: "Work", Email: "me@example.org"},
		},
		newService: func(mail.Account) mail.Service { return nil },
	})
	a.showAccountMenu()

	m, ok := a.overlay.(*menuOverlay)
	if !ok {
		t.Fatalf("overlay = %T after the account menu, want *menuOverlay", a.overlay)
	}
	if len(m.rows) != 3 {
		t.Fatalf("menu has %d entries, want the two accounts and the manage entry", len(m.rows))
	}
	for i, want := range []string{"me@example.com", "me@example.org"} {
		if got := m.rows[i].label; got != "Work" {
			t.Fatalf("row %d label = %q, want the shared display name", i, got)
		}
		if got := m.rows[i].detail; got != want {
			t.Fatalf("row %d detail = %q, want %q", i, got, want)
		}
	}
	if got := m.rows[2].detail; got != "" {
		t.Fatalf("manage row detail = %q, want none", got)
	}
}

// TestMenuRowsWithDetailAreTaller pins the sizes a two-line entry needs: the
// card has to be tall enough for both lines and wide enough for an address,
// while the plain menu keeps its narrow card.
func TestMenuRowsWithDetailAreTaller(t *testing.T) {
	a := testApp(t)
	gtx := testGtx(new(op.Ops), testWindow)
	m := newMenuOverlay(a, image.Rect(24, 56, 56, 88), menuAlignStart, []menuItem{
		{Label: "One"},
		{Label: "Two", Detail: "two@example.com"},
	})

	if got, want := m.rows[0].height(gtx), gtx.Dp(menuRowHeight); got != want {
		t.Fatalf("plain row height = %d, want %d", got, want)
	}
	if got, want := m.rows[1].height(gtx), gtx.Dp(menuRowDetailHeight); got != want {
		t.Fatalf("detail row height = %d, want %d", got, want)
	}

	size, _ := m.geometry(gtx)
	if want := gtx.Dp(menuRowHeight+menuRowDetailHeight) + gtx.Dp(12); size.Y != want {
		t.Fatalf("card height = %d, want %d for a plain and a detail row", size.Y, want)
	}
	if want := gtx.Dp(menuCardDetailWidth); size.X != want {
		t.Fatalf("card width = %d, want the wide card %d", size.X, want)
	}

	plain := newMenuOverlay(a, image.Rect(24, 56, 56, 88), menuAlignStart, []menuItem{{Label: "One"}})
	if size, _ := plain.geometry(gtx); size.X != gtx.Dp(menuCardWidth) {
		t.Fatalf("plain card width = %d, want %d", size.X, gtx.Dp(menuCardWidth))
	}
}

// TestMenuRowLayoutUsesTheDetailHeight keeps the rendered row as tall as the
// geometry assumes; a row pinned to the plain height would clip the address.
func TestMenuRowLayoutUsesTheDetailHeight(t *testing.T) {
	a := testApp(t)
	row := newMenuRow(menuItem{Label: "Work", Detail: "me@example.com"}, nil)
	gtx := testGtx(new(op.Ops), image.Pt(280, 400))
	if got, want := row.Layout(gtx, a.th).Size.Y, gtx.Dp(menuRowDetailHeight); got != want {
		t.Fatalf("detail row lays out at %dpx, want %d", got, want)
	}
}
