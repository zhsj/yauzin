// Package profiling serves the Go runtime profiles over HTTP.
//
// The server is off unless internal/env reports a port from
// YAUZIN_PPROF_PORT, and it always listens on the loopback address: a heap
// snapshot describes everything the process holds, so it must not be
// reachable from the network. This package only listens; reading the
// variable and validating the port belong to internal/env.
//
// The server is a debugging aid, never part of startup: callers log a failure
// and carry on.
package profiling

import (
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
)

// host is the only address the server binds to.
const host = "127.0.0.1"

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
