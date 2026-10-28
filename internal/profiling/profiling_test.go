package profiling

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestStartServesProfiles checks the endpoints on a real listener: the heap
// profile is what the server exists for, and an index that answers while the
// profile itself fails would look fine in a log yet be useless in practice.
func TestStartServesProfiles(t *testing.T) {
	addr, err := Start(0)
	if err != nil {
		t.Fatalf("Start(0): %v", err)
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		t.Fatalf("started on %v, want a loopback TCP address", addr)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/cmdline",
	} {
		resp, err := client.Get("http://" + addr.String() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		if cerr := resp.Body.Close(); cerr != nil {
			t.Fatalf("GET %s: closing body: %v", path, cerr)
		}
		if err != nil {
			t.Fatalf("GET %s: reading body: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("GET %s: empty body", path)
		}
	}
}

// TestStartReportsBusyPort pins that the listener is bound before Start
// returns: a taken port has to surface here, where the caller still logs it,
// instead of inside the serving goroutine that has nobody to tell.
func TestStartReportsBusyPort(t *testing.T) {
	addr, err := Start(0)
	if err != nil {
		t.Fatalf("Start(0): %v", err)
	}
	port := addr.(*net.TCPAddr).Port
	if _, err := Start(port); err == nil {
		t.Fatalf("Start(%d) on a taken port reported no error", port)
	}
}
