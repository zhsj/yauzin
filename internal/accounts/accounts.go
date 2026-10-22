// Package accounts stores the OAuth credentials and refresh tokens of the real
// mail accounts. The file lives next to settings.json and is plain JSON so it
// can be inspected when a sign-in goes wrong; it is written with owner-only
// permissions and never logged.
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

// OAuth is the OAuth client the accounts were authorized with. It is shared:
// Google expects one client per deployment, and the same client is reused when
// a second account signs in.
type OAuth struct {
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

// Entry is one stored account.
type Entry struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name,omitempty"`
	UseProxy bool   `json:"useProxy,omitempty"`
	Token    Token  `json:"token,omitempty"`
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

// Credentials returns the shared OAuth client credentials, if any.
func (s *Store) Credentials() (clientID, clientSecret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.OAuth.ClientID, s.data.OAuth.ClientSecret
}

// SetCredentials stores the shared OAuth client credentials.
func (s *Store) SetCredentials(clientID, clientSecret string) error {
	return s.update(func(f *file) error {
		f.OAuth = OAuth{ClientID: clientID, ClientSecret: clientSecret}
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

// Upsert inserts an account or replaces the one with the same ID, which is how
// signing in again refreshes the credentials of an existing account.
func (s *Store) Upsert(e Entry) error {
	if e.ID == "" {
		return errors.New("accounts: empty account ID")
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
