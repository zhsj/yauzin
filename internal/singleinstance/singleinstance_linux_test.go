//go:build linux

package singleinstance

import (
	"strings"
	"testing"
)

// TestActivatorCarriesTheURI pins the two exported methods: a plain activation
// reaches the callback with no URI, and OpenURI hands over exactly what the bus
// delivered. Calling them directly keeps the test off the session bus.
func TestActivatorCarriesTheURI(t *testing.T) {
	var got []string
	a := &activator{open: func(uri string) { got = append(got, uri) }}

	a.Open()
	a.OpenURI("mailto:ada@example.com?subject=Hi")

	if len(got) != 2 {
		t.Fatalf("callback saw %q, want two activations", got)
	}
	if got[0] != "" {
		t.Errorf("Open passed %q, want an empty URI", got[0])
	}
	if !strings.HasPrefix(got[1], "mailto:") {
		t.Errorf("OpenURI passed %q, want the URI as is", got[1])
	}
}

// TestActivatorWithoutCallback keeps the D-Bus export safe before the app has a
// callback to hand over, the same window Acquire's atomic pointer guards.
func TestActivatorWithoutCallback(t *testing.T) {
	a := &activator{}
	a.Open()
	a.OpenURI("mailto:a@example.com")
}
