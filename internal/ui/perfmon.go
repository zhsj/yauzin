package ui

import (
	"fmt"
	"image"
	"runtime"
	"runtime/metrics"
	"sync"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// The performance readout is a compact two-line block in the top bar:
//
//	12% · 128M
//	7gr · 60fps
//
// cpu/memory on the first row, goroutines/fps on the second. All numbers come
// from the Go runtime, so the readout is identical on every platform. The
// figures cover the Go side of the process only: cgo threads and allocations
// (part of Gio's rendering path) are not included.
const (
	// perfSampleInterval is how often the sampler goroutine wakes up. One
	// second keeps the CPU cost of ReadMemStats (a stop-the-world pause) and
	// the redraw cadence negligible.
	perfSampleInterval = time.Second
	// perfTextSize is the font size of both rows; they deliberately share one
	// size and colour so neither row reads as more important.
	perfTextSize = unit.Sp(11)
	// perfIdleAfter is the frame silence after which fps reads as 0: Gio stops
	// redrawing when nothing happens, and a frozen last value would lie.
	perfIdleAfter = 2 * time.Second
	// perfFPSAlpha is the exponential smoothing factor of the fps estimate.
	perfFPSAlpha = 0.3
	// perfCPUAlpha is the exponential smoothing factor of the cpu estimate.
	perfCPUAlpha = 0.5
	// perfSettingsGap separates the readout from the settings button, so the
	// readout does not look glued to it.
	perfSettingsGap = unit.Dp(12)
	// perfColGap separates the columns inside the readout.
	perfColGap = unit.Dp(3)
)

type perfMonitor struct {
	app *App

	mu      sync.Mutex
	visible bool
	// stop is closed to end the sampler goroutine; nil while not running.
	stop chan struct{}

	cpu    float64   // percent of one core, Go runtime threads
	heapMB float64   // runtime.MemStats.HeapAlloc
	numG   int       // runtime.NumGoroutine
	fps    float64   // smoothed frame rate
	last   time.Time // time of the previous frame, for the fps delta
}

func newPerfMonitor(a *App) *perfMonitor {
	return &perfMonitor{app: a}
}

// isVisible reports whether the readout is currently shown (and sampling).
func (p *perfMonitor) isVisible() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.visible
}

// setVisible turns the readout on or off. The sampler goroutine only lives
// while the readout is visible, so a hidden monitor costs nothing.
func (p *perfMonitor) setVisible(on bool) {
	p.mu.Lock()
	if p.visible == on {
		p.mu.Unlock()
		return
	}
	p.visible = on
	p.last = time.Time{}
	p.fps = 0
	if on {
		stop := make(chan struct{})
		p.stop = stop
		p.mu.Unlock()
		go p.sampleLoop(stop)
		return
	}
	stop := p.stop
	p.stop = nil
	p.cpu, p.heapMB, p.numG = 0, 0, 0
	p.mu.Unlock()
	close(stop)
}

// onFrame feeds the frame timestamp from the event loop into the fps
// estimate. It is a no-op while the readout is hidden.
func (p *perfMonitor) onFrame(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.visible {
		return
	}
	if p.last.IsZero() {
		p.last = now
		return
	}
	dt := now.Sub(p.last).Seconds()
	p.last = now
	if dt <= 0 {
		return
	}
	f := 1 / dt
	if p.fps == 0 {
		p.fps = f
		return
	}
	p.fps += perfFPSAlpha * (f - p.fps)
}

// sampleLoop reads the runtime counters once per second and pushes the new
// snapshot to the UI goroutine via post, whose Invalidate also keeps the 1Hz
// redraw going. It returns when stop is closed.
func (p *perfMonitor) sampleLoop(stop chan struct{}) {
	// The sample slice must not be shared between concurrent readers, so it
	// lives inside the loop: a quickly toggled monitor may briefly run two
	// samplers, the old one draining its last tick.
	samples := []metrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}}
	ticker := time.NewTicker(perfSampleInterval)
	defer ticker.Stop()

	var lastCPU float64
	var last time.Time
	var smoothPct float64
	var haveCPU bool
	// maxPct is the physical ceiling of the utilisation: all GOMAXPROCS
	// threads busy at once. The runtime's per-class accounting can double
	// count during GC bursts, so anything above this is a corrupt sample
	// (spikes of 190000% have been observed) and is dropped.
	maxPct := 100 * float64(runtime.GOMAXPROCS(0))
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		now := time.Now()

		// CPU: the metric is cumulative CPU seconds across all runtime
		// threads; the difference over the elapsed time is the utilisation.
		if cpu, ok := readCPUSeconds(samples); ok {
			if !last.IsZero() {
				if d := now.Sub(last).Seconds(); d > 0 && cpu >= lastCPU {
					if raw := (cpu - lastCPU) / d * 100; raw >= 0 && raw <= maxPct {
						if !haveCPU {
							smoothPct = raw
							haveCPU = true
						} else {
							smoothPct += perfCPUAlpha * (raw - smoothPct)
						}
					}
					// An out-of-range sample keeps the previous smooth
					// value; the baseline below is refreshed either way,
					// so a corrupt counter cannot poison later deltas.
				}
			}
			last, lastCPU = now, cpu
		}

		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		heapMB := float64(ms.HeapAlloc) / (1 << 20)
		numG := runtime.NumGoroutine()

		p.app.post(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.cpu, p.heapMB, p.numG = smoothPct, heapMB, numG
			// A stalled frame loop means nothing is being drawn: decay the
			// frozen fps estimate to zero instead of showing a stale value.
			if p.last.Before(now.Add(-perfIdleAfter)) {
				p.fps = 0
			}
		})
	}
}

// readCPUSeconds reads the cumulative runtime CPU seconds. The second return
// is false when the metric is unavailable (unknown name or unexpected kind),
// in which case the CPU figure stays at zero rather than going negative.
func readCPUSeconds(samples []metrics.Sample) (float64, bool) {
	metrics.Read(samples)
	v := samples[0].Value
	if v.Kind() != metrics.KindFloat64 {
		return 0, false
	}
	return v.Float64(), true
}

// perfRowTexts holds the four formatted readout strings of one frame.
type perfRowTexts struct{ cpu, goroutines, mem, fps string }

// rowTexts formats the current snapshot. Fixed-width fields keep the block
// from breathing every second.
func (p *perfMonitor) rowTexts() perfRowTexts {
	p.mu.Lock()
	cpu, heapMB, numG, fps := p.cpu, p.heapMB, p.numG, p.fps
	p.mu.Unlock()
	return perfRowTexts{
		cpu:        fmt.Sprintf("%3.0f%%", cpu),
		goroutines: fmt.Sprintf("%3dgr", numG),
		mem:        formatHeapMB(heapMB),
		fps:        fmt.Sprintf("%.0ffps", fps),
	}
}

// widths returns the pixel widths of the three columns at the readout font.
func (t perfRowTexts) widths(gtx layout.Context, th *theme) (col1, sep, col3 int) {
	return maxTextWidth(th, gtx, t.cpu, t.goroutines),
		measureTextWidth(th, gtx, "·"),
		maxTextWidth(th, gtx, t.mem, t.fps)
}

// phantomWidth is the width the readout will occupy in the current frame.
// The left control group reserves the same amount as an invisible spacer, so
// toggling the readout shifts neither the search pill nor anything else.
func (p *perfMonitor) phantomWidth(gtx layout.Context, th *theme) int {
	w1, sepW, w3 := p.rowTexts().widths(gtx, th)
	return w1 + 2*gtx.Dp(perfColGap) + sepW + w3
}

// layout draws the two-row readout. The rows are built as three vertical
// columns (values, separator, values) so the two dots sit at exactly the same
// x position regardless of the proportional width of each digit.
func (p *perfMonitor) layout(gtx layout.Context, th *theme) layout.Dimensions {
	txts := p.rowTexts()
	w1, sepW, w3 := txts.widths(gtx, th)

	cell := func(txt string, w int) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = w
			return th.text(gtx, txt, perfTextSize, colMuted, font.Normal, 1)
		}
	}
	sep := func() layout.Widget { return cell("·", sepW) }
	hgapW := func(gtx layout.Context) layout.Dimensions { return hgap(gtx, perfColGap) }
	vgapW := func(gtx layout.Context) layout.Dimensions { return vgap(gtx, 1) }
	col := func(top, bottom layout.Widget) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return column(gtx,
				layout.Rigid(top),
				layout.Rigid(vgapW),
				layout.Rigid(bottom),
			)
		}
	}

	// The line boxes of the digits leave descent space at the bottom, which
	// makes the block ride high under the bar's middle alignment; shift it
	// down slightly so it looks centred.
	return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(col(cell(txts.cpu, w1), cell(txts.goroutines, w1))),
			layout.Rigid(hgapW),
			layout.Rigid(col(sep(), sep())),
			layout.Rigid(hgapW),
			layout.Rigid(col(cell(txts.mem, w3), cell(txts.fps, w3))),
		)
	})
}

// formatHeapMB renders the heap size compactly; the value is Go-side only.
func formatHeapMB(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", mb/1024)
	}
	return fmt.Sprintf("%.0fM", mb)
}

// measureTextWidth returns the natural width of txt at the readout's font.
func measureTextWidth(th *theme, gtx layout.Context, txt string) int {
	m := gtx
	m.Constraints = layout.Constraints{Max: image.Pt(1<<16, 1<<16)}
	macro := op.Record(m.Ops)
	dims := th.text(m, txt, perfTextSize, colMuted, font.Normal, 1)
	macro.Stop()
	return dims.Size.X
}

func maxTextWidth(th *theme, gtx layout.Context, texts ...string) int {
	w := 0
	for _, txt := range texts {
		if x := measureTextWidth(th, gtx, txt); x > w {
			w = x
		}
	}
	return w
}
