package ui

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeSource(t *testing.T, dir string, payload []byte) string {
	t.Helper()
	src := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return src
}

func TestCopyIntoDirNaming(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("attachment payload")
	src := writeSource(t, dir, payload)

	want := []string{"report.txt", "report (1).txt", "report (2).txt"}
	for _, name := range want {
		got, err := copyIntoDir(src, dir, "report.txt")
		if err != nil {
			t.Fatalf("copyIntoDir: %v", err)
		}
		if filepath.Base(got) != name {
			t.Fatalf("copy %q landed at %q, want %q", "report.txt", filepath.Base(got), name)
		}
		if filepath.Dir(got) != dir {
			t.Fatalf("copy landed outside %q: %q", dir, got)
		}
		data, err := os.ReadFile(got)
		if err != nil {
			t.Fatalf("read %q: %v", got, err)
		}
		if !bytes.Equal(data, payload) {
			t.Fatalf("%q holds %q, want %q", got, data, payload)
		}
	}
}

func TestCopyIntoDirConcurrent(t *testing.T) {
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("yauzin"), 16*1024)
	src := writeSource(t, dir, payload)

	const workers = 20
	paths := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = copyIntoDir(src, dir, "shared.bin")
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool, workers)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if seen[paths[i]] {
			t.Fatalf("two workers shared the path %q", paths[i])
		}
		seen[paths[i]] = true
		data, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatalf("read %q: %v", paths[i], err)
		}
		if !bytes.Equal(data, payload) {
			t.Fatalf("%q is %d bytes, want %d: a concurrent save truncated it",
				paths[i], len(data), len(payload))
		}
	}
	if len(seen) != workers {
		t.Fatalf("%d distinct paths for %d saves", len(seen), workers)
	}
}

func TestCopyIntoDirRejectsPathTraversal(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := writeSource(t, parent, []byte("payload"))

	for _, name := range []string{"../../escape.txt", "/etc/passwd", "..", "."} {
		got, err := copyIntoDir(src, dir, name)
		if err != nil {
			t.Fatalf("copyIntoDir(%q): %v", name, err)
		}
		if filepath.Dir(got) != dir {
			t.Fatalf("copyIntoDir(%q) escaped to %q", name, got)
		}
	}
}

func TestCopyIntoDirMissingSource(t *testing.T) {
	dir := t.TempDir()
	if _, err := copyIntoDir(filepath.Join(dir, "absent.bin"), dir, "out.bin"); err == nil {
		t.Fatal("copying a missing file succeeded")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Fatalf("failed copy left %d files behind", len(entries))
	}
}

func TestCopyFileExclusiveKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, []byte("new content"))
	dst := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(dst, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	err := copyFileExclusive(src, dst)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("copyFileExclusive returned %v, want fs.ErrExist", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if strings.TrimSpace(string(data)) != "original" {
		t.Fatalf("existing file was overwritten with %q", data)
	}
}
