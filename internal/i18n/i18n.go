// Package i18n holds the translation catalogs for the application UI.
//
// English is the source language and the fallback: every key is defined in
// translations/en.json and the code always passes the English message as the
// go-i18n default, so a missing translation degrades to readable English
// instead of an empty label or a raw key.
//
// Adding a language only requires dropping a new <tag>.json file into the
// translations directory; the language list shown in the UI is derived from the
// embedded files and the "language.name" entry inside each one.
package i18n

import (
	"embed"
	"encoding/json"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// DefaultLanguage is used when nothing matches the user's preference or the
// system locale.
const DefaultLanguage = "en"

const catalogDir = "translations"

// Locale describes one catalog file.
type Locale struct {
	// Tag is the BCP-47 tag taken from the file name, for example "zh-Hans".
	Tag string
	// Name is the native name stored under the "language.name" key.
	Name string
}

//go:embed translations/*.json
var catalogFS embed.FS

var (
	mu        sync.RWMutex
	bundle    *i18n.Bundle
	localizer *i18n.Localizer
	current   = DefaultLanguage

	languages []Locale
	tags      []language.Tag
	matcher   language.Matcher

	// defaults keeps the English message for every known key so lookups in a
	// partially translated catalog still show English text.
	defaults = map[string]*i18n.Message{}
	// catalogIDs records the keys present in each catalog, used to warn about
	// missing translations at startup.
	catalogIDs = map[string]map[string]bool{}
)

func init() {
	b := i18n.NewBundle(language.English)
	b.RegisterUnmarshalFunc("json", json.Unmarshal)

	entries, err := catalogFS.ReadDir(catalogDir)
	if err != nil {
		slog.Error("read translation catalog", "err", err)
		bundle = b
		localizer = i18n.NewLocalizer(b, DefaultLanguage)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filePath := path.Join(catalogDir, entry.Name())
		data, err := catalogFS.ReadFile(filePath)
		if err != nil {
			slog.Error("read translation file", "file", entry.Name(), "err", err)
			continue
		}
		tag := strings.TrimSuffix(entry.Name(), ".json")
		parsed, err := b.ParseMessageFileBytes(data, entry.Name())
		if err != nil {
			slog.Error("parse translation file", "file", entry.Name(), "err", err)
			continue
		}
		name, ids := catalogMeta(data, tag)
		languages = append(languages, Locale{Tag: parsed.Tag.String(), Name: name})
		tags = append(tags, parsed.Tag)
		catalogIDs[parsed.Tag.String()] = ids
		if parsed.Tag.String() == DefaultLanguage {
			for _, msg := range parsed.Messages {
				defaults[msg.ID] = msg
			}
		}
	}

	bundle = b
	if len(tags) > 0 {
		matcher = language.NewMatcher(tags)
	}
	localizer = i18n.NewLocalizer(b, DefaultLanguage)
	current = DefaultLanguage

	warnMissingKeys()
}

// warnMissingKeys logs every English key without a translation once at startup.
func warnMissingKeys() {
	source := catalogIDs[DefaultLanguage]
	for _, lang := range languages {
		if lang.Tag == DefaultLanguage {
			continue
		}
		ids := catalogIDs[lang.Tag]
		missing := make([]string, 0)
		for id := range source {
			if !ids[id] {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		for _, id := range missing {
			slog.Warn("missing translation", "language", lang.Tag, "id", id)
		}
	}
}

// catalogMeta extracts the native language name and the key set of a file.
func catalogMeta(data []byte, fallback string) (string, map[string]bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Error("decode translation catalog", "err", err)
		return fallback, map[string]bool{}
	}
	ids := make(map[string]bool, len(raw))
	for id := range raw {
		ids[id] = true
	}

	name := fallback
	if entry, ok := raw["language.name"]; ok {
		var plain string
		if err := json.Unmarshal(entry, &plain); err == nil && plain != "" {
			name = plain
		} else {
			var plural map[string]string
			if err := json.Unmarshal(entry, &plural); err == nil && plural["other"] != "" {
				name = plural["other"]
			}
		}
	}
	return name, ids
}

// T translates id using the active language.
//
// Optional data is passed as template data, for example
// T("task.savedTo", map[string]string{"Path": p}).
func T(id string, data ...any) string {
	return localize(id, nil, data...)
}

// N translates id and selects the plural form for count. Unless explicit
// template data is supplied, "Count" is available to the message.
func N(id string, count int, data ...any) string {
	var templateData any = map[string]int{"Count": count}
	if len(data) > 0 && data[0] != nil {
		templateData = data[0]
	}
	return localize(id, &count, templateData)
}

func localize(id string, count *int, data ...any) string {
	var templateData any
	if len(data) > 0 {
		templateData = data[0]
	}
	msg := englishMessage(id)

	mu.RLock()
	loc := localizer
	mu.RUnlock()
	if loc == nil {
		return msg.Other
	}

	cfg := &i18n.LocalizeConfig{
		DefaultMessage: msg,
		TemplateData:   templateData,
	}
	if count != nil {
		cfg.PluralCount = *count
	}
	out, err := loc.Localize(cfg)
	if err != nil {
		slog.Error("translate message", "id", id, "err", err)
		if msg.Other != "" {
			return msg.Other
		}
		return id
	}
	return out
}

func englishMessage(id string) *i18n.Message {
	if msg, ok := defaults[id]; ok {
		return msg
	}
	return &i18n.Message{ID: id, Other: id}
}

// SetLanguage switches the active language and returns the concrete tag that
// ended up being used. Unsupported tags fall back to DefaultLanguage.
func SetLanguage(tag string) string {
	matched := Match(tag)
	mu.Lock()
	if bundle != nil {
		localizer = i18n.NewLocalizer(bundle, matched)
	}
	current = matched
	mu.Unlock()
	return matched
}

// Language returns the tag of the language currently in use.
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Languages lists the embedded catalogs in a stable order.
func Languages() []Locale {
	out := make([]Locale, len(languages))
	copy(out, languages)
	return out
}

// Match resolves tag against the available catalogs and falls back to
// DefaultLanguage when nothing reasonable matches.
func Match(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || strings.EqualFold(tag, "auto") {
		return DefaultLanguage
	}
	for _, available := range languages {
		if strings.EqualFold(available.Tag, tag) {
			return available.Tag
		}
	}
	if matcher == nil || len(tags) == 0 {
		return DefaultLanguage
	}
	parsed, err := language.Parse(tag)
	if err != nil {
		return DefaultLanguage
	}
	_, index, _ := matcher.Match(parsed)
	if index < 0 || index >= len(tags) {
		return DefaultLanguage
	}
	return tags[index].String()
}
