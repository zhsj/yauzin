package i18n

import "testing"

func clearLocaleEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		t.Setenv(key, "")
	}
}

func TestSystemLocalePriority(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("LC_MESSAGES", "zh_CN.UTF-8")
	t.Setenv("LC_ALL", "zh_TW.UTF-8")
	if got := SystemLocale(); got != "zh-TW" {
		t.Fatalf("SystemLocale = %q, want zh-TW", got)
	}
}

func TestSystemLocaleLanguageList(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LANGUAGE", "zh_CN:en_US")
	if got := SystemLocale(); got != "zh-CN" {
		t.Fatalf("SystemLocale = %q, want zh-CN", got)
	}
}

func TestSystemLocaleIgnoresC(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANG", "en_US.UTF-8@euro")
	if got := SystemLocale(); got != "en-US" {
		t.Fatalf("SystemLocale = %q, want en-US", got)
	}
}

func TestSystemLocaleEmpty(t *testing.T) {
	clearLocaleEnv(t)
	if got := SystemLocale(); got != "" {
		t.Fatalf("SystemLocale = %q, want empty", got)
	}
}
