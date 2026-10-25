package ui

import (
	"image"
	"strconv"
	"testing"
	"time"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
)

// appWithManyLabels builds an app whose sidebar is taller than the window.
func appWithManyLabels(t *testing.T, labels int) *App {
	t.Helper()
	a := testApp(t)
	a.folders = []mail.Folder{
		{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox, Unread: 3},
		{ID: "starred", Name: "Starred", Kind: mail.FolderStarred},
		{ID: "sent", Name: "Sent", Kind: mail.FolderSent},
		{ID: "trash", Name: "Trash", Kind: mail.FolderTrash},
	}
	for i := 0; i < labels; i++ {
		a.folders = append(a.folders, mail.Folder{
			ID:    "label:" + strconv.Itoa(i),
			Name:  "Label " + strconv.Itoa(i),
			Kind:  mail.FolderLabel,
			Color: "#0B57D0",
		})
	}
	a.side.reload()
	a.list.setThreads([]mail.ThreadSummary{{
		ID:           "t0",
		Subject:      "Subject",
		Snippet:      "Snippet",
		Participants: []mail.Address{{Name: "Ada", Email: "ada@example.com"}},
		LastDate:     time.Now(),
		MessageCount: 1,
		MessageIDs:   []string{"m0"},
		// More long labels than a narrow row can hold: the strip has to give
		// the width back to the subject instead of overflowing.
		Labels: []string{"label:0", "label:1", "label:2", "label:3"},
	}})
	return a
}

func TestSmallWindowLayout(t *testing.T) {
	a := appWithManyLabels(t, 60)

	// The mailbox alone.
	a.layout(testGtx(new(op.Ops), smallWindow))

	// The same row laid out on its own at the smallest width: the label strip
	// is dropped there, and the row keeps its height.
	dims := a.list.rows[0].Layout(testGtx(new(op.Ops), smallWindow), a.list.threads[0], false, false)
	if want := image.Pt(smallWindow.X, testGtx(new(op.Ops), smallWindow).Dp(listRowHeight)); dims.Size != want {
		t.Fatalf("narrow row size = %v, want %v", dims.Size, want)
	}

	// Every panel, one after another: none of them may panic or depend on a
	// window that is wider than the minimums they assume.
	for name, open := range map[string]func(){
		"settings": a.showSettings,
		"shortcuts": func() {
			a.showSettings()
			a.settings.page = settingsTabShortcuts
		},
		"compose": func() { a.openCompose(composeNew, nil) },
		"menu":    a.showAccountMenu,
	} {
		t.Run(name, func(t *testing.T) {
			open()
			a.layout(testGtx(new(op.Ops), smallWindow))
			a.hideOverlay()
			a.layout(testGtx(new(op.Ops), smallWindow))
		})
	}
}

// TestShortcutsPageScrollsInSmallWindow pushes the shortcuts page past the
// bottom of the smallest panel: the keyboard table must scroll instead of
// being clipped away. The page is one oversized list child, so scrolling
// moves Position.Offset and leaves First at zero.
func TestShortcutsPageScrollsInSmallWindow(t *testing.T) {
	a := testApp(t)
	a.showSettings()
	a.settings.page = settingsTabShortcuts
	a.layout(testGtx(new(op.Ops), smallWindow))
	if got := a.settings.list.Position.Offset; got != 0 {
		t.Fatalf("shortcuts page starts at offset %d, want the top", got)
	}

	a.settings.list.Position.Offset = 100
	a.layout(testGtx(new(op.Ops), smallWindow))
	if a.settings.list.Position.Offset == 0 {
		t.Fatal("the shortcuts table did not scroll in a 420px window")
	}
}

func TestSidebarScrollsToTheLastLabel(t *testing.T) {
	a := appWithManyLabels(t, 60)
	gtx := testGtx(new(op.Ops), smallWindow)

	a.side.Layout(gtx)
	if got := a.side.list.Position.First; got != 0 {
		t.Fatalf("sidebar starts at row %d, want the top", got)
	}

	// Force the column to the end. Gio clamps the requested index to the last
	// row; a scrollable list stays past zero, a list that fits snaps back.
	total := len(a.side.folders) + len(a.side.labels)
	a.side.list.Position.First = total + 10
	a.side.Layout(testGtx(new(op.Ops), smallWindow))
	if a.side.list.Position.First == 0 {
		t.Fatal("60 labels did not scroll in a 420px column")
	}
	if a.side.list.Position.First > total {
		t.Fatalf("first visible row %d is past the %d entries",
			a.side.list.Position.First, total)
	}
}

func TestMenuCardStaysInsideSmallWindow(t *testing.T) {
	a := testApp(t)
	items := make([]menuItem, 40)
	m := newMenuOverlay(a, image.Rect(500, 360, 540, 400), menuAlignEnd, items)

	size, origin := m.geometry(testGtx(new(op.Ops), smallWindow))
	if size.X > smallWindow.X-16 || size.Y > smallWindow.Y-16 {
		t.Fatalf("card %v is larger than the window %v", size, smallWindow)
	}
	if origin.X < 8 || origin.Y < 8 {
		t.Fatalf("card origin %v is outside the window margin", origin)
	}
	if origin.X+size.X > smallWindow.X-8 || origin.Y+size.Y > smallWindow.Y-8 {
		t.Fatalf("card %v at %v overflows the window %v", size, origin, smallWindow)
	}
}

func TestMenuScrollsWhenTallerThanWindow(t *testing.T) {
	a := testApp(t)
	items := make([]menuItem, 40)
	for i := range items {
		items[i] = menuItem{Label: "Item " + strconv.Itoa(i)}
	}
	m := newMenuOverlay(a, image.Rect(8, 8, 48, 40), menuAlignStart, items)

	m.Layout(testGtx(new(op.Ops), smallWindow), a)
	if got := m.list.Position.First; got != 0 {
		t.Fatalf("menu starts at row %d, want the top", got)
	}

	m.list.Position.First = len(m.rows) + 10
	m.Layout(testGtx(new(op.Ops), smallWindow), a)
	if m.list.Position.First == 0 {
		t.Fatal("40 menu entries did not scroll in a 420px window")
	}
}
