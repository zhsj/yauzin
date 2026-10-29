package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/zhsj/yauzin/internal/accounts"
	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// TestComposeSendCreatesTask pins the sidebar row of a send: the subject names
// it, whitespace collapses to one line, an empty subject falls back to the
// placeholder, and the row reports the sent state once the backend answers.
func TestComposeSendCreatesTask(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	c := newComposeView(a, composeNew, nil, "")
	c.to.SetText("friend@example.com")
	c.subject.SetText("  Re:\n  quarterly   report ")
	c.body.SetText("body")

	c.send()
	if len(a.tasks.tasks) != 1 {
		t.Fatalf("tasks = %d, want one per send", len(a.tasks.tasks))
	}
	id := a.tasks.tasks[0].id
	if got := a.tasks.find(id).name; got != "Re: quarterly report" {
		t.Fatalf("task name = %q, want the collapsed subject", got)
	}

	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 1 })
	got := a.tasks.find(id)
	if got == nil || got.state != taskDone {
		t.Fatalf("finished send = %+v, want it kept as done", got)
	}

	// A second, empty subject still produces its own row.
	other := newComposeView(a, composeNew, nil, "")
	other.to.SetText("friend@example.com")
	other.send()
	if len(a.tasks.tasks) != 2 {
		t.Fatalf("tasks = %d, want the second send appended", len(a.tasks.tasks))
	}
	if got := a.tasks.tasks[1].name; got != i18n.T("list.noSubject") {
		t.Fatalf("empty subject task = %q, want the placeholder", got)
	}
	drainUntil(t, a, func() bool { return len(svc.sentDrafts()) == 2 })
}

// TestComposeSendFailureKeepsTask pins the failed state: the row keeps the
// reason the same way a failed save does.
func TestComposeSendFailureKeepsTask(t *testing.T) {
	a, _, _ := composeTestApp(t)
	want := errors.New("relay refused")
	a.sendSMTP = func(context.Context, accounts.Entry, mail.Draft) error { return want }

	c := newComposeView(a, composeNew, nil, "")
	a.overlay = c
	c.to.SetText("friend@example.com")
	c.subject.SetText("no route")
	c.setSender("smtp:alt@example.com")
	c.send()

	drainUntil(t, a, func() bool { return c.errMsg != "" })
	id := a.tasks.tasks[0].id
	got := a.tasks.find(id)
	if got == nil || got.state != taskFailed {
		t.Fatalf("failed send = %+v, want it kept as failed", got)
	}
	if !errors.Is(got.err, want) {
		t.Fatalf("task err = %v, want %v", got.err, want)
	}
}

// TestComposeSendCancelKeepsDraft pins the dismiss control on a running send:
// the work is cancelled, the draft and its content stay open, and no failure is
// reported for a stop the user asked for.
func TestComposeSendCancelKeepsDraft(t *testing.T) {
	a, svc, _ := composeTestApp(t)
	started := make(chan struct{})
	a.sendSMTP = func(ctx context.Context, _ accounts.Entry, _ mail.Draft) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	c := newComposeView(a, composeNew, nil, "")
	a.overlay = c
	c.to.SetText("friend@example.com")
	c.subject.SetText("Cancel me")
	c.body.SetText("body")
	c.setSender("smtp:alt@example.com")
	c.send()

	<-started
	if len(a.tasks.tasks) != 1 {
		t.Fatalf("tasks = %d, want one per send", len(a.tasks.tasks))
	}
	id := a.tasks.tasks[0].id
	a.tasks.dismiss(id)

	if c.sending {
		t.Fatal("the composer still shows sending after the task was cancelled")
	}
	drainUntil(t, a, func() bool { return a.tasks.empty() })

	if c.errMsg != "" {
		t.Fatalf("cancelling the send reported %q", c.errMsg)
	}
	if a.overlay != c {
		t.Fatal("cancelling the send closed the draft")
	}
	if c.to.Text() != "friend@example.com" || c.subject.Text() != "Cancel me" || c.body.Text() != "body" {
		t.Fatalf("the cancelled draft lost its content: %q/%q/%q", c.to.Text(), c.subject.Text(), c.body.Text())
	}
	if sent := svc.sentDrafts(); len(sent) != 0 {
		t.Fatalf("the mailbox backend received %+v", sent)
	}
}
