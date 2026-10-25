package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestSendPlain(t *testing.T) {
	srv := newServer(t, serverOptions{})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Security: SecurityNone,
		From:     mail.Address{Name: "Ada", Email: "ada@example.com"},
	})
	draft := mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Cc:      []mail.Address{{Email: "cc@example.com"}},
		Bcc:     []mail.Address{{Email: "bcc@example.com"}},
		Subject: "hello",
		Body:    "body text",
	}
	if err := sender.Send(context.Background(), draft); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.session()
	if got.mailFrom != "ada@example.com" {
		t.Fatalf("MAIL FROM = %q", got.mailFrom)
	}
	want := []string{"to@example.com", "cc@example.com", "bcc@example.com"}
	if strings.Join(got.rcpts, ",") != strings.Join(want, ",") {
		t.Fatalf("RCPT TO = %v, want %v", got.rcpts, want)
	}
	if got.auth != "" {
		t.Fatalf("auth = %q, want none", got.auth)
	}
	data := string(got.data)
	if strings.Contains(data, "bcc@example.com") {
		t.Fatalf("Bcc leaked into the message:\n%s", data)
	}
	for _, want := range []string{"From: ", "To: <to@example.com>", "Cc: <cc@example.com>", "Subject: hello"} {
		if !strings.Contains(data, want) {
			t.Fatalf("message lacks %q:\n%s", want, data)
		}
	}
}

func TestSendSTARTTLSWithLogin(t *testing.T) {
	srv := newServer(t, serverOptions{startTLS: true, authMechs: "LOGIN PLAIN"})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Username: "user@example.com",
		Password: "s3cret",
		Security: SecuritySTARTTLS,
		From:     mail.Address{Email: "user@example.com"},
	})
	if err := sender.Send(context.Background(), mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "secure",
		Body:    "text",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.session()
	if got.auth != "LOGIN" {
		t.Fatalf("auth = %q, want LOGIN because the server lists it first", got.auth)
	}
	if got.username != "user@example.com" || got.password != "s3cret" {
		t.Fatalf("credentials = %q/%q", got.username, got.password)
	}
	if !got.upgraded {
		t.Fatal("session was not upgraded with STARTTLS")
	}
}

func TestSendImplicitTLS(t *testing.T) {
	srv := newServer(t, serverOptions{implicitTLS: true, authMechs: "PLAIN"})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Username: "user",
		Password: "pass",
		Security: SecurityTLS,
		From:     mail.Address{Email: "user@example.com"},
	})
	if err := sender.Send(context.Background(), mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "tls",
		Body:    "text",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got := srv.session()
	if got.auth != "PLAIN" || got.username != "user" {
		t.Fatalf("auth = %q/%q", got.auth, got.username)
	}
}

func TestSendRequiresSTARTTLS(t *testing.T) {
	srv := newServer(t, serverOptions{})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Security: SecuritySTARTTLS,
		From:     mail.Address{Email: "ada@example.com"},
	})
	err := sender.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send = %v, want a STARTTLS complaint", err)
	}
}

func TestSendReportsAuthFailure(t *testing.T) {
	srv := newServer(t, serverOptions{authMechs: "PLAIN", rejectAuth: true})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Username: "user",
		Password: "wrong",
		Security: SecurityNone,
		From:     mail.Address{Email: "user@example.com"},
	})
	err := sender.Send(context.Background(), mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("Send = %v, want ErrAuth", err)
	}
}

func TestSendThroughProxy(t *testing.T) {
	srv := newServer(t, serverOptions{})
	defer srv.stop()
	proxyAddr, stopProxy := startConnectProxy(t, srv.address())
	defer stopProxy()

	sender, err := New(Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Security: SecurityNone,
		From:     mail.Address{Email: "ada@example.com"},
	}, WithProxyURL("http://"+proxyAddr))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := sender.Send(context.Background(), mail.Draft{
		To:      []mail.Address{{Email: "to@example.com"}},
		Subject: "through the proxy",
		Body:    "text",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := srv.session(); !strings.Contains(string(got.data), "through the proxy") {
		t.Fatalf("message did not arrive: %q", got.data)
	}
}

func TestSendHonoursCancellation(t *testing.T) {
	srv := newServer(t, serverOptions{})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Security: SecurityNone,
		From:     mail.Address{Email: "ada@example.com"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sender.Send(ctx, mail.Draft{
		To:   []mail.Address{{Email: "to@example.com"}},
		Body: "x",
	})
	if err == nil {
		t.Fatal("Send succeeded with a cancelled context")
	}
}

func TestNewValidatesConfig(t *testing.T) {
	base := Config{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, From: mail.Address{Email: "a@example.com"}}
	if _, err := New(base); err != nil {
		t.Fatalf("New(base): %v", err)
	}
	tests := []struct {
		name string
		cfg  Config
	}{
		{"empty host", Config{Port: 587, Security: SecuritySTARTTLS, From: base.From}},
		{"port zero", Config{Host: base.Host, Security: SecuritySTARTTLS, From: base.From}},
		{"port too high", Config{Host: base.Host, Port: 65536, Security: SecuritySTARTTLS, From: base.From}},
		{"unknown security", Config{Host: base.Host, Port: 587, Security: "ssl", From: base.From}},
		{"empty sender", Config{Host: base.Host, Port: 587, Security: SecuritySTARTTLS}},
	}
	for _, tc := range tests {
		if _, err := New(tc.cfg); !errors.Is(err, mail.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", tc.name, err)
		}
	}
	if _, err := New(base, WithProxyURL("socks5h://proxy:1080")); err == nil {
		t.Fatal("New accepted an unsupported proxy scheme")
	}
}

func TestSendRejectsDraftWithoutRecipients(t *testing.T) {
	srv := newServer(t, serverOptions{})
	defer srv.stop()

	sender := newSender(t, srv, Config{
		Host:     srv.host(),
		Port:     srv.port(),
		Security: SecurityNone,
		From:     mail.Address{Email: "ada@example.com"},
	})
	err := sender.Send(context.Background(), mail.Draft{Subject: "nobody"})
	if !errors.Is(err, mail.ErrInvalidArgument) {
		t.Fatalf("Send = %v, want ErrInvalidArgument", err)
	}
}

// newSender builds a sender that trusts the fake server's certificate.
func newSender(t *testing.T, srv *server, cfg Config) *Sender {
	t.Helper()
	sender, err := New(cfg,
		WithTLSConfig(&tls.Config{InsecureSkipVerify: true}), //nolint:gosec // the test server is signed by itself
		WithNow(func() time.Time { return time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC) }),
		WithTimeout(10*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sender
}

// session is what one SMTP session told the fake server.
type session struct {
	mailFrom string
	rcpts    []string
	auth     string
	username string
	password string
	upgraded bool
	data     []byte
}

// serverOptions selects the behaviour of the fake server.
type serverOptions struct {
	startTLS    bool
	implicitTLS bool
	authMechs   string
	rejectAuth  bool
}

// server is a minimal SMTP server: enough of the protocol for the sender to
// complete a session and for the test to read back what arrived.
type server struct {
	opts serverOptions
	ln   net.Listener
	cert tls.Certificate

	mu   sync.Mutex
	last session
}

func newServer(t *testing.T, opts serverOptions) *server {
	t.Helper()
	cert := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &server{opts: opts, ln: ln, cert: cert}
	go s.serve()
	return s
}

func (s *server) address() string { return s.ln.Addr().String() }

func (s *server) host() string {
	host, _, _ := net.SplitHostPort(s.address())
	return host
}

func (s *server) port() int {
	_, port, _ := net.SplitHostPort(s.address())
	var n int
	_, _ = fmt.Sscanf(port, "%d", &n)
	return n
}

func (s *server) session() session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *server) stop() { _ = s.ln.Close() }

func (s *server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if s.opts.implicitTLS {
		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn = tlsConn
	}

	var (
		got     session
		upgrade bool
		tlsOn   = s.opts.implicitTLS
	)
	reader := bufio.NewReader(conn)
	write := func(text string) {
		if _, err := io.WriteString(conn, text); err != nil {
			return
		}
	}
	readLine := func() (string, bool) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", false
		}
		return strings.TrimRight(line, "\r\n"), true
	}
	write("220 fake ESMTP\r\n")

	for {
		line, ok := readLine()
		if !ok {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			write("500 empty command\r\n")
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "EHLO":
			resp := "250-fake\r\n"
			if s.opts.startTLS && !tlsOn {
				resp += "250-STARTTLS\r\n"
			}
			if s.opts.authMechs != "" {
				resp += "250-AUTH " + s.opts.authMechs + "\r\n"
			}
			resp += "250 OK\r\n"
			write(resp)
		case "STARTTLS":
			write("220 ready to start TLS\r\n")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(tlsConn)
			tlsOn, upgrade = true, true
		case "AUTH":
			if s.opts.rejectAuth {
				write("535 5.7.8 credentials rejected\r\n")
				continue
			}
			if len(fields) < 2 {
				write("501 no mechanism\r\n")
				continue
			}
			switch strings.ToUpper(fields[1]) {
			case "PLAIN":
				encoded := ""
				if len(fields) > 2 {
					encoded = fields[2]
				} else {
					write("334 \r\n")
					encoded, ok = readLine()
					if !ok {
						return
					}
				}
				raw, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					write("501 bad base64\r\n")
					continue
				}
				parts := strings.Split(strings.TrimPrefix(string(raw), "\x00"), "\x00")
				got.auth = "PLAIN"
				if len(parts) == 2 {
					got.username, got.password = parts[0], parts[1]
				}
				write("235 2.7.0 accepted\r\n")
			case "LOGIN":
				got.auth = "LOGIN"
				write("334 VXNlcm5hbWU6\r\n")
				line, ok = readLine()
				if !ok {
					return
				}
				decoded, _ := base64.StdEncoding.DecodeString(line)
				got.username = string(decoded)
				write("334 UGFzc3dvcmQ6\r\n")
				line, ok = readLine()
				if !ok {
					return
				}
				decoded, _ = base64.StdEncoding.DecodeString(line)
				got.password = string(decoded)
				write("235 2.7.0 accepted\r\n")
			default:
				write("504 mechanism not supported\r\n")
			}
		case "MAIL":
			got.mailFrom = addressOf(line)
			write("250 2.1.0 OK\r\n")
		case "RCPT":
			got.rcpts = append(got.rcpts, addressOf(line))
			write("250 2.1.5 OK\r\n")
		case "DATA":
			write("354 end with a line containing only a dot\r\n")
			var data strings.Builder
			for {
				line, ok := readLine()
				if !ok {
					return
				}
				if line == "." {
					break
				}
				data.WriteString(strings.TrimPrefix(line, ".") + "\r\n")
			}
			got.data = []byte(data.String())
			write("250 2.0.0 queued\r\n")
		case "QUIT":
			write("221 2.0.0 bye\r\n")
			got.upgraded = upgrade
			s.mu.Lock()
			s.last = got
			s.mu.Unlock()
			return
		case "RSET":
			write("250 2.0.0 OK\r\n")
		default:
			write("502 5.5.2 unknown command\r\n")
		}
	}
}

// addressOf pulls the address out of "MAIL FROM:<a@b>" and "RCPT TO:<a@b>".
func addressOf(line string) string {
	start := strings.Index(line, "<")
	end := strings.LastIndex(line, ">")
	if start < 0 || end <= start {
		return ""
	}
	return line[start+1 : end]
}

// selfSigned creates a certificate for 127.0.0.1 and localhost.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startConnectProxy runs an HTTP CONNECT proxy in front of target and returns
// its address.
func startConnectProxy(t *testing.T, target string) (string, func()) {
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
			go func(conn net.Conn) {
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
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = upstream.Close() }()
				if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}(conn)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}
