package netproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"http://proxy.example.com:8080", "http://proxy.example.com:8080", false},
		{"https://proxy.example.com:8080/", "https://proxy.example.com:8080", false},
		{"socks5://user:pass@proxy.example.com:1080", "socks5://user:pass@proxy.example.com:1080", false},
		{"socks5h://proxy.example.com:1080", "", true},
		{"proxy.example.com:8080", "", true},
		{"ftp://proxy.example.com:21", "", true},
		{"http://proxy.example.com", "", true},
		{"http://proxy.example.com:8080/path", "", true},
	}
	for _, tc := range tests {
		u, err := Parse(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %v, want an error", tc.raw, u)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.raw, err)
			continue
		}
		if tc.want == "" {
			if u != nil {
				t.Errorf("Parse(%q) = %v, want a direct connection", tc.raw, u)
			}
			continue
		}
		if u.String() != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.raw, u, tc.want)
		}
	}
}

func TestNewRejectsBadAddress(t *testing.T) {
	if _, err := New("127.0.0.1:1080"); err == nil {
		t.Fatal("New accepted an address without a scheme")
	}
}

// TestDirectDial checks that an empty proxy really connects without one.
func TestDirectDial(t *testing.T) {
	addr, stop := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		_, _ = io.WriteString(conn, "banner\r\n")
	})
	defer stop()

	d, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.URL() != nil {
		t.Fatalf("URL = %v, want none", d.URL())
	}
	conn, err := d.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "banner\r\n" {
		t.Fatalf("banner = %q/%v", line, err)
	}
}

func TestDialThroughHTTPConnect(t *testing.T) {
	var seen string
	addr, stop := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		requestLine, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		seen = strings.TrimRight(requestLine, "\r\n")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		// Act as the tunnelled server from here on.
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = io.WriteString(conn, "answer:"+line)
	})
	defer stop()

	d, err := New("http://" + addr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conn := dial(t, d, "mail.example.com:587")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "answer:EHLO me\n" {
		t.Fatalf("answer = %q", line)
	}
	if want := "CONNECT mail.example.com:587 HTTP/1.1"; seen != want {
		t.Fatalf("request line = %q, want %q", seen, want)
	}
}

func TestDialThroughHTTPProxyWithCredentials(t *testing.T) {
	var auth string
	addr, stop := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "Proxy-Authorization: ") {
				auth = strings.TrimSpace(strings.TrimPrefix(line, "Proxy-Authorization: "))
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		_, _ = io.Copy(io.Discard, reader)
	})
	defer stop()

	d, err := New("http://user:secret@" + addr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conn := dial(t, d, "mail.example.com:465")
	defer func() { _ = conn.Close() }()
	// Basic dXNlcjpzZWNyZXQ= is user:secret.
	if auth != "Basic dXNlcjpzZWNyZXQ=" {
		t.Fatalf("Proxy-Authorization = %q", auth)
	}
}

func TestDialReportsRefusedTunnel(t *testing.T) {
	addr, stop := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\n\r\n")
	})
	defer stop()

	d, err := New("http://" + addr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := d.DialContext(ctx, "tcp", "mail.example.com:587"); err == nil {
		t.Fatal("DialContext accepted a refused tunnel")
	} else if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want the proxy status in it", err)
	}
}

func TestDialThroughSOCKS5(t *testing.T) {
	addr, stop := listen(t, func(conn net.Conn) {
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		// Greeting: version 5, and at least one method.
		head := make([]byte, 2)
		if _, err := io.ReadFull(reader, head); err != nil {
			return
		}
		methods := make([]byte, head[1])
		if _, err := io.ReadFull(reader, methods); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0}) // no authentication needed
		// Request: version, command, reserved, address type.
		request := make([]byte, 4)
		if _, err := io.ReadFull(reader, request); err != nil {
			return
		}
		var rest int
		switch request[3] {
		case 1: // IPv4
			rest = 4
		case 3: // domain name
			length := make([]byte, 1)
			if _, err := io.ReadFull(reader, length); err != nil {
				return
			}
			rest = int(length[0])
		case 4: // IPv6
			rest = 16
		default:
			return
		}
		if _, err := io.CopyN(io.Discard, reader, int64(rest)+2); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 25})
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		_, _ = io.WriteString(conn, "answer:"+line)
	})
	defer stop()

	d, err := New("socks5://" + addr)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conn := dial(t, d, "mail.example.com:587")
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "answer:EHLO me\n" {
		t.Fatalf("answer = %q", line)
	}
}

// dial opens a connection, writes one line and returns it for the answer.
func dial(t *testing.T, d *Dialer, addr string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, "EHLO me\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	return conn
}

// listen starts a TCP server running handle per connection and returns its
// address together with a stop function.
func listen(t *testing.T, handle func(net.Conn)) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
	stop := func() {
		_ = ln.Close()
		<-done
	}
	return fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port), stop
}
