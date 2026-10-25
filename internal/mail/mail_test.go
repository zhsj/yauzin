package mail

import (
	"errors"
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
