package ui

import (
	"image"
	"strconv"
	"strings"
	"testing"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// testApp builds just enough state to lay out one headless frame. It avoids
// the window and explorer, which are not needed for pure layout, and leaves the
// account without a backend: tests that need one install a fake with
// useService.
func testApp(t *testing.T) *App {
	t.Helper()
	return newAppWith(prefs.Settings{}, appOptions{
		accounts: []mail.Account{{ID: "demo", Kind: mail.KindGmail, Name: "Ada Chen", Email: "ada@example.com"}},
		newService: func(mail.Account) mail.Service {
			return nil
		},
	})
}

func TestLayoutFrames(t *testing.T) {
	a := testApp(t)
	a.folders = []mail.Folder{
		{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox, Unread: 2},
		{ID: "starred", Name: "Starred", Kind: mail.FolderStarred},
		{ID: "work", Name: "Work", Kind: mail.FolderLabel, Color: "#0B57D0", Unread: 1},
		{ID: "bills", Name: "Bills", Kind: mail.FolderLabel, Color: "#1E8E3E"},
	}
	a.side.reload()

	threads := make([]mail.ThreadSummary, 0, 60)
	for i := 0; i < 60; i++ {
		// Half the rows carry two labels, the rest one; the system ids in the
		// backend list must not turn into chips.
		labels := []string{"work", "INBOX"}
		if i%2 == 0 {
			labels = []string{"work", "bills", "CATEGORY_PERSONAL"}
		}
		threads = append(threads, mail.ThreadSummary{
			ID:             "t" + strconv.Itoa(i),
			Subject:        "Subject " + strconv.Itoa(i) + " 中文主题",
			Snippet:        "Snippet 摘要 " + strings.Repeat("x", 40),
			Participants:   []mail.Address{{Name: "Ada Chen", Email: "ada@example.com"}},
			LastDate:       time.Now().Add(-time.Duration(i) * time.Hour),
			MessageCount:   2,
			UnreadCount:    1,
			MessageIDs:     []string{"m" + strconv.Itoa(i)},
			HasAttachments: i%3 == 0,
			Labels:         labels,
		})
	}
	a.list.setThreads(threads)
	a.toolbar.setPage(1, 112, len(threads), true)
	a.list.selected[threads[0].ID] = true

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)

	a.layout(gtx)
	ops.Reset()

	// Reader with a long text body and an HTML-only message.
	a.selectedThread = "t0"
	a.thread = threads[0]
	a.msgs = []*mail.Message{
		{MessageSummary: mail.MessageSummary{ID: "m0", From: mail.Address{Name: "Ada", Email: "ada@example.com"}, Subject: "Hello", Date: time.Now(), Snippet: "hi"}, TextBody: strings.Repeat("long plain text 中文正文\n", 30)},
		{MessageSummary: mail.MessageSummary{ID: "m1", From: mail.Address{Name: "Bob", Email: "bob@example.com"}, Subject: "HTML", Date: time.Now(), Snippet: "html"}, HTMLBody: "<p>html only</p>", Attachments: []mail.Attachment{{ID: "a1", Filename: "report.pdf"}}},
	}
	a.expanded["m1"] = true
	a.layout(gtx)
	ops.Reset()

	// All overlays must lay out without panicking.
	a.overlay = newComposeView(a, composeNew, nil, "")
	a.layout(gtx)
	ops.Reset()
	a.overlay = newSettingsView(a)
	a.layout(gtx)
	ops.Reset()
	// The notification must keep its corner while a panel is open: the panels
	// report their card size, which used to drag the bar up the sidebar.
	a.status.show("saved", false)
	a.layout(gtx)
	ops.Reset()
	a.status.clear()
	a.overlay = newMenuOverlay(a, image.Rect(1100, 40, 1132, 72), menuAlignEnd, []menuItem{
		{Label: "One", Icon: iconMarkRead},
		{Label: "Two", Avatar: "Ada Chen", Checked: true},
	})
	a.layout(gtx)
	ops.Reset()

	// Client-side decorations: the compact header plus the title bar the app
	// draws for itself.
	a.decorated = false
	a.layout(gtx)
	ops.Reset()
	a.maximized = true
	a.layout(gtx)
	ops.Reset()
}

// TestStatusOverlayIgnoresMinimum pins the notification to the window corner:
// overlays() stacks it as an Expanded sibling of the panels, and Stack hands an
// Expanded child the largest sibling's size as its minimum. The panels report
// their card size, so the bar used to anchor to that card and float up to the
// middle of the sidebar while settings was open.
func TestStatusOverlayIgnoresMinimum(t *testing.T) {
	a := testApp(t)
	a.status.show("saved", false)

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	// What layout.Stack hands the second Expanded child with settings open.
	gtx.Constraints.Min = image.Pt(520, 560)

	if dims := a.statusOverlay(gtx); dims.Size != testWindow {
		t.Fatalf("status overlay anchored to %v, want the %v window", dims.Size, testWindow)
	}
}

// TestClientTitleBarFrame covers the combined single-row title bar: the header
// doubles as the window decoration, so it must fit both the controls and the
// window buttons at the smallest supported window width.
func TestClientTitleBarFrame(t *testing.T) {
	a := testApp(t)
	a.decorated = false
	if !a.clientSide() {
		t.Fatal("an undecorated platform must give the app the title bar")
	}
	if got := a.headerHeight(); got != topBarHeightCSD {
		t.Fatalf("title bar height = %v, want %v", got, topBarHeightCSD)
	}

	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	if a.top.controls.Width(gtx) <= 0 {
		t.Fatal("window buttons have no width")
	}
	for _, size := range []image.Point{{1200, 840}, {560, 700}} {
		gtx.Constraints = layout.Constraints{Max: size}
		a.layout(gtx)
		ops.Reset()
		a.maximized = !a.maximized
		a.layout(gtx)
		ops.Reset()
	}
}

// TestMailboxFitsBelowClientTitleBar pins the space the mailbox gets under a
// client-side title bar: the bar covers the top of the window, so the mailbox
// has to be laid out in the remainder. It used to keep the full window height,
// which put the bottom of the reader (its reply row) below the window edge and
// out of reach of the scroll.
func TestMailboxFitsBelowClientTitleBar(t *testing.T) {
	a := testApp(t)
	a.selectedThread = "t0"
	a.thread = mail.ThreadSummary{ID: "t0", Subject: "Subject", MessageCount: 1}
	a.msgs = []*mail.Message{{
		MessageSummary: mail.MessageSummary{ID: "m0", From: mail.Address{Name: "Ada", Email: "ada@example.com"}, Date: time.Now()},
		TextBody:       strings.Repeat("a line of the plain text body\n", 80),
	}}
	a.expanded["m0"] = true

	// viewport is the height the reader's list is given, measured by asking for
	// more scroll than exists and reading back the clamped position.
	viewport := func(clientSide bool) int {
		a.decorated = !clientSide
		var ops op.Ops
		gtx := testGtx(&ops, testWindow)
		a.layout(gtx)
		ops.Reset()
		a.reader.list.Position.Offset = 1 << 20
		a.layout(gtx)
		p := a.reader.list.Position
		return p.Length - p.Offset
	}

	flow := viewport(false)
	below := viewport(true)
	if want := flow + int(topBarHeight-topBarHeightCSD); below != want {
		t.Fatalf("reader viewport under the title bar = %d, want %d: the %vdp bar replaces the %vdp header", below, want, topBarHeightCSD, topBarHeight)
	}
}

// TestBrandWordmarkFitsTitleBar keeps the wordmark inside the combined title
// bar: the label shares the 40dp content row with the 36dp mark, so a later
// size bump has to fail here instead of clipping the text.
func TestBrandWordmarkFitsTitleBar(t *testing.T) {
	a := testApp(t)
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)

	row := gtx.Dp(topBarHeightCSD) - 2*gtx.Dp(4)
	dims := a.th.text(gtx, i18n.T("app.brand"), brandNameSize, colPrimary, brandNameWeight, 1)
	if dims.Size.Y > row {
		t.Fatalf("wordmark line height %dpx does not fit the %dpx title bar row", dims.Size.Y, row)
	}
	// The wordmark is truncated with an ellipsis once it outgrows its cell, and
	// the cell width is what keeps the centred search pill stable.
	if cell := gtx.Sp(brandNameSize) * brandNameEms; dims.Size.X > cell {
		t.Fatalf("wordmark %dpx is wider than its %dpx cell", dims.Size.X, cell)
	}
	if mark := gtx.Dp(brandMarkSize); dims.Size.Y > mark {
		t.Fatalf("wordmark line height %dpx outsizes the %dpx mark", dims.Size.Y, mark)
	}
}

// TestToolbarOverflowAnchor pins the overflow menu anchor to the overflow
// button: it used to be a synthetic band below the toolbar, which opened the
// menu a full button height too low.
func TestToolbarOverflowAnchor(t *testing.T) {
	a := testApp(t)
	var ops op.Ops
	gtx := testGtx(&ops, testWindow)
	a.layout(gtx)

	btn := gtx.Dp(a.toolbar.more.size)
	rect := a.toolbar.moreRect
	if rect.Dx() != btn || rect.Dy() != btn {
		t.Fatalf("overflow anchor %v is not the %dpx button", rect, btn)
	}
	top := gtx.Dp(a.headerHeight()) + 6
	bottom := top + gtx.Dp(48)
	if rect.Min.Y < top || rect.Max.Y > bottom {
		t.Fatalf("overflow anchor %v is outside the toolbar band %d..%d", rect, top, bottom)
	}
}

// TestGlyphOffsetCentresIconBox pins the correction for material labels being
// taller than the fixed cell they are painted in: the glyph keeps its natural
// line box, so a 20sp icon needs a negative offset inside a 20dp cell.
func TestGlyphOffsetCentresIconBox(t *testing.T) {
	th := newTheme(loadFonts())
	gtx := testGtx(new(op.Ops), image.Pt(200, 200))

	cell := image.Pt(20, 20)
	box := th.glyphMetrics(gtx, iconInbox, 20)
	if box.Y <= cell.Y {
		t.Fatalf("natural icon box %v should be taller than the %v cell", box, cell)
	}
	if off := glyphOffset(cell, box); off.Y >= 0 {
		t.Fatalf("offset %v should pull the glyph up inside the cell", off)
	}
	if got := th.glyphMetrics(gtx, iconInbox, 20); got != box {
		t.Fatalf("glyph metrics are not cached: %v then %v", box, got)
	}
	if dims := th.glyphIn(gtx, iconInbox, 20, 20); dims.Size != cell {
		t.Fatalf("glyph cell size = %v, want %v", dims.Size, cell)
	}
	if off := glyphOffset(image.Pt(40, 40), image.Pt(20, 20)); off != image.Pt(10, 10) {
		t.Fatalf("offset of a smaller glyph = %v, want (10,10)", off)
	}
}

// TestColumnKeepsNaturalHeight pins the fix for stacked text sitting at the top
// of a fixed row: a nested Flex inherits the row height as its minimum main
// size and would push the leftover space below the text, so column must report
// the natural height and let the parent centre it.
func TestColumnKeepsNaturalHeight(t *testing.T) {
	th := newTheme(loadFonts())
	gtx := testCell(new(op.Ops), image.Pt(200, 46))

	dims := column(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return th.text(gtx, "收件箱", 14, colText, font.Medium, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return th.text(gtx, "ada@example.com", 12, colMuted, font.Normal, 1)
		}),
	)
	if dims.Size.Y >= 46 {
		t.Fatalf("column was stretched to the row height: %d", dims.Size.Y)
	}
	if dims.Size.X != 200 {
		t.Fatalf("column width = %d, want the full 200px cell", dims.Size.X)
	}
}

// TestTextKeepsLineHeightInFixedRow pins the fix for text sitting at the top of
// a row: a Flex hands children the row height as their minimum constraint, so a
// single-line label must report its natural line height (letting the Flex centre
// it) while still filling the cell width.
func TestTextKeepsLineHeightInFixedRow(t *testing.T) {
	th := newTheme(loadFonts())
	row := testCell(new(op.Ops), image.Pt(200, 36))
	free := testGtx(new(op.Ops), image.Pt(200, 1<<20))

	natural := th.text(free, "收件箱", 14, colText, font.Medium, 1)
	dims := th.text(row, "收件箱", 14, colText, font.Medium, 1)
	if dims.Size.Y != natural.Size.Y {
		t.Fatalf("row text height = %d, want the natural %d", dims.Size.Y, natural.Size.Y)
	}
	if dims.Size.Y >= 36 {
		t.Fatalf("label was stretched to the row height: %d", dims.Size.Y)
	}
	if dims.Size.X != 200 {
		t.Fatalf("label width = %d, want the full 200px cell", dims.Size.X)
	}
}

// TestMessageRowShowsLabelChips covers the label pills of a row: the full slash
// name is drawn, the active label and the system ids are filtered out, and the
// pills stay inside the strip width without changing the row height.
func TestMessageRowShowsLabelChips(t *testing.T) {
	a := testApp(t)
	a.folders = []mail.Folder{
		{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox},
		{ID: "work", Name: "Work", Kind: mail.FolderLabel, Color: "#0B57D0"},
		{ID: "proj", Name: "Work/Project A", Kind: mail.FolderLabel, Color: "#C2185B"},
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
		Labels:       []string{"work", "proj", "INBOX", "UNREAD"},
	}})

	sum := a.list.threads[0]
	chips := a.rowLabels(sum)
	if len(chips) != 2 || chips[0].name != "Work" || chips[1].name != "Work/Project A" {
		t.Fatalf("row labels = %+v, want the two user labels with full names", chips)
	}

	// The label being viewed is what the row sits under, so it is not repeated.
	a.activeFolder = "work"
	if chips := a.rowLabels(sum); len(chips) != 1 || chips[0].name != "Work/Project A" {
		t.Fatalf("row labels under the Work label = %+v, want only the child", chips)
	}
	a.activeFolder = "inbox"

	gtx := testGtx(new(op.Ops), image.Pt(900, 40))
	dims := a.list.rows[0].Layout(gtx, sum, false, false)
	if want := image.Pt(900, gtx.Dp(listRowHeight)); dims.Size != want {
		t.Fatalf("row size = %v, want %v", dims.Size, want)
	}

	strip := a.list.rows[0].labelStrip(testGtx(new(op.Ops), image.Pt(900, 40)), chips, 150)
	if strip.Size.X <= 0 || strip.Size.X > 150 {
		t.Fatalf("label strip width = %d, want a positive width within 150px", strip.Size.X)
	}
	if strip.Size.Y <= 0 || strip.Size.Y >= gtx.Dp(listRowHeight) {
		t.Fatalf("label strip height = %d, want it inside the %vdp row", strip.Size.Y, listRowHeight)
	}
}
