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
// the configured accounts and an "add account" placeholder, Shortcuts the
// read-only table of the keys in shortcutTable.
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
	decoRow  *settingsRow
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
	// action is a trailing icon button for rows that are informational but own
	// one operation, such as re-authorizing an account.
	action *iconButton
	// remove is the two-step delete control of an account row. It sits after
	// action, so the destructive operation stays the rightmost one.
	remove *rowRemove
	onTap  func()
}

// rowRemove is the two-step delete control at the end of an account row. The
// trash button only arms it; the danger label that replaces the button in place
// performs the delete, so a stray click cannot drop an account. The armed state
// belongs to the panel: switching pages disarms it, and rebuilding the panel
// drops it with the old rows.
type rowRemove struct {
	trash    *iconButton
	confirm  widget.Clickable
	armed    bool
	onDelete func()
}

func newRowRemove(onDelete func()) *rowRemove {
	r := &rowRemove{onDelete: onDelete}
	r.trash = newIconButton(iconDelete, 28, func() { r.armed = true })
	r.trash.setTip(i18n.T("settings.accountDelete"))
	return r
}

func (r *rowRemove) disarm() { r.armed = false }

func (r *rowRemove) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if !r.armed {
		return r.trash.Layout(gtx, th)
	}
	if r.confirm.Clicked(gtx) {
		r.armed = false
		if r.onDelete != nil {
			r.onDelete()
		}
		invalidateAfterTap(gtx)
	}
	return r.confirm.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// The label is measured first so the button hugs it, and the fixed
		// height keeps the row from jumping when the trash gives way to it.
		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
		macro := op.Record(gtx.Ops)
		label := th.text(measure, i18n.T("settings.accountDeleteConfirm"), 12, colDanger, font.Medium, 1)
		call := macro.Stop()

		pad := gtx.Dp(10)
		size := image.Pt(label.Size.X+2*pad, gtx.Dp(28))
		gtx.Constraints = layout.Exact(size)
		th.cursor(gtx, pointer.CursorPointer, size)
		fill := colSurface
		if r.confirm.Hovered() {
			fill = colDangerSoft
		}
		strokeRect(gtx, fill, colDanger, 6, 1)
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return label
		})
		return layout.Dimensions{Size: size}
	})
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

	// Boolean settings render as switch rows; switching off falls back to
	// the system behaviour on both. The explanation sits on the row itself so
	// it never turns into a paragraph floating between two groups.
	deco := &settingsRow{
		title:     i18n.T("settings.decorationSwitch"),
		subtitle:  i18n.T("settings.decorationHint"),
		switchCtl: newToggleSwitch(a.csd, nil),
	}
	deco.onTap = func() {
		a.setClientDecorations(!a.csd)
		// The row is the hit target, so the switch never flips on its own:
		// mirror the state that actually took effect, which also covers a
		// setter that returned early.
		deco.switchCtl.on = a.csd
	}
	s.decoRow = deco

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

	for _, acc := range a.accounts {
		account := acc
		subtitle := ""
		if account.Email != "" && account.Email != account.DisplayName() {
			subtitle = account.Email
		}
		// Account rows are informational: which mailbox is in use is answered
		// by the check mark of the top bar's account menu, not here, so the
		// rows stay plain and a click on them does nothing.
		row := &settingsRow{
			lead:     &account,
			title:    account.DisplayName(),
			subtitle: subtitle,
		}
		// Only a real Gmail account has an OAuth grant to refresh; the demo
		// mailbox of the mock backend has nothing to sign in to.
		if strings.HasPrefix(account.ID, "gmail:") {
			target := account
			row.action = newIconButton(iconRefresh, 28, func() { a.showRelauth(target) })
			row.action.setTip(i18n.T("settings.accountRelogin"))
		}
		// Only a stored account can be deleted: the demo mailbox lives in
		// memory and would come back on the next start.
		if a.accountStore != nil && account.ID != demoAccountID {
			target := account
			row.remove = newRowRemove(func() { a.removeAccount(target) })
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
	return s
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

// disarmRemove cancels a pending delete confirmation. Leaving the accounts
// page must not keep a row one click away from being deleted.
func (s *settingsView) disarmRemove() {
	for _, row := range s.accRows {
		if row.remove != nil {
			row.remove.disarm()
		}
	}
}

func (s *settingsView) tab(gtx layout.Context, th *theme, i int) layout.Dimensions {
	t := &s.tabs[i]
	if t.click.Clicked(gtx) && s.page != i {
		s.page = i
		s.disarmRemove()
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
	items = append(items, func(gtx layout.Context) layout.Dimensions {
		return s.decoRow.Layout(gtx, th)
	})
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
	if r.click.Clicked(gtx) && r.interactive() {
		r.onTap()
		invalidateAfterTap(gtx)
	}
	return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		w := gtx.Constraints.Max.X
		if w <= 0 {
			w = gtx.Constraints.Min.X
		}
		// Measure the content first: a subtitle may wrap to a second line and
		// the row has to grow with it instead of clipping it. The height is
		// then clamped to the incoming maximum, so a caller that hands the row
		// an exact cell (the tests do) still gets that cell back.
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
		if r.action != nil {
			action := r.action
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return action.Layout(gtx, th) }),
			)
		}
		if r.remove != nil {
			remove := r.remove
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return remove.Layout(gtx, th) }),
			)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}
