package i18n

import (
	"sort"
	"testing"
)

// TestCatalogKeyParity keeps every catalog on the same key set as English: a
// missing key would fall back to English silently, and an extra one is a
// message the source no longer knows about.
func TestCatalogKeyParity(t *testing.T) {
	source := catalogIDs[DefaultLanguage]
	if len(source) == 0 {
		t.Fatal("no keys loaded from the English catalog")
	}
	for _, lang := range languages {
		if lang.Tag == DefaultLanguage {
			continue
		}
		ids := catalogIDs[lang.Tag]
		var missing, extra []string
		for id := range source {
			if !ids[id] {
				missing = append(missing, id)
			}
		}
		for id := range ids {
			if !source[id] {
				extra = append(extra, id)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		for _, id := range missing {
			t.Errorf("%s is missing key %q", lang.Tag, id)
		}
		for _, id := range extra {
			t.Errorf("%s has extra key %q", lang.Tag, id)
		}
	}
}

func TestLanguages(t *testing.T) {
	got := Languages()
	if len(got) < 2 {
		t.Fatalf("Languages() = %v, want at least en and zh-Hans", got)
	}
	names := map[string]string{}
	for _, lang := range got {
		names[lang.Tag] = lang.Name
	}
	if names[DefaultLanguage] == "" {
		t.Errorf("English language name is empty")
	}
	if names["zh-Hans"] != "简体中文" {
		t.Errorf("zh-Hans name = %q, want 简体中文", names["zh-Hans"])
	}
}

func TestTranslate(t *testing.T) {
	t.Cleanup(func() { SetLanguage(DefaultLanguage) })

	SetLanguage("en")
	if got := T("app.brand"); got != "Yauzin" {
		t.Errorf("en app.brand = %q, want Yauzin", got)
	}
	if got := T("toast.languageChanged"); got == "" {
		t.Error("en toast.languageChanged is empty")
	}

	SetLanguage("zh-Hans")
	if got := T("app.brand"); got != "邮笺" {
		t.Errorf("zh-Hans app.brand = %q, want 邮笺", got)
	}
	if got, want := T("toast.savedTo", map[string]string{"Path": "/tmp/a.pdf"}),
		"已保存到 /tmp/a.pdf"; got != want {
		t.Errorf("zh-Hans toast.savedTo = %q, want %q", got, want)
	}
	if got, want := T("list.range", map[string]int{"Start": 1, "End": 50, "Total": 120}),
		"1–50 / 共 120"; got != want {
		t.Errorf("zh-Hans list.range = %q, want %q", got, want)
	}

	if got := T("missing.key"); got != "missing.key" {
		t.Errorf("missing key = %q, want the key itself", got)
	}
}

func TestPlural(t *testing.T) {
	t.Cleanup(func() { SetLanguage(DefaultLanguage) })

	SetLanguage("en")
	for _, tc := range []struct {
		count int
		want  string
	}{
		{1, "1 message"},
		{3, "3 messages"},
	} {
		if got := N("reader.messageCount", tc.count); got != tc.want {
			t.Errorf("en reader.messageCount(%d) = %q, want %q", tc.count, got, tc.want)
		}
	}

	SetLanguage("zh-Hans")
	if got, want := N("reader.messageCount", 1), "1 封邮件"; got != want {
		t.Errorf("zh-Hans reader.messageCount(1) = %q, want %q", got, want)
	}
	if got, want := N("list.selected", 4), "已选择 4 项"; got != want {
		t.Errorf("zh-Hans list.selected(4) = %q, want %q", got, want)
	}
}

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		tag  string
		want string
	}{
		{"en", "en"},
		{"zh-Hans", "zh-Hans"},
		{"zh-Hans-CN", "zh-Hans"},
		{"zh", "zh-Hans"},
		{"auto", "en"},
		{"", "en"},
		{"fr", "en"},
	} {
		if got := Match(tc.tag); got != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.tag, got, tc.want)
		}
	}

	if got := SetLanguage("zh-Hans"); got != "zh-Hans" {
		t.Errorf("SetLanguage(zh-Hans) = %q", got)
	}
	if got := Language(); got != "zh-Hans" {
		t.Errorf("Language() = %q, want zh-Hans", got)
	}
	SetLanguage(DefaultLanguage)
}
