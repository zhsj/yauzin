// Package netproxy opens TCP connections through the proxy configured in the
// settings.
//
// net/http handles proxies by itself, but a protocol that speaks plain TCP over
// a connection it dials (SMTP) has to tunnel through the proxy on its own: the
// proxy support of net/http lives in its internal bundles and cannot be handed
// to another dialer. This package is that dialer, so both the Gmail REST client
// and the SMTP sender read the same proxy address and reject the same ones.
package netproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

// Parse validates a proxy address from the settings and returns it in the form
// net/http expects. Empty input means a direct connection and is not an error.
// socks5h is not accepted: net/http has no dialer for it, and the socks5 dialer
// already lets the proxy resolve the destination host.
func Parse(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("invalid proxy scheme %q: use http, https or socks5", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("invalid proxy address: missing host")
	}
	if u.Port() == "" {
		return nil, errors.New("invalid proxy address: missing port")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("invalid proxy address: unexpected path")
	}
	u.Path = ""
	return u, nil
}

// Dialer opens connections through one proxy. A nil *Dialer and one built from
// an empty address both connect directly, so callers never branch on whether a
// proxy is configured.
type Dialer struct {
	url *url.URL
}

// New parses raw and returns the matching dialer. An empty raw yields a direct
// dialer.
func New(raw string) (*Dialer, error) {
	u, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	return &Dialer{url: u}, nil
}

// URL returns the proxy address the dialer goes through, nil when it connects
// directly.
func (d *Dialer) URL() *url.URL {
	if d == nil {
		return nil
	}
	return d.url
}

// DialContext connects to addr, tunnelling through the proxy when there is one.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil || d.url == nil {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	switch strings.ToLower(d.url.Scheme) {
	case "socks5":
		return d.dialSOCKS5(ctx, network, addr)
	case "http", "https":
		return d.dialConnect(ctx, addr)
	}
	return nil, fmt.Errorf("netproxy: unsupported proxy scheme %q", d.url.Scheme)
}

// dialSOCKS5 hands the connection to the SOCKS5 dialer of golang.org/x/net,
// which also performs the RFC 1929 username/password exchange.
func (d *Dialer) dialSOCKS5(ctx context.Context, network, addr string) (net.Conn, error) {
	var auth *xproxy.Auth
	if u := d.url.User; u != nil {
		password, _ := u.Password()
		auth = &xproxy.Auth{User: u.Username(), Password: password}
	}
	dialer, err := xproxy.SOCKS5("tcp", d.url.Host, auth, xproxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("netproxy: %w", err)
	}
	cd, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("netproxy: socks5 dialer does not support contexts")
	}
	conn, err := cd.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("netproxy: socks5 %s: %w", d.url.Host, err)
	}
	return conn, nil
}

// dialConnect opens the tunnel of an HTTP proxy: a CONNECT request on a plain
// or TLS connection to the proxy, followed by the raw stream to addr.
func (d *Dialer) dialConnect(ctx context.Context, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.url.Host)
	if err != nil {
		return nil, fmt.Errorf("netproxy: dial proxy %s: %w", d.url.Host, err)
	}
	// The handshake below has to honour ctx as well: a proxy that stops
	// answering would otherwise hold the request until its own timeout.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if strings.EqualFold(d.url.Scheme, "https") {
		host := d.url.Hostname()
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("netproxy: proxy TLS handshake: %w", err)
		}
		conn = tlsConn
	}

	var req strings.Builder
	req.WriteString("CONNECT " + addr + " HTTP/1.1\r\n")
	req.WriteString("Host: " + addr + "\r\n")
	if u := d.url.User; u != nil {
		password, _ := u.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + password))
		req.WriteString("Proxy-Authorization: Basic " + creds + "\r\n")
	}
	req.WriteString("\r\n")
	if _, err := conn.Write([]byte(req.String())); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("netproxy: write CONNECT: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := readResponseHead(reader)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("netproxy: read CONNECT answer: %w", err)
	}
	if !strings.Contains(resp, " 200 ") {
		_ = conn.Close()
		return nil, fmt.Errorf("netproxy: proxy refused the tunnel: %s", resp)
	}
	// The tunnel is theirs now; the SMTP client sets its own deadlines.
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		// A proxy that announced the tunnel early may have already forwarded
		// server data, which the buffered reader holds. Push it back.
		return &bufferedConn{Conn: conn, r: reader}, nil
	}
	return conn, nil
}

// readResponseHead reads the status line of the proxy answer, skipping the
// headers. It reports the status line for the error message.
func readResponseHead(r *bufio.Reader) (string, error) {
	status, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	status = strings.TrimRight(status, "\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return status, err
		}
		if line == "\r\n" || line == "\n" {
			return status, nil
		}
	}
}

// bufferedConn reads the bytes its bufio.Reader already took from the socket
// before handing the rest to the connection.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}
