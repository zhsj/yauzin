// Package accounts stores the OAuth credentials and refresh tokens of the real
// mail accounts, and the servers of the outbound-only SMTP ones. The file lives
// next to settings.json and is plain JSON so it can be inspected when a sign-in
// goes wrong; it is written with owner-only permissions and never logged.
//
// One Store is shared by the UI and every backend, so a token refreshed by a
// request is visible to the next one without a reload.
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// ErrNotExist reports an account ID the store does not hold.
var ErrNotExist = errors.New("accounts: not found")

const fileName = "accounts.json"

// Token is the OAuth token of one account. It is kept independent of any
// oauth2 library type so the on-disk format does not follow a dependency.
type Token struct {
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenType    string    `json:"tokenType,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

// OAuth is the OAuth client of one backend. It is stored once per document:
// Google expects one client per deployment, and the same client is reused when
// a second account signs in. Provider names the backend the client belongs to
// so the file says what the credentials are for instead of relying on the file
// name.
type OAuth struct {
	Provider     mail.Kind `json:"provider,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	ClientSecret string    `json:"clientSecret,omitempty"`
}

// SMTPConfig is the outbound server of a sending account. The password is
// stored in the clear the way the OAuth client secret is: the file is
// owner-only and nothing here is ever logged.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Security is one of smtp.Security: implicit TLS, STARTTLS, or none. The
	// transport validates it, so the file stays a plain string.
	Security string `json:"security"`
	// BccToAccount asks the composer to prefill the Bcc field with the address
	// of the account the draft is written in. The address itself is not stored:
	// one sending account can be used from any mailbox.
	BccToAccount bool `json:"bccToAccount,omitempty"`
}

// Entry is one stored account.
type Entry struct {
	ID       string    `json:"id"`
	Kind     mail.Kind `json:"kind"`
	Email    string    `json:"email"`
	Name     string    `json:"name,omitempty"`
	UseProxy bool      `json:"useProxy,omitempty"`
	Token    Token     `json:"token,omitempty"`
	// SMTP is set for a mail.KindSMTP entry, the outbound-only accounts. They
	// have no token and are never handed to a mailbox backend.
	SMTP *SMTPConfig `json:"smtp,omitempty"`
}

// file is the on-disk document.
type file struct {
	OAuth    OAuth   `json:"oauth,omitempty"`
	Accounts []Entry `json:"accounts,omitempty"`
}

// Store is the accounts file guarded by a mutex. Every mutation writes the
// whole document atomically before it is applied in memory, so a failed write
// leaves both the file and the in-memory copy at the previous state.
type Store struct {
	mu   sync.Mutex
	dir  string
	data file
}

// Open loads the accounts file in dir. A missing file is a fresh install and
// yields an empty store. A broken file is reported but still yields a usable
// empty store, so the caller can warn and keep running instead of dropping the
// whole configuration layer.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("read accounts: %w", err)
	}
	var doc file
	if err := json.Unmarshal(data, &doc); err != nil {
		return s, fmt.Errorf("parse accounts: %w", err)
	}
	s.data = doc
	return s, nil
}

// Credentials returns the OAuth client of the given backend. It reports false
// when the stored client belongs to another backend, so a backend cannot pick
// up the credentials of a different one by accident.
func (s *Store) Credentials(kind mail.Kind) (OAuth, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == "" || s.data.OAuth.Provider != kind {
		return OAuth{}, false
	}
	return s.data.OAuth, true
}

// SetCredentials stores the OAuth client of one backend.
func (s *Store) SetCredentials(o OAuth) error {
	if o.Provider == "" {
		return errors.New("accounts: empty OAuth provider")
	}
	return s.update(func(f *file) error {
		f.OAuth = o
		return nil
	})
}

// List returns a copy of the stored accounts in file order.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Accounts) == 0 {
		return nil
	}
	return append([]Entry(nil), s.data.Accounts...)
}

// SMTPEntries returns the stored outbound-only accounts in file order. They
// are not mailboxes: no backend reads through them, and the composer offers
// them as senders of a draft written in another account.
func (s *Store) SMTPEntries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Entry
	for _, e := range s.data.Accounts {
		if e.Kind == mail.KindSMTP {
			out = append(out, e)
		}
	}
	return out
}

// Entry returns the stored account with the given ID.
func (s *Store) Entry(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.data.Accounts {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Token returns the token of the given account.
func (s *Store) Token(id string) (Token, bool) {
	e, ok := s.Entry(id)
	if !ok {
		return Token{}, false
	}
	return e.Token, true
}

// SaveToken replaces the token of an existing account. It reports ErrNotExist
// for an unknown ID instead of creating a half-populated account.
func (s *Store) SaveToken(id string, t Token) error {
	return s.update(func(f *file) error {
		for i := range f.Accounts {
			if f.Accounts[i].ID == id {
				f.Accounts[i].Token = t
				return nil
			}
		}
		return ErrNotExist
	})
}

// Remove deletes the account with the given ID together with its token. The
// OAuth client is deployment wide, not per account, so it stays behind. It
// reports ErrNotExist for an unknown ID instead of rewriting the file, so a
// stale list cannot silently drop another entry.
func (s *Store) Remove(id string) error {
	return s.update(func(f *file) error {
		for i := range f.Accounts {
			if f.Accounts[i].ID == id {
				f.Accounts = append(f.Accounts[:i], f.Accounts[i+1:]...)
				return nil
			}
		}
		return ErrNotExist
	})
}

// Upsert inserts an account or replaces the one with the same ID, which is how
// signing in again refreshes the credentials of an existing account. Both the
// ID and the backend kind are required: an entry without a kind could not be
// handed to any backend on the next start.
func (s *Store) Upsert(e Entry) error {
	if err := validateEntry(e); err != nil {
		return err
	}
	return s.update(func(f *file) error {
		for i := range f.Accounts {
			if f.Accounts[i].ID == e.ID {
				f.Accounts[i] = e
				return nil
			}
		}
		f.Accounts = append(f.Accounts, e)
		return nil
	})
}

// Replace stores e and drops oldID in one write, keeping the position of the
// entry it replaces. A sending account is filed under its sender address, so
// editing that address renames the entry; doing both in one write keeps a
// failed write from leaving either two copies or none.
func (s *Store) Replace(oldID string, e Entry) error {
	if err := validateEntry(e); err != nil {
		return err
	}
	if oldID == "" || oldID == e.ID {
		return s.Upsert(e)
	}
	return s.update(func(f *file) error {
		next := make([]Entry, 0, len(f.Accounts)+1)
		placed := false
		for _, existing := range f.Accounts {
			switch existing.ID {
			case oldID:
				if !placed {
					next = append(next, e)
					placed = true
				}
			case e.ID:
				// A stale copy of the target ID cannot survive the rename.
			default:
				next = append(next, existing)
			}
		}
		if !placed {
			next = append(next, e)
		}
		f.Accounts = next
		return nil
	})
}

// validateEntry rejects an entry the next start could not hand to a backend.
func validateEntry(e Entry) error {
	if e.ID == "" {
		return errors.New("accounts: empty account ID")
	}
	if e.Kind == "" {
		return errors.New("accounts: empty account kind")
	}
	if e.Kind == mail.KindSMTP && e.SMTP == nil {
		return errors.New("accounts: SMTP account without a server")
	}
	return nil
}

// update applies mutate to a copy of the document and writes it before the
// store switches to it.
func (s *Store) update(mutate func(*file) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.data
	next.Accounts = append([]Entry(nil), s.data.Accounts...)
	if err := mutate(&next); err != nil {
		return err
	}
	if err := writeFile(s.dir, next); err != nil {
		return err
	}
	s.data = next
	return nil
}

// writeFile writes the document atomically with owner-only permissions.
func writeFile(dir string, doc file) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create accounts dir: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode accounts: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".accounts-*.json")
	if err != nil {
		return fmt.Errorf("create accounts temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Remove is a no-op after a successful rename.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod accounts: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write accounts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close accounts: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, fileName)); err != nil {
		return fmt.Errorf("replace accounts: %w", err)
	}
	return nil
}
