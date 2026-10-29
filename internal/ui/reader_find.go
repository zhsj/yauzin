package ui

import (
	"image"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

const (
	// findHitLimit bounds the hits kept for highlighting. A pathological query
	// (one letter in a megabyte-long body) would otherwise make every frame
	// walk tens of thousands of ranges; the counter marks the overflow instead.
	findHitLimit = 1000
	// findBarHeight is the height of the find row under the reader toolbar.
	findBarHeight unit.Dp = 40
	// findRevealDivisor is the fraction of the viewport the current hit is
	// moved to when a jump happens: one third from the top leaves the
	// preceding lines readable.
	findRevealDivisor = 3
)

// findHit is one match of the reader's find: the message it was found in and
// the rune range of the match in that message's plain-text body.
type findHit struct {
	msgID string
	start int
	end   int
}

// findMarks is the highlight geometry of one message card. The rectangles are
// relative to the body text widget and come from the layout of an earlier
// frame: Gio only reports glyph positions after the text was laid out, so the
// rectangles of the frame that finds a new match are painted one frame later.
type findMarks struct {
	gen   int
	width int
	text  string
	rects []image.Rectangle
	// hitOf[i] is the index in findBar.hits of the hit rects[i] belongs to: a
	// hit spanning a line break owns several rectangles. The current hit is
	// looked up per frame instead of being cached, so moving the cursor
	// re-colours and re-scrolls without measuring the text again.
	hitOf []int
}

// valid reports whether the marks describe the body shown at width under the
// hits of generation gen. The width is part of the key: a resize moves every
// line, and painting the old rectangles would flash the highlight elsewhere.
// The current hit is deliberately not part of the key: invalidating on every
// next/previous would skip painting the whole conversation for one frame.
func (m *findMarks) valid(gen, width int, text string) bool {
	return gen != 0 && m.gen == gen && m.width == width && m.text == text
}

// current returns the first rectangle of the given hit, or false when this card
// holds no rectangle for it.
func (m *findMarks) current(cur int) (image.Rectangle, bool) {
	for i, hit := range m.hitOf {
		if hit == cur {
			return m.rects[i], true
		}
	}
	return image.Rectangle{}, false
}

// paint draws the rectangles under the text. The current hit gets the stronger
// colour.
func (m *findMarks) paint(gtx layout.Context, cur int) {
	for i, r := range m.rects {
		col := colFindMark
		if m.hitOf[i] == cur {
			col = colFindCurrent
		}
		area := clip.Rect(r).Push(gtx.Ops)
		paint.Fill(gtx.Ops, col)
		area.Pop()
	}
}

// findBar is the find-in-conversation row of the reader: the query, the hits it
// produced and the cursor into them. It only holds transient state; the
// messages, the cards and the scroll position stay with the App and the reader.
type findBar struct {
	reader *readerView

	open     bool
	ed       widget.Editor
	prev     *iconButton
	next     *iconButton
	closeBtn *iconButton

	// hits are the matches in message order, at most findHitLimit of them.
	hits []findHit
	// capped records that the conversation holds more matches than hits.
	capped bool
	// cur is the index of the current hit in hits.
	cur int
	// gen changes whenever hits are recomputed. Cards key their cached
	// highlight geometry on it, so a stale frame is never painted.
	gen int
	// query and ids are the inputs the current hits were computed from.
	query string
	ids   string

	// focused mirrors the focus of the find editor, so the shortcuts do not
	// steal the keys while the user types a query.
	focused bool
	// focusRequested hands the keyboard to the editor on the next frame: the
	// toolbar button callback runs without a layout context.
	focusRequested bool
	// jump asks the next laid-out frame to bring the current hit into view.
	jump bool
	// tries bounds the reveal correction, so a hit that cannot be centred
	// stops instead of keeping the window redrawing.
	tries int
}

func newFindBar(r *readerView) *findBar {
	f := &findBar{reader: r}
	f.ed.SingleLine = true
	f.ed.Submit = true
	f.prev = newIconButton(iconExpandLess, 28, func() { f.step(-1) })
	f.prev.setGlyph(18)
	f.next = newIconButton(iconExpandMore, 28, func() { f.step(1) })
	f.next.setGlyph(18)
	f.closeBtn = newIconButton(iconClose, 28, f.hide)
	f.closeBtn.setGlyph(16)
	return f
}

// active reports whether the cards should highlight their matches.
func (f *findBar) active() bool {
	return f.open && f.query != ""
}

// show opens the row and hands it the keyboard. Triggered again while it is
// open, the existing query is selected so typing replaces it.
func (f *findBar) show() {
	f.open = true
	f.focusRequested = true
	f.focused = true
	if n := utf8.RuneCountInString(f.ed.Text()); n > 0 {
		f.ed.SetCaret(0, n)
	}
	f.reader.app.invalidate()
}

// hide closes the row, drops the query and its hits and gives the keyboard back
// to the window root.
func (f *findBar) hide() {
	f.open = false
	f.focused = false
	f.focusRequested = false
	f.ed.SetText("")
	f.clear()
	f.reader.app.blurRequested = true
	f.reader.app.invalidate()
}

// reset forgets the row without touching the focus. It runs when the reader
// switches conversations or closes.
func (f *findBar) reset() {
	f.open = false
	f.focused = false
	f.focusRequested = false
	f.ed.SetText("")
	f.clear()
}

// clear drops the hits of the previous query. The generation goes up even when
// there was nothing there, so the cards drop their cached rectangles.
func (f *findBar) clear() {
	f.gen++
	f.hits = f.hits[:0]
	f.capped = false
	f.cur = 0
	f.query = ""
	f.ids = ""
	f.jump = false
	f.tries = 0
}

// trackFocus samples the focus of the find editor and answers a pending focus
// request. It is called from handleKeys, before the layout that would read the
// editor events, the same way the top bar tracks its search field.
func (f *findBar) trackFocus(gtx layout.Context) {
	if f.focusRequested {
		f.focusRequested = false
		f.focused = true
		gtx.Execute(key.FocusCmd{Tag: &f.ed})
		return
	}
	f.focused = f.open && gtx.Focused(&f.ed)
}

// sync recomputes the hits when the query or the messages changed and reports
// whether it did. It runs right after the editor was laid out, so a character
// typed in this frame is part of the count.
func (f *findBar) sync(msgs []*mail.Message) bool {
	sig := messageSignature(msgs)
	query := f.ed.Text()
	if query == f.query && sig == f.ids {
		return false
	}
	f.gen++
	f.query, f.ids = query, sig
	f.hits = f.hits[:0]
	f.capped = false
	f.cur = 0
	f.jump = false
	f.tries = 0
	if query != "" {
		for _, msg := range msgs {
			body := plainBody(msg)
			if body == "" {
				continue
			}
			budget := findHitLimit - len(f.hits)
			ranges := findRanges(body, query, budget+1)
			if len(ranges) > budget {
				f.capped = true
				ranges = ranges[:budget]
			}
			for _, r := range ranges {
				f.hits = append(f.hits, findHit{msgID: msg.ID, start: r[0], end: r[1]})
			}
			if f.capped {
				break
			}
		}
	}
	if len(f.hits) > 0 {
		f.reveal()
	}
	return true
}

// hitsFor returns the hits of one message and the index of the first one in the
// whole list. Hits are collected message by message, so a message's matches are
// contiguous.
func (f *findBar) hitsFor(msgID string) (int, []findHit) {
	start := -1
	for i, hit := range f.hits {
		if hit.msgID == msgID {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			return start, f.hits[start:i]
		}
	}
	if start >= 0 {
		return start, f.hits[start:]
	}
	return 0, nil
}

// step moves the cursor by delta hits, wrapping at both ends.
func (f *findBar) step(delta int) {
	n := len(f.hits)
	if n == 0 {
		return
	}
	f.cur = (f.cur + delta%n + n) % n
	f.reveal()
}

// reveal asks the next layout to scroll the current hit into view. The message
// is expanded first: a folded card has no body to highlight.
func (f *findBar) reveal() {
	a := f.reader.app
	hit := f.hits[f.cur]
	row := msgRow(a.msgs, hit.msgID)
	if row < 0 {
		return
	}
	if a.expanded == nil {
		a.expanded = map[string]bool{}
	}
	a.expanded[hit.msgID] = true
	pos := &f.reader.list.Position
	// A card that is already the first laid-out one keeps its offset: the
	// correction below measures from the top of the card anyway, and resetting
	// to zero would flash the top of a long message before the hit is reached.
	if pos.First != row {
		pos.First = row
		pos.Offset = 0
	}
	f.jump = true
	f.tries = 0
}

// counter is the label of the row: "3/12", "3/1000+" when the cap was hit, or
// the no-match note.
func (f *findBar) counter() string {
	switch {
	case f.query == "":
		return ""
	case len(f.hits) == 0:
		return i18n.T("reader.find.empty")
	case f.capped:
		return i18n.T("reader.find.countCapped", map[string]int{
			"Current": f.cur + 1,
			"Total":   len(f.hits),
		})
	default:
		return i18n.T("reader.find.count", map[string]int{
			"Current": f.cur + 1,
			"Total":   len(f.hits),
		})
	}
}

// Layout draws the find row: the icon, the query field, the counter and the
// three controls.
func (f *findBar) Layout(gtx layout.Context, a *App) layout.Dimensions {
	th := a.th
	h := gtx.Dp(findBarHeight)
	size := image.Pt(gtx.Constraints.Max.X, h)
	gtx.Constraints = layout.Exact(size)

	area := clip.Rect{Max: size}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface)
	area.Pop()
	line := clip.Rect{Min: image.Pt(0, h-1), Max: size}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBorder)
	line.Pop()

	submitted := false
	layout.Inset{Left: 14, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.glyphIn(gtx, iconSearch, 18, 18)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 10) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				submitted = handleEditorEvents(gtx, &f.ed)
				// The editor spans the whole cell, the same way the top bar
				// search field does; otherwise the hint floats in the middle.
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = 0
				es := th.editor(&f.ed, i18n.T("reader.find.hint"), textSecondary)
				es.Color, es.HintColor = colText, colFaint
				es.SelectionColor = colSelection
				return es.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt := f.counter()
				if txt == "" {
					return layout.Dimensions{}
				}
				col := colMuted
				if len(f.hits) == 0 {
					col = colFaint
				}
				return layout.Inset{Left: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, txt, textMeta, col, font.Normal, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 6) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				f.prev.setDisabled(len(f.hits) == 0)
				return f.prev.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				f.next.setDisabled(len(f.hits) == 0)
				return f.next.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return f.closeBtn.Layout(gtx, th)
			}),
		)
	})

	changed := f.sync(a.msgs)
	if submitted {
		f.step(1)
	}
	if changed || submitted {
		// The hits of this frame were only just computed: the cards collect
		// their geometry now and paint it on the next frame.
		invalidateAfterTap(gtx)
	}
	return layout.Dimensions{Size: size}
}

// afterLayout corrects the scroll offset once the target card has been laid
// out. layout.List can only start at a child (Position.First), never at a pixel
// of an unmeasured card, so the first part of a jump puts the card at the top
// edge and this second part moves the current hit to the wanted line.
func (f *findBar) afterLayout(gtx layout.Context) {
	if !f.open || !f.jump || len(f.hits) == 0 || f.reader.viewH <= 0 {
		return
	}
	a := f.reader.app
	hit := f.hits[f.cur]
	card, ok := f.reader.cards[hit.msgID]
	if !ok {
		f.jump = false
		return
	}
	row := msgRow(a.msgs, hit.msgID)
	pos := &f.reader.list.Position
	if row < 0 || pos.First != row {
		// The list pulled the position back, or the user scrolled away: the
		// jump is no longer the thing being looked at.
		f.jump = false
		return
	}
	// The card is the first visible child, so the list laid it out this frame
	// and its rectangles belong to the current body and width. Which of them
	// is the current hit is read from the cursor now, not from collect time,
	// so next/previous re-scrolls immediately.
	r, ok := card.marks.current(f.cur)
	if !ok {
		// The hit has no geometry yet (the card just unfolded, or the hit was
		// collected against an older body): wait a couple of frames.
		if f.tries++; f.tries > 3 {
			f.jump = false
		}
		return
	}
	bodyY := gtx.Dp(cardInsetTop) + card.headH
	screenY := -pos.Offset + bodyY + r.Min.Y
	want := f.reader.viewH / findRevealDivisor
	delta := screenY - want
	if delta > -gtx.Dp(2) && delta < gtx.Dp(2) {
		f.jump = false
		return
	}
	if f.tries >= 3 {
		f.jump = false
		return
	}
	f.tries++
	off := pos.Offset + delta
	if off < 0 {
		off = 0
	}
	if card.height > f.reader.viewH && off > card.height-f.reader.viewH {
		off = card.height - f.reader.viewH
	}
	if off == pos.Offset {
		f.jump = false
		return
	}
	pos.Offset = off
	invalidateAfterTap(gtx)
}

// plainBody is the text the reader shows for a message. The find offsets are
// rune offsets into exactly this string, which is also what the Selectable was
// given, so a hit maps straight onto glyphs.
func plainBody(msg *mail.Message) string {
	return strings.TrimSpace(msg.TextBody)
}

// msgRow returns the index of the message with the given ID in the conversation,
// or -1 when it is not there (the thread was refreshed under the find).
func msgRow(msgs []*mail.Message, id string) int {
	for i, msg := range msgs {
		if msg.ID == id {
			return i
		}
	}
	return -1
}

// messageSignature changes whenever the conversation the hits describe does:
// another thread, a refresh, or an edited body.
func messageSignature(msgs []*mail.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		b.WriteString(msg.ID)
		b.WriteByte(0)
		b.WriteString(strconv.Itoa(len(msg.TextBody)))
		b.WriteByte(0)
	}
	return b.String()
}

// findRanges returns the non-overlapping rune ranges of query in body, matched
// without regard to case. A full result means there may be more than limit
// matches; the caller reads that as the cap.
func findRanges(body, query string, limit int) [][2]int {
	if limit <= 0 || body == "" || query == "" {
		return nil
	}
	q := foldCase(query)
	text := foldCase(body)
	if len(q) == 0 || len(text) < len(q) {
		return nil
	}
	out := make([][2]int, 0, 8)
	for i := 0; i+len(q) <= len(text); {
		if text[i] != q[0] {
			i++
			continue
		}
		if slices.Equal(text[i:i+len(q)], q) {
			out = append(out, [2]int{i, i + len(q)})
			if len(out) == limit {
				return out
			}
			i += len(q)
			continue
		}
		i++
	}
	return out
}

// foldCase lowercases text rune by rune. Lowercasing the whole string is not
// enough: a mapping that changes the byte length would shift every rune offset
// the Selectable is asked about.
func foldCase(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, unicode.ToLower(r))
	}
	return out
}
