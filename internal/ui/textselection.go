package ui

import (
	"image"
	"image/color"
	"io"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
)

// textSelection is one run of selectable text in the reader: the selection
// state, the message or conversation it belongs to, and the pass-through
// pointer area that reports right clicks on it.
//
// The selection is rebuilt whenever the owner changes, so a fragment selected
// in one message never survives into the next one drawn at the same place.
type textSelection struct {
	owner string
	sel   widget.Selectable
	// pressed records a secondary-button press routed to this run during the
	// frame being laid out. Take consumes it.
	pressed bool
}

// Select returns the Selectable of owner. The highlight is painted even
// without the keyboard focus: opening the selection menu moves the focus away
// from the text, and the selected fragment has to stay visible behind it.
func (s *textSelection) Select(owner string) *widget.Selectable {
	if s.owner != owner {
		s.owner = owner
		s.sel = widget.Selectable{}
		s.pressed = false
	}
	s.sel.PaintSelectionWhenUnfocused = true
	return &s.sel
}

// Selected returns the text selected in owner, or "" when the caller holds no
// selection there. A card asks this before quoting a reply, when the layout of
// the frame that made the selection is long over.
func (s *textSelection) Selected(owner string) string {
	if s.owner != owner || s.sel.SelectionLen() == 0 {
		return ""
	}
	return s.sel.SelectedText()
}

// Wrap draws wrapped selectable text of owner and declares the pointer area
// that catches right clicks on it. lineHeight is the baseline distance; zero
// keeps Gio's default of 1.2 times the text size.
func (s *textSelection) Wrap(gtx layout.Context, th *theme, owner, txt string, size, lineHeight unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	sel := s.ready(gtx, owner)
	dims := th.selectableText(gtx, sel, txt, size, lineHeight, col, weight)
	s.area(gtx, dims.Size)
	return dims
}

// Line is Wrap for a single truncated line, the way the head of a card shows
// its sender.
func (s *textSelection) Line(gtx layout.Context, th *theme, owner, txt string, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	sel := s.ready(gtx, owner)
	dims := th.selectableLine(gtx, sel, txt, size, col, weight)
	s.area(gtx, dims.Size)
	return dims
}

// Take reports the selection a right click landed on during this frame and
// consumes the press.
func (s *textSelection) Take() (string, bool) {
	pressed := s.pressed
	s.pressed = false
	if !pressed || s.sel.SelectionLen() == 0 {
		return "", false
	}
	return s.sel.SelectedText(), true
}

// ready prepares the selection state of owner and drains the presses routed to
// this run since the last frame.
func (s *textSelection) ready(gtx layout.Context, owner string) *widget.Selectable {
	sel := s.Select(owner)
	s.pressed = false
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: s, Kinds: pointer.Press})
		if !ok {
			return sel
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		// Only a right click on something selected opens the menu: the entries
		// act on the selection, and there is nothing to offer without one.
		// Gio's click and drag gestures ignore non-primary buttons, so the
		// selection is untouched by this press.
		if pe.Buttons&pointer.ButtonSecondary == 0 || sel.SelectionLen() == 0 {
			continue
		}
		s.pressed = true
	}
}

// area declares the pointer area of the text, frontmost and pass-through. The
// pass-through op lets the selectable below keep its own click and drag
// gestures, while the press is still delivered to this tag.
func (s *textSelection) area(gtx layout.Context, size image.Point) {
	bounds := clip.Rect{Max: size}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, s)
	pass.Pop()
	bounds.Pop()
}

// showTextMenu opens the context menu of a text selection at the pointer,
// which is where the right click landed. The pointer is a better anchor than
// the text itself: a selected run may reach past the card that holds it.
func (a *App) showTextMenu(text string) {
	items := a.selectionMenuItems(text)
	if len(items) == 0 {
		return
	}
	p := a.pointer
	a.showMenu(image.Rect(p.X, p.Y, p.X+1, p.Y+1), menuAlignStart, items)
}

// selectionMenuItems lists what can be done with a selected fragment. The
// browser entry only appears for a whole http(s) URL: any other scheme is not
// something the platform opener should be handed.
func (a *App) selectionMenuItems(text string) []menuItem {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	items := []menuItem{{
		Label: i18n.T("reader.menu.copy"),
		OnTap: func() { a.copyText(text) },
	}}
	if link := webURL(text); link != "" {
		items = append(items, menuItem{
			Label: i18n.T("reader.menu.openLink"),
			OnTap: func() { a.openWebLink(link) },
		})
	}
	return items
}

// copyText stages a clipboard write for the next frame. The menu callback runs
// without a layout context, so the command is executed by flushClipboard.
func (a *App) copyText(text string) {
	if text == "" {
		return
	}
	a.clipPending = text
	a.invalidate()
}

// flushClipboard executes a clipboard write staged by copyText. It runs at the
// start of a frame, where a layout context is available.
func (a *App) flushClipboard(gtx layout.Context) {
	if a.clipPending == "" {
		return
	}
	text := a.clipPending
	a.clipPending = ""
	writeClipboard(gtx, text)
}

// writeClipboard puts text on the clipboard during the frame gtx belongs to.
func writeClipboard(gtx layout.Context, text string) {
	gtx.Execute(clipboard.WriteCmd{
		Type: "application/text",
		Data: io.NopCloser(strings.NewReader(text)),
	})
}

// openWebLink hands an http(s) address to the default browser.
func (a *App) openWebLink(link string) {
	if err := openPath(link); err != nil {
		logErr("open link", err)
		a.notify(i18n.T("toast.openFailed", map[string]string{"Error": err.Error()}), true)
	}
}
