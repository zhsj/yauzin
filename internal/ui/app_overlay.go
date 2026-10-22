package ui

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

func (a *App) layout(gtx layout.Context) {
	gtx.Constraints.Min = gtx.Constraints.Max
	// Tips recorded last frame are stale; the hovered buttons queue fresh
	// ones while this frame is laid out.
	a.th.tips = a.th.tips[:0]
	// Consume the pointer moves first, so the window position the tipped
	// buttons read during this frame comes from the same batch of events as
	// the button-local positions they captured.
	a.readPointerEvents(gtx)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colPageBG)
	area.Pop()

	// The title bar stays above overlays so the window can always be moved and
	// closed; the mailbox is laid out below it. Overlays keep window
	// coordinates, which the menu anchors rely on.
	bar := 0
	if a.clientSide() {
		bar = gtx.Dp(a.headerHeight())
	}
	if bar > 0 && gtx.Constraints.Max.Y > bar {
		// The mailbox gets the window minus the bar, in both directions: a
		// Max that kept the full height made the mailbox half a title bar
		// taller than the space it is drawn in, which pushed the bottom of
		// the reader (the reply row) below the window edge.
		content := gtx
		area := image.Pt(content.Constraints.Max.X, content.Constraints.Max.Y-bar)
		content.Constraints.Min = area
		content.Constraints.Max = area
		off := op.Offset(image.Pt(0, bar)).Push(gtx.Ops)
		a.shell(content, false)
		off.Pop()

		a.overlays(gtx)

		title := gtx
		title.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, bar))
		a.top.Layout(title)
		a.registerPointerArea(gtx)
		return
	}
	a.stack(gtx, true)
	a.registerPointerArea(gtx)
}

// registerPointerArea declares the full-window pass-through pointer area. It
// runs after every other area of the frame: being the frontmost node and
// pass-through, the area never misses a move and never blocks the widgets
// below it. The moves it receives are consumed by readPointerEvents at the
// start of the next frame.
func (a *App) registerPointerArea(gtx layout.Context) {
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	event.Op(gtx.Ops, a)
	pass.Pop()
	area.Pop()
}

// readPointerEvents consumes the pointer moves routed to the full-window area
// and records the position in window coordinates. Tipped buttons combine it
// with the position their own area reports to locate themselves in the window
// and anchor their hover labels.
func (a *App) readPointerEvents(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: a, Kinds: pointer.Move})
		if !ok {
			break
		}
		if pe, ok := ev.(pointer.Event); ok {
			a.pointer = image.Pt(int(pe.Position.X+.5), int(pe.Position.Y+.5))
		}
	}
	a.th.pointer = a.pointer
}

// stack draws the mailbox with the full-window layers on top of it. withTop is
// false when the caller draws the title bar separately, above the overlays.
func (a *App) stack(gtx layout.Context, withTop bool) {
	layout.Stack{Alignment: layout.NW}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return a.shell(gtx, withTop)
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return a.overlays(gtx)
		}),
	)
}

// overlays draws the modal panel and the status bar above the mailbox.
func (a *App) overlays(gtx layout.Context) layout.Dimensions {
	if a.overlay != nil {
		return layout.Stack{Alignment: layout.NW}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return a.overlay.Layout(gtx, a)
			}),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return a.statusOverlay(gtx)
			}),
		)
	}
	return a.statusOverlay(gtx)
}

// statusOverlay draws the notification in the bottom left corner of the
// window, the way Gmail places its snackbar.
func (a *App) statusOverlay(gtx layout.Context) layout.Dimensions {
	if !a.status.visible() {
		return layout.Dimensions{}
	}
	// overlays() stacks this above the panel as an Expanded child, and Expanded
	// hands a child the largest sibling's size as its minimum. The panels report
	// their card size, so without pinning the constraints back to the window the
	// SW alignment would anchor to the card and lift the bar to the middle of
	// the sidebar.
	gtx.Constraints.Min = gtx.Constraints.Max
	return layout.Stack{Alignment: layout.SW}.Layout(gtx,
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 24, Bottom: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return a.status.Layout(gtx, a.th)
			})
		}),
	)
}

// statusDismissDelay is how long a status message stays on screen.
const statusDismissDelay = 4 * time.Second

func (a *App) notify(msg string, isErr bool) {
	if msg == "" {
		return
	}
	a.status.show(msg, isErr)
	gen := a.status.gen
	a.status.arm(statusDismissDelay, func() {
		a.post(func() { a.expireStatus(gen) })
	})
}

// expireStatus clears the bar unless a newer message replaced the one the
// timer was armed for. The timer may already have fired when the callback is
// queued, so the generation check is still what protects the newer message.
func (a *App) expireStatus(gen int) {
	if a.status.gen != gen {
		return
	}
	a.status.clear()
}

// drawTooltips replays the hover labels queued while laying out this frame.
// Only labels recorded in this frame exist in the queue, so no stale
// operations ever reach the GPU. Labels anchored outside the window are
// nudged back inside.
func (a *App) drawTooltips(gtx layout.Context) {
	for _, tip := range a.th.tips {
		anchor := tip.anchor
		if anchor.X+tip.size.X > gtx.Constraints.Max.X-8 {
			anchor.X = gtx.Constraints.Max.X - 8 - tip.size.X
		}
		if anchor.Y+tip.size.Y > gtx.Constraints.Max.Y-8 {
			anchor.Y = gtx.Constraints.Max.Y - 8 - tip.size.Y
		}
		off := op.Offset(anchor).Push(gtx.Ops)
		tip.call.Add(gtx.Ops)
		off.Pop()
	}
}

func (a *App) showOverlay(o overlay) {
	// The panel owns the keyboard: leave the search field before its editors
	// take over the focus.
	a.blurRequested = true
	a.discardCompose(o)
	a.closeOverlay()
	a.overlay = o
	switch v := o.(type) {
	case *composeView:
		a.compose = v
	case *settingsView:
		a.settings = v
	case *menuOverlay:
		a.menu = v
	case *accountDialog:
		a.accountAdd = v
	}
}

// closeOverlay releases the resources the current panel holds. The sign-in
// dialog owns a pending authorization context that has to be cancelled.
func (a *App) closeOverlay() {
	if a.accountAdd != nil {
		a.accountAdd.abort()
		a.accountAdd = nil
	}
	a.compose = nil
	a.settings = nil
	a.menu = nil
}

func (a *App) hideOverlay() {
	// The mailbox gets the keyboard back once the panel is gone.
	a.blurRequested = true
	a.discardCompose(nil)
	a.overlay = nil
	a.closeOverlay()
}

// discardCompose drops the staged attachment files of the draft that is being
// replaced by next, so a draft closed without sending leaves nothing behind.
func (a *App) discardCompose(next overlay) {
	if a.compose == nil {
		return
	}
	if c, ok := next.(*composeView); ok && c == a.compose {
		return
	}
	a.compose.discard()
}

// showMenu opens a dropdown whose top left corner is anchored under rect.
func (a *App) showMenu(rect image.Rectangle, items []menuItem) {
	if len(items) == 0 {
		return
	}
	a.showOverlay(newMenuOverlay(a, rect, items))
}

// showSettings opens the panel on its first page, the way the gear button
// does.
func (a *App) showSettings() {
	a.showSettingsPage(settingsTabGeneral)
}

// showSettingsPage opens the panel on the given tab, so an entry point such as
// the account menu can land where the user is heading.
func (a *App) showSettingsPage(page int) {
	s := newSettingsView(a)
	if page >= 0 && page < len(s.tabs) {
		s.page = page
	}
	a.showOverlay(s)
}

// backToAccounts leaves the add-account wizard for the settings panel it was
// opened from. The account has not been added, so the list the user came from
// is where they should land instead of the whole overlay disappearing.
func (a *App) backToAccounts() {
	a.showSettingsPage(settingsTabAccounts)
}

// showRelauth re-authorizes an existing Gmail account. The stored OAuth client
// is reused, so the only thing the user has to do is approve the consent
// screen again; the dialog opens straight on the step that shows the URL.
func (a *App) showRelauth(acc mail.Account) {
	if a.accountStore == nil {
		a.notify(i18n.T("settings.accountSignInFailed"), true)
		return
	}
	client, ok := a.accountStore.Credentials(mail.KindGmail)
	if !ok || client.ClientID == "" || client.ClientSecret == "" {
		a.notify(i18n.T("settings.accountSignInFailed"), true)
		return
	}
	d := newAccountDialog(a)
	d.relogin = &acc
	if entry, ok := a.accountStore.Entry(acc.ID); ok {
		d.useProxy = entry.UseProxy
	}
	d.clientID.SetText(client.ClientID)
	d.clientSecret.SetText(client.ClientSecret)
	d.step = accountStepAuth
	a.showOverlay(d)
	d.start()
}

func (a *App) showAccountMenu() {
	items := make([]menuItem, 0, len(a.accounts)+1)
	for _, acc := range a.accounts {
		id := acc.ID
		name := acc.DisplayName()
		items = append(items, menuItem{
			Label:   name,
			Avatar:  name,
			Checked: id == a.activeAcc,
			OnTap:   func() { a.switchAccount(id) },
		})
	}
	items = append(items, menuItem{
		Label: i18n.T("settings.manageAccounts"),
		Icon:  iconSettings,
		OnTap: func() { a.showSettingsPage(settingsTabAccounts) },
	})
	a.showMenu(a.top.menuAnchor, items)
}

func (a *App) openCompose(mode composeMode, msg *mail.Message) {
	a.showOverlay(newComposeView(a, mode, msg))
}
