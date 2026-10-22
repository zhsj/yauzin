package ui

import (
	"context"
	"testing"
	"time"

	"github.com/zhsj/yauzin/internal/mail"
)

// progressiveService holds the folder details until the test closes gate, so a
// test can look at the sidebar after the names arrived but before the counters
// did. That gap is what the three-stage load leaves visible.
type progressiveService struct {
	fakeService
	foldersFirst  []mail.Folder
	foldersSecond []mail.Folder
	gate          chan struct{}
}

func (p *progressiveService) waitGate(ctx context.Context) error {
	if p.gate == nil {
		return nil
	}
	select {
	case <-p.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *progressiveService) Folders(context.Context) ([]mail.Folder, error) {
	return p.foldersFirst, nil
}

func (p *progressiveService) FolderDetails(ctx context.Context, folders []mail.Folder) ([]mail.Folder, error) {
	if err := p.waitGate(ctx); err != nil {
		return nil, err
	}
	last := folders
	if len(p.foldersSecond) > 0 {
		last = p.foldersSecond
	}
	return last, nil
}

// TestFoldersFillCountersInPlace keeps the sidebar usable while the label
// counters are still loading: the names appear right away and the unread
// counters follow when the details arrive.
func TestFoldersFillCountersInPlace(t *testing.T) {
	a := testApp(t)
	svc := &progressiveService{
		foldersFirst: []mail.Folder{
			{ID: "inbox", Kind: mail.FolderInbox},
			{ID: "work", Name: "Work", Kind: mail.FolderLabel},
		},
		foldersSecond: []mail.Folder{
			{ID: "inbox", Kind: mail.FolderInbox, Unread: 4},
			{ID: "work", Name: "Work", Kind: mail.FolderLabel, Unread: 7, Color: "#0b57d0"},
		},
		gate: make(chan struct{}),
	}
	useService(a, svc)

	a.reloadFolders()
	waitFor(t, a, "the folder names", func() bool {
		return len(a.folders) == 2 && a.folders[0].Unread == 0 && a.folders[1].Color == ""
	})

	close(svc.gate)
	waitFor(t, a, "the label counters", func() bool {
		return len(a.folders) == 2 && a.folders[0].Unread == 4 && a.folders[1].Unread == 7
	})
	if got := a.side.folders[0].count; got != 4 {
		t.Fatalf("sidebar inbox count = %d, want 4", got)
	}
}

// staleService ignores its cancelled context on purpose, so a test can make
// a superseded request return its rows after a newer one took over.
type staleService struct {
	fakeService
	rows    []mail.ThreadSummary
	release chan struct{}
}

func (l *staleService) ListThreads(_ context.Context, _ mail.ListRequest) (mail.ThreadPage, error) {
	<-l.release
	return mail.ThreadPage{Threads: l.rows, Total: len(l.rows)}, nil
}

// TestSupersededRequestResultIsDropped guards the generation check: a request
// that was superseded and ignores its cancellation must not overwrite what is
// on screen when its result finally arrives.
func TestSupersededRequestResultIsDropped(t *testing.T) {
	a := testApp(t)
	late := &staleService{
		rows:    []mail.ThreadSummary{{ID: "stale", Subject: "stale"}},
		release: make(chan struct{}),
	}
	useService(a, late)

	a.loadThreads()

	// A new load supersedes the old one; its empty page clears the rows.
	useService(a, &fakeService{})
	a.loadThreads()
	waitFor(t, a, "the empty page", func() bool { return len(a.list.threads) == 0 })

	// The old request ignores the cancellation and returns its rows; wait for
	// the result to be posted, then drain it so the guard can drop it.
	close(late.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.postMu.Lock()
		pending := len(a.queue) > 0
		a.postMu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stale result was never posted")
		}
		time.Sleep(time.Millisecond)
	}
	a.drain()
	if len(a.list.threads) != 0 {
		t.Fatalf("stale result put %d rows back on screen", len(a.list.threads))
	}
}
