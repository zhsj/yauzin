package ui

import (
	"strings"
	"testing"

	"gioui.org/font"
)

func TestCJKTypeface(t *testing.T) {
	for _, tc := range []struct {
		name    string
		locales []string
		want    font.Typeface
	}{
		{"simplified script", []string{"zh-Hans"}, cjkSCTypeface},
		{"simplified region", []string{"zh-CN"}, cjkSCTypeface},
		{"bare chinese", []string{"zh"}, cjkSCTypeface},
		{"traditional script", []string{"zh-Hant"}, cjkTCTypeface},
		{"taiwan", []string{"zh-TW"}, cjkTCTypeface},
		{"hong kong underscore", []string{"zh_HK"}, cjkTCTypeface},
		{"macau", []string{"zh-MO"}, cjkTCTypeface},
		{"taiwan with script", []string{"zh-Hant-TW"}, cjkTCTypeface},
		{"upper case", []string{"ZH-Hant"}, cjkTCTypeface},
		{"japanese", []string{"ja"}, cjkJPTypeface},
		{"japanese region", []string{"ja-JP"}, cjkJPTypeface},
		{"korean", []string{"ko-KR"}, cjkKRTypeface},
		{"english", []string{"en-US"}, cjkSCTypeface},
		{"empty", []string{""}, cjkSCTypeface},
		{"unknown", []string{"xx-YY"}, cjkSCTypeface},
		{"no locale at all", nil, cjkSCTypeface},
		{"first cjk wins", []string{"en-US", "ja-JP"}, cjkJPTypeface},
		{"skip empty then take", []string{"", "zh-TW"}, cjkTCTypeface},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cjkTypeface(tc.locales...); got != tc.want {
				t.Errorf("cjkTypeface(%q) = %q, want %q", tc.locales, got, tc.want)
			}
		})
	}
}

func TestTextTypefaceFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		locales []string
		want    string
	}{
		{"chinese", []string{"zh-CN"}, "Noto Sans, Noto Sans CJK SC, sans-serif, emoji"},
		{"japanese", []string{"ja-JP"}, "Noto Sans, Noto Sans CJK JP, sans-serif, emoji"},
		{"no cjk locale", []string{"en-US"}, "Noto Sans, Noto Sans CJK SC, sans-serif, emoji"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(textTypefaceFor(tc.locales...)); got != tc.want {
				t.Errorf("textTypefaceFor(%q) = %q, want %q", tc.locales, got, tc.want)
			}
		})
	}
}

// TestTextTypefaceOrder guards the two ordering rules the family list depends
// on: Latin must precede the CJK families, and emoji must stay last.
func TestTextTypefaceOrder(t *testing.T) {
	parts := strings.Split(string(textTypeface()), ", ")
	if len(parts) != 4 {
		t.Fatalf("family list %q has %d parts, want 4", strings.Join(parts, ", "), len(parts))
	}
	if parts[0] != latinTypeface {
		t.Errorf("first family = %q, want %q", parts[0], latinTypeface)
	}
	if !strings.HasPrefix(parts[1], "Noto Sans CJK ") {
		t.Errorf("second family = %q, want a Noto Sans CJK variant", parts[1])
	}
	if parts[2] != "sans-serif" {
		t.Errorf("third family = %q, want sans-serif", parts[2])
	}
	if parts[3] != "emoji" {
		t.Errorf("last family = %q, want emoji", parts[3])
	}
}
