package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Settings{Language: "zh-Hans", LogLevel: "debug", ActiveAccount: "gmail:user@example.com"}
	if err := saveTo(dir, want); err != nil {
		t.Fatalf("saveTo: %v", err)
	}
	got, err := loadFrom(dir)
	if err != nil {
		t.Fatalf("loadFrom: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSaveActiveAccountJSONKey(t *testing.T) {
	dir := t.TempDir()
	if err := saveTo(dir, Settings{ActiveAccount: "gmail:user@example.com", Proxy: "socks5://127.0.0.1:1080"}); err != nil {
		t.Fatalf("saveTo: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := raw["activeAccount"]; got != "gmail:user@example.com" {
		t.Fatalf("activeAccount = %v, want the stored account ID", got)
	}
	if _, ok := raw["proxy"]; !ok {
		t.Fatalf("proxy was dropped from the document: %s", data)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	dir := t.TempDir()
	if err := saveTo(dir, Settings{Language: "en"}); err != nil {
		t.Fatalf("saveTo: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadCorruptFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadFrom(dir); err == nil {
		t.Fatal("loadFrom accepted corrupt JSON")
	}
}

func TestConfigDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)
	if got := Dir(); got != dir {
		t.Fatalf("Dir = %q, want %q", got, dir)
	}
	if err := Save(Settings{Language: "en"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadOrZero()
	if err != nil {
		t.Fatalf("LoadOrZero: %v", err)
	}
	if got.Language != "en" {
		t.Fatalf("LoadOrZero = %+v, want language en", got)
	}
}

func TestLoadOrZeroMissingFile(t *testing.T) {
	t.Setenv("YAUZIN_CONFIG_DIR", t.TempDir())
	got, err := LoadOrZero()
	if err != nil {
		t.Fatalf("a missing settings file is the first run, got error %v", err)
	}
	if got != (Settings{}) {
		t.Fatalf("got %+v, want the zero value", got)
	}
}

func TestLoadOrZeroReportsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadOrZero(); err == nil {
		t.Fatal("LoadOrZero hid a corrupt settings file")
	}
}

func TestLoadOrZeroReportsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAUZIN_CONFIG_DIR", dir)
	// A directory where the settings file is expected cannot be read, which
	// works even when the tests run as root.
	if err := os.Mkdir(filepath.Join(dir, fileName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := LoadOrZero(); err == nil {
		t.Fatal("LoadOrZero hid an unreadable settings file")
	}
}
