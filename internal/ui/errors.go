package ui

import (
	"errors"
	"math"
	"strconv"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/smtp"
)

// errorText renders a service error in the active language.
//
// The domain layer reports categories, not sentences, so the mapping to a
// message lives here: a backend error never reaches the user as an English
// protocol string, and adding a language does not touch the transports.
func errorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, mail.ErrNotFound):
		return i18n.T("error.notFound")
	case errors.Is(err, mail.ErrUnsupported):
		return i18n.T("error.unsupported")
	case errors.Is(err, mail.ErrInvalidArgument):
		return i18n.T("error.invalidArgument")
	case errors.Is(err, mail.ErrUnauthorized):
		return i18n.T("error.unauthorized")
	case errors.Is(err, mail.ErrRateLimited):
		// The backend already retried with a backoff; what is left is a request
		// the user can simply repeat a little later.
		return i18n.T("error.rateLimited")
	case errors.Is(err, mail.ErrQuotaWait):
		return quotaWaitText(err)
	case errors.Is(err, smtp.ErrAuth):
		// The sending server refused the credentials: the protocol sentence
		// below it is only noise for the user.
		return i18n.T("error.smtpAuth")
	}
	// Anything else is a transport or filesystem failure; its text is the only
	// detail the user can act on.
	return err.Error()
}

// quotaWaitText says how long the account's quota needs before the request can
// be sent again. The budget already refused to sit on the wait, so the number
// tells the user when a retry will work instead of when the request expired.
func quotaWaitText(err error) string {
	var wait *mail.QuotaWaitError
	if !errors.As(err, &wait) {
		return i18n.T("error.rateLimited")
	}
	seconds := int(math.Ceil(wait.Wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return i18n.T("error.quotaWait", map[string]string{"Seconds": strconv.Itoa(seconds)})
}
