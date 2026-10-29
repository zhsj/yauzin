package ui

import (
	"errors"
	"fmt"
	"image"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestDownloadPanelLifecycle(t *testing.T) {
	p := newDownloadPanel()
	task := p.begin("m1\x00a1", "report.pdf")
	if task == nil {
		t.Fatal("begin returned no task")
	}
	if p.begin("m1\x00a1", "report.pdf") != nil {
		t.Fatal("a second click started another download of the same attachment")
	}

	p.progress(task.id, mail.Progress{Done: 40, Total: 100})
	// A retried request restarts its count: the panel has to keep the high mark.
	p.progress(task.id, mail.Progress{Done: 10, Total: 100})
	if got := p.find(task.id); got.done != 40 || got.total != 100 {
		t.Fatalf("progress = %d/%d, want 40/100", got.done, got.total)
	}

	p.finish(task.id, "report.pdf")
	got := p.find(task.id)
	if got.state != downloadDone {
		t.Fatalf("state = %v, want done", got.state)
	}
	if got.savedTo != "report.pdf" {
		t.Fatalf("savedTo = %q", got.savedTo)
	}
	if got.done != got.total {
		t.Fatalf("finished task at %d/%d, want a full bar", got.done, got.total)
	}

	// A late report must not reopen a finished task.
	p.progress(task.id, mail.Progress{Done: 5, Total: 100})
	if again := p.find(task.id); again.state != downloadDone || again.done != 100 {
		t.Fatalf("finished task changed to %+v", again)
	}
}

func TestDownloadPanelFail(t *testing.T) {
	p := newDownloadPanel()
	task := p.begin("k", "broken.bin")
	want := errors.New("disk full")
	p.fail(task.id, want, true)
	got := p.find(task.id)
	if got.state != downloadFailed {
		t.Fatalf("state = %v, want failed", got.state)
	}
	if !errors.Is(got.err, want) {
		t.Fatalf("err = %v, want %v", got.err, want)
	}
	if !got.partial {
		t.Fatal("partial failure lost its flag")
	}
	p.fail(task.id, errors.New("second"), false)
	if again := p.find(task.id); !errors.Is(again.err, want) || !again.partial {
		t.Fatal("a second failure replaced the first")
	}
}

// TestDownloadPanelCancel pins that closing the save dialog leaves no history:
// nothing was downloaded, so there is no task worth keeping.
func TestDownloadPanelCancel(t *testing.T) {
	p := newDownloadPanel()
	done := p.begin("done", "done.bin")
	p.finish(done.id, "done.bin")
	open := p.begin("open", "open.bin")

	p.cancel(open.id)
	if p.find(open.id) != nil {
		t.Fatal("cancelled task is still in the list")
	}
	if p.find(done.id) == nil {
		t.Fatal("cancelling one task dropped another")
	}
	// Cancelling twice, or an id that never existed, is harmless.
	p.cancel(open.id)
	p.cancel(9999)
	if len(p.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(p.tasks))
	}
}

// TestDownloadPanelRepeatsFinishedSave pins that the same attachment can be
// saved again after the first attempt finished, as a new task.
func TestDownloadPanelRepeatsFinishedSave(t *testing.T) {
	p := newDownloadPanel()
	first := p.begin("k", "a.bin")
	p.finish(first.id, "a.bin")

	second := p.begin("k", "a.bin")
	if second == nil {
		t.Fatal("a finished attachment could not be saved again")
	}
	if second.id == first.id {
		t.Fatal("the second save reused the first task")
	}
	if len(p.tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(p.tasks))
	}
	p.progress(second.id, mail.Progress{Done: 1, Total: 2})
	if got := p.find(first.id); got.done != got.total {
		t.Fatalf("progress of the new save reached the old task: %+v", got)
	}
	if got := p.find(second.id); got.done != 1 {
		t.Fatalf("new task at %d, want 1", got.done)
	}
}

// TestDownloadPanelKeepsHistoryBounded pins the cap and the rule that a running
// task is never evicted for a finished one.
func TestDownloadPanelKeepsHistoryBounded(t *testing.T) {
	p := newDownloadPanel()
	running := p.begin("running", "keep.bin")
	for i := 0; i < downloadTaskLimit+5; i++ {
		task := p.begin(fmt.Sprintf("k%d", i), "a.bin")
		p.finish(task.id, "a.bin")
	}
	if len(p.tasks) != downloadTaskLimit {
		t.Fatalf("history holds %d tasks, want %d", len(p.tasks), downloadTaskLimit)
	}
	if p.find(running.id) == nil {
		t.Fatal("a running task was evicted from the history")
	}
}

func TestDownloadPanelWithKnownAndUnknownTotal(t *testing.T) {
	p := newDownloadPanel()
	known := p.begin("known", "known.bin")
	p.progress(known.id, mail.Progress{Done: 25, Total: 100})
	unknown := p.begin("unknown", "unknown.bin")
	p.progress(unknown.id, mail.Progress{Done: 4096})

	if got := progressLabel(p.find(known.id)); got != "25%" {
		t.Fatalf("known label = %q, want 25%%", got)
	}
	if got := progressLabel(p.find(unknown.id)); got != "…" {
		t.Fatalf("unknown label = %q, want an ellipsis", got)
	}

	// The percentage never runs past 100 even if the backend overshoots.
	p.progress(known.id, mail.Progress{Done: 150, Total: 100})
	if got := progressLabel(p.find(known.id)); got != "100%" {
		t.Fatalf("label = %q, want 100%%", got)
	}
}

// TestDownloadPanelLayout pins the section's size in the sidebar: it hugs the
// sidebar width, and stops growing once the visible rows are full.
func TestDownloadPanelLayout(t *testing.T) {
	a := testApp(t)
	if dims := a.downloads.LayoutSection(testGtx(new(op.Ops), testWindow), a.th); dims.Size != (image.Point{}) {
		t.Fatalf("empty section drew %v", dims.Size)
	}

	running := a.downloads.begin("m1\x00a1", "a-very-long-attachment-name-that-must-be-truncated.pdf")
	a.downloads.progress(running.id, mail.Progress{Done: 40, Total: 100})
	unknown := a.downloads.begin("m2\x00a2", "unknown.bin")
	a.downloads.progress(unknown.id, mail.Progress{Done: 1024})
	done := a.downloads.begin("m3\x00a3", "done.bin")
	a.downloads.finish(done.id, "done.bin")
	failed := a.downloads.begin("m4\x00a4", "failed.bin")
	a.downloads.fail(failed.id, errors.New("network unreachable"), true)

	var ops op.Ops
	sidebar := image.Pt(240, 340)
	gtx := testGtx(&ops, sidebar)
	dims := a.downloads.LayoutSection(gtx, a.th)
	if dims.Size.X != sidebar.X {
		t.Fatalf("section width = %d, want the sidebar width %d", dims.Size.X, sidebar.X)
	}
	if dims.Size.Y <= 0 || dims.Size.Y > sidebar.Y {
		t.Fatalf("section height = %d, want a positive height inside %d", dims.Size.Y, sidebar.Y)
	}

	// More tasks than visible rows: the section scrolls instead of growing.
	full := dims.Size
	for i := 0; i < downloadSectionRows+3; i++ {
		task := a.downloads.begin(fmt.Sprintf("more%d", i), fmt.Sprintf("file%d.bin", i))
		a.downloads.finish(task.id, "file.bin")
	}
	gtx = testGtx(&ops, sidebar)
	if grown := a.downloads.LayoutSection(gtx, a.th); grown.Size.Y != full.Y {
		t.Fatalf("section grew to %d with more tasks, want %d", grown.Size.Y, full.Y)
	}

	// The whole frame has to survive the section with the snackbar, on a narrow
	// and short window.
	a.status.show("saved", false)
	gtx = testGtx(&ops, smallWindow)
	a.layout(gtx)
	ops.Reset()
}
