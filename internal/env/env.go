// Package env centralizes the YAUZIN_* variables that configure the
// application itself: where its files live, whether the profiling server
// runs, and whether the development mode is on.
//
// Variables that describe the desktop rather than the application (locale,
// cursor theme, XDG directories) stay with the packages that interpret them:
// they have their own fallback rules and are not part of this switchboard.
package env

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// ConfigDirVar overrides the directory holding settings.json and
	// accounts.json. An empty value means the platform default.
	ConfigDirVar = "YAUZIN_CONFIG_DIR"
	// PprofPortVar enables the profiling server on the named loopback port.
	PprofPortVar = "YAUZIN_PPROF_PORT"
	// DevVar turns on the development mode. Any non-empty value enables it.
	DevVar = "YAUZIN_DEV"
)

// ConfigDir returns the configuration directory override, or "" when the
// variable is unset. Resolving the platform default stays with prefs, the
// package that owns the files.
func ConfigDir() string {
	return os.Getenv(ConfigDirVar)
}

// PprofPort reads PprofPortVar and returns the port to listen on. ok is false
// when the variable is unset or empty, which leaves the profiling server off.
// err reports a value that is not a port number in 1..65535.
func PprofPort() (port int, ok bool, err error) {
	raw := strings.TrimSpace(os.Getenv(PprofPortVar))
	if raw == "" {
		return 0, false, nil
	}
	port, err = strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, false, fmt.Errorf("%s=%q is not a port number in 1..65535", PprofPortVar, raw)
	}
	return port, true, nil
}

// Dev reports whether the development mode is on: any value that is not
// blank, so YAUZIN_DEV=1, =true and =yes all mean the same thing. The reading
// is deliberately coarse; there is no way to spell "off" besides leaving it
// unset or empty.
func Dev() bool {
	return strings.TrimSpace(os.Getenv(DevVar)) != ""
}
