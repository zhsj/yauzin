//go:build linux

// Package cursor resolves the cursor theme configured in the desktop
// environment and exports it through the environment variables the Gio
// backends read.
//
// Wayland has no protocol for asking the compositor which cursor theme is in
// use: toolkits either load the theme by name (Gio does this, reading
// XCURSOR_THEME/XCURSOR_SIZE) or let the compositor draw the cursor through
// wp_cursor_shape_v1, which Gio does not implement. Copying those two variables
// is therefore the only way to make a Gio application follow the desktop
// setting.
package cursor

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	// envTheme and envSize are the X11 Xcursor variables. Gio reads them on
	// both Linux backends: os_wayland.go loads the theme with them and
	// os_x11.go passes them to XcursorLibraryLoadCursor.
	envTheme = "XCURSOR_THEME"
	envSize  = "XCURSOR_SIZE"

	portalDest  = "org.freedesktop.portal.Desktop"
	portalPath  = "/org/freedesktop/portal/desktop"
	portalIface = "org.freedesktop.portal.Settings"

	// gnomeSchema is the GSettings schema holding the cursor keys and the
	// portal namespace mirroring it. xdg-desktop-portal-gtk implements that
	// namespace by forwarding dconf; the KDE portal registers only
	// org.freedesktop.appearance and org.kde.*, so the portal lookup fails
	// there and the KDE source below takes over.
	gnomeSchema     = "org.gnome.desktop.interface"
	portalNamespace = gnomeSchema

	// portalTimeout bounds the D-Bus round trip so a stalled portal can never
	// delay the first window.
	portalTimeout = 750 * time.Millisecond

	// commandTimeout bounds the kreadconfig and gsettings invocations that
	// stand in for the portal on KDE and GNOME.
	commandTimeout = 750 * time.Millisecond

	// maxCursorSize rejects obviously bogus values from a broken config.
	maxCursorSize = 512
)

// Theme is a cursor theme resolved from the desktop environment.
type Theme struct {
	// Name is the icon theme directory holding the cursor images.
	Name string
	// Size is the nominal cursor size in pixels, or zero when unknown.
	Size int
	// Source names the origin of the values, for logging only.
	Source string
}

// The sources are variables so tests can replace the D-Bus round trip and the
// process spawns without depending on what the machine has installed.
var (
	portalLookup = portalTheme
	kdeLookup    = kdeTheme
	gnomeLookup  = gnomeTheme
)

// lastExport remembers the values Apply wrote into the environment. A later
// call has to tell them apart from an explicit user setting: without that the
// exported theme would shadow the desktop settings, be reported as
// "environment" and never follow a theme change made while the app sits in the
// tray.
var lastExport struct {
	mu          sync.Mutex
	theme, size string
}

// Apply resolves the session cursor theme and exports XCURSOR_THEME and
// XCURSOR_SIZE for the current process. It is safe to call before every window
// creation: Gio reads the variables when it creates a window, so a theme change
// is picked up by the next window. Apply never fails the application.
func Apply(ctx context.Context) {
	home, _ := os.UserHomeDir()
	th, ok := Resolve(ctx, hideOwnExports(os.Getenv), home)
	if !ok {
		return
	}
	if th.Name != "" {
		if err := os.Setenv(envTheme, th.Name); err != nil {
			slog.Warn("export cursor theme", "theme", th.Name, "err", err)
			return
		}
	}
	if th.Size > 0 {
		if err := os.Setenv(envSize, strconv.Itoa(th.Size)); err != nil {
			slog.Warn("export cursor size", "size", th.Size, "err", err)
			return
		}
	} else if ownExport(envSize) != "" {
		// A size this package exported earlier must not outlive the resolution
		// that produced it: the next Apply would read it back as an explicit
		// user setting and never follow the desktop again.
		if err := os.Unsetenv(envSize); err != nil {
			slog.Warn("clear exported cursor size", "err", err)
		}
	}
	rememberExport(th)
	// Logged once at startup and again if the window is rebuilt, which is the
	// only way to tell where a surprising cursor came from.
	slog.Info("cursor theme resolved", "theme", th.Name, "size", th.Size, "source", th.Source)
}

// hideOwnExports wraps env so values this package exported read as unset. A
// user who explicitly sets the same value is indistinguishable from an exported
// one, which is harmless: resolving again yields the same theme.
func hideOwnExports(env func(string) string) func(string) string {
	return func(key string) string {
		value := env(key)
		if value == "" {
			return ""
		}
		lastExport.mu.Lock()
		own := lastExport.theme
		if key == envSize {
			own = lastExport.size
		}
		lastExport.mu.Unlock()
		if value == own {
			return ""
		}
		return value
	}
}

// ownExport returns the value this package last exported for key.
func ownExport(key string) string {
	lastExport.mu.Lock()
	defer lastExport.mu.Unlock()
	if key == envSize {
		return lastExport.size
	}
	return lastExport.theme
}

// rememberExport records what Apply just exported.
func rememberExport(th Theme) {
	lastExport.mu.Lock()
	lastExport.theme, lastExport.size = th.Name, ""
	if th.Size > 0 {
		lastExport.size = strconv.Itoa(th.Size)
	}
	lastExport.mu.Unlock()
}

// Resolve returns the cursor theme for the session described by env and home.
// Sources are consulted in order: the environment, the XDG desktop portal,
// KDE's kreadconfig and finally GNOME's gsettings. A candidate is only returned
// when its theme directory exists, because a theme that cannot be loaded makes
// the Wayland backend fail to create the window. ok is false when nothing
// usable was found.
func Resolve(ctx context.Context, env func(string) string, home string) (Theme, bool) {
	size := parseSize(env(envSize))

	if name := strings.TrimSpace(env(envTheme)); name != "" {
		if themeExists(name, env, home) {
			return Theme{Name: name, Size: size, Source: "environment"}, true
		}
		slog.Warn("ignoring XCURSOR_THEME naming a missing theme", "theme", name)
	}

	sources := []struct {
		name  string
		theme func() (Theme, bool)
	}{
		{"desktop portal", func() (Theme, bool) { return portalLookup(ctx) }},
		{"KDE configuration", func() (Theme, bool) { return kdeLookup(ctx) }},
		{"GNOME settings", func() (Theme, bool) { return gnomeLookup(ctx) }},
	}
	for _, src := range sources {
		th, ok := src.theme()
		if !ok {
			continue
		}
		if !themeExists(th.Name, env, home) {
			slog.Debug("cursor theme not installed", "theme", th.Name, "source", src.name)
			continue
		}
		// An explicit XCURSOR_SIZE wins over the desktop setting.
		if size == 0 {
			size = th.Size
		}
		th.Size = size
		th.Source = src.name
		return th, true
	}

	// A size without a theme can still be exported: Gio keeps its default
	// theme and only the size changes.
	if size > 0 {
		return Theme{Size: size, Source: "environment"}, true
	}
	return Theme{}, false
}

// portalTheme reads cursor-theme and cursor-size from the XDG desktop portal.
func portalTheme(ctx context.Context) (Theme, bool) {
	conn, err := dbus.SessionBus()
	if err != nil {
		slog.Debug("cursor theme: no session bus", "err", err)
		return Theme{}, false
	}
	obj := conn.Object(portalDest, portalPath)
	ctx, cancel := context.WithTimeout(ctx, portalTimeout)
	defer cancel()

	// ReadAll fetches the whole namespace in one round trip and keeps the
	// values one variant deep. It needs portal Settings version 2.
	var all map[string]map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, portalIface+".ReadAll", 0, []string{portalNamespace}).Store(&all); err == nil {
		if ns := all[portalNamespace]; ns != nil {
			th := Theme{Name: stringValue(ns["cursor-theme"]), Size: intValue(ns["cursor-size"])}
			if th.Name != "" || th.Size > 0 {
				return th, true
			}
		}
	}

	// Older portals only implement Read. Its reply is a variant that may wrap
	// another variant.
	name, okName := portalRead(ctx, obj, "cursor-theme")
	size, okSize := portalRead(ctx, obj, "cursor-size")
	th := Theme{Name: stringValue(name), Size: intValue(size)}
	if !okName && !okSize {
		return Theme{}, false
	}
	if th.Name == "" && th.Size == 0 {
		return Theme{}, false
	}
	return th, true
}

// portalRead reads one settings key through org.freedesktop.portal.Settings.
func portalRead(ctx context.Context, obj dbus.BusObject, key string) (any, bool) {
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, portalIface+".Read", 0, portalNamespace, key).Store(&v); err != nil {
		slog.Debug("cursor theme: portal read failed", "key", key, "err", err)
		return nil, false
	}
	return unwrapVariant(v), true
}

// unwrapVariant flattens the nested variants the portal returns.
func unwrapVariant(v dbus.Variant) any {
	val := v.Value()
	for {
		inner, ok := val.(dbus.Variant)
		if !ok {
			return val
		}
		val = inner.Value()
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// intValue converts the integer types the portal can return.
func intValue(v any) int {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int32:
		n = int64(x)
	case int64:
		n = x
	case uint32:
		n = int64(x)
	case uint64:
		n = int64(x)
	case float64:
		n = int64(x)
	default:
		return 0
	}
	return clampSize(int(n))
}

// parseSize parses a cursor size from the text a settings tool prints.
func parseSize(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return clampSize(n)
}

// clampSize rejects sizes a cursor theme cannot have.
func clampSize(n int) int {
	if n < 1 || n > maxCursorSize {
		return 0
	}
	return n
}

// kdeTheme reads the KDE cursor setting through kreadconfig, which opens
// kcminputrc the way KWin does: KConfig cascades to the system-wide
// configuration, so the effective value may live in $XDG_CONFIG_DIRS rather
// than in the user file. KDE writes neither key until the user changes the
// cursor, which is why a plain file read found nothing.
func kdeTheme(ctx context.Context) (Theme, bool) {
	return themeFromCommands(ctx, []string{"kreadconfig6", "kreadconfig5"},
		[]string{"--file", "kcminputrc", "--group", "Mouse", "--key", "cursorTheme"},
		[]string{"--file", "kcminputrc", "--group", "Mouse", "--key", "cursorSize"})
}

// gnomeTheme reads the GNOME cursor keys through gsettings. It is the last
// source because gsettings answers with the schema default (Adwaita) even on a
// desktop that never touched dconf.
func gnomeTheme(ctx context.Context) (Theme, bool) {
	return themeFromCommands(ctx, []string{"gsettings"},
		[]string{"get", gnomeSchema, "cursor-theme"},
		[]string{"get", gnomeSchema, "cursor-size"})
}

// themeFromCommands reads a theme name and size by running the first installed
// binary of bins twice, once per key. Both calls share one deadline.
func themeFromCommands(ctx context.Context, bins, nameArgs, sizeArgs []string) (Theme, bool) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	th := Theme{
		Name: trimValue(readSetting(ctx, bins, nameArgs...)),
		Size: parseSize(readSetting(ctx, bins, sizeArgs...)),
	}
	if th.Name == "" && th.Size == 0 {
		return Theme{}, false
	}
	return th, true
}

// readSetting runs the first installed binary of bins and returns the trimmed
// first line of its output. A missing binary, a failing command or an expired
// deadline yields an empty string: the caller then treats the key as unset and
// Resolve falls through to the next source. Arguments are fixed constants, so
// no shell takes part in the call.
func readSetting(ctx context.Context, bins []string, args ...string) string {
	for _, bin := range bins {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, bin, args...).Output()
		if err != nil {
			slog.Debug("cursor setting: command failed", "command", bin, "err", err)
			continue
		}
		line, _, _ := strings.Cut(string(out), "\n")
		return line
	}
	return ""
}

// trimValue strips the whitespace and the matching quotes around a value:
// gsettings prints strings as 'Adwaita' and ini files may quote values too.
func trimValue(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) >= 2 {
		if q := value[0]; (q == '"' || q == '\'') && value[len(value)-1] == q {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

// themeExists reports whether name is an icon theme with cursor images, which
// is what wl_cursor_theme_load needs.
func themeExists(name string, env func(string) string, home string) bool {
	// A dot component contains no separator but still walks out of the icon
	// directory when it is joined with one.
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, os.PathSeparator) {
		return false
	}
	for _, dir := range iconDirs(env, home) {
		info, err := os.Stat(filepath.Join(dir, name, "cursors"))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// iconDirs lists the directories libXcursor and libwayland-cursor search, in
// their order of preference.
func iconDirs(env func(string) string, home string) []string {
	var dirs []string
	for _, dir := range filepath.SplitList(env("XCURSOR_PATH")) {
		if dir = expandHome(dir, home); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	if dir := env("XDG_DATA_HOME"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "icons"))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".icons"), filepath.Join(home, ".local", "share", "icons"))
	}
	dataDirs := env("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dir := range filepath.SplitList(dataDirs) {
		if dir != "" {
			dirs = append(dirs, filepath.Join(dir, "icons"))
		}
	}
	dirs = append(dirs, "/usr/share/icons", "/usr/local/share/icons")
	return dirs
}

// expandHome resolves the "~/" prefix XCURSOR_PATH entries may carry.
func expandHome(dir, home string) string {
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") && home != "" {
		return filepath.Join(home, dir[2:])
	}
	return dir
}
