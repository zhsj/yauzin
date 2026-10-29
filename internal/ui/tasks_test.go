package ui

import (
	"errors"
	"fmt"
	"image"
	"testing"

	"gioui.org/op"

	"github.com/zhsj/yauzin/internal/mail"
)

func TestTaskPanelLifecycle(t *testing.T) {
	p := newTaskPanel()
	task := p.begin(taskDownload, "m1\x00a1", "report.pdf")
	if task == nil {
		t.Fatal("begin returned no task")
	}
	if p.begin(taskDownload, "m1\x00a1", "report.pdf") != nil {
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
	if got.state != taskDone {
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
	if again := p.find(task.id); again.state != taskDone || again.done != 100 {
		t.Fatalf("finished task changed to %+v", again)
	}
}

func TestTaskPanelFail(t *testing.T) {
	p := newTaskPanel()
	task := p.begin(taskDownload, "k", "broken.bin")
	want := errors.New("disk full")
	p.fail(task.id, want, true)
	got := p.find(task.id)
	if got.state != taskFailed {
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

// TestTaskPanelDrop pins that closing the save dialog leaves no history:
// nothing was downloaded, so there is no task worth keeping.
func TestTaskPanelDrop(t *testing.T) {
	p := newTaskPanel()
	done := p.begin(taskDownload, "done", "done.bin")
	p.finish(done.id, "done.bin")
	open := p.begin(taskDownload, "open", "open.bin")

	p.drop(open.id)
	if p.find(open.id) != nil {
		t.Fatal("dropped task is still in the list")
	}
	if p.find(done.id) == nil {
		t.Fatal("dropping one task dropped another")
	}
	// Dropping twice, or an id that never existed, is harmless.
	p.drop(open.id)
	p.drop(9999)
	if len(p.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(p.tasks))
	}
}

// TestTaskPanelRepeatsFinishedSave pins that the same attachment can be saved
// again after the first attempt finished, as a new task.
func TestTaskPanelRepeatsFinishedSave(t *testing.T) {
	p := newTaskPanel()
	first := p.begin(taskDownload, "k", "a.bin")
	p.finish(first.id, "a.bin")

	second := p.begin(taskDownload, "k", "a.bin")
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

// TestTaskPanelKeepsHistoryBounded pins the cap and the rule that a running
// task is never evicted for a finished one.
func TestTaskPanelKeepsHistoryBounded(t *testing.T) {
	p := newTaskPanel()
	running := p.begin(taskDownload, "running", "keep.bin")
	for i := 0; i < taskLimit+5; i++ {
		task := p.begin(taskDownload, fmt.Sprintf("k%d", i), "a.bin")
		p.finish(task.id, "a.bin")
	}
	if len(p.tasks) != taskLimit {
		t.Fatalf("history holds %d tasks, want %d", len(p.tasks), taskLimit)
	}
	if p.find(running.id) == nil {
		t.Fatal("a running task was evicted from the history")
	}
}

func TestTaskPanelKnownAndUnknownTotal(t *testing.T) {
	p := newTaskPanel()
	known := p.begin(taskDownload, "known", "known.bin")
	p.progress(known.id, mail.Progress{Done: 25, Total: 100})
	unknown := p.begin(taskDownload, "unknown", "unknown.bin")
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

// TestTaskPanelSend pins the second kind of job: a send passes no key, so two
// drafts can be in flight, and it ends as done or failed like a save.
func TestTaskPanelSend(t *testing.T) {
	p := newTaskPanel()
	first := p.begin(taskSend, "", "Quarterly report")
	if first.kind != taskSend {
		t.Fatalf("kind = %v, want a send", first.kind)
	}
	if second := p.begin(taskSend, "", "Quarterly report"); second == nil {
		t.Fatal("two sends of the same subject were deduplicated")
	}
	p.finish(first.id, "")
	if got := p.find(first.id); got.state != taskDone || got.savedTo != "" {
		t.Fatalf("finished send = %+v", got)
	}

	second := p.begin(taskSend, "", "Another subject")
	p.fail(second.id, errors.New("relay refused"), false)
	got := p.find(second.id)
	if got.state != taskFailed || got.partial {
		t.Fatalf("failed send = %+v, want a plain failure", got)
	}
}

// TestTaskPanelDismiss pins the row's dismiss control: a running task is
// cancelled on the way out, a finished one is only removed, and the slice is
// compacted before the next layout instead of during the row callbacks.
func TestTaskPanelDismiss(t *testing.T) {
	p := newTaskPanel()
	cancelled := false
	running := p.begin(taskDownload, "running", "keep.bin")
	p.setCancel(running.id, func() { cancelled = true })
	done := p.begin(taskDownload, "done", "done.bin")
	p.finish(done.id, "done.bin")

	// The button is wired to dismiss its own row.
	running.btn.onTap()
	if !cancelled {
		t.Fatal("the dismiss control did not cancel the running task")
	}
	if p.find(running.id) != nil {
		t.Fatal("the cancelled task survived the dismiss")
	}
	// A late result of the cancelled work must not reopen the row.
	p.progress(running.id, mail.Progress{Done: 1, Total: 2})
	p.finish(running.id, "keep.bin")
	if p.find(running.id) != nil {
		t.Fatal("a late report reopened a dismissed task")
	}
	if len(p.tasks) != 2 {
		t.Fatalf("tasks = %d before compact, want the dismissed row kept for the frame", len(p.tasks))
	}
	p.compact()
	if len(p.tasks) != 1 || p.find(done.id) == nil {
		t.Fatalf("tasks = %d, want only the finished one", len(p.tasks))
	}

	p.dismiss(done.id)
	if !p.empty() {
		t.Fatal("the section still reports a task after the last one was removed")
	}
	p.compact()
	if len(p.tasks) != 0 {
		t.Fatalf("tasks = %d, want the list empty", len(p.tasks))
	}
	// Dismissing twice is harmless.
	p.dismiss(done.id)
}

// TestTaskCardGeometry pins the card and the hairline between rows: the card
// hugs the row count, and the divider starts at the text's left edge and stops
// short of the card's right border.
func TestTaskCardGeometry(t *testing.T) {
	var ops op.Ops
	gtx := testGtx(&ops, image.Pt(240, 340))
	row := gtx.Dp(taskRowHeight)
	if got, want := taskCardSize(gtx, 1), image.Pt(240, row+2*gtx.Dp(taskCardPadY)); got != want {
		t.Fatalf("card of one row = %v, want %v", got, want)
	}
	if got, want := taskCardSize(gtx, taskSectionRows),
		image.Pt(240, taskSectionRows*row+2*gtx.Dp(taskCardPadY)); got != want {
		t.Fatalf("card of %d rows = %v, want %v", taskSectionRows, got, want)
	}

	// The indent is the row padding plus the icon cell and its gap, so the line
	// starts under the text and not under the icon.
	if got := gtx.Dp(taskDividerIndent); got != 40 {
		t.Fatalf("divider indent = %d, want 40", got)
	}
	want := image.Rect(40, row-1, 240-6, row)
	if got := taskDividerRect(gtx, row); got != want {
		t.Fatalf("divider = %v, want %v", got, want)
	}

	// A row too narrow to hold the indent collapses the line instead of
	// painting it backwards.
	narrow := gtx
	narrow.Constraints.Max.X = 30
	if got := taskDividerRect(narrow, row); got.Dx() != 0 {
		t.Fatalf("narrow divider = %v, want no width", got)
	}
}

// TestTaskPanelLayout pins the section's size in the sidebar: it hugs the
// sidebar width, stops growing once the visible rows are full, and draws both
// kinds of job.
func TestTaskPanelLayout(t *testing.T) {
	a := testApp(t)
	if dims := a.tasks.LayoutSection(testGtx(new(op.Ops), testWindow), a.th); dims.Size != (image.Point{}) {
		t.Fatalf("empty section drew %v", dims.Size)
	}

	running := a.tasks.begin(taskDownload, "m1\x00a1", "a-very-long-attachment-name-that-must-be-truncated.pdf")
	a.tasks.progress(running.id, mail.Progress{Done: 40, Total: 100})
	unknown := a.tasks.begin(taskDownload, "m2\x00a2", "unknown.bin")
	a.tasks.progress(unknown.id, mail.Progress{Done: 1024})
	sent := a.tasks.begin(taskSend, "", "Quarterly report")
	a.tasks.finish(sent.id, "")
	failed := a.tasks.begin(taskSend, "", "Bounced message")
	a.tasks.fail(failed.id, errors.New("network unreachable"), false)

	var ops op.Ops
	sidebar := image.Pt(240, 340)
	gtx := testGtx(&ops, sidebar)
	dims := a.tasks.LayoutSection(gtx, a.th)
	if dims.Size.X != sidebar.X {
		t.Fatalf("section width = %d, want the sidebar width %d", dims.Size.X, sidebar.X)
	}
	if dims.Size.Y <= 0 || dims.Size.Y > sidebar.Y {
		t.Fatalf("section height = %d, want a positive height inside %d", dims.Size.Y, sidebar.Y)
	}

	// More tasks than visible rows: the section scrolls instead of growing.
	full := dims.Size
	for i := 0; i < taskSectionRows+3; i++ {
		task := a.tasks.begin(taskDownload, fmt.Sprintf("more%d", i), fmt.Sprintf("file%d.bin", i))
		a.tasks.finish(task.id, "file.bin")
	}
	gtx = testGtx(&ops, sidebar)
	if grown := a.tasks.LayoutSection(gtx, a.th); grown.Size.Y != full.Y {
		t.Fatalf("section grew to %d with more tasks, want %d", grown.Size.Y, full.Y)
	}

	// The whole frame has to survive the section with the snackbar, on a narrow
	// and short window.
	a.status.show("saved", false)
	gtx = testGtx(&ops, smallWindow)
	a.layout(gtx)
	ops.Reset()
}
