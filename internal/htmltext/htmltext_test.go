package htmltext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToText(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "plain text survives",
			src:  "hello world",
			want: "hello world",
		},
		{
			name: "paragraphs become blank lines",
			src:  "<p>first</p><p>second</p>",
			want: "first\n\nsecond",
		},
		{
			name: "br becomes a line break",
			src:  "first<br>second",
			want: "first\nsecond",
		},
		{
			name: "list items each get a line",
			src:  "<ul><li>one</li><li>two</li></ul>",
			want: "one\ntwo",
		},
		{
			name: "blockquote is separated by a blank line",
			src:  "<blockquote>quoted</blockquote>after",
			want: "quoted\n\nafter",
		},
		{
			name: "scripts and styles are dropped",
			src:  `<style>body{color:red}</style><script>alert(1)</script><p>visible</p>`,
			want: "visible",
		},
		{
			name: "iframes and objects are dropped",
			src:  `<iframe src="https://example.com">frame</iframe><object>obj</object><p>kept</p>`,
			want: "kept",
		},
		{
			name: "non breaking spaces are normalised",
			src:  "a\u00a0b",
			want: "a b",
		},
		{
			name: "whitespace runs collapse",
			src:  "<p>a\n\n\t   b</p>",
			want: "a b",
		},
		{
			// Each text node is collapsed on its own, so the tail of one and the
			// head of the next used to keep two spaces.
			name: "inline nodes share one space",
			src:  `<span>a </span><span> b</span>`,
			want: "a b",
		},
		{
			name: "tabs collapse with spaces",
			src:  "<p>a \t \t b</p>",
			want: "a b",
		},
		{
			name: "empty input",
			src:  "",
			want: "",
		},
		{
			name: "markup only",
			src:  "<div><span> </span></div>",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToText(tt.src); got != tt.want {
				t.Fatalf("ToText(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestToTextTruncatesHugeInput(t *testing.T) {
	src := "<p>" + strings.Repeat("a", maxInput) + "</p>"
	got := ToText(src)
	if got == "" {
		t.Fatal("expected text from truncated input")
	}
	if len(got) > maxInput {
		t.Fatalf("truncated text is %d bytes, want at most %d", len(got), maxInput)
	}
}

func TestTruncateRunesDoesNotSplitRunes(t *testing.T) {
	src := strings.Repeat("界", 10) // three bytes each
	for _, limit := range []int{0, 1, 2, 3, 4, 10, 29} {
		got := truncateRunes(src, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("truncateRunes(%d) split a rune: %q", limit, got)
		}
		if len(got) > limit {
			t.Fatalf("truncateRunes(%d) returned %d bytes", limit, len(got))
		}
	}
}

func TestToTextKeepsUTF8ForHugeInput(t *testing.T) {
	got := ToText("<p>" + strings.Repeat("界", maxInput/2) + "</p>")
	if !utf8.ValidString(got) {
		t.Fatal("truncating a multi-byte message produced invalid UTF-8")
	}
	if len(got) > maxInput {
		t.Fatalf("truncated text is %d bytes, want at most %d", len(got), maxInput)
	}
}
