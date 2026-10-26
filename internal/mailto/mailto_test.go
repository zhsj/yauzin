package mailto

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want Message
		ok   bool
	}{
		{name: "recipient", uri: "mailto:ada@example.com", want: Message{To: "ada@example.com"}, ok: true},
		{name: "empty message", uri: "mailto:", ok: true},
		{name: "scheme case", uri: "MAILTO:ada@example.com", want: Message{To: "ada@example.com"}, ok: true},
		{
			name: "several recipients",
			uri:  "mailto:a@example.com,b@example.com",
			want: Message{To: "a@example.com, b@example.com"},
			ok:   true,
		},
		{
			name: "spaces and empty entries",
			uri:  "mailto: a@example.com , ,b@example.com ",
			want: Message{To: "a@example.com, b@example.com"},
			ok:   true,
		},
		{
			name: "percent encoded address",
			uri:  "mailto:ada%2Btag%40example.com",
			want: Message{To: "ada+tag@example.com"},
			ok:   true,
		},
		{
			name: "query fields",
			uri:  "mailto:a@example.com?cc=b@example.com&bcc=c@example.com&subject=Hello%20World&body=Line%201%0ALine%202",
			want: Message{
				To:      "a@example.com",
				Cc:      "b@example.com",
				Bcc:     "c@example.com",
				Subject: "Hello World",
				Body:    "Line 1\nLine 2",
			},
			ok: true,
		},
		{
			name: "to header without path",
			uri:  "mailto:?to=a@example.com,b@example.com",
			want: Message{To: "a@example.com, b@example.com"},
			ok:   true,
		},
		{
			name: "path and to header accumulate",
			uri:  "mailto:a@example.com?to=b@example.com,c@example.com",
			want: Message{To: "a@example.com, b@example.com, c@example.com"},
			ok:   true,
		},
		{
			name: "duplicate recipient collapsed",
			uri:  "mailto:a@example.com?to=a@example.com",
			want: Message{To: "a@example.com"},
			ok:   true,
		},
		{
			name: "first subject wins",
			uri:  "mailto:?subject=one&subject=two",
			want: Message{Subject: "one"},
			ok:   true,
		},
		{
			name: "empty subject falls through",
			uri:  "mailto:?subject=&subject=two",
			want: Message{Subject: "two"},
			ok:   true,
		},
		{
			name: "unknown headers ignored",
			uri:  "mailto:?x-note=hi&references=%3Cid%40example.com%3E",
			want: Message{},
			ok:   true,
		},
		{
			name: "plus stays literal",
			uri:  "mailto:?subject=C%2B%2B&body=a+b",
			want: Message{Subject: "C++", Body: "a+b"},
			ok:   true,
		},
		{
			name: "encoded separator stays in value",
			uri:  "mailto:?body=a%26b%3Dc",
			want: Message{Body: "a&b=c"},
			ok:   true,
		},
		{
			name: "line breaks in single line fields",
			uri:  "mailto:a%0D%0Ab@example.com?subject=Hi%0D%0AThere",
			want: Message{To: "a b@example.com", Subject: "Hi There"},
			ok:   true,
		},
		{
			name: "body line endings normalized",
			uri:  "mailto:?body=a%0D%0Ab%0Dc%0Ad",
			want: Message{Body: "a\nb\nc\nd"},
			ok:   true,
		},
		{
			name: "header case",
			uri:  "mailto:?CC=b@example.com&Bcc=c@example.com&SUBJECT=Hi",
			want: Message{Cc: "b@example.com", Bcc: "c@example.com", Subject: "Hi"},
			ok:   true,
		},
		{
			name: "in reply to",
			uri:  "mailto:a@example.com?in-reply-to=%3Cparent%40example.com%3E",
			want: Message{To: "a@example.com", InReplyTo: "<parent@example.com>"},
			ok:   true,
		},
		{
			name: "in reply to keeps a bare id",
			uri:  "mailto:?InReplyTo=parent%40example.com",
			want: Message{InReplyTo: "parent@example.com"},
			ok:   true,
		},
		{
			name: "first in reply to wins",
			uri:  "mailto:?in-reply-to=one&in-reply-to=two",
			want: Message{InReplyTo: "one"},
			ok:   true,
		},
		{
			name: "line breaks in in reply to",
			uri:  "mailto:?in-reply-to=%3Cid%40example.com%3E%0D%0ABcc%3A%20x",
			want: Message{InReplyTo: "<id@example.com> Bcc: x"},
			ok:   true,
		},
		{name: "invalid escape in path", uri: "mailto:a%zz@example.com", ok: false},
		{name: "invalid escape in query", uri: "mailto:a@example.com?subject=%zz", ok: false},
		{name: "other scheme", uri: "https://example.com/mailto:a@example.com", ok: false},
		{name: "missing colon", uri: "mailto", ok: false},
		{name: "empty argument", uri: "", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse(tc.uri)
			if ok != tc.ok {
				t.Fatalf("Parse(%q) ok = %v, want %v", tc.uri, ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tc.uri, got, tc.want)
			}
		})
	}
}

func TestFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{name: "no arguments", args: nil},
		{name: "other arguments ignored", args: []string{"--verbose", "/tmp/draft.eml"}},
		{name: "single uri", args: []string{"mailto:a@example.com"}, want: "mailto:a@example.com", ok: true},
		{
			name: "last uri wins",
			args: []string{"mailto:a@example.com", "--flag", "MAILTO:b@example.com?subject=Hi"},
			want: "MAILTO:b@example.com?subject=Hi",
			ok:   true,
		},
		{name: "bare scheme", args: []string{"mailto:"}, want: "mailto:", ok: true},
		{name: "letters only", args: []string{"mailto"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FromArgs(tc.args)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("FromArgs(%q) = %q, %v, want %q, %v", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}
