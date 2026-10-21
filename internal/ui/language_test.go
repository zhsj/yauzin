package ui

import (
	"testing"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/prefs"
)

// TestSetLanguageAppliesImmediately covers the live language switch: the views
// that cache their labels are rebuilt, the settings panel is redrawn in the new
// language, and the choice is persisted for the next start.
func TestSetLanguageAppliesImmediately(t *testing.T) {
	t.Cleanup(func() { i18n.SetLanguage(i18n.DefaultLanguage) })
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())

	a := testApp(t)
	a.folders = []mail.Folder{{ID: "inbox", Name: "Inbox", Kind: mail.FolderInbox}}
	a.side.reload()
	a.settings = newSettingsView(a)
	a.overlay = a.settings

	a.setLanguage("zh-Hans")

	if got := i18n.Language(); got != "zh-Hans" {
		t.Fatalf("language after the switch = %q, want zh-Hans", got)
	}
	if got, want := a.side.compose.label, "写邮件"; got != want {
		t.Fatalf("compose pill label = %q, want %q", got, want)
	}
	if len(a.side.folders) == 0 || a.side.folders[0].label != "收件箱" {
		t.Fatalf("folder labels were not rebuilt: %+v", a.side.folders)
	}
	saved, err := prefs.LoadOrZero()
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if got := saved.Language; got != "zh-Hans" {
		t.Fatalf("saved language = %q, want zh-Hans", got)
	}

	active := map[string]bool{}
	for _, row := range a.settings.langRows {
		active[row.title] = row.active
	}
	if !active["简体中文"] || active["English"] {
		t.Fatalf("the check mark did not follow the new language: %v", active)
	}
}
