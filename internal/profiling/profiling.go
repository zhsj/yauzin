// Package profiling serves the Go runtime profiles over HTTP.
//
// The server is off unless YAUZIN_PPROF_PORT names a port, and it always
// listens on the loopback address: a heap snapshot describes everything the
// process holds, so it must not be reachable from the network. The port is
// the only thing the environment chooses, which keeps the setting free of
// address parsing and of any way to pick a routable interface.
//
// The server is a debugging aid, never part of startup: callers log a failure
// and carry on.
package profiling

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
)

// Env is the variable that enables the server. Its value is a port number.
const Env = "YAUZIN_PPROF_PORT"

// host is the only address the server binds to.
const host = "127.0.0.1"

// Port reads Env and returns the port to listen on. ok is false when the
// variable is unset or empty, which leaves the server off. err reports a value
// that is not a port number in 1..65535.
func Port() (port int, ok bool, err error) {
	raw := strings.TrimSpace(os.Getenv(Env))
	if raw == "" {
		return 0, false, nil
	}
	port, err = strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, false, fmt.Errorf("%s=%q is not a port number in 1..65535", Env, raw)
	}
	return port, true, nil
}

// Start listens on the loopback address and serves /debug/pprof/* in the
// background. The listener is bound before Start returns, so a port that is
// already taken is reported as an error here; port 0 picks a free one, and the
// returned address says which.
//
// The server runs for the life of the process. A desktop app has no moment at
// which it stops needing profiles, and the exit path tears the process down
// anyway, so there is nothing to close.
func Start(port int) (net.Addr, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen for profiles: %w", err)
	}
	go func() {
		// Serve only returns on a listener error, and by then nobody is left
		// to hear about it: the profiles are optional, and taking the process
		// down over them would be worse than losing them.
		_ = http.Serve(ln, newMux())
	}()
	return ln.Addr(), nil
}

// newMux returns the private handler of the server. The pprof handlers are
// also registered on http.DefaultServeMux by their package's init, so this
// mux is what keeps them unreachable through any other server the process may
// start, and it is the only thing a test can inspect.
func newMux() *http.ServeMux {
	m := http.NewServeMux()
	// The five routes net/http/pprof registers itself. The named profiles
	// below /debug/pprof/ are dispatched by Index and need no route of their
	// own.
	m.HandleFunc("/debug/pprof/", pprof.Index)
	m.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	m.HandleFunc("/debug/pprof/profile", pprof.Profile)
	m.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	m.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return m
}
