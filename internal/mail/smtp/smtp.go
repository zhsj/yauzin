// Package smtp sends a draft through a plain SMTP server. It is the transport
// behind an outbound-only sending account: there is no mailbox to read, so the
// package offers Send alone instead of mail.Service.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	netsmtp "net/smtp"
	"strings"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/mail/rfc822"
	"github.com/zhsj/yauzin/internal/netproxy"
)

// Security is how the connection to the server is encrypted.
type Security string

const (
	// SecurityTLS connects with implicit TLS, the way port 465 works.
	SecurityTLS Security = "tls"
	// SecuritySTARTTLS connects in the clear and upgrades before the first
	// command of substance. The server has to advertise STARTTLS, otherwise the
	// send fails instead of falling back to plaintext.
	SecuritySTARTTLS Security = "starttls"
	// SecurityNone sends in the clear, which only makes sense for a relay on
	// the local network.
	SecurityNone Security = "none"
)

// ErrAuth reports that the server rejected the credentials, so the UI can point
// the user at the username and password instead of showing a protocol sentence.
var ErrAuth = errors.New("smtp: authentication failed")

// defaultTimeout bounds one send when the caller sets no deadline of its own.
const defaultTimeout = 60 * time.Second

// Config describes one SMTP server and the identity it sends as.
type Config struct {
	Host string
	Port int
	// Username and Password are the AUTH credentials. An empty Username skips
	// authentication, which a relay on localhost may not need.
	Username string
	Password string
	Security Security
	// From is the sender written into the message. It may differ from Username:
	// many providers let an authenticated user send as one of its aliases.
	From mail.Address
}

// Sender sends drafts through one configured server. It holds no connection
// between sends and is safe for concurrent use.
type Sender struct {
	cfg     Config
	dialer  *netproxy.Dialer
	tlsConf *tls.Config
	timeout time.Duration
	now     func() time.Time
}

// options collects the injected dependencies of a Sender.
type options struct {
	proxyURL  string
	tlsConfig *tls.Config
	timeout   time.Duration
	now       func() time.Time
}

// Option customizes a Sender.
type Option func(*options)

// WithProxyURL sends the connection through raw, which netproxy.Parse has
// already validated. Empty means a direct connection.
func WithProxyURL(raw string) Option {
	return func(o *options) { o.proxyURL = raw }
}

// WithTLSConfig replaces the TLS configuration, which tests use to trust their
// own certificate.
func WithTLSConfig(c *tls.Config) Option {
	return func(o *options) { o.tlsConfig = c }
}

// WithTimeout bounds one send when the context carries no deadline.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.timeout = d
		}
	}
}

// WithNow replaces the clock used for the Date header.
func WithNow(now func() time.Time) Option {
	return func(o *options) {
		if now != nil {
			o.now = now
		}
	}
}

// New validates cfg and builds the sender.
func New(cfg Config, opts ...Option) (*Sender, error) {
	o := options{timeout: defaultTimeout, now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	if err := validate(cfg); err != nil {
		return nil, err
	}
	dialer, err := netproxy.New(o.proxyURL)
	if err != nil {
		return nil, err
	}
	return &Sender{
		cfg:     cfg,
		dialer:  dialer,
		tlsConf: o.tlsConfig,
		timeout: o.timeout,
		now:     o.now,
	}, nil
}

// Config returns the server the sender talks to.
func (s *Sender) Config() Config { return s.cfg }

// validate rejects a configuration the sender cannot act on before anything is
// dialled.
func validate(cfg Config) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return fmt.Errorf("%w: empty SMTP host", mail.ErrInvalidArgument)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("%w: SMTP port %d out of range", mail.ErrInvalidArgument, cfg.Port)
	}
	switch cfg.Security {
	case SecurityTLS, SecuritySTARTTLS, SecurityNone:
	default:
		return fmt.Errorf("%w: unknown SMTP security %q", mail.ErrInvalidArgument, cfg.Security)
	}
	if cfg.From.Email == "" {
		return fmt.Errorf("%w: empty sender address", mail.ErrInvalidArgument)
	}
	return nil
}

// Send renders draft and delivers it. It reports ErrAuth when the server
// rejects the credentials and ErrInvalidArgument for a draft without
// recipients.
func (s *Sender) Send(ctx context.Context, draft mail.Draft) error {
	if err := draft.Validate(); err != nil {
		return err
	}
	// The blind recipients travel in the envelope only: the header would show
	// them to every other recipient.
	msg, err := rfc822.Prepare(draft, s.cfg.From, rfc822.Options{Date: s.now()})
	if err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok && s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	recipients := draft.Recipients()
	slog.Debug("smtp send", "host", s.cfg.Host, "port", s.cfg.Port,
		"from", s.cfg.From.Email, "recipients", len(recipients))

	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	// A cancelled context breaks the session; the deadline set while dialling
	// only covers a context that expires.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()

	c, err := netsmtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting from %s: %w", s.cfg.Host, err)
	}
	defer func() { _ = c.Close() }()

	if s.cfg.Security == SecuritySTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp: %s does not offer STARTTLS", s.cfg.Host)
		}
		if err := c.StartTLS(s.tlsFor()); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(s.authFor(c)); err != nil {
			return authError(err)
		}
	}

	if err := c.Mail(s.cfg.From.Email); err != nil {
		return fmt.Errorf("smtp: sender %s rejected: %w", s.cfg.From.Email, err)
	}
	for _, addr := range recipients {
		if err := c.Rcpt(addr.Email); err != nil {
			return fmt.Errorf("smtp: recipient %s rejected: %w", addr.Email, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	// The message is streamed into DATA: attachments are read from disk a
	// block at a time and never held in memory as a whole.
	if _, err := msg.WriteTo(w); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: finish message: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp: quit: %w", err)
	}
	return nil
}

// authFor picks a mechanism the server advertises, preferring PLAIN and falling
// back to LOGIN. A server that advertises nothing gets PLAIN, which is what the
// protocol assumes for AUTH without an extension list.
func (s *Sender) authFor(c *netsmtp.Client) netsmtp.Auth {
	if ok, params := c.Extension("AUTH"); ok {
		for _, m := range strings.Fields(params) {
			switch strings.ToUpper(m) {
			case "PLAIN":
				return &plainAuth{username: s.cfg.Username, password: s.cfg.Password}
			case "LOGIN":
				return &loginAuth{username: s.cfg.Username, password: s.cfg.Password}
			}
		}
	}
	return &plainAuth{username: s.cfg.Username, password: s.cfg.Password}
}
