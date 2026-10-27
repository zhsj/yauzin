package tray

import (
	"strings"
	"testing"
)

// fakeBackend records what the shell forwards. It keeps the shell tests free of
// a bus.
type fakeBackend struct {
	active  bool
	title   string
	tooltip string
	open    string
	quit    string
	closed  int
}

func (f *fakeBackend) setActive(active bool) { f.active = active }

func (f *fakeBackend) setTitle(title string) { f.title = title }

func (f *fakeBackend) setTooltip(text string) { f.tooltip = text }

func (f *fakeBackend) setMenuLabels(open, quit string) {
	f.open, f.quit = open, quit
}

func (f *fakeBackend) close() { f.closed++ }

func TestNilTrayIsSafe(t *testing.T) {
	var tr *Tray
	tr.SetActive(true)
	tr.Relabel("Yauzin")
	tr.Close()
}

func TestZeroTrayIsSafe(t *testing.T) {
	tr := &Tray{}
	tr.SetActive(true)
	tr.Relabel("Yauzin")
	tr.Close()
}

func TestSetActiveForwards(t *testing.T) {
	b := &fakeBackend{}
	tr := &Tray{backend: b}
	tr.SetActive(true)
	if !b.active {
		t.Fatal("active was not forwarded")
	}
	tr.SetActive(false)
	if b.active {
		t.Fatal("inactive was not forwarded")
	}
}

func TestRelabelForwards(t *testing.T) {
	b := &fakeBackend{}
	tr := &Tray{backend: b}
	tr.Relabel("Yauzin")
	if b.title != "Yauzin" {
		t.Fatalf("title = %q, want Yauzin", b.title)
	}
	if !strings.Contains(b.tooltip, "Yauzin") {
		t.Fatalf("tooltip = %q, want it to name the app", b.tooltip)
	}
	if b.open == "" || b.quit == "" {
		t.Fatalf("menu labels = %q, %q, want both set", b.open, b.quit)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	b := &fakeBackend{}
	tr := &Tray{backend: b}
	tr.Close()
	tr.Close()
	if b.closed != 1 {
		t.Fatalf("close calls = %d, want 1", b.closed)
	}
}
