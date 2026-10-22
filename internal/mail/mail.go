// Package mail defines the protocol independent mail domain model and the
// Service contract implemented by every backend (Gmail API, IMAP/SMTP, mock).
package mail

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Kind identifies how an account is accessed.
type Kind string

const (
	// KindGmail talks to the Gmail REST API with OAuth2.
	KindGmail Kind = "gmail"
	// KindIMAP talks to a generic IMAP/SMTP server.
	KindIMAP Kind = "imap"
)

// Address is a single mail address with an optional display name.
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

// String renders the address in RFC 5322 style. A display name containing
// characters that are special in an address list is quoted, so a name such as
// `Doe, John` survives a round trip through ParseAddressList instead of turning
// into two recipients.
func (a Address) String() string {
	if a.Email == "" {
		return a.Name
	}
	if a.Name == "" {
		return a.Email
	}
	return quoteDisplayName(a.Name) + " <" + a.Email + ">"
}

// addressSpecials are the characters that force a display name to be quoted.
const addressSpecials = `()<>[]:;@\,."`

// quoteDisplayName wraps a display name in quotes when it needs them, escaping
// backslashes and quotes as RFC 5322 requires.
func quoteDisplayName(name string) string {
	if name == "" {
		return name
	}
	if !strings.ContainsAny(name, addressSpecials) && name == strings.TrimSpace(name) {
		return name
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
	return `"` + escaped + `"`
}

// FolderKind is the semantic role of a folder in the UI.
type FolderKind string

const (
	FolderInbox   FolderKind = "inbox"
	FolderStarred FolderKind = "starred"
	FolderSent    FolderKind = "sent"
	FolderTrash   FolderKind = "trash"
	FolderLabel   FolderKind = "label"
)

// Folder is a mailbox or label exposed by a backend.
type Folder struct {
	ID     string
	Name   string
	Kind   FolderKind
	Color  string
	Unread int
	// Total is the number of conversations (threads) in the folder or label,
	// filled in by FolderDetails. It backs the list toolbar's "N of Total"
	// range label; backends that cannot tell leave it at zero.
	Total int
}

// Account describes one configured mail account.
type Account struct {
	ID    string `json:"id"`
	Kind  Kind   `json:"kind"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DisplayName returns the account name, falling back to the address.
func (a Account) DisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Email
}

// Attachment is a file attached to a message.
type Attachment struct {
	ID       string
	Filename string
	MIMEType string
	Size     int64
}

// MessageSummary is the metadata rendered in the message list.
type MessageSummary struct {
	ID             string
	AccountID      string
	From           Address
	To             []Address
	Subject        string
	Snippet        string
	Date           time.Time
	Unread         bool
	Starred        bool
	HasAttachments bool
	Labels         []string
}

// Message is a fully loaded message body plus attachments.
type Message struct {
	MessageSummary
	Cc          []Address
	HTMLBody    string
	TextBody    string
	Attachments []Attachment
}

// ThreadSummary is one conversation rendered as a single list row. Backends
// merge messages that belong to the same conversation before returning them so
// that pagination counts conversations instead of individual messages.
type ThreadSummary struct {
	// ID identifies the conversation; it is stable across pages.
	ID string
	// AccountID is the account that owns the conversation.
	AccountID string
	// Subject is the normalized subject shared by every message in the thread.
	Subject string
	// Snippet is the preview text taken from the newest message.
	Snippet string
	// Participants holds the distinct senders, newest first.
	Participants []Address
	// LastDate is the timestamp of the newest message.
	LastDate time.Time
	// MessageCount is how many messages the conversation contains.
	MessageCount int
	// UnreadCount is how many messages in the conversation are unread.
	UnreadCount int
	// Starred reports whether any message in the conversation is starred.
	Starred bool
	// HasAttachments reports whether any message carries an attachment.
	HasAttachments bool
	// Labels are the label IDs applied to any message in the conversation.
	Labels []string
	// MessageIDs lists the message IDs oldest first.
	MessageIDs []string
}

// Unread reports whether the conversation still has unread messages.
func (t ThreadSummary) Unread() bool { return t.UnreadCount > 0 }

// ListRequest selects one page of conversations of one folder.
//
// FolderID is required: browsing always names a folder or label, and an empty
// value reports ErrInvalidArgument. Search across folders is SearchRequest.
//
// PageToken is opaque to the caller: it is whatever the previous ThreadPage
// reported, and an empty value asks for the first page. Limit <= 0 means the
// backend default.
type ListRequest struct {
	FolderID  string
	PageToken string
	Limit     int
}

// SearchRequest selects one page of search results across the whole mailbox.
//
// Query is handed to the backend as written, so its own syntax works without
// the UI parsing it (for Gmail: from:, subject:, has:attachment and the rest of
// the search operators). An empty Query reports ErrInvalidArgument. PageToken
// and Limit mean the same as in ListRequest.
type SearchRequest struct {
	Query     string
	PageToken string
	Limit     int
}

// ThreadPage is one page of conversations.
//
// NextPageToken is empty on the last page. Total is the number of conversations
// matching the request (not messages), or an estimate; a negative value means
// the backend cannot tell. Threads always describe whole conversations, never
// only the messages that matched a search.
type ThreadPage struct {
	Threads       []ThreadSummary
	Total         int
	NextPageToken string
}

// Flag is a message state that can be toggled.
type Flag int

const (
	FlagRead Flag = iota
	FlagStarred
)

// DraftAttachment references a local file to send.
type DraftAttachment struct {
	Name string
	Path string
}

// Draft is a message being composed.
type Draft struct {
	To          []Address
	Cc          []Address
	Subject     string
	Body        string
	Attachments []DraftAttachment
}

// Errors shared by all backends. They report a category, not a sentence: the UI
// maps them to a message in the active language with errors.Is, so the text
// here is never shown as is.
var (
	// ErrNotFound reports a conversation, message, attachment or folder the
	// backend does not have.
	ErrNotFound = errors.New("mail: not found")
	// ErrUnsupported reports an operation this account cannot perform.
	ErrUnsupported = errors.New("mail: unsupported operation")
	// ErrInvalidArgument reports a request the backend cannot make sense of,
	// such as an unknown page token or a draft without recipients.
	ErrInvalidArgument = errors.New("mail: invalid argument")
	// ErrUnauthorized reports that the account's credentials no longer work:
	// the token was revoked, expired without a usable refresh token, or the
	// OAuth client was removed. The account has to be authorized again.
	ErrUnauthorized = errors.New("mail: unauthorized")
)

// Service is the single abstraction the UI depends on. Implementations must be
// safe for concurrent use: the UI issues requests from several goroutines and
// never serialises them. Every method honours ctx, including while it waits for
// its own locks.
type Service interface {
	// Folders returns the sidebar entries without the unread counters and label
	// colours. The names come straight from the folder list; the counters and
	// colours are filled in by FolderDetails afterwards.
	Folders(ctx context.Context) ([]Folder, error)
	// FolderDetails fills in the unread counters and label colours of the
	// folders Folders returned.
	FolderDetails(ctx context.Context, folders []Folder) ([]Folder, error)
	// ListThreads returns one page of conversations of one folder, newest
	// first.
	ListThreads(ctx context.Context, req ListRequest) (ThreadPage, error)
	// SearchThreads returns one page of conversations matching req.Query across
	// every folder and label, newest first. Backends exclude trash and spam the
	// way Gmail does. An empty query reports ErrInvalidArgument.
	SearchThreads(ctx context.Context, req SearchRequest) (ThreadPage, error)
	// GetThread returns every message of a conversation, oldest first.
	GetThread(ctx context.Context, threadID string) ([]*Message, error)
	SetFlag(ctx context.Context, ids []string, flag Flag, on bool) error
	Archive(ctx context.Context, ids []string) error
	Delete(ctx context.Context, ids []string) error
	Send(ctx context.Context, draft Draft) error
	SaveAttachment(ctx context.Context, messageID, attachmentID, dir string) (string, error)
	// RawMessage returns the message source as the backend stores it, for the
	// reader's "view original" page. It is a separate read from GetThread: the
	// parsed bodies are all the reader shows, so the source is only fetched
	// when the user asks for it.
	RawMessage(ctx context.Context, messageID string) ([]byte, error)
	Close() error
}

// ParseAddressList splits a comma or semicolon separated address list.
//
// The text is scanned rather than split: a separator inside a quoted display
// name or inside angle brackets does not separate anything, so
// `"Doe, John" <john@example.com>` stays one address.
func ParseAddressList(raw string) []Address {
	var out []Address
	var field strings.Builder
	inQuote, escaped := false, false
	angleDepth := 0

	flush := func() {
		text := strings.TrimSpace(field.String())
		field.Reset()
		if text == "" {
			return
		}
		if addr, ok := parseAddress(text); ok {
			out = append(out, addr)
		}
	}

	for _, r := range raw {
		switch {
		case escaped:
			field.WriteRune(r)
			escaped = false
		case inQuote && r == '\\':
			field.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			field.WriteRune(r)
		case inQuote:
			field.WriteRune(r)
		case r == '<':
			angleDepth++
			field.WriteRune(r)
		case r == '>':
			if angleDepth > 0 {
				angleDepth--
			}
			field.WriteRune(r)
		case angleDepth == 0 && (r == ',' || r == ';' || r == '\n'):
			flush()
		default:
			field.WriteRune(r)
		}
	}
	flush()
	return out
}

// parseAddress reads one field of an address list: either `Name <email>` or a
// bare address. It reports false when the field carries no address at all.
func parseAddress(raw string) (Address, bool) {
	open := strings.LastIndex(raw, "<")
	close := strings.LastIndex(raw, ">")
	if open >= 0 && close > open {
		email := strings.TrimSpace(raw[open+1 : close])
		if email == "" {
			return Address{}, false
		}
		return Address{Name: unquoteDisplayName(strings.TrimSpace(raw[:open])), Email: email}, true
	}
	email := strings.TrimSpace(raw)
	if email == "" {
		return Address{}, false
	}
	return Address{Email: email}, true
}

// unquoteDisplayName reverses quoteDisplayName.
func unquoteDisplayName(name string) string {
	if len(name) < 2 || name[0] != '"' || name[len(name)-1] != '"' {
		return name
	}
	var b strings.Builder
	escaped := false
	for _, r := range name[1 : len(name)-1] {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FormatAddressList renders addresses separated by commas.
//
// It is reserved for backends that hand a recipient list to a protocol layer
// (SMTP's RCPT TO, JMAP's replyTo): no code path in the app calls it yet, only
// the tests do. Keep it exported so a real backend does not have to reintroduce
// the same loop.
func FormatAddressList(addrs []Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

// subjectPrefixes are the reply/forward markers stripped when conversations are
// grouped by subject.
var subjectPrefixes = []string{"re:", "re：", "fwd:", "fw:", "回复:", "回复：", "转发:", "转发："}

// NormalizeSubject removes reply and forward prefixes so that messages of the
// same conversation share a grouping key.
func NormalizeSubject(subject string) string {
	s := strings.TrimSpace(subject)
	for {
		lower := strings.ToLower(s)
		trimmed := false
		for _, prefix := range subjectPrefixes {
			if strings.HasPrefix(lower, prefix) {
				s = strings.TrimSpace(s[len(prefix):])
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	return s
}

// AppendUniqueAddresses adds addresses to dst, skipping duplicates matched by
// case-insensitive email. It is meant for the handful of addresses of one
// conversation; the set is rebuilt per call.
func AppendUniqueAddresses(dst []Address, addrs ...Address) []Address {
	seen := make(map[string]struct{}, len(dst)+len(addrs))
	for _, a := range dst {
		seen[strings.ToLower(strings.TrimSpace(a.Email))] = struct{}{}
	}
	for _, addr := range addrs {
		email := strings.TrimSpace(addr.Email)
		if email == "" {
			continue
		}
		key := strings.ToLower(email)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, addr)
	}
	return dst
}
