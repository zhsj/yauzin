package i18n

import (
	"os"
	"strings"
)

// SystemLocale returns the locale configured in the environment, normalized to
// a BCP 47 style tag such as "zh-CN" or "en-US". It returns an empty string
// when no usable locale is set, in which case Match falls back to the default
// language.
//
// The lookup order follows POSIX: LC_ALL wins over LC_MESSAGES, which wins over
// LANG. LANGUAGE is a GNU extension holding a colon separated preference list;
// its first entry is used when the POSIX variables are empty or set to C.
func SystemLocale() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		for _, raw := range strings.Split(os.Getenv(key), ":") {
			if tag := normalizeLocale(raw); tag != "" {
				return tag
			}
		}
	}
	return ""
}

// normalizeLocale strips the encoding and modifier components of a POSIX
// locale and converts the territory separator to a dash.
func normalizeLocale(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '@'); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.ReplaceAll(raw, "_", "-")
	switch strings.ToLower(raw) {
	case "c", "posix":
		return ""
	}
	return raw
}
