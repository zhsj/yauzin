package profiling

import (
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestPort pins the translation of the variable into a port: anything that is
// not a plain number in range keeps the server off, because guessing what an
// address-shaped value meant is how a heap snapshot ends up on the network.
func TestPort(t *testing.T) {
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
				t.Setenv(Env, "0")
				if err := os.Unsetenv(Env); err != nil {
					t.Fatalf("unset %s: %v", Env, err)
				}
			} else {
				t.Setenv(Env, tt.value)
			}

			port, ok, err := Port()
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("Port() error = %v, wantErr %t", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("Port() ok = %t, want %t", ok, tt.wantOK)
			}
			if port != tt.want {
				t.Fatalf("Port() = %d, want %d", port, tt.want)
			}
		})
	}
}

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
