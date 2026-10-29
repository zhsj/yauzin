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

// The download panel is the persistent record of attachment saves: a task is
// added when a download starts, reports progress while it runs, and stays in
// the sidebar afterwards. Nothing is written to disk, so the panel starts
// empty after a restart.
const (
	downloadRowHeight = unit.Dp(48)
	// downloadSectionRows is how many rows are visible before the section
	// scrolls. The section sits at the bottom of the sidebar and must leave the
	// folder list enough room.
	downloadSectionRows = 3
	// downloadTaskLimit bounds the history kept in memory. The oldest finished
	// task is dropped first; a running one is never dropped.
	downloadTaskLimit = 50
)

// downloadState is where one saved attachment is.
type downloadState uint8

const (
	downloadRunning downloadState = iota
	downloadDone
	downloadFailed
)

// downloadTask is one attachment save. id identifies the attempt, while key
// identifies the attachment: a second click while the file is still
// downloading is ignored instead of starting a second download.
type downloadTask struct {
	id      uint64
	key     string
	name    string
	state   downloadState
	done    int64
	total   int64
	savedTo string
	err     error
	// partial marks a failed save whose target file exists but may be empty or
	// incomplete, because the dialog already created it.
	partial bool
}

// downloadPanel owns the task list. Every field is written on the UI
// goroutine; workers reach it only through post callbacks.
type downloadPanel struct {
	tasks []*downloadTask
	seq   uint64
	list  layout.List
}

func newDownloadPanel() *downloadPanel {
	return &downloadPanel{list: layout.List{Axis: layout.Vertical}}
}

func (p *downloadPanel) empty() bool { return len(p.tasks) == 0 }

// begin adds a task and returns it. It returns nil when the same attachment is
// already downloading, so the caller does not fetch it twice.
func (p *downloadPanel) begin(key, name string) *downloadTask {
	for _, t := range p.tasks {
		if t.key == key && t.state == downloadRunning {
			return nil
		}
	}
	p.seq++
	t := &downloadTask{id: p.seq, key: key, name: name, state: downloadRunning}
	p.tasks = append(p.tasks, t)
	p.trim()
	return t
}

// trim keeps the history bounded. Running tasks survive an overflow; the
// oldest finished one goes first.
func (p *downloadPanel) trim() {
	for len(p.tasks) > downloadTaskLimit {
		drop := -1
		for i, t := range p.tasks {
			if t.state != downloadRunning {
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

// find returns the task with id, nil when it was dropped from the history.
func (p *downloadPanel) find(id uint64) *downloadTask {
	for _, t := range p.tasks {
		if t.id == id {
			return t
		}
	}
	return nil
}

// progress records how far a save has come. A retried request starts its count
// over, so Done only moves forward; a late report after the task finished is
// ignored rather than reopening it.
func (p *downloadPanel) progress(id uint64, pr mail.Progress) {
	t := p.find(id)
	if t == nil || t.state != downloadRunning {
		return
	}
	if pr.Total > 0 {
		t.total = pr.Total
	}
	if pr.Done > t.done {
		t.done = pr.Done
	}
}

// finish marks a task as saved. savedTo is the user visible name or path.
func (p *downloadPanel) finish(id uint64, savedTo string) {
	t := p.find(id)
	if t == nil || t.state != downloadRunning {
		return
	}
	t.state = downloadDone
	t.savedTo = savedTo
	if t.total > 0 {
		t.done = t.total
	}
}

// fail marks a task as failed and keeps the reason for the row. partial is true
// when the target file was already created and may be left incomplete.
func (p *downloadPanel) fail(id uint64, err error, partial bool) {
	t := p.find(id)
	if t == nil || t.state != downloadRunning {
		return
	}
	t.state = downloadFailed
	t.err = err
	t.partial = partial
}

// cancel drops a task that never started downloading, for example when the
// user closed the save dialog: there is no history worth keeping.
func (p *downloadPanel) cancel(id uint64) {
	for i, t := range p.tasks {
		if t.id == id {
			p.tasks = append(p.tasks[:i], p.tasks[i+1:]...)
			return
		}
	}
}

// LayoutSection draws the fixed task list at the bottom of the sidebar: a
// separator, the section label and up to downloadSectionRows rows, scrolling
// inside the section. It returns an empty size when there is no task.
func (p *downloadPanel) LayoutSection(gtx layout.Context, th *theme) layout.Dimensions {
	if len(p.tasks) == 0 {
		return layout.Dimensions{}
	}
	rows := min(len(p.tasks), downloadSectionRows)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return hline(gtx) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 10, Bottom: 4, Left: 14, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.sectionLabel(gtx, i18n.T("download.title"))
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			viewport := gtx
			viewport.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, rows*gtx.Dp(downloadRowHeight)))
			return p.list.Layout(viewport, len(p.tasks), func(gtx layout.Context, i int) layout.Dimensions {
				return p.row(gtx, th, p.tasks[i])
			})
		}),
	)
}

// row draws one task: icon, file name and a status line.
func (p *downloadPanel) row(gtx layout.Context, th *theme, t *downloadTask) layout.Dimensions {
	h := gtx.Dp(downloadRowHeight)
	gtx.Constraints.Min.Y = h
	gtx.Constraints.Max.Y = h
	ic := iconFile
	switch t.state {
	case downloadDone:
		ic = iconCheckPrimary
	case downloadFailed:
		ic = icon{glyph: glyphAlert, col: colDanger}
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return th.glyphIn(gtx, ic, 16, 20)
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
	)
}

// status is the second line of a row: the progress of a running save, the
// destination of a finished one, or the reason of a failed one.
func (p *downloadPanel) status(gtx layout.Context, th *theme, t *downloadTask) layout.Dimensions {
	switch t.state {
	case downloadDone:
		return th.text(gtx, i18n.T("download.savedTo", map[string]string{"Path": t.savedTo}), textTiny, colMuted, font.Normal, 1)
	case downloadFailed:
		if t.partial {
			return th.text(gtx, i18n.T("download.failedPartial", map[string]string{"Error": errorText(t.err)}), textTiny, colDanger, font.Normal, 1)
		}
		return th.text(gtx, i18n.T("download.failed", map[string]string{"Error": errorText(t.err)}), textTiny, colDanger, font.Normal, 1)
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
func progressLabel(t *downloadTask) string {
	if t.total <= 0 {
		return "…"
	}
	return fmt.Sprintf("%d%%", min(t.done*100/t.total, 100))
}

// progressTrack draws the 4dp bar of a running task. A known total fills it in
// proportion; an unknown one moves a segment back and forth and asks for the
// next frame so the motion continues.
func progressTrack(gtx layout.Context, t *downloadTask) layout.Dimensions {
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
