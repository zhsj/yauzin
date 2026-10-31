package pgp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGPG installs a script named gpg in a private PATH and returns it. The
// real binary is never run: signing a message in a test would need a key ring
// and a pinentry, neither of which exists on a build machine.
func fakeGPG(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gpg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write fake gpg: %v", err)
	}
	fakeAgent(t, dir, "")
	// The fake wins the lookup, but the real PATH stays behind it so the script
	// can still use cat and sleep.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// fakeAgent replaces the probe next to the fake gpg. An empty script is the
// silent success of a reachable agent.
func fakeAgent(t *testing.T, dir, script string) {
	t.Helper()
	body := "#!/bin/sh\n"
	if script != "" {
		body += script + "\n"
	}
	body += "exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "gpg-connect-agent"), []byte(body), 0o755); err != nil {
		t.Fatalf("write fake gpg-connect-agent: %v", err)
	}
}

// sign runs one signing call and returns its result and error.
func sign(t *testing.T, ctx context.Context, entity string) ([]byte, string, error) {
	t.Helper()
	s, err := New(" 0123 4567 89ab cdef ")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s.SignDetached(ctx, strings.NewReader(entity))
}

func TestSignDetachedSignsTheEntity(t *testing.T) {
	dir := t.TempDir()
	stdinOut := filepath.Join(dir, "stdin")
	argsOut := filepath.Join(dir, "args")
	t.Setenv("PGP_STDIN", stdinOut)
	t.Setenv("PGP_ARGS", argsOut)
	fakeGPG(t, `
printf '%s\n' "$@" > "$PGP_ARGS"
cat > "$PGP_STDIN"
echo '[GNUPG:] NEED_PASSPHRASE 0123456789ABCDEF 0123456789ABCDEF0123456789ABCDEF01234567 1 0' >&3
echo '[GNUPG:] SIG_CREATED D 1 8 00 1700000000 0123456789ABCDEF0123456789ABCDEF01234567' >&3
printf -- '-----BEGIN PGP SIGNATURE-----\n\nAAAA\n-----END PGP SIGNATURE-----\n'
`)

	const entity = "Content-Type: text/plain\r\n\r\nhello\r\n"
	armored, micalg, err := sign(t, context.Background(), entity)
	if err != nil {
		t.Fatalf("SignDetached: %v", err)
	}
	if want := "-----BEGIN PGP SIGNATURE-----\n\nAAAA\n-----END PGP SIGNATURE-----\n"; string(armored) != want {
		t.Fatalf("armored = %q, want %q", armored, want)
	}
	if micalg != "pgp-sha256" {
		t.Fatalf("micalg = %q, want pgp-sha256 from the status line", micalg)
	}

	got, err := os.ReadFile(stdinOut)
	if err != nil {
		t.Fatalf("read stdin capture: %v", err)
	}
	if string(got) != entity {
		t.Fatalf("gpg stdin = %q, want the entity verbatim", got)
	}

	args, err := os.ReadFile(argsOut)
	if err != nil {
		t.Fatalf("read args capture: %v", err)
	}
	gotArgs := string(args)
	for _, want := range []string{
		"--batch", "--no-tty", "--detach-sign", "--armor", "--digest-algo\nSHA256",
		"--local-user\n0123456789ABCDEF", "--status-fd\n3", "--no-autostart",
	} {
		if !strings.Contains(gotArgs, want) {
			t.Fatalf("gpg args %q missing %q", gotArgs, want)
		}
	}
}

func TestSignDetachedReportsDismissedPinentry(t *testing.T) {
	fakeGPG(t, `
echo '[GNUPG:] FAILURE sign 46' >&3
echo 'gpg: signing failed: Operation cancelled' >&2
exit 2
`)
	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("SignDetached = %v, want ErrCancelled", err)
	}
}

func TestSignDetachedRecognisesCancelledMessage(t *testing.T) {
	// Not every gpg build emits FAILURE for a dismissed dialog; the message is
	// the fallback that keeps the UI from calling it an unknown failure.
	fakeGPG(t, `
echo 'gpg: signing failed: Operation cancelled' >&2
exit 2
`)
	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("SignDetached = %v, want ErrCancelled", err)
	}
}

func TestSignDetachedReportsUnusableAgent(t *testing.T) {
	argsOut := filepath.Join(t.TempDir(), "args")
	t.Setenv("PGP_ARGS", argsOut)
	dir := fakeGPG(t, `
printf '%s\n' "$@" > "$PGP_ARGS"
echo '[GNUPG:] SIG_CREATED D 1 8 00 1700000000 0123456789ABCDEF0123456789ABCDEF01234567' >&3
printf 'sig\n'
`)
	fakeAgent(t, dir, `echo 'gpg-connect-agent: no gpg-agent running in this session' >&2`)

	_, _, err := sign(t, context.Background(), "x")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SignDetached = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "no gpg-agent running") {
		t.Fatalf("error %q does not carry the probe's message", err)
	}
	if _, statErr := os.Stat(argsOut); statErr == nil {
		t.Fatal("gpg ran even though the probe failed")
	}
}

func TestSignDetachedReportsFailingProbe(t *testing.T) {
	dir := fakeGPG(t, "cat > /dev/null; printf 'sig\\n'")
	fakeAgent(t, dir, "exit 3")

	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SignDetached = %v, want ErrUnavailable", err)
	}
}

func TestSignDetachedReportsMissingKey(t *testing.T) {
	fakeGPG(t, `
echo '[GNUPG:] NO_SECKEY 0123456789ABCDEF' >&3
echo 'gpg: skipped "ada@example.com": No secret key' >&2
exit 2
`)
	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("SignDetached = %v, want ErrNoKey", err)
	}
}

func TestSignDetachedReportsOtherFailures(t *testing.T) {
	fakeGPG(t, `
echo '[GNUPG:] FAILURE sign 11' >&3
echo 'gpg: signing failed: Bad passphrase' >&2
exit 2
`)
	_, _, err := sign(t, context.Background(), "x")
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("SignDetached = %v, want ErrFailed", err)
	}
	if !strings.Contains(err.Error(), "Bad passphrase") {
		t.Fatalf("error %q does not carry gpg's message", err)
	}
}

func TestSignDetachedRejectsUnknownDigest(t *testing.T) {
	fakeGPG(t, `
echo '[GNUPG:] SIG_CREATED D 1 99 00 1700000000 0123456789ABCDEF0123456789ABCDEF01234567' >&3
printf 'sig\n'
`)
	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrFailed) {
		t.Fatalf("SignDetached = %v, want ErrFailed for an unknown digest", err)
	}
}

func TestSignDetachedWithoutGPG(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := sign(t, context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SignDetached = %v, want ErrUnavailable", err)
	}
}

func TestSignDetachedStopsOnCancel(t *testing.T) {
	fakeGPG(t, "exec sleep 10")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, _, err := sign(t, ctx, "x"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("SignDetached = %v, want ErrCancelled", err)
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "0123456789ABCDEF", want: "0123456789ABCDEF"},
		{in: "0123456789abcdef", want: "0123456789ABCDEF"},
		{in: " 0123 4567 89ab cdef ", want: "0123456789ABCDEF"},
		{in: "0123-4567-89AB-CDEF", want: "0123456789ABCDEF"},
		{in: "0x0123456789ABCDEF", want: "0123456789ABCDEF"},
		{in: "0X0123456789abcdef", want: "0123456789ABCDEF"},
		{in: "ABCD:EF01:2345:6789:ABCD:EF01:2345:6789:ABCD:EF01", want: "ABCDEF0123456789ABCDEF0123456789ABCDEF01"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "0123456789ABCDE", wantErr: true},                           // 15 digits
		{in: "0123456789ABCDEF0", wantErr: true},                         // 17 digits
		{in: "0123456789ABCDEF0123456789ABCDEF0123456", wantErr: true},   // 39 digits
		{in: "0123456789ABCDEF0123456789ABCDEF012345678", wantErr: true}, // 41 digits
		{in: "ada@example.com", wantErr: true},
		{in: "Ada Lovelace", wantErr: true},
		{in: "0123456789ABCDEG", wantErr: true},
		{in: "0x0x0123456789ABCDEF", wantErr: true},
	}
	for _, tc := range tests {
		got, err := NormalizeKey(tc.in)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("NormalizeKey(%q) = %q, %v; want ErrInvalidKey", tc.in, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeKey(%q) = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewRejectsInvalidKey(t *testing.T) {
	if _, err := New("ada@example.com"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("New = %v, want ErrInvalidKey", err)
	}
	s, err := New("0123456789abcdef")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Key() != "0123456789ABCDEF" {
		t.Fatalf("Key = %q, want the normalized form", s.Key())
	}
}
