package ui

import (
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/pgp"
)

// normalizeSigningKey validates what the user typed into a signing key field.
// An empty field clears the key, which turns signing off for that identity;
// anything else has to be a long key ID or a full fingerprint, because gpg
// would also accept a mail address and then sign with whichever key it happens
// to resolve to.
func normalizeSigningKey(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return pgp.NormalizeKey(raw)
}

// storedSigningKey returns the key stored for one identity, empty when the
// identity does not sign.
func storedSigningKey(a *App, id string) string {
	if a.accountStore == nil {
		return ""
	}
	if entry, ok := a.accountStore.Entry(id); ok {
		return entry.SigningKey
	}
	return ""
}

// signingKeyRow edits the OpenPGP key one identity signs with. Every identity
// that can put a draft on the wire gets a row, so the settings page answers
// "which key does this address use" in one place instead of hiding the field in
// two different forms. The key is stored next to the credentials in
// accounts.json; the passphrase never is, because gpg-agent asks for it when the
// composer signs.
type signingKeyRow struct {
	app   *App
	accID string
	// title is the identity's address. The row shows the address alone: that
	// is what a key belongs to, and the full explanation lives once at the top
	// of the page instead of beside every field.
	title string
	edit  widget.Editor
	box   widget.Clickable
	save  *pillButton
	// saved is the stored value, so the button stays inactive until the field
	// differs from it.
	saved string
	err   string
}

func newSigningKeyRow(a *App, accID, title, key string) *signingKeyRow {
	f := &signingKeyRow{app: a, accID: accID, title: title, saved: key}
	f.edit.SingleLine = true
	f.edit.SetText(key)
	f.save = newPillButton(i18n.T("settings.save"), icon{}, true, f.store)
	return f
}

// store writes the key back to the account store. It is a separate write from
// the rest of the account details because Gmail accounts have no form of their
// own: the accordion only carries switches and links.
func (f *signingKeyRow) store() {
	a := f.app
	if a.accountStore == nil {
		f.err = i18n.T("settings.signingKeySaveFailed")
		return
	}
	key, err := normalizeSigningKey(f.edit.Text())
	if err != nil {
		f.err = i18n.T("settings.signingKeyInvalid")
		return
	}
	entry, ok := a.accountStore.Entry(f.accID)
	if !ok {
		f.err = i18n.T("settings.signingKeySaveFailed")
		return
	}
	entry.SigningKey = key
	if err := a.accountStore.Upsert(entry); err != nil {
		logErr("save signing key", err)
		f.err = i18n.T("settings.signingKeySaveFailed")
		return
	}
	f.err = ""
	f.saved = key
	// Show the normalized form: the key is stored upper case, and the field
	// should not claim otherwise.
	f.edit.SetText(key)
}

func (f *signingKeyRow) Layout(gtx layout.Context, th *theme) layout.Dimensions {
	f.save.disabled = strings.TrimSpace(f.edit.Text()) == f.saved
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 12, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, f.title, textMain, colText, font.Medium, 1)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 16, Right: 12, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return th.boxedField(gtx, &f.edit, &f.box, i18n.T("settings.signingKeyPlaceholder"), textSecondary, gtx.Dp(36))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.save.Layout(gtx, th) }),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if f.err == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: 16, Right: 16, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.wrappedText(gtx, f.err, textMeta, colDanger, font.Normal)
			})
		}),
	)
}
