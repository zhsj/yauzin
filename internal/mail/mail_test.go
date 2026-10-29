package mail

import (
	"errors"
	"strings"
	"testing"
)

func TestAddressString(t *testing.T) {
	tests := []struct {
		name string
		addr Address
		want string
	}{
		{"bare address", Address{Email: "john@example.com"}, "john@example.com"},
		{"simple name", Address{Name: "John", Email: "john@example.com"}, "John <john@example.com>"},
		{"hyphenated name", Address{Name: "Jean-Claude", Email: "jc@example.com"}, "Jean-Claude <jc@example.com>"},
		{"name with a comma", Address{Name: "Doe, John", Email: "john@example.com"}, `"Doe, John" <john@example.com>`},
		{"name with quotes", Address{Name: `He said "hi"`, Email: "john@example.com"}, `"He said \"hi\"" <john@example.com>`},
		{"name with a backslash", Address{Name: `back\slash`, Email: "john@example.com"}, `"back\\slash" <john@example.com>`},
		{"name with a dot", Address{Name: "Dr. Who", Email: "who@example.com"}, `"Dr. Who" <who@example.com>`},
		{"padded name", Address{Name: "  padded  ", Email: "p@example.com"}, `"  padded  " <p@example.com>`},
		{"name only", Address{Name: "undisclosed-recipients"}, "undisclosed-recipients"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.addr.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAddressRoundTripsThroughList(t *testing.T) {
	names := []string{
		"",
		"Plain Name",
		"Doe, John",
		`He said "hi"`,
		`back\slash`,
		"名字",
		"  padded  ",
		"Colon: Name",
	}
	for _, name := range names {
		addr := Address{Name: name, Email: "john@example.com"}
		got := ParseAddressList(addr.String())
		if len(got) != 1 {
			t.Fatalf("%q split into %d addresses", addr.String(), len(got))
		}
		if got[0] != addr {
			t.Fatalf("%q parsed as %+v, want %+v", addr.String(), got[0], addr)
		}
	}
}

func TestParseAddressList(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []Address
	}{
		{
			name: "comma separated",
			raw:  "a@example.com, b@example.com",
			want: []Address{{Email: "a@example.com"}, {Email: "b@example.com"}},
		},
		{
			name: "semicolon separated",
			raw:  "a@example.com; b@example.com",
			want: []Address{{Email: "a@example.com"}, {Email: "b@example.com"}},
		},
		{
			// The comma inside the quotes belongs to the name.
			name: "quoted name with a comma",
			raw:  `"Doe, John" <john@example.com>, jane@example.com`,
			want: []Address{
				{Name: "Doe, John", Email: "john@example.com"},
				{Email: "jane@example.com"},
			},
		},
		{
			name: "display name",
			raw:  "John Doe <john@example.com>",
			want: []Address{{Name: "John Doe", Email: "john@example.com"}},
		},
		{
			name: "escaped quotes in the name",
			raw:  `"He said \"hi\"" <john@example.com>`,
			want: []Address{{Name: `He said "hi"`, Email: "john@example.com"}},
		},
		{
			name: "empty segments are dropped",
			raw:  " , a@example.com,  ,",
			want: []Address{{Email: "a@example.com"}},
		},
		{
			name: "field without an address is dropped",
			raw:  "Name <>, a@example.com",
			want: []Address{{Email: "a@example.com"}},
		},
		{
			name: "empty input",
			raw:  "   ",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseAddressList(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d addresses (%+v), want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("address %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFormatAddressList(t *testing.T) {
	got := FormatAddressList([]Address{
		{Name: "Doe, John", Email: "john@example.com"},
		{Email: "jane@example.com"},
	})
	want := `"Doe, John" <john@example.com>, jane@example.com`
	if got != want {
		t.Fatalf("FormatAddressList = %q, want %q", got, want)
	}
	want = `"Doe, John" <john@example.com>, jane@example.com`
	if round := FormatAddressList(ParseAddressList(got)); round != want {
		t.Fatalf("round trip = %q, want %q", round, want)
	}
}

func TestNormalizeSubject(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello", "Hello"},
		{"Re: Hello", "Hello"},
		{"re: hello", "hello"},
		{"RE: Re: Hello", "Hello"},
		{"Fwd: Re: Hello", "Hello"},
		{"回复：你好", "你好"},
		{"转发: 报告", "报告"},
		{"   spaced   ", "spaced"},
	}
	for _, tt := range tests {
		if got := NormalizeSubject(tt.in); got != tt.want {
			t.Fatalf("NormalizeSubject(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDraftRecipients(t *testing.T) {
	draft := Draft{
		To:  []Address{{Email: "to@example.com"}, {Email: "shared@example.com"}},
		Cc:  []Address{{Email: "cc@example.com"}, {Email: "Shared@Example.com"}},
		Bcc: []Address{{Email: "bcc@example.com"}, {Email: "to@example.com"}},
	}
	got := draft.Recipients()
	want := []string{"to@example.com", "shared@example.com", "cc@example.com", "bcc@example.com"}
	if len(got) != len(want) {
		t.Fatalf("Recipients = %+v, want %d addresses", got, len(want))
	}
	for i, email := range want {
		if got[i].Email != email {
			t.Fatalf("address %d = %q, want %q", i, got[i].Email, email)
		}
	}
}

func TestDraftValidate(t *testing.T) {
	if err := (Draft{}).Validate(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Validate(empty) = %v, want ErrInvalidArgument", err)
	}
	if err := (Draft{Bcc: []Address{{Email: "bcc@example.com"}}}).Validate(); err != nil {
		t.Fatalf("Validate(bcc only) = %v, want nil", err)
	}
}

func TestNewMessageID(t *testing.T) {
	tests := []struct{ email, domain string }{
		{"user@example.com", "example.com"},
		{"user@mail.example.org", "mail.example.org"},
		{"no-at-sign", "yauzin"},
		{"trailing@", "yauzin"},
	}
	for _, tt := range tests {
		id := NewMessageID(tt.email)
		if _, ok := NormalizeMessageID(id); !ok {
			t.Fatalf("NewMessageID(%q) = %q, which is not a valid ID", tt.email, id)
		}
		if !strings.HasSuffix(id, "@"+tt.domain+">") {
			t.Fatalf("NewMessageID(%q) = %q, want the %q domain", tt.email, id, tt.domain)
		}
	}
	first, second := NewMessageID("user@example.com"), NewMessageID("user@example.com")
	if first == second {
		t.Fatal("two Message-IDs came out identical")
	}
}

func TestNormalizeMessageID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"bracketed", "<abc@example.com>", "<abc@example.com>", true},
		{"padded", "  <abc@example.com>  ", "<abc@example.com>", true},
		{"bare", "abc@example.com", "<abc@example.com>", true},
		{"empty", "   ", "", false},
		{"spaces", "<a@b> <c@d>", "", false},
		{"newline", "<a@b>\r\nBcc: x@y", "", false},
		{"nested brackets", "<<a@b>>", "", false},
		{"not an address", "abc", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizeMessageID(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("NormalizeMessageID(%q) = %q, %v; want %q, %v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestReplyReferences(t *testing.T) {
	tests := []struct {
		name   string
		parent *Message
		want   []string
	}{
		{
			name:   "parent without references",
			parent: &Message{MessageID: "<b@example.com>"},
			want:   []string{"<b@example.com>"},
		},
		{
			name: "keeps the ancestor chain",
			parent: &Message{
				MessageID:  "<c@example.com>",
				References: []string{"<a@example.com>", "<b@example.com>"},
			},
			want: []string{"<a@example.com>", "<b@example.com>", "<c@example.com>"},
		},
		{
			name: "drops repeats and junk",
			parent: &Message{
				MessageID:  "c@example.com",
				References: []string{"<a@example.com>", "<a@example.com>", "", "<b@example.com>"},
			},
			want: []string{"<a@example.com>", "<b@example.com>", "<c@example.com>"},
		},
		{
			name:   "no message ID contributes nothing",
			parent: &Message{References: []string{"", "  "}},
			want:   nil,
		},
		{
			name:   "missing parent",
			parent: nil,
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplyReferences(tt.parent)
			if len(got) != len(tt.want) {
				t.Fatalf("ReplyReferences = %q, want %q", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ReplyReferences = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestReplyRecipients(t *testing.T) {
	me := []Address{{Email: "me@example.com"}}
	parent := &Message{
		MessageSummary: MessageSummary{
			From: Address{Name: "Bob", Email: "bob@example.com"},
			To:   []Address{{Name: "Me", Email: "ME@example.com"}, {Name: "Ann", Email: "ann@example.com"}},
		},
		Cc: []Address{{Name: "Cid", Email: "cid@example.com"}, {Email: "ann@example.com"}},
	}
	tests := []struct {
		name   string
		parent *Message
		all    bool
		wantTo []string
		wantCc []string
	}{
		{
			name:   "reply goes to the sender alone",
			parent: parent,
			wantTo: []string{"bob@example.com"},
		},
		{
			name:   "reply all adds the other original recipients",
			parent: parent,
			all:    true,
			wantTo: []string{"bob@example.com", "ann@example.com"},
			wantCc: []string{"cid@example.com"},
		},
		{
			name: "reply all leaves out the caller",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Email: "me@example.com"},
					To:   []Address{{Email: "bob@example.com"}},
				},
				Cc: []Address{{Email: "Me@Example.com"}},
			},
			all:    true,
			wantTo: []string{"bob@example.com"},
		},
		{
			name: "reply-to wins for a reply",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Email: "list@example.com"},
					To:   []Address{{Email: "ann@example.com"}},
				},
				Cc:      []Address{{Email: "cid@example.com"}},
				ReplyTo: []Address{{Name: "Bob", Email: "bob@example.com"}},
			},
			wantTo: []string{"bob@example.com"},
		},
		{
			name: "reply all ignores the original recipients behind reply-to",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Email: "list@example.com"},
					To:   []Address{{Email: "ann@example.com"}},
				},
				Cc:      []Address{{Email: "cid@example.com"}},
				ReplyTo: []Address{{Email: "bob@example.com"}, {Email: "BOB@example.com"}},
			},
			all:    true,
			wantTo: []string{"bob@example.com"},
		},
		{
			name: "mail-followup-to answers a reply",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Name: "Marco", Email: "md@example.org"},
					To:   []Address{{Email: "list@example.org"}},
				},
				MailFollowupTo: []Address{{Email: "list@example.org"}},
			},
			wantTo: []string{"list@example.org"},
		},
		{
			name: "mail-followup-to answers a reply all",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Name: "Marco", Email: "md@example.org"},
					To:   []Address{{Email: "list@example.org"}},
				},
				Cc:             []Address{{Email: "cid@example.org"}},
				MailFollowupTo: []Address{{Email: "list@example.org"}, {Email: "LIST@example.org"}},
			},
			all:    true,
			wantTo: []string{"list@example.org"},
		},
		{
			name: "reply-to wins over mail-followup-to",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Email: "list@example.org"},
				},
				ReplyTo:        []Address{{Email: "bob@example.org"}},
				MailFollowupTo: []Address{{Email: "list@example.org"}},
			},
			all:    true,
			wantTo: []string{"bob@example.org"},
		},
		{
			name: "mail-followup-to without usable addresses falls through",
			parent: &Message{
				MessageSummary: MessageSummary{
					From: Address{Email: "bob@example.com"},
					To:   []Address{{Email: "me@example.com"}, {Email: "ann@example.com"}},
				},
				Cc:             []Address{{Email: "cid@example.com"}},
				MailFollowupTo: []Address{{Email: "  "}},
			},
			all:    true,
			wantTo: []string{"bob@example.com", "ann@example.com"},
			wantCc: []string{"cid@example.com"},
		},
		{
			name:   "missing parent",
			parent: nil,
			all:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			to, cc := ReplyRecipients(tt.parent, me, tt.all)
			if got := addressEmails(to); strings.Join(got, ",") != strings.Join(tt.wantTo, ",") {
				t.Fatalf("To = %v, want %v", got, tt.wantTo)
			}
			if got := addressEmails(cc); strings.Join(got, ",") != strings.Join(tt.wantCc, ",") {
				t.Fatalf("Cc = %v, want %v", got, tt.wantCc)
			}
		})
	}
}

// addressEmails lists the addresses of one recipient row for comparison.
func addressEmails(addrs []Address) []string {
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.Email)
	}
	return out
}

func TestAppendUniqueAddresses(t *testing.T) {
	base := []Address{{Email: "a@example.com"}}
	got := AppendUniqueAddresses(base,
		Address{Name: "A again", Email: "A@EXAMPLE.COM"},
		Address{Name: "B", Email: " b@example.com "},
		Address{Email: "   "},
		Address{Email: "b@example.com"},
	)
	if len(got) != 2 {
		t.Fatalf("got %d addresses (%+v), want 2", len(got), got)
	}
	if got[1].Name != "B" || got[1].Email != " b@example.com " {
		t.Fatalf("second address = %+v, want B kept as given", got[1])
	}
}
