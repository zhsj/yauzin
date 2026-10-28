package ui

import (
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/logging"
	"github.com/zhsj/yauzin/internal/mail"
)

// settingsView is the modal panel opened from the gear button. It has three
// pages picked with the underlined tabs under the title: General holds the
// appearance card (language, client-side title bar, performance monitor), the
// network card (proxy address) and the logging card (console level), Accounts
// the configured accounts as an accordion (one account opens its proxy switch,
// re-login and delete rows) plus an "add account" row, Shortcuts the read-only
// table of the keys in shortcutTable.
type settingsView struct {
	app     *App
	blocker *tapBlocker
	close   *iconButton
	tabs    []settingsTab
	page    int
	// langRow shows the current language and opens the candidates below it:
	// langRows are only laid out while langOpen, so the list never pushes the
	// switches down permanently.
	langRow  *settingsRow
	langRows []*settingsRow
	langOpen bool
	// logRow is the console level selector. It expands in place like the
	// language row, for the same reason: a menu would close the panel.
	logRow   *settingsRow
	logRows  []*settingsRow
	logOpen  bool
	perfRows []*settingsRow
	accRows  []*settingsRow
	// smtpRows are the sending accounts: they have no mailbox, so they get a
	// group of their own instead of joining the account accordion.
	smtpRows []*settingsRow
	// expanded is the account whose details are open: the accounts page is an
	// accordion, so at most one account is expanded at a time. confirmDelete
	// names the account whose delete row a first click has armed.
	expanded      string
	confirmDelete string
	// The network section: one proxy address shared by the accounts that opt
	// into it. proxySaved is the stored value, so the save button can stay
	// inactive until the field differs from it; proxyErr holds the form
	// message of the last save attempt.
	proxy      *widget.Editor
	proxyBox   widget.Clickable
	proxySave  *pillButton
	proxyErr   string
	proxySaved string
	list       layout.List
}

// Indices of settingsView.tabs, used to open the panel on a given page.
const (
	settingsTabGeneral = iota
	settingsTabAccounts
	settingsTabShortcuts
)

// settingsTab is one tab of the page switcher. The order of settingsView.tabs
// has to match the settingsTab* indices.
type settingsTab struct {
	click widget.Clickable
	label string
}

type settingsRow struct {
	click    widget.Clickable
	lead     *mail.Account
	icon     iconGlyph
	title    string
	subtitle string
	// value and valueIcon fill the trailing slot of rows that open a choice
	// instead of toggling: the selector row keeps the current choice readable
	// while the candidates are collapsed.
	value     string
	valueIcon iconGlyph
	active    bool
	// disabled dims the row and drops every interaction, for entries that are
	// listed but have no implementation yet.
	disabled bool
	// indent nests a row one level deeper, used by the language candidates
	// under their selector row.
	indent bool
	// switchCtl replaces the trailing check mark: switch rows are boolean,
	// and the row itself stays the hit target so a click anywhere toggles
	// the setting exactly once.
	switchCtl *toggleSwitch
	// details is the expandable block drawn under the row inside the same
	// card item, so the account and its options read as one entry instead of
	// a stack of sibling rows.
	details rowDetails
	// syncState runs at the start of every layout and mirrors the state the
	// row displays from the place that owns it (the store, the app), so a
	// failed write or an outside change shows up on the next frame instead of
	// leaving a stale switch behind.
	syncState func()
	onTap     func()
}

// logLevels lists the selectable console levels from quietest to noisiest,
// paired with the i18n key of their label.
var logLevels = []struct {
	name  string
	label string
}{
	{logging.LevelError, "settings.logLevelError"},
	{logging.LevelWarn, "settings.logLevelWarn"},
	{logging.LevelInfo, "settings.logLevelInfo"},
	{logging.LevelDebug, "settings.logLevelDebug"},
}

// logLevelLabel translates the label of a normalized level name.
func logLevelLabel(name string) string {
	for _, l := range logLevels {
		if l.name == name {
			return i18n.T(l.label)
		}
	}
	return i18n.T("settings.logLevelInfo")
}

// rowDetails is the block a row expands into. The account rows and the sending
// account rows draw different content through the same slot, so the card and
// the accordion stay unaware of what is inside.
type rowDetails interface {
	Layout(layout.Context, *theme) layout.Dimensions
}

// accountDetails is the expandable block of one account. It is painted on a
// tinted sub-card so the proxy switch and the secondary actions read as one
// group instead of sibling rows, and the delete confirmation replaces the
// action line in place instead of adding another row.
type accountDetails struct {
	proxy   *settingsRow
	relogin *outlineButton
	remove  *outlineButton
	cancel  *outlineButton
	confirm *outlineButton
	armed   bool
}

// newAccountDetails builds the block of an expandable account. Only a stored
// Gmail account reaches this point (see settingsView.expandable), so the proxy
// switch and the delete links always have an entry behind them.
func newAccountDetails(a *App, s *settingsView, acc mail.Account) *accountDetails {
	d := &accountDetails{
		proxy:   newAccountProxyRow(a, acc),
		relogin: newOutlineButton(i18n.T("settings.accountRelogin"), func() { a.showRelauth(acc) }),
		remove:  newOutlineButton(i18n.T("settings.accountDelete"), nil),
		cancel:  newOutlineButton(i18n.T("settings.cancel"), nil),
		confirm: newOutlineButton(i18n.T("settings.accountDeleteConfirm"), func() { a.removeAccount(acc) }),
		armed:   s.confirmDelete == acc.ID,
	}
	d.remove.danger = true
	d.confirm.danger = true
	// The armed state belongs to the panel, not to the block: the links rebuild
	// the rows so the visible block and the stored state never disagree.
	d.remove.onTap = func() {
		s.confirmDelete = acc.ID
		s.buildAccountRows(a)
	}
	d.cancel.onTap = func() {
		s.confirmDelete = ""
		s.buildAccountRows(a)
	}
	return d
}

func (d *accountDetails) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	return tintedBlock(gtx, func(gtx layout.Context) layout.Dimensions {
		return d.content(gtx, th)
	})
}

// tintedBlock paints content on the recessed sub-card of an expanded row. It
// measures the content first: a hint may wrap, and the tint has to cover
// exactly what the content took.
func tintedBlock(gtx layout.Context, content func(layout.Context) layout.Dimensions) layout.Dimensions {
	return layout.Inset{Left: 12, Right: 12, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		w := gtx.Constraints.Max.X
		if w <= 0 {
			w = gtx.Constraints.Min.X
		}
		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(w, 1<<16)}
		macro := op.Record(gtx.Ops)
		measured := content(measure)
		call := macro.Stop()

		size := image.Pt(w, measured.Size.Y)
		gtx.Constraints = layout.Exact(size)
		rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: 8, NE: 8, SE: 8, SW: 8}
		area := rr.Push(gtx.Ops)
		paint.Fill(gtx.Ops, colPageBG)
		area.Pop()
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}

func (d *accountDetails) content(gtx layout.Context, th *theme) layout.Dimensions {
	return column(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.proxy.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return d.actions(gtx, th)
		}),
	)
}

// actions is the second line of the sub-card: the secondary links on the right,
// or the delete confirmation that replaces them in place.
func (d *accountDetails) actions(gtx layout.Context, th *theme) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 12, Bottom: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if d.armed {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, i18n.T("settings.accountDeletePrompt"), 12, colDanger, font.Normal, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.cancel.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.confirm.Layout(gtx, th) }),
			)
		}
		// SpaceStart keeps the leftover space in front, so the links sit on the
		// right edge of the sub-card like the other trailing actions.
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceStart}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.relogin.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.remove.Layout(gtx, th) }),
		)
	})
}

// newAccountProxyRow is the per-account proxy choice inside the expandable
// block of the accounts page. It is an ordinary switch row: the row itself is
// the hit target, while the switch only displays the stored value, like the
// other boolean rows of the panel.
func newAccountProxyRow(a *App, acc mail.Account) *settingsRow {
	row := &settingsRow{
		title:     i18n.T("settings.accountUseProxy"),
		switchCtl: newToggleSwitch(false, nil),
	}
	row.onTap = func() { a.setAccountProxy(acc, !row.switchCtl.on) }
	row.syncState = func() {
		on := false
		if a.accountStore != nil {
			if entry, ok := a.accountStore.Entry(acc.ID); ok {
				on = entry.UseProxy
			}
		}
		row.switchCtl.on = on
		// Without an address there is nothing to connect through: the row
		// stays visible so the option can be found, but it cannot be switched.
		if strings.TrimSpace(a.proxy) == "" {
			row.disabled = true
			row.subtitle = i18n.T("settings.proxyUnset")
			return
		}
		row.disabled = false
		row.subtitle = ""
		if on {
			row.subtitle = a.proxy
		}
	}
	return row
}

func newSettingsView(a *App) *settingsView {
	s := &settingsView{
		app:     a,
		blocker: newTapBlocker(colOverlay, a.hideOverlay),
		close:   newIconButton(iconClose, 32, a.hideOverlay),
	}
	s.list.Axis = layout.Vertical

	s.tabs = []settingsTab{
		{label: i18n.T("settings.tabGeneral")},
		{label: i18n.T("settings.tabAccounts")},
		{label: i18n.T("settings.tabShortcuts")},
	}

	// The languages are a collapsed choice rather than a permanent list: the
	// selector row shows the active one and expands in place, because a
	// dropdown overlay would replace this panel (App.showOverlay clears
	// a.settings and the menu closes the whole overlay).
	activeLang := i18n.Language()
	activeName := activeLang
	for _, loc := range i18n.Languages() {
		tag, label := loc.Tag, loc.Name
		if tag == activeLang {
			activeName = label
		}
		s.langRows = append(s.langRows, &settingsRow{
			title:  label,
			active: tag == activeLang,
			indent: true,
			onTap: func() {
				a.setLanguage(tag)
			},
		})
	}
	s.langRow = &settingsRow{
		title:     i18n.T("settings.language"),
		value:     activeName,
		valueIcon: glyphExpandMore,
		onTap: func() {
			s.langOpen = !s.langOpen
		},
	}

	// The log level behaves like the language choice, except that it takes
	// effect in place: the panel is not rebuilt, so the expanded list only has
	// to collapse and mirror the level that actually stuck.
	for i := range logLevels {
		level := logLevels[i]
		s.logRows = append(s.logRows, &settingsRow{
			title:  i18n.T(level.label),
			active: level.name == a.logLevel,
			indent: true,
			onTap: func() {
				a.setLogLevel(level.name)
				s.logOpen = false
				s.logRow.value = logLevelLabel(a.logLevel)
				for j, row := range s.logRows {
					row.active = logLevels[j].name == a.logLevel
				}
			},
		})
	}
	s.logRow = &settingsRow{
		title: i18n.T("settings.logLevel"),
		value: logLevelLabel(a.logLevel),
		onTap: func() {
			s.logOpen = !s.logOpen
		},
	}

	// Boolean settings render as switch rows, with the explanation on the row
	// itself so it never turns into a paragraph floating between two groups.
	perf := &settingsRow{
		title:     i18n.T("settings.perfMonitor"),
		subtitle:  i18n.T("settings.perfHint"),
		switchCtl: newToggleSwitch(a.perf.isVisible(), nil),
	}
	perf.onTap = func() {
		a.setPerfMonitor(!a.perf.isVisible())
		perf.switchCtl.on = a.perf.isVisible()
	}
	s.perfRows = []*settingsRow{perf}

	s.proxy = &widget.Editor{SingleLine: true}
	s.proxy.SetText(a.proxy)
	s.proxySaved = strings.TrimSpace(a.proxy)
	s.proxySave = newPillButton(i18n.T("settings.save"), icon{}, true, func() {
		raw := strings.TrimSpace(s.proxy.Text())
		if err := a.setProxy(raw); err != nil {
			s.proxyErr = i18n.T("settings.proxyInvalid")
			return
		}
		s.proxyErr = ""
		s.proxySaved = raw
	})

	s.buildAccountRows(a)
	s.buildSMTPRows(a)
	return s
}

// buildAccountRows rebuilds the card of the accounts page. The rows are one
// flat list, so the details of the expanded account are neighbours of its own
// account row and the card draws its separators without extra glue.
func (s *settingsView) buildAccountRows(a *App) {
	s.accRows = nil
	for _, acc := range a.accounts {
		account := acc
		subtitle := ""
		if account.Email != "" && account.Email != account.DisplayName() {
			subtitle = account.Email
		}
		canOpen := s.expandable(a, account)
		open := canOpen && s.expanded == account.ID
		// Which mailbox is in use is answered by the check mark of the top
		// bar's account menu, not here: the row opens the account's details
		// and never switches the mailbox.
		row := &settingsRow{
			lead:     &account,
			title:    account.DisplayName(),
			subtitle: subtitle,
		}
		if canOpen {
			row.valueIcon = glyphExpandMore
			if open {
				row.valueIcon = glyphExpandLess
			}
			row.onTap = func() {
				s.confirmDelete = ""
				if s.expanded == account.ID {
					s.expanded = ""
				} else {
					s.expanded = account.ID
				}
				s.buildAccountRows(a)
			}
		}
		if open {
			row.details = newAccountDetails(a, s, account)
		}
		s.accRows = append(s.accRows, row)
	}
	s.accRows = append(s.accRows, &settingsRow{
		icon:  glyphPlus,
		title: i18n.T("settings.addAccount"),
		onTap: func() {
			a.showOverlay(newAccountDialog(a))
		},
	})
}

// expandable reports whether an account has details worth opening. Only a
// stored Gmail account does: the demo mailbox has neither a proxy choice nor
// a token to refresh or delete.
func (s *settingsView) expandable(a *App, acc mail.Account) bool {
	if a.accountStore == nil || !strings.HasPrefix(acc.ID, "gmail:") {
		return false
	}
	_, ok := a.accountStore.Entry(acc.ID)
	return ok
}

func (s *settingsView) Layout(gtx layout.Context, a *App) layout.Dimensions {
	_ = s.blocker.Layout(gtx)

	size := panelSize(gtx, image.Pt(gtx.Dp(560), gtx.Dp(560)), image.Pt(320, 240))
	origin := image.Pt(
		(gtx.Constraints.Max.X-size.X)/2,
		(gtx.Constraints.Max.Y-size.Y)/2,
	)

	cardGtx, pop := placeCard(gtx, size, origin)
	defer pop()
	drawCard(cardGtx, size, 16)

	th := a.th
	layout.Flex{Axis: layout.Vertical}.Layout(cardGtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 10, Bottom: 10, Left: 20, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.text(gtx, i18n.T("settings.title"), 18, colText, font.Bold, 1)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.close.Layout(gtx, th) }),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 2, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return s.tabsBar(gtx, th)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 14, Right: 14}.Layout(gtx, hline)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return s.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return s.pageContent(gtx)
			})
		}),
	)
	return layout.Dimensions{Size: size}
}

// tabsBar draws the Interface/Accounts tab row: plain text tabs, the active
// one bold with a primary-coloured underline on the hairline below.
func (s *settingsView) tabsBar(gtx layout.Context, th *theme) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(s.tabs))
	for i := range s.tabs {
		i := i
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.tab(gtx, th, i)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx, children...)
}

// disarmDelete cancels a pending delete confirmation. Leaving the accounts
// page must not keep a row one click away from being deleted; the expanded
// account stays open, only the armed row turns back into "delete".
func (s *settingsView) disarmDelete() {
	if s.confirmDelete == "" || s.app == nil {
		return
	}
	s.confirmDelete = ""
	s.buildAccountRows(s.app)
}

func (s *settingsView) tab(gtx layout.Context, th *theme, i int) layout.Dimensions {
	t := &s.tabs[i]
	if t.click.Clicked(gtx) && s.page != i {
		s.page = i
		s.disarmDelete()
		invalidateAfterTap(gtx)
	}
	return t.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(gtx.Dp(72), gtx.Dp(40))
		gtx.Constraints = layout.Exact(size)
		th.cursor(gtx, pointer.CursorPointer, size)
		selected := s.page == i
		if !selected && t.click.Hovered() {
			fillRect(gtx, colHover, 8)
		}
		col, weight := colMuted, font.Normal
		if selected {
			col, weight = colText, font.Bold
		}
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.text(gtx, t.label, 14, col, weight, 1)
		})
		if selected {
			// Tab indicator: a primary bar resting on the hairline below.
			barH := gtx.Dp(3)
			inset := gtx.Dp(10)
			bar := clip.Rect{
				Min: image.Pt(inset, size.Y-barH),
				Max: image.Pt(size.X-inset, size.Y),
			}.Push(gtx.Ops)
			paint.Fill(gtx.Ops, colPrimary)
			bar.Pop()
		}
		return layout.Dimensions{Size: size}
	})
}

// pageContent lays out the sections of the current page.
func (s *settingsView) pageContent(gtx layout.Context) layout.Dimensions {
	th := s.app.th
	var children []layout.FlexChild
	switch s.page {
	case settingsTabGeneral:
		children = append(children,
			layout.Rigid(sectionHeading(th, i18n.T("settings.appearance"))),
			layout.Rigid(s.appearanceCard(th)),
			layout.Rigid(sectionHeading(th, i18n.T("settings.network"))),
			layout.Rigid(settingsCard([]layout.Widget{s.proxySection(th)})),
			layout.Rigid(sectionHeading(th, i18n.T("settings.logging"))),
			layout.Rigid(s.loggingCard(th)),
			layout.Rigid(pageBottom),
		)
	case settingsTabShortcuts:
		sections := shortcutSections()
		for i := range sections {
			sec := sections[i]
			children = append(children,
				layout.Rigid(sectionHeading(th, i18n.T(sec.group.sectionID()))),
				layout.Rigid(settingsCard([]layout.Widget{shortcutRows(sec.rows, th)})),
			)
		}
		children = append(children,
			layout.Rigid(sectionHint(th, i18n.T("shortcut.hint"))),
			layout.Rigid(pageBottom),
		)
	default:
		// settingsTabAccounts, and the fallback for an index the tabs do not
		// offer.
		children = append(children,
			layout.Rigid(sectionHeading(th, i18n.T("settings.accounts"))),
			layout.Rigid(settingsCard(rowWidgets(s.accRows, th))),
			// Sending accounts are not mailboxes: they get their own group so
			// the account accordion stays about what can be read.
			layout.Rigid(sectionHeading(th, i18n.T("settings.sendAccounts"))),
			layout.Rigid(sectionHint(th, i18n.T("settings.sendAccountsHint"))),
			layout.Rigid(settingsCard(rowWidgets(s.smtpRows, th))),
			layout.Rigid(pageBottom),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// appearanceCard groups the interface choices: the language selector (and its
// candidates while expanded), the client-side title bar and the performance
// monitor.
func (s *settingsView) appearanceCard(th *theme) layout.Widget {
	s.langRow.valueIcon = glyphExpandMore
	if s.langOpen {
		s.langRow.valueIcon = glyphExpandLess
	}
	items := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return s.langRow.Layout(gtx, th)
	}}
	if s.langOpen {
		items = append(items, rowWidgets(s.langRows, th)...)
	}
	items = append(items, rowWidgets(s.perfRows, th)...)
	return settingsCard(items)
}

// loggingCard groups the console level selector and, while expanded, its
// candidates. The selector carries the current level on the right, so the
// collapsed card still answers which level is active.
func (s *settingsView) loggingCard(th *theme) layout.Widget {
	s.logRow.valueIcon = glyphExpandMore
	if s.logOpen {
		s.logRow.valueIcon = glyphExpandLess
	}
	items := []layout.Widget{func(gtx layout.Context) layout.Dimensions {
		return s.logRow.Layout(gtx, th)
	}}
	if s.logOpen {
		items = append(items, rowWidgets(s.logRows, th)...)
	}
	return settingsCard(items)
}

// pageBottom closes the scroll content with breathing room under the last
// card, so the panel edge never cuts a row.
func pageBottom(gtx layout.Context) layout.Dimensions {
	return vgap(gtx, 16)
}

// sectionHeading is the small label above a group card. It lines up with the
// text inside the card: the page inset (16) plus the card inset (16).
func sectionHeading(th *theme, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 18, Bottom: 6, Left: 32, Right: 32}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.sectionLabel(gtx, label)
		})
	}
}

// sectionHint is the muted explanation that is not tied to a single row.
func sectionHint(th *theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 8, Bottom: 4, Left: 32, Right: 32}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.wrappedText(gtx, text, 12, colMuted, font.Normal)
		})
	}
}

// settingsCard draws a group of rows on a white card outlined by a hairline.
// The fill matches the panel, so the border and the separator between items
// are what mark the group; items are separated by a line that stops short of
// the card edges, and the first and last item get no line above or below them.
func settingsCard(items []layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			if w <= 0 {
				w = gtx.Constraints.Min.X
			}
			// Measure the rows first: the card has to know its height before it
			// can be painted behind them.
			measure := gtx
			measure.Constraints = layout.Constraints{Max: image.Pt(w, 1<<16)}
			macro := op.Record(gtx.Ops)
			dims := layout.Flex{Axis: layout.Vertical}.Layout(measure, cardChildren(items)...)
			call := macro.Stop()

			size := image.Pt(w, dims.Size.Y)
			if size.Y <= 0 {
				return layout.Dimensions{Size: size}
			}
			gtx.Constraints = layout.Exact(size)
			strokeRect(gtx, colSurface, colBorder, 10, 1)
			// Clip the rows to the card so a hovered first or last row cannot
			// paint its square corners over the rounded outline.
			rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: 10, NE: 10, SE: 10, SW: 10}
			area := rr.Push(gtx.Ops)
			call.Add(gtx.Ops)
			area.Pop()
			return layout.Dimensions{Size: size}
		})
	}
}

// cardChildren interleaves the rows with separators.
func cardChildren(items []layout.Widget) []layout.FlexChild {
	out := make([]layout.FlexChild, 0, len(items)*2)
	for i, item := range items {
		if i > 0 {
			out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 16}.Layout(gtx, hline)
			}))
		}
		w := item
		out = append(out, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return w(gtx)
		}))
	}
	return out
}

// rowWidgets turns settings rows into the items of a card.
func rowWidgets(list []*settingsRow, th *theme) []layout.Widget {
	out := make([]layout.Widget, 0, len(list))
	for _, row := range list {
		r := row
		out = append(out, func(gtx layout.Context) layout.Dimensions {
			return r.Layout(gtx, th)
		})
	}
	return out
}

// proxySection is the body of the network card: the address title with its
// explanation, the editor with the save button on one line, and the last
// validation error below them.
func (s *settingsView) proxySection(th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// The button only turns active once the field differs from the stored
		// address, so a stray click cannot rewrite settings with the same value.
		s.proxySave.disabled = strings.TrimSpace(s.proxy.Text()) == s.proxySaved
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: 12, Bottom: 2, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, i18n.T("settings.proxy"), 14, colText, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 16, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.wrappedText(gtx, i18n.T("settings.proxyHint"), 12, colMuted, font.Normal)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 16, Right: 16, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return th.boxedField(gtx, s.proxy, &s.proxyBox, i18n.T("settings.proxyPlaceholder"), 13, gtx.Dp(36))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return s.proxySave.Layout(gtx, th)
						}),
					)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if s.proxyErr == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Left: 16, Right: 16, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.wrappedText(gtx, s.proxyErr, 12, colDanger, font.Normal)
				})
			}),
		)
	}
}

// rows lays out a settings group without a card, one row per entry. The
// settings pages use rowWidgets with settingsCard; the add-account dialog,
// which is a card itself, uses the bare rows.
func rows(list []*settingsRow, th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(list))
		for _, row := range list {
			r := row
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return r.Layout(gtx, th)
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

// shortcutSection is one section of the shortcuts page: the group named by the
// heading and the rows listed under it.
type shortcutSection struct {
	group shortcutGroup
	rows  []shortcut
}

// shortcutSections splits shortcutTable into its display groups. The table is
// declared in display order, so one pass is enough and the page can never show
// an action the dispatch does not know about.
func shortcutSections() []shortcutSection {
	var out []shortcutSection
	for _, s := range shortcutTable {
		if len(out) == 0 || out[len(out)-1].group != s.group {
			out = append(out, shortcutSection{group: s.group})
		}
		out[len(out)-1].rows = append(out[len(out)-1].rows, s)
	}
	return out
}

// shortcutRows lays out the read-only rows of one shortcuts group.
func shortcutRows(list []shortcut, th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(list))
		for i := range list {
			sc := list[i]
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return shortcutRow(gtx, th, sc)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

// shortcutRow is one "action — keys" line. It has no Clickable: the page is a
// read-only reference, so rows have neither hover nor a hand cursor.
func shortcutRow(gtx layout.Context, th *theme, sc shortcut) layout.Dimensions {
	h := gtx.Dp(40)
	w := gtx.Constraints.Max.X
	if w <= 0 {
		w = gtx.Constraints.Min.X
	}
	size := image.Pt(w, h)
	gtx.Constraints = layout.Exact(size)
	layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			// The action takes the slack and truncates instead of pushing the
			// caps off the row when a translation is long.
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, i18n.T(sc.action), 14, colText, font.Normal, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return keyCaps(gtx, th, sc.caps)
			}),
		)
	})
	return layout.Dimensions{Size: size}
}

// keyCaps lays out the caps of one shortcut side by side.
func keyCaps(gtx layout.Context, th *theme, caps []string) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(caps)*2)
	for i := range caps {
		if i > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return hgap(gtx, 4)
			}))
		}
		label := caps[i]
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return keyCap(gtx, th, label)
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
}

// keyCap draws one key label in a rounded box sized to its text. The label is
// recorded unconstrained first so the box hugs it; a minimum width keeps the
// single-character caps from looking cramped.
func keyCap(gtx layout.Context, th *theme, label string) layout.Dimensions {
	measure := gtx
	measure.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
	macro := op.Record(gtx.Ops)
	dims := th.text(measure, label, 11, colMuted, font.Medium, 1)
	call := macro.Stop()

	pad := gtx.Dp(7)
	w := max(dims.Size.X+2*pad, gtx.Dp(24))
	h := gtx.Dp(22)
	size := image.Pt(w, h)
	gtx.Constraints = layout.Exact(size)
	strokeRect(gtx, colPageBG, colBorder, 5, 1)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		call.Add(gtx.Ops)
		return dims
	})
	return layout.Dimensions{Size: size}
}

// interactive reports whether the row has an action behind it. Rows without
// one are informational (the account list) and must not hint at a click.
func (r *settingsRow) interactive() bool {
	return !r.disabled && r.onTap != nil
}

func (r *settingsRow) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if r.syncState != nil {
		r.syncState()
	}
	if r.click.Clicked(gtx) && r.interactive() {
		r.onTap()
		invalidateAfterTap(gtx)
	}
	cell := func(gtx layout.Context) layout.Dimensions {
		return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			if w <= 0 {
				w = gtx.Constraints.Min.X
			}
			// Measure the content first: a subtitle may wrap to a second line
			// and the row has to grow with it instead of clipping it. The
			// height is then clamped to the incoming maximum, so a caller that
			// hands the row an exact cell (the tests do) still gets it back.
			measure := gtx
			measure.Constraints = layout.Constraints{Max: image.Pt(w, 1<<16)}
			macro := op.Record(gtx.Ops)
			content := r.content(measure, th)
			call := macro.Stop()

			h := content.Size.Y + 2*gtx.Dp(10)
			if min := gtx.Dp(46); h < min {
				h = min
			}
			if maxY := gtx.Constraints.Max.Y; maxY > 0 && h > maxY {
				h = maxY
			}
			size := image.Pt(w, h)
			if r.interactive() {
				th.cursor(gtx, pointer.CursorPointer, size)
				if r.click.Hovered() {
					rect := image.Rect(gtx.Dp(4), gtx.Dp(2), size.X-gtx.Dp(4), size.Y-gtx.Dp(2))
					if rect.Dx() > 0 && rect.Dy() > 0 {
						rr := clip.RRect{Rect: rect, NW: 8, NE: 8, SE: 8, SW: 8}
						hs := rr.Push(gtx.Ops)
						paint.Fill(gtx.Ops, colHover)
						hs.Pop()
					}
				}
			}
			gtx.Constraints = layout.Exact(size)
			off := op.Offset(image.Pt(0, (h-content.Size.Y)/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			off.Pop()
			return layout.Dimensions{Size: size}
		})
	}
	if r.details == nil {
		return cell(gtx)
	}
	// The details belong to this row: keeping both in one card item stops the
	// card from drawing a separator between the account and its options.
	return column(gtx,
		layout.Rigid(cell),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return r.details.Layout(gtx, th)
		}),
	)
}

// content is the row's inner layout: the optional leading avatar or glyph, the
// title with its optional subtitle, and the trailing slot (switch, current
// value with its indicator, or the check mark of an active entry).
func (r *settingsRow) content(gtx layout.Context, th *theme) layout.Dimensions {
	titleCol, subtitleCol, iconCol := colText, colMuted, colMuted
	if r.disabled {
		titleCol, subtitleCol, iconCol = colMuted, colFaint, colFaint
	}
	left := 16
	if r.indent {
		left = 32
	}
	return layout.Inset{Left: unit.Dp(left), Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, 6)
		if r.lead != nil {
			acc := *r.lead
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.avatar(gtx, acc.DisplayName(), 30)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			)
		} else if r.icon != 0 {
			ic := icon{glyph: r.icon, col: iconCol}
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.glyphIn(gtx, ic, 20, 24)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			)
		}
		weight := font.Normal
		if r.active {
			weight = font.Bold
		}
		texts := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, r.title, 14, titleCol, weight, 1)
			}),
		}
		if r.subtitle != "" {
			texts = append(texts,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 2) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// Two lines are enough for a short explanation; anything
					// longer belongs in the documentation, not the panel.
					return th.text(gtx, r.subtitle, 12, subtitleCol, font.Normal, 2)
				}),
			)
		}
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return column(gtx, texts...)
		}))
		switch {
		case r.switchCtl != nil:
			sw := r.switchCtl
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sw.Layout(gtx, th)
				}),
			)
		case r.value != "" || r.valueIcon != 0:
			val, glyph := r.value, r.valueIcon
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if val == "" {
								return layout.Dimensions{}
							}
							return th.text(gtx, val, 13, colMuted, font.Normal, 1)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if val == "" || glyph == 0 {
								return layout.Dimensions{}
							}
							return hgap(gtx, 4)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if glyph == 0 {
								return layout.Dimensions{}
							}
							return th.glyphIn(gtx, icon{glyph: glyph, col: iconCol}, 18, 20)
						}),
					)
				}),
			)
		case r.active:
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.glyphIn(gtx, iconCheckPrimary, 18, 20)
			}))
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}
