package ui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestTempDirUsesRuntimeDir pins the location: the scratch directory lives in
// the private runtime directory of the session, created owner-only, and the
// lazy creation reuses it.
func TestTempDirUsesRuntimeDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)

	a := &App{}
	t.Cleanup(a.cleanupTemp)

	dir, err := a.tempDir()
	if err != nil {
		t.Fatalf("tempDir: %v", err)
	}
	if want := filepath.Join(base, runtimeDirName); dir != want {
		t.Fatalf("tempDir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o, want 700", info.Mode().Perm())
	}
	if again, err := a.tempDir(); err != nil || again != dir {
		t.Fatalf("second tempDir = %q, %v; want %q", again, err, dir)
	}
}

// TestTempDirDropsLeftovers checks that a directory left by a killed run is
// not reused: the files inside belong to no draft of this process.
func TestTempDirDropsLeftovers(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)
	stale := filepath.Join(base, runtimeDirName)
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "staged.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	a := &App{}
	t.Cleanup(a.cleanupTemp)
	dir, err := a.tempDir()
	if err != nil {
		t.Fatalf("tempDir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("kept stale entries: %v", entries)
	}
}

// TestTempDirFallsBackWithoutRuntimeDir covers the unusable cases: without a
// private session directory the scratch files go to the system temp directory.
func TestTempDirFallsBackWithoutRuntimeDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct{ name, value string }{
		{"unset", ""},
		{"relative", "relative-runtime-dir"},
		{"missing", missing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", tt.value)
			a := &App{}
			t.Cleanup(a.cleanupTemp)
			dir, err := a.tempDir()
			if err != nil {
				t.Fatalf("tempDir: %v", err)
			}
			if !strings.HasPrefix(filepath.Base(dir), "yauzin-mail-") {
				t.Fatalf("dir = %q, want a system temp directory", dir)
			}
			if got, want := filepath.Dir(dir), filepath.Clean(os.TempDir()); got != want {
				t.Fatalf("parent = %q, want %q", got, want)
			}
		})
	}
}

// TestCleanupTempIsFinal covers the exit path: the directory is removed, the
// call is repeatable, and the directory cannot come back afterwards.
func TestCleanupTempIsFinal(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)

	a := &App{}
	dir, err := a.tempDir()
	if err != nil {
		t.Fatalf("tempDir: %v", err)
	}
	a.cleanupTemp()
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat after cleanup = %v, want the directory gone", err)
	}
	a.cleanupTemp()
	if got, err := a.tempDir(); !errors.Is(err, errTempClosed) || got != "" {
		t.Fatalf("tempDir after cleanup = %q, %v; want errTempClosed", got, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tempDir recreated %q: %v", dir, err)
	}
}

// TestTempDirConcurrentCleanup exercises the race the signal handler creates:
// the UI goroutine may ask for the directory while the process is leaving, and
// the cleanup must win.
func TestTempDirConcurrentCleanup(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", base)

	a := &App{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if dir, err := a.tempDir(); err == nil && dir == "" {
				t.Error("tempDir returned an empty path without an error")
			}
		}()
	}
	a.cleanupTemp()
	wg.Wait()

	if _, err := os.Stat(filepath.Join(base, runtimeDirName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the directory survived cleanup: %v", err)
	}
}
