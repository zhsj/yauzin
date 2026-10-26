package ui

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/smtp"
)

// securityChoice is one encryption option of the SMTP form, paired with the
// port that mode conventionally uses. The order is the order of the form.
type securityChoice struct {
	value string
	label string
	port  string
}

// securityChoices lists the transport encryptions from the most common to the
// one that should stay an exception.
var securityChoices = []securityChoice{
	{value: string(smtp.SecuritySTARTTLS), label: "settings.smtpSecuritySTARTTLS", port: "587"},
	{value: string(smtp.SecurityTLS), label: "settings.smtpSecurityTLS", port: "465"},
	{value: string(smtp.SecurityNone), label: "settings.smtpSecurityNone", port: "25"},
}

// securityPort returns the conventional port of one mode, empty for an unknown
// one.
func securityPort(value string) string {
	for _, c := range securityChoices {
		if c.value == value {
			return c.port
		}
	}
	return ""
}

// securityLabel translates the label of one mode.
func securityLabel(value string) string {
	for _, c := range securityChoices {
		if c.value == value {
			return i18n.T(c.label)
		}
	}
	return value
}

// smtpDialog adds or edits one sending account. It only writes accounts.json:
// nothing is read through the server, so the mailbox list and the backends stay
// untouched.
type smtpDialog struct {
	app     *App
	blocker *tapBlocker
	close   *iconButton
	// edit is the entry being changed; nil while adding one.
	edit *accounts.Entry

	host     widget.Editor
	port     widget.Editor
	username widget.Editor
	password widget.Editor
	name     widget.Editor
	email    widget.Editor

	hostBox     widget.Clickable
	portBox     widget.Clickable
	usernameBox widget.Clickable
	passwordBox widget.Clickable
	nameBox     widget.Clickable
	emailBox    widget.Clickable

	security string
	secRow   *settingsRow
	secRows  []*settingsRow
	secOpen  bool

	bccSwitch   *formSwitch
	proxySwitch *formSwitch
	hasProxy    bool
	proxy       string

	err    string
	save   *pillButton
	cancel *pillButton
	list   layout.List
}

func newSMTPDialog(a *App, edit *accounts.Entry) *smtpDialog {
	d := &smtpDialog{
		app:      a,
		blocker:  newTapBlocker(colOverlay, a.backToAccounts),
		close:    newIconButton(iconClose, 32, a.backToAccounts),
		edit:     edit,
		proxy:    a.proxy,
		hasProxy: strings.TrimSpace(a.proxy) != "",
		security: string(smtp.SecuritySTARTTLS),
	}
	for _, ed := range []*widget.Editor{&d.host, &d.port, &d.username, &d.name, &d.email} {
		ed.SingleLine = true
	}
	d.port.Filter = "0123456789"
	d.password.SingleLine = true
	d.password.Mask = '•'

	// A configured proxy is the common reason to need one here, so it defaults
	// on the way the Gmail form does.
	useProxy := d.hasProxy
	bcc := true
	if edit != nil && edit.SMTP != nil {
		cfg := edit.SMTP
		d.host.SetText(cfg.Host)
		d.port.SetText(strconv.Itoa(cfg.Port))
		d.username.SetText(cfg.Username)
		d.password.SetText(cfg.Password)
		d.name.SetText(edit.Name)
		d.email.SetText(edit.Email)
		bcc = cfg.BccToAccount
		useProxy = edit.UseProxy
		if cfg.Security != "" {
			d.security = cfg.Security
		}
	} else {
		d.port.SetText(securityPort(d.security))
	}

	d.secRow = &settingsRow{
		title:     i18n.T("settings.smtpSecurity"),
		value:     securityLabel(d.security),
		valueIcon: glyphExpandMore,
		onTap:     func() { d.secOpen = !d.secOpen },
	}
	for _, c := range securityChoices {
		choice := c
		d.secRows = append(d.secRows, &settingsRow{
			title:  i18n.T(choice.label),
			active: choice.value == d.security,
			indent: true,
			onTap:  func() { d.setSecurity(choice.value) },
		})
	}
	d.bccSwitch = newFormSwitch(bcc, "settings.smtpBccTitle", "settings.smtpBccHint")
	proxySwitch := newFormSwitch(useProxy, "settings.accountUseProxy", "")
	if !d.hasProxy {
		proxySwitch.disabled = true
		proxySwitch.subtitle = i18n.T("settings.proxyUnset")
	}
	if useProxy && d.hasProxy {
		proxySwitch.subtitle = d.proxy
	}
	d.proxySwitch = proxySwitch

	d.cancel = newPillButton(i18n.T("settings.cancel"), icon{}, false, a.backToAccounts)
	label := i18n.T("settings.smtpAdd")
	if edit != nil {
		label = i18n.T("settings.save")
	}
	d.save = newPillButton(label, icon{}, true, d.submit)
	d.list.Axis = layout.Vertical
	return d
}

// defaultPortText fills the port field when it is empty or still carries the
// conventional port of another mode: a port the user typed is left alone.
func defaultPortText(d *smtpDialog) string {
	current := strings.TrimSpace(d.port.Text())
	if current == "" {
		return securityPort(d.security)
	}
	for _, c := range securityChoices {
		if c.port == current {
			return securityPort(d.security)
		}
	}
	return current
}

// setSecurity switches the encryption and, when the port is still one of the
// conventional ones, the port with it.
func (d *smtpDialog) setSecurity(value string) {
	d.secOpen = false
	if d.security == value {
		return
	}
	d.security = value
	for i, c := range securityChoices {
		d.secRows[i].active = c.value == value
	}
	d.secRow.value = securityLabel(value)
	if port := securityPort(value); port != "" && defaultPortText(d) != strings.TrimSpace(d.port.Text()) {
		d.port.SetText(port)
	}
}

// submit validates the form and writes the entry. Every failure stays in the
// dialog: a rejected write must not lose what the user typed.
func (d *smtpDialog) submit() {
	a := d.app
	if a.accountStore == nil {
		d.err = i18n.T("settings.smtpSaveFailed")
		return
	}
	entry, ok := d.entry()
	if !ok {
		return
	}
	var saveErr error
	if d.edit != nil && d.edit.ID != "" {
		saveErr = a.accountStore.Replace(d.edit.ID, entry)
	} else {
		saveErr = a.accountStore.Upsert(entry)
	}
	if saveErr != nil {
		logErr("save sending account", saveErr)
		d.err = i18n.T("settings.smtpSaveFailed")
		return
	}
	name := entry.Name
	if name == "" {
		name = entry.Email
	}
	toast := "toast.smtpAdded"
	if d.edit != nil {
		toast = "toast.smtpUpdated"
	}
	a.notify(i18n.T(toast, map[string]string{"Name": name}), false)
	a.backToAccounts()
}

// entry reads the form into a stored entry. It reports false and fills d.err
// when something is missing, so the caller never writes a half configuration.
func (d *smtpDialog) entry() (accounts.Entry, bool) {
	d.err = ""
	host := strings.TrimSpace(d.host.Text())
	if host == "" {
		d.err = i18n.T("settings.smtpHostMissing")
		return accounts.Entry{}, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(d.port.Text()))
	if err != nil || port < 1 || port > 65535 {
		d.err = i18n.T("settings.smtpPortInvalid")
		return accounts.Entry{}, false
	}
	addrs := mail.ParseAddressList(d.email.Text())
	if len(addrs) != 1 || !strings.Contains(addrs[0].Email, "@") {
		d.err = i18n.T("settings.smtpAddressInvalid")
		return accounts.Entry{}, false
	}
	address := addrs[0]
	// The ID follows the sender address, so two entries cannot claim the same
	// identity; saving it again replaces the stored one.
	id := string(mail.KindSMTP) + ":" + strings.ToLower(address.Email)
	return accounts.Entry{
		ID:       id,
		Kind:     mail.KindSMTP,
		Email:    address.Email,
		Name:     strings.TrimSpace(d.name.Text()),
		UseProxy: d.proxySwitch.on(),
		SMTP: &accounts.SMTPConfig{
			Host:         host,
			Port:         port,
			Username:     strings.TrimSpace(d.username.Text()),
			Password:     d.password.Text(),
			Security:     d.security,
			BccToAccount: d.bccSwitch.on(),
		},
	}, true
}

func (d *smtpDialog) Layout(gtx layout.Context, a *App) layout.Dimensions {
	_ = d.blocker.Layout(gtx)

	size := panelSize(gtx, image.Pt(gtx.Dp(560), gtx.Dp(600)), image.Pt(320, 240))
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
							return th.text(gtx, d.titleText(), 18, colText, font.Bold, 1)
						})
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.close.Layout(gtx, th) }),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 14, Right: 14}.Layout(gtx, hline)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return d.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
				return d.content(gtx, th)
			})
		}),
	)
	return layout.Dimensions{Size: size}
}

// titleText is the heading: adding a sending account or editing one.
func (d *smtpDialog) titleText() string {
	if d.edit != nil {
		return i18n.T("settings.smtpTitleEdit")
	}
	return i18n.T("settings.smtpTitleAdd")
}

func (d *smtpDialog) content(gtx layout.Context, th *theme) layout.Dimensions {
	d.secRow.valueIcon = glyphExpandMore
	if d.secOpen {
		d.secRow.valueIcon = glyphExpandLess
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.wrappedText(gtx, i18n.T("settings.smtpIntro"), 12, colMuted, font.Normal)
			})
		}),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpHost"), &d.host, &d.hostBox, i18n.T("settings.smtpHostHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpPort"), &d.port, &d.portBox, i18n.T("settings.smtpPortHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 12) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.secRow.Layout(gtx, th) }),
	}
	if d.secOpen {
		children = append(children, layout.Rigid(rows(d.secRows, th)))
	}
	children = append(children,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.security != string(smtp.SecurityNone) {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.wrappedText(gtx, i18n.T("settings.smtpPlainWarning"), 12, colDanger, font.Normal)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 12) }),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpUsername"), &d.username, &d.usernameBox, i18n.T("settings.smtpUsernameHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpPassword"), &d.password, &d.passwordBox, i18n.T("settings.smtpPasswordHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpSenderName"), &d.name, &d.nameBox, i18n.T("settings.smtpSenderNameHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(labelledField(th, i18n.T("settings.smtpSenderAddress"), &d.email, &d.emailBox, i18n.T("settings.smtpSenderAddressHint"))),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 14) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.bccSwitch.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 6) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.proxySwitch.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if d.err == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.wrappedText(gtx, d.err, 12, colDanger, font.Normal)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceEnd}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.cancel.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.save.Layout(gtx, th) }),
			)
		}),
	)
	return layout.Inset{Top: 8, Bottom: 14, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// buildSMTPRows lists the sending accounts for the accounts page. They are not
// mailboxes, so they get their own group instead of joining the account
// accordion, and the add row at the end opens the same form the wizard does.
func (s *settingsView) buildSMTPRows(a *App) {
	s.smtpRows = nil
	if a.accountStore != nil {
		for _, entry := range a.accountStore.SMTPEntries() {
			item := entry
			open := s.expanded == item.ID
			row := &settingsRow{
				icon:      glyphSend,
				title:     smtpEntryName(item),
				subtitle:  smtpEntrySubtitle(item),
				valueIcon: glyphExpandMore,
			}
			if open {
				row.valueIcon = glyphExpandLess
				row.details = newSMTPDetails(a, s, item)
			}
			row.onTap = func() {
				s.confirmDelete = ""
				if s.expanded == item.ID {
					s.expanded = ""
				} else {
					s.expanded = item.ID
				}
				s.buildSMTPRows(a)
			}
			s.smtpRows = append(s.smtpRows, row)
		}
	}
	s.smtpRows = append(s.smtpRows, &settingsRow{
		icon:  glyphPlus,
		title: i18n.T("settings.smtpAddRow"),
		onTap: func() { a.showOverlay(newSMTPDialog(a, nil)) },
	})
}

// smtpEntryName is what a sending account is called in the list: the display
// name when it has one, the address otherwise.
func smtpEntryName(entry accounts.Entry) string {
	if entry.Name != "" {
		return entry.Name
	}
	return entry.Email
}

// smtpEntrySubtitle is the address a sending account sends as, the way a
// mailbox row shows its address. It is skipped when the title already shows the
// address, so the row never repeats itself.
func smtpEntrySubtitle(entry accounts.Entry) string {
	if entry.Email == "" || entry.Email == smtpEntryName(entry) {
		return ""
	}
	return entry.Email
}

// smtpAccountDetails is the expandable block of one sending account: the two
// actions. It mirrors accountDetails, which cannot be reused because a sending
// account has neither a proxy switch of its own nor a sign-in to repeat.
type smtpAccountDetails struct {
	edit    *outlineButton
	remove  *outlineButton
	cancel  *outlineButton
	confirm *outlineButton
	armed   bool
}

func newSMTPDetails(a *App, s *settingsView, entry accounts.Entry) *smtpAccountDetails {
	d := &smtpAccountDetails{
		edit: newOutlineButton(i18n.T("settings.smtpEdit"), func() {
			item := entry
			a.showOverlay(newSMTPDialog(a, &item))
		}),
		remove:  newOutlineButton(i18n.T("settings.accountDelete"), nil),
		cancel:  newOutlineButton(i18n.T("settings.cancel"), nil),
		confirm: newOutlineButton(i18n.T("settings.accountDeleteConfirm"), func() { a.removeSMTPEntry(entry) }),
		armed:   s.confirmDelete == entry.ID,
	}
	d.remove.danger = true
	d.confirm.danger = true
	// The armed state belongs to the panel, not to the block: the links rebuild
	// the rows so the visible block and the stored state never disagree.
	d.remove.onTap = func() {
		s.confirmDelete = entry.ID
		s.buildSMTPRows(a)
	}
	d.cancel.onTap = func() {
		s.confirmDelete = ""
		s.buildSMTPRows(a)
	}
	return d
}

func (d *smtpAccountDetails) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	return tintedBlock(gtx, func(gtx layout.Context) layout.Dimensions {
		return d.actions(gtx, th)
	})
}

// actions is the second line of the sub-card: the edit and delete links, or the
// delete confirmation that replaces them in place.
func (d *smtpAccountDetails) actions(gtx layout.Context, th *theme) layout.Dimensions {
	return layout.Inset{Left: 16, Right: 12, Top: 10, Bottom: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		if d.armed {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, i18n.T("settings.smtpDeletePrompt"), 12, colDanger, font.Normal, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.cancel.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.confirm.Layout(gtx, th) }),
			)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceStart}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{} }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.edit.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.remove.Layout(gtx, th) }),
		)
	})
}

// labelledField is one labelled editor box, shared by the sign-in and SMTP
// forms so the two dialogs look the same.
func labelledField(th *theme, label string, ed *widget.Editor, box *widget.Clickable, hint string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, label, 12, colMuted, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				h := gtx.Dp(40)
				if h > gtx.Constraints.Max.Y {
					h = gtx.Constraints.Max.Y
				}
				return th.boxedField(gtx, ed, box, hint, 14, h)
			}),
		)
	}
}

// formSwitch is one boolean row of a form: the text and the switch are a single
// hit target, and the switch only displays the state, so a click toggles it
// exactly once. The owner reads the value with on when it saves the form.
type formSwitch struct {
	click    widget.Clickable
	sw       *toggleSwitch
	title    string
	subtitle string
	disabled bool
}

func newFormSwitch(on bool, titleKey, subtitleKey string) *formSwitch {
	f := &formSwitch{title: i18n.T(titleKey)}
	if subtitleKey != "" {
		f.subtitle = i18n.T(subtitleKey)
	}
	f.sw = newToggleSwitch(on, nil)
	return f
}

// on reports the current state.
func (f *formSwitch) on() bool { return f.sw.on }

func (f *formSwitch) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	if f.disabled {
		return f.row(gtx, th)
	}
	if f.click.Clicked(gtx) {
		f.sw.on = !f.sw.on
		invalidateAfterTap(gtx)
	}
	return f.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// The row is the hit target, so the hand cursor must be declared
		// inside it: a cursor op pushed outside is shadowed by this area.
		macro := op.Record(gtx.Ops)
		row := f.row(gtx, th)
		call := macro.Stop()
		th.cursor(gtx, pointer.CursorPointer, row.Size)
		call.Add(gtx.Ops)
		return row
	})
}

func (f *formSwitch) row(gtx layout.Context, th *theme) layout.Dimensions {
	return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						col := colText
						if f.disabled {
							col = colMuted
						}
						return th.text(gtx, f.title, 13, col, font.Medium, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if f.subtitle == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Top: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return th.wrappedText(gtx, f.subtitle, 12, colMuted, font.Normal)
						})
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 12) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.sw.Layout(gtx, th) }),
		)
	})
}
