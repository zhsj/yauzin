package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestOpenMissingFileIsEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %+v, want none", got)
	}
	if o, ok := s.Credentials(mail.KindGmail); ok {
		t.Fatalf("Credentials = %+v, want none", o)
	}
}

func TestOpenReportsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := Open(dir)
	if err == nil {
		t.Fatal("Open accepted corrupt JSON")
	}
	if s == nil {
		t.Fatal("Open returned no store, want a usable empty one")
	}
	if len(s.List()) != 0 {
		t.Fatalf("corrupt file still produced accounts: %+v", s.List())
	}
}

func TestUpsertRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	entry := Entry{
		ID:       "gmail:user@example.com",
		Kind:     mail.KindGmail,
		Email:    "user@example.com",
		Name:     "User",
		UseProxy: true,
		Token: Token{
			AccessToken:  "access",
			RefreshToken: "refresh",
			TokenType:    "Bearer",
			ExpiresAt:    time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		},
	}
	client := OAuth{Provider: mail.KindGmail, ClientID: "client-id", ClientSecret: "client-secret"}
	if err := s.SetCredentials(client); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	if err := s.Upsert(entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Entry(entry.ID)
	if !ok {
		t.Fatal("account missing after reopen")
	}
	if got.Email != entry.Email || got.Kind != entry.Kind || got.Name != entry.Name || got.UseProxy != entry.UseProxy {
		t.Fatalf("entry = %+v, want %+v", got, entry)
	}
	if got.Token != entry.Token {
		t.Fatalf("token = %+v, want %+v", got.Token, entry.Token)
	}
	if o, ok := reopened.Credentials(mail.KindGmail); !ok || o != client {
		t.Fatalf("credentials = %+v/%v, want %+v", o, ok, client)
	}
}

func TestUpsertReplacesSameID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "a", Kind: mail.KindGmail, Email: "old@example.com"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := s.Upsert(Entry{ID: "a", Kind: mail.KindGmail, Email: "new@example.com", UseProxy: true}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("List = %+v, want one entry", list)
	}
	if list[0].Email != "new@example.com" || !list[0].UseProxy {
		t.Fatalf("entry = %+v, want the replacement", list[0])
	}
}

func TestUpsertRejectsEmptyID(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{Kind: mail.KindGmail, Email: "user@example.com"}); err == nil {
		t.Fatal("Upsert accepted an empty ID")
	}
}

func TestUpsertRejectsEmptyKind(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "a", Email: "user@example.com"}); err == nil {
		t.Fatal("Upsert accepted an empty kind")
	}
}

func TestSMTPEntryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	entry := Entry{
		ID:       "smtp:me@example.com",
		Kind:     mail.KindSMTP,
		Email:    "me@example.com",
		Name:     "Me",
		UseProxy: true,
		SMTP: &SMTPConfig{
			Host:         "smtp.example.com",
			Port:         587,
			Username:     "me@example.com",
			Password:     "secret",
			Security:     "starttls",
			BccToAccount: true,
		},
	}
	if err := s.Upsert(entry); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Entry(entry.ID)
	if !ok {
		t.Fatal("SMTP account missing after reopen")
	}
	if got.Kind != mail.KindSMTP || got.Email != entry.Email || got.Name != entry.Name || !got.UseProxy {
		t.Fatalf("entry = %+v, want %+v", got, entry)
	}
	if got.SMTP == nil || *got.SMTP != *entry.SMTP {
		t.Fatalf("smtp = %+v, want %+v", got.SMTP, entry.SMTP)
	}
	if got.Token != (Token{}) {
		t.Fatalf("SMTP account carries a token: %+v", got.Token)
	}
	senders := reopened.SMTPEntries()
	if len(senders) != 1 || senders[0].ID != entry.ID {
		t.Fatalf("SMTPEntries = %+v, want only %q", senders, entry.ID)
	}
}

func TestSMTPEntriesSkipsMailboxes(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "gmail:a@example.com", Kind: mail.KindGmail, Email: "a@example.com"}); err != nil {
		t.Fatalf("Upsert mailbox: %v", err)
	}
	if err := s.Upsert(Entry{ID: "smtp:b@example.com", Kind: mail.KindSMTP, Email: "b@example.com", SMTP: &SMTPConfig{Host: "h"}}); err != nil {
		t.Fatalf("Upsert sender: %v", err)
	}
	if got := s.SMTPEntries(); len(got) != 1 || got[0].ID != "smtp:b@example.com" {
		t.Fatalf("SMTPEntries = %+v, want only the sender", got)
	}
	if got := s.List(); len(got) != 2 {
		t.Fatalf("List = %d entries, want both stores to keep every entry", len(got))
	}
}

func TestUpsertRejectsSMTPWithoutServer(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "smtp:a@example.com", Kind: mail.KindSMTP, Email: "a@example.com"}); err == nil {
		t.Fatal("Upsert accepted an SMTP account without a server")
	}
}

func TestCredentialsRejectOtherProvider(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	client := OAuth{Provider: mail.KindGmail, ClientID: "id", ClientSecret: "secret"}
	if err := s.SetCredentials(client); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	if o, ok := s.Credentials(mail.KindIMAP); ok {
		t.Fatalf("Credentials(imap) = %+v, want none", o)
	}
	if err := s.SetCredentials(OAuth{ClientID: "id"}); err == nil {
		t.Fatal("SetCredentials accepted an empty provider")
	}
}

func TestSaveToken(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "a", Kind: mail.KindGmail, Email: "a@example.com"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	tok := Token{AccessToken: "new", RefreshToken: "r", ExpiresAt: time.Unix(1000, 0)}
	if err := s.SaveToken("a", tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	if got, ok := s.Token("a"); !ok || got != tok {
		t.Fatalf("Token = %+v/%v, want %+v", got, ok, tok)
	}
	if err := s.SaveToken("missing", tok); !errors.Is(err, ErrNotExist) {
		t.Fatalf("SaveToken(unknown) = %v, want ErrNotExist", err)
	}
}

func TestRemoveDropsEntryAndToken(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	client := OAuth{Provider: mail.KindGmail, ClientID: "id", ClientSecret: "secret"}
	if err := s.SetCredentials(client); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	gone := Entry{ID: "gmail:gone@example.com", Kind: mail.KindGmail, Email: "gone@example.com"}
	gone.Token = Token{AccessToken: "a", RefreshToken: "r"}
	kept := Entry{ID: "gmail:kept@example.com", Kind: mail.KindGmail, Email: "kept@example.com"}
	kept.Token = Token{RefreshToken: "r2"}
	for _, e := range []Entry{gone, kept} {
		if err := s.Upsert(e); err != nil {
			t.Fatalf("Upsert %q: %v", e.ID, err)
		}
	}

	if err := s.Remove(gone.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := s.Entry(gone.ID); ok {
		t.Fatal("removed account is still listed")
	}
	if _, ok := s.Token(gone.ID); ok {
		t.Fatal("removed account kept its token")
	}
	if got, ok := s.Entry(kept.ID); !ok || got.Token != kept.Token {
		t.Fatalf("other account = %+v/%v, want %+v", got, ok, kept)
	}
	if o, ok := s.Credentials(mail.KindGmail); !ok || o != client {
		t.Fatalf("credentials = %+v/%v, want them kept", o, ok)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := reopened.Entry(gone.ID); ok {
		t.Fatal("removed account came back after reopen")
	}
	if list := reopened.List(); len(list) != 1 || list[0].ID != kept.ID {
		t.Fatalf("List = %+v, want only %q", list, kept.ID)
	}
	if o, ok := reopened.Credentials(mail.KindGmail); !ok || o != client {
		t.Fatalf("credentials after reopen = %+v/%v, want them kept", o, ok)
	}

	if err := s.Remove(gone.ID); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Remove(unknown) = %v, want ErrNotExist", err)
	}
}

func TestWriteUsesOwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Upsert(Entry{ID: "a", Kind: mail.KindGmail}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Upsert(Entry{ID: id, Kind: mail.KindGmail}); err != nil {
			t.Fatalf("Upsert %q: %v", id, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != fileName {
			t.Fatalf("leftover file %q", e.Name())
		}
	}
}

func TestDocumentShape(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.SetCredentials(OAuth{Provider: mail.KindGmail, ClientID: "id", ClientSecret: "secret"}); err != nil {
		t.Fatalf("SetCredentials: %v", err)
	}
	if err := s.Upsert(Entry{ID: "gmail:a@example.com", Kind: mail.KindGmail, Email: "a@example.com", UseProxy: true}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// The proxy address belongs to settings.json; the accounts file must not
	// grow a second copy.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["proxy"]; ok {
		t.Fatalf("accounts.json contains a proxy field: %s", data)
	}
	// The credentials and the account must name their backend, so the file
	// says what they belong to without relying on the account ID prefix.
	oauth, ok := raw["oauth"].(map[string]any)
	if !ok || oauth["provider"] != string(mail.KindGmail) {
		t.Fatalf("oauth.provider missing: %s", data)
	}
	accounts, ok := raw["accounts"].([]any)
	if !ok || len(accounts) != 1 {
		t.Fatalf("accounts = %v, want one entry: %s", raw["accounts"], data)
	}
	account, ok := accounts[0].(map[string]any)
	if !ok || account["kind"] != string(mail.KindGmail) {
		t.Fatalf("accounts[0].kind missing: %s", data)
	}
}

func TestConcurrentUpserts(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Upsert(Entry{ID: string(rune('a' + i)), Kind: mail.KindGmail}); err != nil {
				t.Errorf("Upsert: %v", err)
			}
			if _, err := Open(dir); err != nil {
				t.Errorf("concurrent Open: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := len(s.List()); got != 8 {
		t.Fatalf("List = %d entries, want 8", got)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := len(reopened.List()); got != 8 {
		t.Fatalf("reopened = %d entries, want 8", got)
	}
}
