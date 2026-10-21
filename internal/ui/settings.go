package ui

import (
	"image"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"yauzin/internal/i18n"
	"yauzin/internal/mail"
)

// settingsView is the modal panel opened from the gear button. It has three
// pages picked with the underlined tabs under the title: Interface holds
// the language list and the boolean switches, Accounts the configured
// accounts and an "add account" placeholder, Shortcuts the read-only table of
// the keys in shortcutTable.
type settingsView struct {
	app      *App
	blocker  *tapBlocker
	close    *iconButton
	tabs     []settingsTab
	page     int
	langRows []*settingsRow
	perfRows []*settingsRow
	accRows  []*settingsRow
	decoRow  *settingsRow
	list     layout.List
}

// settingsTab is one tab of the page switcher.
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
	active   bool
	// switchCtl replaces the trailing check mark: switch rows are boolean,
	// and the row itself stays the hit target so a click anywhere toggles
	// the setting exactly once.
	switchCtl *toggleSwitch
	onTap     func()
}

func newSettingsView(a *App) *settingsView {
	s := &settingsView{
		app:     a,
		blocker: newTapBlocker(colOverlay, a.hideOverlay),
		close:   newIconButton(iconClose, 32, a.hideOverlay),
	}
	s.list.Axis = layout.Vertical

	s.tabs = []settingsTab{
		{label: i18n.T("settings.tabInterface")},
		{label: i18n.T("settings.tabAccounts")},
		{label: i18n.T("settings.tabShortcuts")},
	}

	activeLang := i18n.Language()
	for _, loc := range i18n.Languages() {
		tag, label := loc.Tag, loc.Name
		s.langRows = append(s.langRows, &settingsRow{
			title:  label,
			active: tag == activeLang,
			onTap: func() {
				a.setLanguage(tag)
			},
		})
	}

	// Boolean settings render as switch rows; switching off falls back to
	// the system behaviour on both.
	s.decoRow = &settingsRow{
		title:     i18n.T("settings.decorationSwitch"),
		switchCtl: newToggleSwitch(a.csd, nil),
		onTap:     func() { a.setClientDecorations(!a.csd) },
	}

	s.perfRows = []*settingsRow{
		{
			title:     i18n.T("settings.perfMonitor"),
			switchCtl: newToggleSwitch(a.perf.isVisible(), nil),
			onTap:     func() { a.setPerfMonitor(!a.perf.isVisible()) },
		},
	}

	for _, acc := range a.accounts {
		account := acc
		subtitle := ""
		if account.Email != "" && account.Email != account.DisplayName() {
			subtitle = account.Email
		}
		s.accRows = append(s.accRows, &settingsRow{
			lead:     &account,
			title:    account.DisplayName(),
			subtitle: subtitle,
			active:   account.ID == a.activeAcc,
			onTap: func() {
				a.switchAccount(account.ID)
			},
		})
	}

	s.accRows = append(s.accRows, &settingsRow{
		icon:  glyphPlus,
		title: i18n.T("settings.addAccount"),
		onTap: func() {
			a.notify(i18n.T("settings.addAccountSoon"), false)
		},
	})
	return s
}

func (s *settingsView) Layout(gtx layout.Context, a *App) layout.Dimensions {
	_ = s.blocker.Layout(gtx)

	size := panelSize(gtx, image.Pt(gtx.Dp(520), gtx.Dp(560)), image.Pt(320, 240))
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

func (s *settingsView) tab(gtx layout.Context, th *theme, i int) layout.Dimensions {
	t := &s.tabs[i]
	if t.click.Clicked(gtx) && s.page != i {
		s.page = i
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
	case 0:
		children = append(children,
			layout.Rigid(sectionHeading(th, i18n.T("settings.language"))),
			layout.Rigid(rows(s.langRows, th)),
			layout.Rigid(sectionHeading(th, i18n.T("settings.decoration"))),
			layout.Rigid(rows([]*settingsRow{s.decoRow}, th)),
			layout.Rigid(sectionHint(th, i18n.T("settings.decorationHint"))),
			layout.Rigid(sectionHeading(th, i18n.T("settings.performance"))),
			layout.Rigid(rows(s.perfRows, th)),
			layout.Rigid(sectionHint(th, i18n.T("settings.perfHint"))),
		)
	case 2:
		sections := shortcutSections()
		for i := range sections {
			sec := sections[i]
			children = append(children,
				layout.Rigid(sectionHeading(th, i18n.T(sec.group.sectionID()))),
				layout.Rigid(shortcutRows(sec.rows, th)),
			)
		}
		children = append(children, layout.Rigid(sectionHint(th, i18n.T("shortcut.hint"))))
	default:
		children = append(children,
			layout.Rigid(sectionHeading(th, i18n.T("settings.accounts"))),
			layout.Rigid(rows(s.accRows, th)),
		)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// sectionHeading is the small label above a group of settings rows.
func sectionHeading(th *theme, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 4, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.sectionLabel(gtx, label)
		})
	}
}

// sectionHint is the muted explanation below a group of settings rows.
func sectionHint(th *theme, text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 4, Bottom: 10, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.wrappedText(gtx, text, 12, colMuted, font.Normal)
		})
	}
}

// rows lays out a settings group, one row per entry.
func rows(list []*settingsRow, th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(list))
		for _, row := range list {
			r := row
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 8, Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
	layout.Inset{Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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

func (r *settingsRow) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if r.click.Clicked(gtx) && r.onTap != nil {
		r.onTap()
		invalidateAfterTap(gtx)
	}
	return r.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(46)
		w := gtx.Constraints.Max.X
		if w <= 0 {
			w = gtx.Constraints.Min.X
		}
		size := image.Pt(w, h)
		th.cursor(gtx, pointer.CursorPointer, size)
		if r.click.Hovered() {
			rr := clip.RRect{Rect: image.Rectangle{Max: size}, NW: 8, NE: 8, SE: 8, SW: 8}
			hs := rr.Push(gtx.Ops)
			paint.Fill(gtx.Ops, colHover)
			hs.Pop()
		}
		gtx.Constraints = layout.Exact(size)
		layout.Inset{Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{}
			if r.lead != nil {
				acc := *r.lead
				children = append(children,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.avatar(gtx, acc.DisplayName(), 30)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
				)
			} else if r.icon != 0 {
				ic := icon{glyph: r.icon, col: colMuted}
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
					return th.text(gtx, r.title, 14, colText, weight, 1)
				}),
			}
			if r.subtitle != "" {
				texts = append(texts,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 1) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.text(gtx, r.subtitle, 12, colMuted, font.Normal, 1)
					}),
				)
			}
			children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return column(gtx, texts...)
			}))
			if r.switchCtl != nil {
				sw := r.switchCtl
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sw.Layout(gtx, th)
				}))
			} else if r.active {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.glyphIn(gtx, iconCheckPrimary, 18, 20)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
		})
		return layout.Dimensions{Size: size}
	})
}
