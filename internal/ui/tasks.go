package ui

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/zhsj/yauzin/internal/i18n"
	"github.com/zhsj/yauzin/internal/mail"
)

// The task panel is the persistent record of background work: saving an
// attachment and sending a message both appear here. A task is added when the
// work starts, reports progress while it runs, and stays in the sidebar
// afterwards. Nothing is written to disk, so the panel starts empty after a
// restart.
const (
	taskRowHeight = unit.Dp(48)
	// taskSectionRows is how many rows are visible before the section
	// scrolls. The section sits at the bottom of the sidebar and must leave the
	// folder list enough room.
	taskSectionRows = 3
	// taskLimit bounds the history kept in memory. The oldest finished task is
	// dropped first; a running one is never dropped.
	taskLimit = 50
	// taskButtonSize is the square of the row's dismiss control.
	taskButtonSize = unit.Dp(20)
	// The task section is a white card with a hairline border, the way the
	// settings sub-cards sit on the page background.
	taskCardRadius = unit.Dp(8)
	// taskCardPadY keeps the first and the last row off the card border.
	taskCardPadY = unit.Dp(4)
	// taskRowPadX and taskRowPadRight keep the icon and the dismiss control
	// off the card border.
	taskRowPadX     = unit.Dp(10)
	taskRowPadRight = unit.Dp(6)
	taskIconCell    = unit.Dp(20)
	taskIconGap     = unit.Dp(10)
	// taskDividerIndent is where the hairline between rows starts: the row's
	// left padding plus the icon cell and its gap, which is exactly the text's
	// left edge.
	taskDividerIndent = taskRowPadX + taskIconCell + taskIconGap
)

// taskKind says which job a row reports, since the two jobs word their status
// differently and carry different icons.
type taskKind uint8

const (
	taskDownload taskKind = iota
	taskSend
)

// taskState is where one job is.
type taskState uint8

const (
	taskRunning taskState = iota
	taskDone
	taskFailed
)

// task is one background job. id identifies the attempt, while key identifies
// the work: a second save of an attachment that is still downloading is
// ignored instead of starting a second transfer. Send tasks pass no key, so
// two messages can be in flight at once.
type task struct {
	id    uint64
	kind  taskKind
	key   string
	name  string
	state taskState
	done  int64
	total int64
	// savedTo is the destination of a finished download.
	savedTo string
	err     error
	// partial marks a failed save whose target file exists but may be empty or
	// incomplete, because the dialog already created it.
	partial bool
	// cancel stops the work while it runs. It is cleared once the task ends,
	// so a finished task does not keep the draft it was started from alive.
	cancel func()
	// removed marks a task the user dismissed. The slice is only compacted
	// before the section is laid out: a row callback must not renumber the
	// list it is indexing.
	removed bool
	// btn is the row's dismiss control, which cancels a running task and
	// removes a finished one. It is built once with the task because a
	// clickable recreated per frame would lose its click.
	btn *iconButton
}

// taskPanel owns the task list. Every field is written on the UI goroutine;
// workers reach it only through post callbacks.
type taskPanel struct {
	tasks []*task
	seq   uint64
	list  layout.List
}

func newTaskPanel() *taskPanel {
	return &taskPanel{list: layout.List{Axis: layout.Vertical}}
}

// empty reports whether the section has anything to draw.
func (p *taskPanel) empty() bool {
	for _, t := range p.tasks {
		if !t.removed {
			return false
		}
	}
	return true
}

// begin adds a task and returns it. It returns nil when a task with the same
// key is still running, so the same attachment is fetched only once.
func (p *taskPanel) begin(kind taskKind, key, name string) *task {
	if key != "" {
		for _, t := range p.tasks {
			if !t.removed && t.key == key && t.state == taskRunning {
				return nil
			}
		}
	}
	p.seq++
	t := &task{id: p.seq, kind: kind, key: key, name: name, state: taskRunning}
	t.btn = newIconButton(iconClose, taskButtonSize, func() { p.dismiss(t.id) })
	t.btn.setGlyph(16)
	p.tasks = append(p.tasks, t)
	p.trim()
	return t
}

// setCancel installs the cancel of a running task. The async helpers hand it
// back only after the work started, which is still the same UI turn.
func (p *taskPanel) setCancel(id uint64, cancel func()) {
	if t := p.find(id); t != nil {
		t.cancel = cancel
	}
}

// trim keeps the history bounded. Running tasks survive an overflow; the
// oldest finished one goes first. It runs when a task is added, never from a
// row callback, so it may renumber the list directly.
func (p *taskPanel) trim() {
	for len(p.tasks) > taskLimit {
		drop := -1
		for i, t := range p.tasks {
			if t.state != taskRunning {
				drop = i
				break
			}
		}
		if drop < 0 {
			return
		}
		p.tasks = append(p.tasks[:drop], p.tasks[drop+1:]...)
	}
}

// compact drops the tasks the user dismissed. It runs before the section is
// laid out, never from a row callback.
func (p *taskPanel) compact() {
	kept := p.tasks[:0]
	for _, t := range p.tasks {
		if !t.removed {
			kept = append(kept, t)
		}
	}
	for i := len(kept); i < len(p.tasks); i++ {
		p.tasks[i] = nil
	}
	p.tasks = kept
}

// find returns the task with id, nil when it was dropped from the history or
// dismissed: a late report then becomes a no-op instead of reopening a row.
func (p *taskPanel) find(id uint64) *task {
	for _, t := range p.tasks {
		if t.id == id && !t.removed {
			return t
		}
	}
	return nil
}

// progress records how far a save has come. A retried request starts its count
// over, so Done only moves forward; a late report after the task finished is
// ignored rather than reopening it.
func (p *taskPanel) progress(id uint64, pr mail.Progress) {
	t := p.find(id)
	if t == nil || t.state != taskRunning {
		return
	}
	if pr.Total > 0 {
		t.total = pr.Total
	}
	if pr.Done > t.done {
		t.done = pr.Done
	}
}

// finish marks a task as done and remembers what the row reports. savedTo is
// the download's destination; a send has nothing to show beyond "sent".
func (p *taskPanel) finish(id uint64, savedTo string) {
	t := p.find(id)
	if t == nil || t.state != taskRunning {
		return
	}
	t.state = taskDone
	t.savedTo = savedTo
	t.cancel = nil
	if t.total > 0 {
		t.done = t.total
	}
}

// fail marks a task as failed and keeps the reason for the row. partial is true
// when the target file was already created and may be left incomplete.
func (p *taskPanel) fail(id uint64, err error, partial bool) {
	t := p.find(id)
	if t == nil || t.state != taskRunning {
		return
	}
	t.state = taskFailed
	t.err = err
	t.partial = partial
	t.cancel = nil
}

// drop removes a task that never started, for example when the user closed the
// save dialog: there is no history worth keeping and nothing to cancel.
func (p *taskPanel) drop(id uint64) {
	for i, t := range p.tasks {
		if t.id == id {
			p.tasks = append(p.tasks[:i], p.tasks[i+1:]...)
			return
		}
	}
}

// dismiss is the row's dismiss control: a running task is cancelled first, a
// finished one is only removed from the history.
func (p *taskPanel) dismiss(id uint64) {
	t := p.find(id)
	if t == nil {
		return
	}
	if t.state == taskRunning && t.cancel != nil {
		cancel := t.cancel
		t.cancel = nil
		cancel()
	}
	t.removed = true
}

// taskCardSize is the exact size of the task card: the visible rows plus the
// hairline of breathing room above and below them.
func taskCardSize(gtx layout.Context, rows int) image.Point {
	w := gtx.Constraints.Max.X
	if w <= 0 {
		w = gtx.Constraints.Min.X
	}
	return image.Pt(w, rows*gtx.Dp(taskRowHeight)+2*gtx.Dp(taskCardPadY))
}

// taskDividerRect is the hairline between two rows: it starts where the text
// starts and runs to the row's right padding.
func taskDividerRect(gtx layout.Context, rowHeight int) image.Rectangle {
	h := gtx.Dp(1)
	left := gtx.Dp(taskDividerIndent)
	right := max(gtx.Constraints.Max.X-gtx.Dp(taskRowPadRight), left)
	return image.Rect(left, rowHeight-h, right, rowHeight)
}

// LayoutSection draws the fixed task list at the bottom of the sidebar: a gap,
// the section label and up to taskSectionRows rows in a white card, scrolling
// inside the card. It returns an empty size when there is no task.
func (p *taskPanel) LayoutSection(gtx layout.Context, th *theme) layout.Dimensions {
	p.compact()
	if len(p.tasks) == 0 {
		return layout.Dimensions{}
	}
	rows := min(len(p.tasks), taskSectionRows)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// No separator line: the card's border already divides the section
		// from the folders above, and two lines this close read as noise.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 10) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 14, Right: 4, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.sectionLabel(gtx, i18n.T("task.title"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.card(gtx, th, rows)
		}),
	)
}

// card draws the white rounded panel holding the visible rows. Its height is
// known from the row count, so the background needs no second layout pass.
func (p *taskPanel) card(gtx layout.Context, th *theme, rows int) layout.Dimensions {
	size := taskCardSize(gtx, rows)
	gtx.Constraints = layout.Exact(size)
	return layout.Stack{Alignment: layout.NW}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return strokeRect(gtx, colSurface, colBorder, taskCardRadius, 1)
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: taskCardPadY, Bottom: taskCardPadY}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(size.X, rows*gtx.Dp(taskRowHeight)))
				return p.list.Layout(gtx, len(p.tasks), func(gtx layout.Context, i int) layout.Dimensions {
					return p.row(gtx, th, p.tasks[i], i < len(p.tasks)-1)
				})
			})
		}),
	)
}

// row draws one task: icon, name and a status line, with the dismiss control at
// the end. divider draws the hairline separating it from the row below, which
// the last row of the list does not get.
func (p *taskPanel) row(gtx layout.Context, th *theme, t *task, divider bool) layout.Dimensions {
	h := gtx.Dp(taskRowHeight)
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	if divider {
		// Painted before the content and outside the row's horizontal padding,
		// so it can run all the way to the card's right edge.
		area := clip.Rect(taskDividerRect(gtx, h)).Push(gtx.Ops)
		paint.Fill(gtx.Ops, colBorder)
		area.Pop()
	}
	ic := iconFile
	if t.kind == taskSend {
		ic = iconSent
	}
	switch t.state {
	case taskDone:
		ic = iconCheckPrimary
	case taskFailed:
		ic = icon{glyph: glyphAlert, col: colDanger}
	}
	if t.state == taskRunning {
		t.btn.setTip(i18n.T("task.cancel"))
	} else {
		t.btn.setTip(i18n.T("task.remove"))
	}
	return layout.Inset{Left: taskRowPadX, Right: taskRowPadRight}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: taskIconGap}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return th.glyphIn(gtx, ic, 16, taskIconCell)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return th.text(gtx, t.name, textMeta, colText, font.Normal, 1)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return vgap(gtx, 6)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return p.status(gtx, th, t)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return t.btn.Layout(gtx, th)
				})
			}),
		)
	})
}

// status is the second line of a row: the progress of a running save, the
// destination of a finished one, the result of a send, or the reason of a
// failure.
func (p *taskPanel) status(gtx layout.Context, th *theme, t *task) layout.Dimensions {
	switch t.state {
	case taskDone:
		if t.kind == taskSend {
			return th.text(gtx, i18n.T("task.sent"), textTiny, colMuted, font.Normal, 1)
		}
		return th.text(gtx, i18n.T("task.savedTo", map[string]string{"Path": t.savedTo}), textTiny, colMuted, font.Normal, 1)
	case taskFailed:
		if t.partial {
			return th.text(gtx, i18n.T("task.failedPartial", map[string]string{"Error": errorText(t.err)}), textTiny, colDanger, font.Normal, 1)
		}
		return th.text(gtx, i18n.T("task.failed", map[string]string{"Error": errorText(t.err)}), textTiny, colDanger, font.Normal, 1)
	}
	if t.kind == taskSend {
		return th.text(gtx, i18n.T("task.sending"), textTiny, colMuted, font.Normal, 1)
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return progressTrack(gtx, t)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hgap(gtx, 8) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The percentage keeps a fixed cell, so the bar does not jump as
			// the number goes from 9% to 10%.
			gtx.Constraints.Min.X = gtx.Dp(34)
			gtx.Constraints.Max.X = gtx.Dp(34)
			return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.text(gtx, progressLabel(t), textTiny, colMuted, font.Normal, 1)
			})
		}),
	)
}

// progressLabel is the percentage of a known transfer, or an ellipsis while the
// total is unknown.
func progressLabel(t *task) string {
	if t.total <= 0 {
		return "…"
	}
	return fmt.Sprintf("%d%%", min(t.done*100/t.total, 100))
}

// progressTrack draws the 4dp bar of a running task. A known total fills it in
// proportion; an unknown one moves a segment back and forth and asks for the
// next frame so the motion continues.
func progressTrack(gtx layout.Context, t *task) layout.Dimensions {
	h := gtx.Dp(4)
	w := gtx.Constraints.Max.X
	if w <= 0 || h <= 0 {
		return layout.Dimensions{}
	}
	radius := gtx.Dp(2)
	track := clip.RRect{Rect: image.Rect(0, 0, w, h), NW: radius, NE: radius, SE: radius, SW: radius}
	ts := track.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colSurface2)
	ts.Pop()

	var fill image.Rectangle
	if t.total > 0 {
		if fillW := int(int64(w) * min(max(t.done, 0), t.total) / t.total); fillW > 0 {
			fill = image.Rect(0, 0, fillW, h)
		}
	} else {
		segW := max(w/4, gtx.Dp(16))
		if segW > w {
			segW = w
		}
		span := w - segW
		const period = 1400 * time.Millisecond
		phase := math.Mod(float64(gtx.Now.UnixMilli()), float64(period.Milliseconds())) /
			float64(period.Milliseconds())
		if phase < 0 {
			phase += 1
		}
		if phase > 0.5 {
			phase = 1 - phase
		}
		x := int(float64(span) * phase * 2)
		fill = image.Rect(x, 0, x+segW, h)
		// Nothing else invalidates a moving bar; one frame later it is where
		// the clock says it should be.
		gtx.Execute(op.InvalidateCmd{})
	}
	if !fill.Empty() {
		fr := clip.RRect{Rect: fill, NW: radius, NE: radius, SE: radius, SW: radius}
		fs := fr.Push(gtx.Ops)
		paint.Fill(gtx.Ops, colPrimary)
		fs.Pop()
	}
	return layout.Dimensions{Size: image.Pt(w, h)}
}
