package smtp

import (
	"errors"
	"fmt"
	netsmtp "net/smtp"
	"net/textproto"
	"strings"
)

// plainAuth implements AUTH PLAIN.
//
// net/smtp's PlainAuth refuses to send the password over an unencrypted
// connection and treats only localhost as trusted. That is the right default,
// but this package has to honour an explicit "no encryption" choice for a relay
// on a private network, so the mechanism is implemented here instead.
type plainAuth struct {
	username string
	password string
}

func (a *plainAuth) Start(*netsmtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	// net/smtp calls Next with more=false on the final answer as well, so only
	// a real challenge counts as a server that does not speak PLAIN.
	if !more {
		return nil, nil
	}
	return nil, errors.New("smtp: server asked for a second PLAIN exchange")
}

// loginAuth implements the non-standard but very common AUTH LOGIN, which asks
// for the username and then the password in two base64 encoded prompts.
type loginAuth struct {
	username string
	password string
}

func (a *loginAuth) Start(*netsmtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	prompt := strings.ToLower(string(fromServer))
	switch {
	case strings.Contains(prompt, "user"):
		return []byte(a.username), nil
	case strings.Contains(prompt, "pass"):
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN prompt %q", fromServer)
}

// authError classifies a failure of the AUTH exchange: a rejected credential is
// reported as ErrAuth, anything else keeps its protocol text.
func authError(err error) error {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		switch protoErr.Code {
		case 501, 504, 530, 534, 535:
			return fmt.Errorf("%w: %v", ErrAuth, protoErr)
		}
	}
	return fmt.Errorf("smtp: authenticate: %w", err)
}
