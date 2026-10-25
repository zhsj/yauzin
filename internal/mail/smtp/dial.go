package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
)

// dial opens the connection the session runs on: through the configured proxy
// when there is one, wrapped in TLS when the server expects implicit TLS. The
// context deadline becomes the deadline of the whole session, so every later
// command inherits it.
func (s *Sender) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := s.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smtp: connect %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if s.cfg.Security != SecurityTLS {
		return conn, nil
	}
	tlsConn := tls.Client(conn, s.tlsFor())
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smtp: TLS handshake with %s: %w", s.cfg.Host, err)
	}
	return tlsConn, nil
}

// tlsFor returns the client configuration, defaulting to a verified connection
// to the configured host.
func (s *Sender) tlsFor() *tls.Config {
	if s.tlsConf != nil {
		return s.tlsConf.Clone()
	}
	return &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
}
