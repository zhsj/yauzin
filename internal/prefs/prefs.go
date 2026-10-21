// Package prefs stores the small amount of user configuration the UI keeps
// between runs. The file is deliberately plain JSON so it can be inspected and
// edited by hand; account credentials will live elsewhere once real backends
// are added.
package prefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Settings is the persisted UI state.
type Settings struct {
	// Language holds the interface language tag, or "auto" to follow the
	// system locale.
	Language string `json:"language,omitempty"`
	// ClientDecorations draws the window title bar in the application instead
	// of leaving it to the window manager. It is needed on window managers that
	// hide their frame without telling the client (KWin answers server-side
	// decoration for its "no border" rule).
	ClientDecorations bool `json:"clientDecorations,omitempty"`
	// ShowPerfMonitor displays the compact CPU/memory/goroutine/FPS readout in
	// the top bar. Sampling only runs while it is visible.
	ShowPerfMonitor bool `json:"showPerfMonitor,omitempty"`
}

const (
	appDirName = "yauzin"
	fileName   = "settings.json"
)

// LoadOrZero reads the settings from the user config directory. A missing file
// is the first run and yields the zero value with no error, so a bad file never
// prevents the application from starting. Anything else (permissions, a broken
// JSON document) is reported: the caller decides whether to warn, but it must
// not write the zero value back over settings it failed to read.
func LoadOrZero() (Settings, error) {
	s, err := loadFrom(configDir())
	if err != nil {
		if IsNotExist(err) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	return s, nil
}

// Save writes the settings atomically with owner-only permissions.
func Save(s Settings) error {
	return saveTo(configDir(), s)
}

// configDir returns the directory holding settings.json. YAUZIN_CONFIG_DIR
// overrides the platform default, which is useful for portable setups and
// tests.
func configDir() string {
	if dir := os.Getenv("YAUZIN_CONFIG_DIR"); dir != "" {
		return dir
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return "." + appDirName
	}
	return filepath.Join(base, appDirName)
}

func loadFrom(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("parse settings: %w", err)
	}
	return s, nil
}

func saveTo(dir string, s Settings) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create settings temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Remove is a no-op after a successful rename.
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod settings: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, fileName)); err != nil {
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

// IsNotExist reports whether err is a missing settings file.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
