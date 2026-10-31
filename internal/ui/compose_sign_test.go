package ui

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
	"github.com/zhsj/yauzin/internal/pgp"
)

// testSignature is what the injected signer returns: the composer only carries
// the record, so the bytes never reach a transport in these tests.
func testSignature(key string) *mail.Signature {
	return &mail.Signature{Data: []byte("-----BEGIN PGP SIGNATURE-----\r\n\r\nAAAA\r\n-----END PGP SIGNATURE-----\r\n"), Micalg: "pgp-sha256", Key: key}
}

// signedComposer returns a composer whose chosen sender has a signing key and a
// signer that answers immediately.
func signedComposer(t *testing.T) (*App, *composeView, *accounts.Store) {
	t.Helper()
	a, _, store := composeTestApp(t)
	entry, _ := store.Entry("smtp:alt@example.com")
	entry.SigningKey = "0123456789ABCDEF"
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("store key: %v", err)
	}
	c := newComposeView(a, composeNew, nil, "")
	c.setSender("smtp:alt@example.com")
	c.to.SetText("friend@example.com")
	c.body.SetText("body")
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		return testSignature("0123456789ABCDEF"), nil
	}
	a.overlay = c
	return a, c, store
}

func TestComposeSignMarksTheDraft(t *testing.T) {
	a, c, _ := signedComposer(t)
	var asked struct {
		key   string
		draft mail.Draft
	}
	a.signDraft = func(_ context.Context, key string, draft mail.Draft) (*mail.Signature, error) {
		asked.key, asked.draft = key, draft
		return testSignature(key), nil
	}

	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })

	if asked.key != "0123456789ABCDEF" {
		t.Fatalf("signed with %q, want the sender's key", asked.key)
	}
	if asked.draft.Body != "body" {
		t.Fatalf("signed body = %q", asked.draft.Body)
	}
	if c.signing {
		t.Fatal("the composer still reports signing after the signature arrived")
	}
	if c.signed.Key != "0123456789ABCDEF" {
		t.Fatalf("signed = %+v", c.signed)
	}
	if task := a.tasks.tasks[0]; task.kind != taskSign || task.state != taskDone {
		t.Fatalf("task = %+v, want a finished signing task", task)
	}
}

// TestComposeSignButtonFollowsTheIdentityKey pins the visibility rule of the
// footer: the button exists only while the chosen identity has a key, so the
// composer never shows a control whose step cannot run.
func TestComposeSignButtonFollowsTheIdentityKey(t *testing.T) {
	_, c, _ := signedComposer(t)
	if !c.canSign() {
		t.Fatalf("sender %q has a key but the footer hides the button", c.fromID)
	}

	// The demo mailbox carries no key.
	c.setSender("demo")
	if c.canSign() {
		t.Fatal("the footer offers signing for an identity without a key")
	}
}

// TestComposeSignAgainReplacesTheSignature keeps re-signing available: the
// button stays live on a signed draft and the newer signature replaces the old
// one instead of being refused.
func TestComposeSignAgainReplacesTheSignature(t *testing.T) {
	a, c, _ := signedComposer(t)
	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })
	first := c.signed
	gen := c.signGen

	a.signDraft = func(_ context.Context, key string, _ mail.Draft) (*mail.Signature, error) {
		return testSignature(key), nil
	}
	c.sign()
	drainUntil(t, a, func() bool { return !c.signing })

	if c.signed == nil || c.signed == first {
		t.Fatalf("signed = %+v, want the second signature", c.signed)
	}
	if c.signGen <= gen {
		t.Fatalf("signGen = %d, want it past %d", c.signGen, gen)
	}
	if c.errMsg != "" {
		t.Fatalf("errMsg = %q, want a clean re-sign", c.errMsg)
	}
}

// TestComposeSignKeepsTheOldBadge covers the frame between the click and the
// new signature: the old signature still covers the draft, so the badge stays
// up while gpg works.
func TestComposeSignKeepsTheOldBadge(t *testing.T) {
	a, c, _ := signedComposer(t)
	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })
	first := c.signed

	release := make(chan struct{})
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		<-release
		return testSignature("0123456789ABCDEF"), nil
	}
	c.sign()
	if !c.signing {
		t.Fatal("the second run is not in flight")
	}
	if c.signed != first {
		t.Fatal("the badge vanished while the new signature was being made")
	}

	close(release)
	drainUntil(t, a, func() bool { return !c.signing })
}

// TestComposeSignWithoutKeyDoesNothing covers the guard behind the hidden
// button: an identity without a key must not reach gpg, not start a task and
// not put a message in the footer, because no click can get here.
func TestComposeSignWithoutKeyDoesNothing(t *testing.T) {
	a, _, _ := composeTestApp(t)
	called := false
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		called = true
		return testSignature(""), nil
	}
	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText("friend@example.com")
	// The demo mailbox has no key configured.
	c.sign()

	if called {
		t.Fatal("gpg was asked to sign without a key")
	}
	if c.signing {
		t.Fatal("the composer entered the signing state without a key")
	}
	if c.signed != nil {
		t.Fatalf("signed = %+v, want nothing", c.signed)
	}
	if c.errMsg != "" {
		t.Fatalf("errMsg = %q, want the guard to stay silent", c.errMsg)
	}
	if len(a.tasks.tasks) != 0 {
		t.Fatalf("tasks = %d, want none without a key", len(a.tasks.tasks))
	}
}

func TestComposeSignFailureKeepsDraftUnsigned(t *testing.T) {
	a, c, _ := signedComposer(t)
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		return nil, pgp.ErrNoKey
	}

	c.sign()
	drainUntil(t, a, func() bool { return c.errMsg != "" })

	if c.signed != nil {
		t.Fatal("a failed signature left the draft marked signed")
	}
	if !strings.Contains(c.errMsg, i18n.T("error.gpgNoKey")) {
		t.Fatalf("errMsg = %q, want it to explain the missing key", c.errMsg)
	}
	if c.body.Text() != "body" || c.to.Text() != "friend@example.com" {
		t.Fatal("the draft lost its content")
	}
}

func TestComposeSignDroppedWhenTheDraftChanges(t *testing.T) {
	a, c, _ := signedComposer(t)
	release := make(chan struct{})
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		<-release
		return testSignature("0123456789ABCDEF"), nil
	}

	c.sign()
	// The body changed while gpg was working, which is what watchBody reports.
	c.clearSignature()
	close(release)
	drainUntil(t, a, func() bool { return !c.signing })

	if c.signed != nil {
		t.Fatal("the signature of the older draft was applied to the edited one")
	}
}

func TestComposeBodyChangeDropsTheSignature(t *testing.T) {
	a, c, _ := signedComposer(t)
	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })

	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Constraints{Max: image.Pt(600, 400)}}
	c.body.SetText("body, edited")
	c.watchBody(gtx)

	if c.signed != nil {
		t.Fatal("the signature survived an edit to the body it covers")
	}
}

func TestComposeAttachmentChangeDropsTheSignature(t *testing.T) {
	a, c, _ := signedComposer(t)
	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })

	c.addAttachment(mail.DraftAttachment{Name: "note.txt", Path: t.TempDir() + "/note.txt"})
	if c.signed != nil {
		t.Fatal("the signature survived a new attachment")
	}

	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })
	c.removeAttachment(0)
	if c.signed != nil {
		t.Fatal("the signature survived a removed attachment")
	}
}

func TestComposeSenderChangeDropsTheSignature(t *testing.T) {
	a, _, store := signedComposer(t)
	entry, _ := store.Entry("smtp:quiet@example.com")
	entry.SigningKey = "ABCDEF0123456789"
	if err := store.Upsert(entry); err != nil {
		t.Fatalf("store key: %v", err)
	}
	// The composer read its senders when it opened, so build it after both keys
	// exist.
	c := newComposeView(a, composeNew, nil, "")
	a.overlay = c
	c.setSender("smtp:alt@example.com")
	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })

	c.setSender("smtp:quiet@example.com")
	if c.signed != nil {
		t.Fatal("the signature of the previous identity survived")
	}
}

func TestComposeSendCarriesTheSignature(t *testing.T) {
	a, c, _ := signedComposer(t)
	sent := make(chan mail.Draft, 1)
	a.sendSMTP = func(_ context.Context, _ accounts.Entry, draft mail.Draft) error {
		sent <- draft
		return nil
	}

	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })
	c.send()

	select {
	case draft := <-sent:
		if draft.Signature == nil {
			t.Fatal("the draft went out without its signature")
		}
		if string(draft.Signature.Data) != string(c.signed.Data) {
			t.Fatal("the transported signature is not the one the composer made")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the draft never reached the sender")
	}
}

func TestComposeSendUnsignedDraftStaysUnsigned(t *testing.T) {
	a, c, _ := signedComposer(t)
	sent := make(chan mail.Draft, 1)
	a.sendSMTP = func(_ context.Context, _ accounts.Entry, draft mail.Draft) error {
		sent <- draft
		return nil
	}

	// The identity has a key, but the user never asked for a signature: the
	// two-step design sends it as it is.
	c.send()
	select {
	case draft := <-sent:
		if draft.Signature != nil {
			t.Fatal("the draft was signed without being asked to")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the draft never reached the sender")
	}
}

func TestComposeSendWhileSigningDoesNothing(t *testing.T) {
	a, c, _ := signedComposer(t)
	called := make(chan struct{}, 1)
	a.sendSMTP = func(context.Context, accounts.Entry, mail.Draft) error {
		called <- struct{}{}
		return nil
	}
	release := make(chan struct{})
	a.signDraft = func(context.Context, string, mail.Draft) (*mail.Signature, error) {
		<-release
		return testSignature("0123456789ABCDEF"), nil
	}

	c.sign()
	c.send()
	close(release)
	drainUntil(t, a, func() bool { return !c.signing })

	if len(called) != 0 {
		t.Fatal("a send started while the signature was still being made")
	}
}

func TestComposeSignCancelKeepsDraftUnsigned(t *testing.T) {
	a, c, _ := signedComposer(t)
	a.signDraft = func(ctx context.Context, _ string, _ mail.Draft) (*mail.Signature, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	c.sign()
	drainUntil(t, a, func() bool { return len(a.tasks.tasks) == 1 })
	a.tasks.dismiss(a.tasks.tasks[0].id)
	drainUntil(t, a, func() bool { return !c.signing })

	if c.signed != nil {
		t.Fatal("a cancelled signature marked the draft signed")
	}
	if want := i18n.T("compose.signCancelled"); c.errMsg != want {
		t.Fatalf("errMsg = %q, want %q", c.errMsg, want)
	}
	if c.body.Text() != "body" {
		t.Fatal("the draft lost its content")
	}
}

func TestComposeDiscardStopsSigning(t *testing.T) {
	a, c, _ := signedComposer(t)
	stopped := make(chan struct{})
	a.signDraft = func(ctx context.Context, _ string, _ mail.Draft) (*mail.Signature, error) {
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}

	c.sign()
	c.discard()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the composer left gpg running")
	}
	if c.signed != nil || c.signing {
		t.Fatalf("state after discard: signed=%v signing=%v", c.signed, c.signing)
	}
}

func TestComposeStaleSignatureErrorClearsTheBadge(t *testing.T) {
	a, c, _ := signedComposer(t)
	a.sendSMTP = func(context.Context, accounts.Entry, mail.Draft) error {
		return mail.ErrSignatureStale
	}

	c.sign()
	drainUntil(t, a, func() bool { return c.signed != nil })
	c.send()
	drainUntil(t, a, func() bool { return c.errMsg != "" })

	if c.signed != nil {
		t.Fatal("the stale signature stayed on the draft")
	}
	if !strings.Contains(c.errMsg, i18n.T("error.signatureStale")) {
		t.Fatalf("errMsg = %q, want the re-sign hint", c.errMsg)
	}
}

// TestComposeSignErrorTextIsLocalised pins the mapping from the pgp errors to
// the sentences the footer shows.
func TestComposeSignErrorTextIsLocalised(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{pgp.ErrNoKey, i18n.T("error.gpgNoKey")},
		{pgp.ErrCancelled, i18n.T("error.gpgCancelled")},
		{pgp.ErrUnavailable, i18n.T("error.gpgUnavailable")},
		{pgp.ErrInvalidKey, i18n.T("error.gpgInvalidKey")},
	} {
		if got := errorText(tc.err); got != tc.want {
			t.Errorf("errorText(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if got := errorText(errors.Join(pgp.ErrFailed, errors.New("key expired"))); !strings.Contains(got, "key expired") {
		t.Errorf("errorText(ErrFailed) = %q, want gpg's own message kept", got)
	}
}
