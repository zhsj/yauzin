// Package pgp signs mail through the system gpg binary.
//
// A real gpg-agent is the only way a passphrase or a smart-card PIN can reach
// the desktop pinentry, and pinentry is the one place a secret is typed. A pure
// Go OpenPGP implementation would have to collect the passphrase itself, so the
// signature is always produced by gpg and this package never sees a secret.
//
// The package knows nothing about MIME: it takes an entity as a stream and
// returns the armored detached signature, which lets the renderer stay free of
// process execution and tests inject a fake.
package pgp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Errors callers distinguish.
var (
	// ErrInvalidKey reports user input that is neither a long key ID nor a
	// fingerprint, so it is rejected before it reaches gpg.
	ErrInvalidKey = errors.New("pgp: invalid signing key")
	// ErrNoKey reports that gpg has no secret key for the configured ID.
	ErrNoKey = errors.New("pgp: signing key not found")
	// ErrCancelled reports that the user dismissed the pinentry dialog or that
	// the signing context expired.
	ErrCancelled = errors.New("pgp: signing cancelled")
	// ErrUnavailable reports that gpg could not be run or that it cannot reach
	// its agent.
	ErrUnavailable = errors.New("pgp: gpg not available")
	// ErrFailed is every other signing failure: an expired key, a rejected
	// passphrase, an unpinned smart card.
	ErrFailed = errors.New("pgp: signing failed")
)

const (
	// longKeyIDLen and fingerprintLen are the only key forms accepted. gpg also
	// takes a mail address or a user ID in --local-user, but those resolve
	// against whatever key ring happens to be present, and an account that
	// silently signs with the wrong key is worse than one that refuses to.
	longKeyIDLen   = 16
	fingerprintLen = 40

	// gpgBinary is looked up in PATH; distributions ship it under this name
	// (2.x dropped the gpg2 alias).
	gpgBinary = "gpg"

	// connectAgentBinary is the client whose only useful answer here is
	// whether an agent is listening.
	connectAgentBinary = "gpg-connect-agent"

	// waitDelay bounds Wait after gpg's own exit, so a lingering helper holding
	// the pipes cannot hang the signing task.
	waitDelay = 5 * time.Second

	// gpgErrCanceled is GPG_ERR_CANCELED, the code gpg reports when the user
	// closes pinentry.
	gpgErrCanceled = 46
)

// micalgByHash maps the hash algorithm number of a SIG_CREATED status line to
// the micalg value RFC 3156 puts in the multipart/signed Content-Type.
var micalgByHash = map[string]string{
	"1":  "pgp-md5",
	"2":  "pgp-sha1",
	"3":  "pgp-ripemd160",
	"8":  "pgp-sha256",
	"9":  "pgp-sha384",
	"10": "pgp-sha512",
	"11": "pgp-sha224",
}

// Signer signs with one key. It holds no state between calls and is safe for
// concurrent use.
type Signer struct {
	key string
}

// New returns a Signer for key, which must already be acceptable to
// NormalizeKey.
func New(key string) (*Signer, error) {
	normalized, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	return &Signer{key: normalized}, nil
}

// Key returns the normalized key the signer uses.
func (s *Signer) Key() string { return s.key }

// SignDetached signs entity and returns the armored detached signature. The
// reader is consumed exactly once and never buffered as a whole, so an entity
// carrying large attachments streams straight into gpg.
func (s *Signer) SignDetached(ctx context.Context, entity io.Reader) ([]byte, string, error) {
	path, err := exec.LookPath(gpgBinary)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := checkAgent(ctx); err != nil {
		return nil, "", err
	}

	// The status lines go to a dedicated descriptor: mixing them into stderr
	// would tie the parse to the wording of gpg's human messages.
	statusR, statusW, err := os.Pipe()
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrFailed, err)
	}
	defer func() { _ = statusR.Close() }()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path,
		"--batch",
		"--no-tty",
		"--yes",
		"--armor",
		"--detach-sign",
		"--digest-algo", "SHA256",
		"--local-user", s.key,
		"--output", "-",
		"--status-fd", "3",
		"--no-autostart",
	)
	cmd.Stdin = entity
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.ExtraFiles = []*os.File{statusW}
	// LC_ALL is pinned so the messages this package greps for do not depend on
	// the user's locale. Everything else is inherited unchanged: pinentry is
	// started by gpg-agent and needs DISPLAY or WAYLAND_DISPLAY to open at all.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = waitDelay

	start := time.Now()
	if err := cmd.Start(); err != nil {
		_ = statusW.Close()
		return nil, "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// The child owns its copy of the write end; the parent must drop its own or
	// the read below never sees EOF.
	_ = statusW.Close()

	statusCh := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(statusR)
		statusCh <- data
	}()

	runErr := cmd.Wait()
	status := parseStatus(<-statusCh)

	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrCancelled, ctxErr)
	}
	if runErr != nil {
		err := classify(runErr, status, stderr.Bytes())
		slog.Debug("pgp sign failed", "key", s.key, "exit", exitCode(runErr),
			"elapsed", time.Since(start), "err", err)
		return nil, "", err
	}
	if len(stdout.Bytes()) == 0 {
		return nil, "", fmt.Errorf("%w: gpg produced no signature", ErrFailed)
	}
	micalg, ok := micalgByHash[status.hashAlgo]
	if !ok {
		// RFC 3156 only knows the pgp-* names; an unknown hash would make the
		// Content-Type lie about the signature.
		return nil, "", fmt.Errorf("%w: unsupported digest algorithm %q", ErrFailed, status.hashAlgo)
	}

	slog.Debug("pgp sign", "key", s.key, "fingerprint", status.fingerprint,
		"micalg", micalg, "elapsed", time.Since(start))

	return stdout.Bytes(), micalg, nil
}

// checkAgent reports whether gpg has an agent to sign with. The probe runs
// with --no-autostart so it never creates an agent of its own: in a Flatpak
// sandbox that agent would have no pinentry and would take over the socket the
// host's agent uses. gpg-connect-agent exits zero even when it cannot connect,
// so silence on stderr is what a usable agent looks like.
func checkAgent(ctx context.Context) error {
	path, err := exec.LookPath(connectAgentBinary)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, "--no-autostart", "/bye")
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = waitDelay

	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %v", ErrCancelled, ctxErr)
	}
	if msg := lastLine(stderr.Bytes()); msg != "" {
		return fmt.Errorf("%w: %s", ErrUnavailable, msg)
	}
	if runErr != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, runErr)
	}
	return nil
}

// status is the subset of the gpg status protocol this package reads.
type status struct {
	hashAlgo    string
	fingerprint string
	failure     int
	hasFailure  bool
	noKey       bool
}

// parseStatus walks the [GNUPG:] lines gpg wrote to the status descriptor.
func parseStatus(data []byte) status {
	var st status
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "[GNUPG:] ")
		if !ok {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "SIG_CREATED":
			// SIG_CREATED <type> <pk_algo> <hash_algo> <class> <time> <fpr>
			if len(fields) > 3 {
				st.hashAlgo = fields[3]
			}
			if len(fields) > 6 {
				st.fingerprint = fields[6]
			}
		case "FAILURE":
			// FAILURE <what> <code>; the code is the libgpg-error value.
			if len(fields) > 2 {
				if code, err := strconv.Atoi(fields[2]); err == nil {
					st.failure, st.hasFailure = code, true
				}
			}
		case "NO_SECKEY", "INV_SGNR", "KEYREVOKED":
			st.noKey = true
		}
	}
	return st
}

// classify turns a failed run into one of the package errors. The status lines
// decide whenever they can; the tail of stderr is the fallback, because older
// gpg versions and some smart-card drivers report only there.
func classify(runErr error, st status, stderr []byte) error {
	msg := lastLine(stderr)
	switch {
	case st.noKey:
		return fmt.Errorf("%w: %s", ErrNoKey, msg)
	case st.hasFailure && st.failure == gpgErrCanceled:
		return ErrCancelled
	case strings.Contains(msg, "cancelled"), strings.Contains(msg, "canceled"):
		return ErrCancelled
	case strings.Contains(msg, "No secret key"), strings.Contains(msg, "secret key not available"):
		return fmt.Errorf("%w: %s", ErrNoKey, msg)
	}
	if msg == "" {
		msg = runErr.Error()
	}
	return fmt.Errorf("%w: %s", ErrFailed, msg)
}

// lastLine returns gpg's most specific message: the last non-empty stderr line.
func lastLine(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// exitCode reports the process exit status, or -1 when the run never started or
// was killed by a signal.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// NormalizeKey turns user input into the form the settings file stores: a long
// key ID (16 hex digits) or a full fingerprint (40), upper case, without the 0x
// prefix and without the spaces, dashes or colons gpg prints between groups.
// Anything else — a mail address, a user ID, an 8 digit short key ID — is
// rejected, because the caller would otherwise store a key the user never
// named.
func NormalizeKey(s string) (string, error) {
	key := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n', '-', ':', '_':
			return -1
		}
		return r
	}, s)
	if len(key) >= 2 && key[0] == '0' && (key[1] == 'x' || key[1] == 'X') {
		key = key[2:]
	}
	if len(key) != longKeyIDLen && len(key) != fingerprintLen {
		return "", fmt.Errorf("%w: %q is neither a long key ID nor a fingerprint", ErrInvalidKey, s)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return "", fmt.Errorf("%w: %q is not hexadecimal", ErrInvalidKey, s)
		}
	}
	return strings.ToUpper(key), nil
}
