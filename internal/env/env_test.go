package env

import (
	"os"
	"testing"
)

// TestConfigDir pins the override: the variable is returned verbatim, and an
// unset one leaves the platform default to the caller.
func TestConfigDir(t *testing.T) {
	t.Setenv(ConfigDirVar, "/tmp/yauzin-config")
	if got := ConfigDir(); got != "/tmp/yauzin-config" {
		t.Fatalf("ConfigDir() = %q, want the override", got)
	}

	t.Setenv(ConfigDirVar, "")
	if got := ConfigDir(); got != "" {
		t.Fatalf("ConfigDir() = %q, want empty when unset", got)
	}
}

// TestPprofPort pins the translation of the variable into a port: anything
// that is not a plain number in range keeps the server off, because guessing
// what an address-shaped value meant is how a heap snapshot ends up on the
// network.
func TestPprofPort(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		unset   bool
		want    int
		wantOK  bool
		wantErr bool
	}{
		{name: "unset", unset: true},
		{name: "empty", value: ""},
		{name: "port", value: "6060", want: 6060, wantOK: true},
		{name: "lowest port", value: "1", want: 1, wantOK: true},
		{name: "highest port", value: "65535", want: 65535, wantOK: true},
		{name: "surrounding space", value: " 6060 ", want: 6060, wantOK: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "above range", value: "65536", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
		{name: "not a number", value: "abc", wantErr: true},
		{name: "host and port", value: "127.0.0.1:6060", wantErr: true},
		{name: "wildcard", value: "0.0.0.0:6060", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.unset {
				// Setenv registers the restore of the original value; the
				// unset that follows it is what this case is about.
				t.Setenv(PprofPortVar, "0")
				if err := os.Unsetenv(PprofPortVar); err != nil {
					t.Fatalf("unset %s: %v", PprofPortVar, err)
				}
			} else {
				t.Setenv(PprofPortVar, tt.value)
			}

			port, ok, err := PprofPort()
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("PprofPort() error = %v, wantErr %t", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("PprofPort() ok = %t, want %t", ok, tt.wantOK)
			}
			if port != tt.want {
				t.Fatalf("PprofPort() = %d, want %d", port, tt.want)
			}
		})
	}
}

// TestDev pins the coarse switch: every non-blank value enables it, so there
// is no value that could accidentally read as "on" when meant as "off".
func TestDev(t *testing.T) {
	for _, value := range []string{"1", "true", "yes", "0", "false", "on"} {
		t.Setenv(DevVar, value)
		if !Dev() {
			t.Fatalf("Dev() = false for %s=%q, want true", DevVar, value)
		}
	}

	for _, value := range []string{"", " ", "\t"} {
		t.Setenv(DevVar, value)
		if Dev() {
			t.Fatalf("Dev() = true for %s=%q, want false", DevVar, value)
		}
	}
}
