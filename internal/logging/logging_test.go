package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", LevelInfo},
		{"info", LevelInfo},
		{"DEBUG", LevelDebug},
		{" debug ", LevelDebug},
		{"Error", LevelError},
		{"warn", LevelWarn},
		{"warning", LevelInfo},
		{"verbose", LevelInfo},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSetLevelReturnsNormalizedName(t *testing.T) {
	t.Cleanup(func() { SetLevel(LevelInfo) })

	if got := SetLevel(" DEBUG "); got != LevelDebug {
		t.Fatalf("SetLevel returned %q, want %q", got, LevelDebug)
	}
	if got := SetLevel("nonsense"); got != LevelInfo {
		t.Fatalf("SetLevel returned %q for an unknown name, want %q", got, LevelInfo)
	}
}

// TestHandlerFilters checks that the installed level gates the records, which
// is what keeps the per-request debug lines out of the console by default.
func TestHandlerFilters(t *testing.T) {
	t.Cleanup(func() { SetLevel(LevelInfo) })

	for _, tc := range []struct {
		level     string
		debugSeen bool
		infoSeen  bool
	}{
		{LevelError, false, false},
		{LevelWarn, false, false},
		{LevelInfo, false, true},
		{LevelDebug, true, true},
	} {
		SetLevel(tc.level)
		var buf bytes.Buffer
		log := slog.New(Handler(&buf))

		log.Debug("request")
		log.Info("start")

		if got := strings.Contains(buf.String(), "request"); got != tc.debugSeen {
			t.Errorf("level %q: debug record present = %v, want %v (log: %q)",
				tc.level, got, tc.debugSeen, buf.String())
		}
		if got := strings.Contains(buf.String(), "start"); got != tc.infoSeen {
			t.Errorf("level %q: info record present = %v, want %v (log: %q)",
				tc.level, got, tc.infoSeen, buf.String())
		}
	}
}
