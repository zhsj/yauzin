package ui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestErrorTextMapsCategories pins the contract between the domain errors and
// the messages the user sees: a backend wraps its categories, the UI unwraps
// them, and only unknown failures fall back to the raw text.
func TestErrorTextMapsCategories(t *testing.T) {
	previous := i18n.Language()
	defer i18n.SetLanguage(previous)
	i18n.SetLanguage("en")

	wrapped := func(err error) error { return fmt.Errorf("transport: %w", err) }

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"not found", wrapped(mail.ErrNotFound), i18n.T("error.notFound")},
		{"unsupported", wrapped(mail.ErrUnsupported), i18n.T("error.unsupported")},
		{"invalid argument", wrapped(mail.ErrInvalidArgument), i18n.T("error.invalidArgument")},
		{"unauthorized", wrapped(mail.ErrUnauthorized), i18n.T("error.unauthorized")},
		{"unknown", errors.New("dial tcp 127.0.0.1:993: refused"), "dial tcp 127.0.0.1:993: refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errorText(tt.err); got != tt.want {
				t.Fatalf("errorText(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
