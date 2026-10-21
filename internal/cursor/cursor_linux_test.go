//go:build linux

package cursor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// envMap turns a map into the lookup function Resolve expects.
func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// mkTheme creates a fake icon theme with cursor images.
func mkTheme(t *testing.T, iconsDir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(iconsDir, name, "cursors"), 0o755); err != nil {
		t.Fatalf("create theme %q: %v", name, err)
	}
}

// stubLookup replaces one source and reports how often it was consulted.
func stubLookup(t *testing.T, target *func(context.Context) (Theme, bool), th Theme, ok bool) *int {
	t.Helper()
	calls := 0
	prev := *target
	*target = func(context.Context) (Theme, bool) {
		calls++
		return th, ok
	}
	t.Cleanup(func() { *target = prev })
	return &calls
}

// stubPortal replaces the D-Bus portal round trip.
func stubPortal(t *testing.T, th Theme, ok bool) *int {
	t.Helper()
	return stubLookup(t, &portalLookup, th, ok)
}

// stubKDE replaces the kreadconfig invocation.
func stubKDE(t *testing.T, th Theme, ok bool) *int {
	t.Helper()
	return stubLookup(t, &kdeLookup, th, ok)
}

// stubGNOME replaces the gsettings invocation.
func stubGNOME(t *testing.T, th Theme, ok bool) *int {
	t.Helper()
	return stubLookup(t, &gnomeLookup, th, ok)
}

func TestResolvePrefersEnvironment(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "EnvCursor")
	portal := stubPortal(t, Theme{Name: "PortalCursor", Size: 24}, true)

	env := envMap(map[string]string{
		"XDG_DATA_HOME": data,
		"XCURSOR_THEME": "EnvCursor",
		"XCURSOR_SIZE":  "36",
	})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("Resolve reported no theme")
	}
	if th.Name != "EnvCursor" || th.Size != 36 || th.Source != "environment" {
		t.Fatalf("unexpected theme: %+v", th)
	}
	if *portal != 0 {
		t.Fatalf("portal consulted %d times for an explicit theme", *portal)
	}
}

func TestResolveFallsThroughMissingEnvironmentTheme(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "PortalCursor")
	stubPortal(t, Theme{Name: "PortalCursor", Size: 24}, true)

	env := envMap(map[string]string{
		"XDG_DATA_HOME": data,
		"XCURSOR_THEME": "NotInstalled",
	})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("Resolve reported no theme")
	}
	if th.Name != "PortalCursor" || th.Size != 24 || th.Source != "desktop portal" {
		t.Fatalf("unexpected theme: %+v", th)
	}
}

func TestResolveRejectsPortalThemeMissingOnDisk(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "KdeCursor")
	stubPortal(t, Theme{Name: "Ghost", Size: 20}, true)
	stubKDE(t, Theme{Name: "KdeCursor", Size: 28}, true)
	stubGNOME(t, Theme{}, false)

	env := envMap(map[string]string{"XDG_DATA_HOME": data})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("Resolve reported no theme")
	}
	if th.Name != "KdeCursor" || th.Size != 28 || th.Source != "KDE configuration" {
		t.Fatalf("unexpected theme: %+v", th)
	}
}

func TestResolveKDEConfiguration(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "KdeCursor")
	stubPortal(t, Theme{}, false)
	stubKDE(t, Theme{Name: "KdeCursor", Size: 28}, true)
	gnome := stubGNOME(t, Theme{}, false)

	env := envMap(map[string]string{"XDG_DATA_HOME": data})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("Resolve reported no theme")
	}
	if th.Name != "KdeCursor" || th.Size != 28 || th.Source != "KDE configuration" {
		t.Fatalf("unexpected theme: %+v", th)
	}
	if *gnome != 0 {
		t.Fatalf("gsettings consulted %d times after a KDE hit, want 0", *gnome)
	}
}

func TestResolveFallsBackToGNOMESettings(t *testing.T) {
	home := t.TempDir()
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "GnomeCursor")
	portal := stubPortal(t, Theme{}, false)
	kde := stubKDE(t, Theme{}, false)
	stubGNOME(t, Theme{Name: "GnomeCursor", Size: 24}, true)

	env := envMap(map[string]string{"XDG_DATA_HOME": data})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("Resolve reported no theme")
	}
	if th.Name != "GnomeCursor" || th.Size != 24 || th.Source != "GNOME settings" {
		t.Fatalf("unexpected theme: %+v", th)
	}
	if *portal != 1 || *kde != 1 {
		t.Fatalf("portal/kde consulted %d/%d times, want 1/1", *portal, *kde)
	}
}

func TestResolveEnvironmentSizeOnly(t *testing.T) {
	home := t.TempDir()
	stubPortal(t, Theme{}, false)
	stubKDE(t, Theme{}, false)
	stubGNOME(t, Theme{}, false)

	env := envMap(map[string]string{"XCURSOR_SIZE": "48"})
	th, ok := Resolve(context.Background(), env, home)
	if !ok {
		t.Fatal("a size alone should be reported")
	}
	if th.Name != "" || th.Size != 48 {
		t.Fatalf("unexpected theme: %+v", th)
	}
}

func TestResolveNothing(t *testing.T) {
	stubPortal(t, Theme{}, false)
	stubKDE(t, Theme{}, false)
	stubGNOME(t, Theme{}, false)
	if th, ok := Resolve(context.Background(), envMap(nil), t.TempDir()); ok {
		t.Fatalf("expected no theme, got %+v", th)
	}
}

func TestResolveUsesXCursorPath(t *testing.T) {
	home := t.TempDir()
	mkTheme(t, filepath.Join(home, "icons"), "HomeCursor")
	stubPortal(t, Theme{Name: "HomeCursor"}, true)

	env := envMap(map[string]string{"XCURSOR_PATH": "~/icons"})
	th, ok := Resolve(context.Background(), env, home)
	if !ok || th.Name != "HomeCursor" {
		t.Fatalf("unexpected theme: %+v ok=%v", th, ok)
	}
}

func TestTrimValue(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", ""},
		{" 'Adwaita' ", "Adwaita"},
		{`breeze_cursors`, "breeze_cursors"},
		{`"Breeze Cursors"`, "Breeze Cursors"},
		{`'unbalanced"`, `'unbalanced"`},
	} {
		if got := trimValue(tc.raw); got != tc.want {
			t.Errorf("trimValue(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 0},
		{" 24 ", 24},
		{"0", 0},
		{"-4", 0},
		{"abc", 0},
		{"1024", 0},
		{"48", 48},
	} {
		if got := parseSize(tc.raw); got != tc.want {
			t.Errorf("parseSize(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestIntValue(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int
	}{
		{int32(24), 24},
		{uint32(32), 32},
		{int64(48), 48},
		{float64(16), 16},
		{"24", 0},
		{nil, 0},
		{int32(0), 0},
	} {
		if got := intValue(tc.in); got != tc.want {
			t.Errorf("intValue(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestThemeExists(t *testing.T) {
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "Present")
	env := envMap(map[string]string{"XDG_DATA_HOME": data})

	if !themeExists("Present", env, t.TempDir()) {
		t.Fatal("existing theme not found")
	}
	// "." and ".." contain no separator but still escape the icon directory
	// when they are joined with one.
	for _, name := range []string{"", ".", "..", "Absent", "../escape"} {
		if themeExists(name, env, t.TempDir()) {
			t.Fatalf("theme %q must not exist", name)
		}
	}
}

// resetOwnExports clears the record of what Apply exported, so tests do not
// inherit it from each other.
func resetOwnExports(t *testing.T) {
	t.Helper()
	lastExport.mu.Lock()
	lastExport.theme, lastExport.size = "", ""
	lastExport.mu.Unlock()
	t.Cleanup(func() {
		lastExport.mu.Lock()
		lastExport.theme, lastExport.size = "", ""
		lastExport.mu.Unlock()
	})
}

func TestHideOwnExports(t *testing.T) {
	lastExport.mu.Lock()
	lastExport.theme, lastExport.size = "Breeze", "24"
	lastExport.mu.Unlock()
	t.Cleanup(func() {
		lastExport.mu.Lock()
		lastExport.theme, lastExport.size = "", ""
		lastExport.mu.Unlock()
	})

	env := envMap(map[string]string{
		envTheme:        "Breeze",
		envSize:         "32",
		"XDG_DATA_HOME": "/data",
	})
	lookup := hideOwnExports(env)
	if got := lookup(envTheme); got != "" {
		t.Fatalf("exported theme read back as %q, want empty", got)
	}
	if got := lookup(envSize); got != "32" {
		t.Fatalf("a different size must survive, got %q", got)
	}
	if got := lookup("XDG_DATA_HOME"); got != "/data" {
		t.Fatalf("unrelated variable changed: %q", got)
	}
}

func TestApplyReResolvesAfterExport(t *testing.T) {
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "ApplyCursor")
	calls := stubPortal(t, Theme{Name: "ApplyCursor", Size: 24}, true)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(envTheme, "")
	t.Setenv(envSize, "")
	resetOwnExports(t)

	Apply(context.Background())
	Apply(context.Background())

	if *calls != 2 {
		t.Fatalf("portal consulted %d times, want 2", *calls)
	}
	if got := os.Getenv(envTheme); got != "ApplyCursor" {
		t.Fatalf("exported theme = %q, want ApplyCursor", got)
	}
	if got := os.Getenv(envSize); got != "24" {
		t.Fatalf("exported size = %q, want 24", got)
	}
}

func TestApplyClearsExportedSizeWhenResolutionHasNone(t *testing.T) {
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "SizeCursor")
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(envTheme, "")
	t.Setenv(envSize, "")
	resetOwnExports(t)
	t.Cleanup(func() {
		_ = os.Unsetenv(envTheme)
		_ = os.Unsetenv(envSize)
	})

	stubPortal(t, Theme{Name: "SizeCursor", Size: 24}, true)
	Apply(context.Background())
	if got := os.Getenv(envSize); got != "24" {
		t.Fatalf("exported size = %q, want 24", got)
	}

	// The theme is still there but the desktop no longer reports a size.
	stubPortal(t, Theme{Name: "SizeCursor"}, true)
	Apply(context.Background())
	if got := os.Getenv(envSize); got != "" {
		t.Fatalf("exported size %q outlived the resolution, want it cleared", got)
	}

	// A leftover size would be read back as an explicit user setting and the
	// portal would never be consulted again.
	calls := stubPortal(t, Theme{Name: "SizeCursor"}, true)
	Apply(context.Background())
	if *calls != 1 {
		t.Fatalf("portal consulted %d times, want 1", *calls)
	}
}

func TestApplyKeepsExplicitEnvironment(t *testing.T) {
	data := t.TempDir()
	mkTheme(t, filepath.Join(data, "icons"), "UserCursor")
	calls := stubPortal(t, Theme{Name: "PortalCursor", Size: 24}, true)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv(envTheme, "UserCursor")
	t.Setenv(envSize, "32")
	resetOwnExports(t)

	Apply(context.Background())

	if *calls != 0 {
		t.Fatalf("portal consulted %d times for an explicit theme", *calls)
	}
	if got := os.Getenv(envTheme); got != "UserCursor" {
		t.Fatalf("explicit theme overwritten with %q", got)
	}
	if got := os.Getenv(envSize); got != "32" {
		t.Fatalf("explicit size overwritten with %q", got)
	}
}
