package ui

import (
	"context"
	"errors"
	"image"
	"net/http"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/gmail"
)

// signInTimeout bounds one browser authorization. The user may have to create
// an OAuth client and log in, so it is far longer than a mail request.
const signInTimeout = 5 * time.Minute

// accountStep is the page of the add-account wizard. Every protocol is listed
// on the first step; only the ones with a backend behind them can be entered.
type accountStep int

const (
	accountStepType accountStep = iota
	accountStepGmail
	// accountStepAuth shows the consent URL: the app no longer opens the
	// browser itself, so the user can copy the link or open it with a button.
	accountStepAuth
)

// accountDialog adds one account. The first step picks the protocol, the
// second is the Gmail form: the user supplies the OAuth client they created in
// Google Cloud, the app opens the system browser and the loopback callback
// returns the token. The dialog stays open across the wait so a failure is
// explained in place.
type accountDialog struct {
	app     *App
	blocker *tapBlocker
	close   *iconButton

	step     accountStep
	back     *linkButton
	authBack *linkButton
	typeRows []*settingsRow

	clientID        widget.Editor
	clientSecret    widget.Editor
	clientIDBox     widget.Clickable
	clientSecretBox widget.Clickable
	useProxy        bool
	// hasProxy mirrors the stored proxy setting: without an address the
	// switch makes no sense and is not shown.
	hasProxy bool
	proxy    string

	busy        bool
	err         string
	signIn      *pillButton
	cancel      *pillButton
	proxySwitch *toggleSwitch

	// The authorization step: the consent URL is displayed instead of being
	// handed to the browser. copyPending carries the copy click from the button
	// callback (which has no layout context) to the frame that writes it.
	authURL     string
	authField   widget.Editor
	copyLink    *pillButton
	openLink    *pillButton
	copyPending bool
	// relogin is the account being re-authorized; nil while adding one.
	relogin *mail.Account

	// pending is true while the authorization goroutine owns the flow. Closing
	// the dialog clears it, so a late callback cannot resurrect the panel.
	pending  bool
	cancelFn context.CancelFunc
	list     layout.List
}

func newAccountDialog(a *App) *accountDialog {
	// Every way out of the wizard except a successful sign-in returns to the
	// settings panel it was opened from; closing the whole overlay would drop
	// the user back into the mailbox.
	d := &accountDialog{
		app:      a,
		blocker:  newTapBlocker(colOverlay, a.backToAccounts),
		close:    newIconButton(iconClose, 32, a.backToAccounts),
		proxy:    a.proxy,
		hasProxy: strings.TrimSpace(a.proxy) != "",
	}
	d.clientID.SingleLine = true
	d.clientSecret.SingleLine = true
	d.clientSecret.Mask = '•'
	if a.accountStore != nil {
		if o, ok := a.accountStore.Credentials(mail.KindGmail); ok && (o.ClientID != "" || o.ClientSecret != "") {
			d.clientID.SetText(o.ClientID)
			d.clientSecret.SetText(o.ClientSecret)
		}
	}
	// A configured proxy is the common reason to open this dialog; default to
	// using it, and let the user turn it off per account.
	d.useProxy = d.hasProxy
	if d.hasProxy {
		d.proxySwitch = newToggleSwitch(d.useProxy, func(on bool) { d.useProxy = on })
	}
	d.back = newLinkButton(i18n.T("settings.accountBack"), icon{glyph: glyphBack}, func() {
		d.step = accountStepType
		d.err = ""
		d.list.ScrollTo(0)
	})
	// While the consent URL is on screen the back link returns to the client
	// form, which means giving up the pending wait.
	d.authBack = newLinkButton(i18n.T("settings.accountBack"), icon{glyph: glyphBack}, func() {
		d.abort()
		d.step = accountStepGmail
		d.err = ""
		d.list.ScrollTo(0)
	})
	// IMAP is on the roadmap but has no backend yet, so the row is listed
	// disabled rather than hiding the option altogether. The SMTP row opens a
	// form of its own: a sending account has no mailbox to sign in to.
	d.typeRows = []*settingsRow{
		{
			icon:     glyphMail,
			title:    i18n.T("settings.accountTypeGmail"),
			subtitle: i18n.T("settings.accountTypeGmailHint"),
			onTap: func() {
				d.step = accountStepGmail
				d.err = ""
				d.list.ScrollTo(0)
			},
		},
		{
			icon:     glyphSend,
			title:    i18n.T("settings.accountTypeSMTP"),
			subtitle: i18n.T("settings.accountTypeSMTPHint"),
			onTap: func() {
				a.showOverlay(newSMTPDialog(a, nil))
			},
		},
		{
			icon:     glyphInbox,
			title:    i18n.T("settings.accountTypeIMAP"),
			subtitle: i18n.T("settings.accountTypeIMAPHint"),
			disabled: true,
		},
	}
	// The consent URL is read-only but selectable, so a fragment can be copied
	// without pressing the button.
	d.authField.ReadOnly = true
	d.authField.SingleLine = true
	d.copyLink = newPillButton(i18n.T("settings.accountCopyLink"), icon{}, false, func() { d.copyPending = true })
	d.copyLink.disabled = true
	d.openLink = newPillButton(i18n.T("settings.accountOpenLink"), icon{}, false, d.openAuthURL)
	d.openLink.disabled = true

	d.signIn = newPillButton(i18n.T("settings.accountSignIn"), icon{}, true, d.start)
	d.signIn.disabled = true
	d.cancel = newPillButton(i18n.T("settings.cancel"), icon{}, false, a.backToAccounts)
	d.list.Axis = layout.Vertical
	return d
}

// abort cancels a pending authorization when the dialog is closed or replaced.
func (d *accountDialog) abort() {
	d.pending = false
	d.busy = false
	if d.cancelFn != nil {
		d.cancelFn()
		d.cancelFn = nil
	}
}

// start launches the authorization off the UI goroutine and switches to the
// step that shows the consent URL.
func (d *accountDialog) start() {
	if d.busy || d.pending {
		return
	}
	clientID := strings.TrimSpace(d.clientID.Text())
	clientSecret := strings.TrimSpace(d.clientSecret.Text())
	if clientID == "" {
		d.err = i18n.T("settings.accountClientIDMissing")
		return
	}

	var client *http.Client
	if d.useProxy {
		c, err := gmail.HTTPClient(d.proxy)
		if err != nil {
			d.err = i18n.T("settings.proxyInvalid")
			return
		}
		client = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	d.cancelFn = cancel
	d.busy = true
	d.err = ""
	d.pending = true
	d.step = accountStepAuth
	d.authURL = ""
	d.authField.SetText("")
	d.list.ScrollTo(0)

	go func() {
		res, err := gmail.SignIn(ctx, gmail.SignInOptions{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			HTTPClient:   client,
			// The browser is opened on demand from the authorization step, so
			// the dialog only receives the URL here.
			OnAuthURL: func(raw string) {
				d.app.post(func() {
					if !d.pending || d.app.accountAdd != d {
						return
					}
					d.authURL = raw
					d.authField.SetText(raw)
				})
			},
			Timeout: signInTimeout,
		})
		d.app.post(func() {
			if !d.pending || d.app.accountAdd != d {
				return
			}
			d.busy = false
			d.pending = false
			cancel()
			d.cancelFn = nil
			if err != nil {
				d.clearAuthURL()
				d.err = signInErrorText(err)
				return
			}
			d.finish(res, clientID, clientSecret)
		})
	}()
}

// clearAuthURL drops the consent URL of a finished attempt: the loopback
// listener has been closed, so the link cannot complete any more and showing it
// would only invite a failing retry.
func (d *accountDialog) clearAuthURL() {
	d.authURL = ""
	d.authField.SetText("")
}

// openAuthURL hands the shown consent URL to the system browser.
func (d *accountDialog) openAuthURL() {
	if d.authURL == "" {
		return
	}
	if err := openPath(d.authURL); err != nil {
		d.err = err.Error()
	}
}

// finish stores the new account and switches to it. Anything that fails here
// stays in the dialog: the token is already valid, so the user should be able
// to retry without authorizing again.
func (d *accountDialog) finish(res gmail.SignInResult, clientID, clientSecret string) {
	a := d.app
	if a.accountStore == nil {
		d.err = i18n.T("settings.accountSignInFailed")
		return
	}
	// A re-login refreshes one mailbox: signing in with a different Google
	// account must not add or replace an entry behind the user's back.
	if d.relogin != nil && !strings.EqualFold(strings.TrimSpace(res.Email), strings.TrimSpace(d.relogin.Email)) {
		d.clearAuthURL()
		d.err = i18n.T("settings.accountEmailMismatch", map[string]string{
			"Email":   res.Email,
			"Account": d.relogin.DisplayName(),
		})
		return
	}
	client := accounts.OAuth{Provider: mail.KindGmail, ClientID: clientID, ClientSecret: clientSecret}
	if err := a.accountStore.SetCredentials(client); err != nil {
		logErr("save OAuth client", err)
		d.err = err.Error()
		return
	}
	id := "gmail:" + strings.ToLower(strings.TrimSpace(res.Email))
	name := strings.TrimSpace(res.Name)
	if name == "" {
		// A grant without profileScope reports no name; keep the one an earlier
		// authorization stored instead of dropping it.
		if old, ok := a.accountStore.Entry(id); ok {
			name = old.Name
		}
	}
	entry := accounts.Entry{
		ID:       id,
		Kind:     mail.KindGmail,
		Email:    res.Email,
		Name:     name,
		UseProxy: d.useProxy,
		Token:    res.Token,
	}
	if err := a.accountStore.Upsert(entry); err != nil {
		logErr("save account", err)
		d.err = err.Error()
		return
	}

	acc := mail.Account{ID: id, Kind: mail.KindGmail, Name: name, Email: res.Email}
	svc := a.newService(acc)
	if svc == nil {
		d.err = i18n.T("settings.accountSignInFailed")
		return
	}
	a.cancelRequests()
	if old := a.services[id]; old != nil {
		_ = old.Close()
	}
	a.services[id] = svc
	a.addAccount(acc)
	if d.relogin != nil {
		// The refreshed account keeps its place in the list, so the dialog
		// returns to the accounts page instead of switching mailboxes.
		a.notify(i18n.T("toast.accountUpdated", map[string]string{"Name": acc.DisplayName()}), false)
		a.showSettingsPage(settingsTabAccounts)
		if a.activeAcc == id {
			a.reloadCheckedNow()
		}
		return
	}
	a.rebuildSettingsPanel()
	a.notify(i18n.T("toast.accountAdded", map[string]string{"Name": acc.DisplayName()}), false)
	if a.activeAcc == id {
		// Signing in again replaces the backend of the mailbox on screen; its
		// data has to be fetched with the new token, starting from a baseline
		// the fresh backend recorded itself.
		a.hideOverlay()
		a.reloadCheckedNow()
		return
	}
	a.switchAccount(id)
}

// addAccount adds acc to the account list or refreshes its address when the
// same mailbox signs in again.
func (a *App) addAccount(acc mail.Account) {
	for i := range a.accounts {
		if a.accounts[i].ID == acc.ID {
			a.accounts[i] = acc
			return
		}
	}
	a.accounts = append(a.accounts, acc)
}

// signInErrorText localizes the failures of the authorization flow.
func signInErrorText(err error) string {
	switch {
	case errors.Is(err, gmail.ErrAuthDenied):
		return i18n.T("error.authDenied")
	case errors.Is(err, gmail.ErrAuthTimeout):
		return i18n.T("error.authTimeout")
	case errors.Is(err, mail.ErrUnauthorized):
		return i18n.T("error.unauthorized")
	case errors.Is(err, mail.ErrInvalidArgument):
		return errorText(err)
	}
	return errorText(err)
}

func (d *accountDialog) Layout(gtx layout.Context, a *App) layout.Dimensions {
	_ = d.blocker.Layout(gtx)

	size := panelSize(gtx, image.Pt(gtx.Dp(560), gtx.Dp(500)), image.Pt(320, 240))
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
							return th.text(gtx, d.titleText(), textHeading, colText, font.Bold, 1)
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

// titleText is the dialog heading: adding an account or re-authorizing one.
func (d *accountDialog) titleText() string {
	if d.relogin != nil {
		return i18n.T("settings.accountRelauthTitle")
	}
	return i18n.T("settings.accountTitle")
}

func (d *accountDialog) content(gtx layout.Context, th *theme) layout.Dimensions {
	switch d.step {
	case accountStepGmail:
		return d.gmailContent(gtx, th)
	case accountStepAuth:
		return d.authContent(gtx, th)
	}
	return d.typeContent(gtx, th)
}

// typeContent is the first step: one row per account protocol. The rows carry
// the settings row style and the ones without a backend are disabled.
func (d *accountDialog) typeContent(gtx layout.Context, th *theme) layout.Dimensions {
	return layout.Inset{Top: 8, Bottom: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 20, Right: 20, Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.wrappedText(gtx, i18n.T("settings.accountChooseType"), textMeta, colMuted, font.Normal)
				})
			}),
			layout.Rigid(rows(d.typeRows, th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 14) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceEnd}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return d.cancel.Layout(gtx, th)
						}),
					)
				})
			}),
		)
	})
}

// gmailContent is the second step: the OAuth client form. The back link keeps
// the protocol step reachable without closing the dialog.
func (d *accountDialog) gmailContent(gtx layout.Context, th *theme) layout.Dimensions {
	// Going back while the browser flow owns the dialog would hide the only
	// progress feedback, so the link stays dimmed until the flow returns.
	d.back.disabled = d.busy || d.pending
	return layout.Inset{Top: 8, Bottom: 14, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return d.back.Layout(gtx, th)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.wrappedText(gtx, i18n.T("settings.accountIntro"), textMeta, colMuted, font.Normal)
				})
			}),
			layout.Rigid(labelledField(th, i18n.T("settings.accountClientID"), &d.clientID, &d.clientIDBox, "1234.apps.googleusercontent.com")),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(labelledField(th, i18n.T("settings.accountClientSecret"), &d.clientSecret, &d.clientSecretBox, i18n.T("settings.accountClientSecretHint"))),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 14) }),
			layout.Rigid(d.proxyRow(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(d.errorRow(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(d.buttons(th)),
		)
	})
}

// authContent shows the consent URL. The browser is never opened implicitly:
// the user copies the link or presses the button once the URL is there.
func (d *accountDialog) authContent(gtx layout.Context, th *theme) layout.Dimensions {
	d.copyLink.disabled = d.authURL == ""
	d.openLink.disabled = d.authURL == ""
	d.authBack.disabled = false
	return layout.Inset{Top: 8, Bottom: 14, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// Naming the target account keeps the user from authorizing a
				// different mailbox while re-logging in.
				if d.relogin == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, d.relogin.DisplayName(), textMain, colText, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.wrappedText(gtx, i18n.T("settings.accountAuthHint"), textMeta, colMuted, font.Normal)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.text(gtx, i18n.T("settings.accountAuthURL"), textMeta, colMuted, font.Medium, 1)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if d.authURL == "" {
					return layout.Inset{Top: 4, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return th.wrappedText(gtx, i18n.T("settings.accountWaiting"), textMeta, colMuted, font.Normal)
					})
				}
				return th.boxedField(gtx, &d.authField, nil, "", textMeta, gtx.Dp(38))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.copyLink.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.openLink.Layout(gtx, th)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(d.errorRow(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceEnd}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if d.relogin != nil {
							return layout.Dimensions{}
						}
						return d.authBack.Layout(gtx, th)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return d.cancel.Layout(gtx, th)
					}),
				)
			}),
		}
		dims := layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		if d.copyPending {
			d.copyPending = false
			if d.authURL != "" {
				writeClipboard(gtx, d.authURL)
				d.app.notify(i18n.T("toast.linkCopied"), false)
			}
		}
		return dims
	})
}

// proxyRow shows the per-account proxy switch, or explains that no proxy is
// configured. The address itself lives in the settings, not here.
func (d *accountDialog) proxyRow(th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !d.hasProxy {
			return th.wrappedText(gtx, i18n.T("settings.proxyUnset"), textMeta, colMuted, font.Normal)
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.proxySwitch.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 10) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				label := i18n.T("settings.accountUseProxy")
				if d.useProxy {
					label = label + " · " + d.proxy
				}
				return th.wrappedText(gtx, label, textMeta, colMuted, font.Normal)
			}),
		)
	}
}

func (d *accountDialog) errorRow(th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if d.err == "" {
			return layout.Dimensions{}
		}
		return th.wrappedText(gtx, d.err, textMeta, colDanger, font.Normal)
	}
}

func (d *accountDialog) buttons(th *theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		d.signIn.disabled = d.busy || strings.TrimSpace(d.clientID.Text()) == ""
		if d.busy {
			d.signIn.setText(i18n.T("settings.accountWaiting"))
		} else {
			d.signIn.setText(i18n.T("settings.accountSignIn"))
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Spacing: layout.SpaceEnd}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.cancel.Layout(gtx, th)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return d.signIn.Layout(gtx, th)
			}),
		)
	}
}
