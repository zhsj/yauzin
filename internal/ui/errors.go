package ui

import (
	"errors"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
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
	}
	// Anything else is a transport or filesystem failure; its text is the only
	// detail the user can act on.
	return err.Error()
}
