// Terminal renderers: the per-job step waterfall, the cross-run diff, and
// the slowest-steps ranking. Output is plain UTF-8 text — no ANSI colors —
// so it pastes cleanly into PR comments and issue reports.
package render

import (
	"fmt"
	"io"
	"time"

	"github.com/JaydenCJ/ciblame/internal/diffrun"
	"github.com/JaydenCJ/ciblame/internal/run"
	"github.com/JaydenCJ/ciblame/internal/timeline"
)

// TextOptions tune the waterfall renderer.
type TextOptions struct {
	Width  int  // waterfall track width in cells
	Groups bool // show the top log folds under each step
}

// nameWidth sizes the step-name column: wide enough for the longest name,
// bounded so the waterfall still fits a normal terminal.
func nameWidth(jobs []*run.Job) int {
	w := len("step")
	for _, j := range jobs {
		for _, s := range j.Steps {
			if n := len([]rune(s.Name)); n > w {
				w = n
			}
		}
	}
	if w > 36 {
		w = 36
	}
	return w
}

// Text writes the step-waterfall report for a run.
func Text(w io.Writer, r *run.Run, opts TextOptions) {
	if opts.Width <= 0 {
		opts.Width = 28
	}
	fmt.Fprintf(w, "ciblame report — %s\n", r.Label)
	fmt.Fprintf(w, "%s · %s · wall %s · job time %s\n",
		Count(len(r.Jobs), "job"), Count(r.StepCount(), "step"), Dur(r.Wall()), Dur(r.JobTime()))
	nw := nameWidth(r.Jobs)

	for _, j := range r.Jobs {
		fmt.Fprintln(w)
		writeJobHeader(w, j)
		if j.LogOnly {
			fmt.Fprintf(w, "  (combined job log only — no per-step files in this archive)\n")
			continue
		}
		total := j.Duration()
		fmt.Fprintf(w, "   #  %s  %9s  %8s\n", Pad("step", nw), "start", "dur")
		for _, sp := range timeline.Spans(j) {
			s := sp.Step
			mark := ""
			if s.Failed {
				mark = "  ✗ failed"
			} else if !s.Timed {
				mark = "  (no timestamps)"
			}
			fmt.Fprintf(w, "  %2d  %s  %9s  %8s  %s%s\n",
				s.Number,
				Pad(Truncate(s.Name, nw), nw),
				SignedDur(sp.Offset),
				Dur(s.Duration()),
				timeline.Bar(sp.Offset, s.Duration(), total, opts.Width),
				mark)
			if opts.Groups {
				writeGroups(w, s)
			}
		}
		if oh := timeline.Overhead(j); oh > 0 && total > 0 {
			fmt.Fprintf(w, "  between-step overhead: %s (%s of the job)\n",
				Dur(oh), Pct(float64(oh)/float64(total)*100))
		}
	}
}

func writeJobHeader(w io.Writer, j *run.Job) {
	fmt.Fprintf(w, "job %s · %s · %s", j.Name, Dur(j.Duration()), Count(len(j.Steps), "step"))
	if j.Timed {
		fmt.Fprintf(w, " · started %s", Clock(j.Start))
	}
	if j.Image != "" {
		fmt.Fprintf(w, " · %s", j.Image)
	}
	if j.Failed {
		fmt.Fprintf(w, " · FAILED")
	}
	fmt.Fprintln(w)
}

// writeGroups prints a step's three longest folds, skipping sub-100ms noise.
func writeGroups(w io.Writer, s *run.Step) {
	for _, i := range timeline.TopGroups(s, 3) {
		g := s.Groups[i]
		if g.Duration() < 100*time.Millisecond {
			continue
		}
		fmt.Fprintf(w, "      └─ %8s  %s\n", Dur(g.Duration()), Truncate(g.Title, 60))
	}
}

// SlowText writes the cross-job slowest-steps ranking.
func SlowText(w io.Writer, r *run.Run, entries []timeline.Slow) {
	fmt.Fprintf(w, "ciblame slow — %s · top %d of %s · job time %s\n\n",
		r.Label, len(entries), Count(r.StepCount(), "step"), Dur(r.JobTime()))
	fmt.Fprintf(w, "  %8s  %6s  %s\n", "dur", "share", "job · step")
	for _, e := range entries {
		mark := ""
		if e.Step.Failed {
			mark = "  ✗"
		}
		fmt.Fprintf(w, "  %8s  %6s  %s · %s%s\n",
			Dur(e.Step.Duration()), Pct(e.Share), e.Job.Name, e.Step.Name, mark)
	}
}

// DiffText writes the two-run comparison. Steps whose absolute delta is
// below minDelta are folded into a single summary line per job; deltaBar
// magnitude is scaled against the largest shown step delta in the report.
func DiffText(w io.Writer, rep *diffrun.Report, minDelta time.Duration) {
	fmt.Fprintf(w, "ciblame diff — %s → %s\n", rep.BaseLabel, rep.HeadLabel)
	fmt.Fprintf(w, "job time  %7s → %-7s  %s%s\n", Dur(rep.BaseJobTime), Dur(rep.HeadJobTime),
		SignedDur(rep.JobTimeDelta()), pctSuffix(rep.JobTimeDelta(), rep.BaseJobTime))
	fmt.Fprintf(w, "wall      %7s → %-7s  %s\n", Dur(rep.BaseWall), Dur(rep.HeadWall),
		SignedDur(rep.WallDelta()))

	scale := maxShownDelta(rep, minDelta)
	for i := range rep.Jobs {
		jd := &rep.Jobs[i]
		fmt.Fprintln(w)
		switch jd.Kind {
		case diffrun.Added:
			fmt.Fprintf(w, "job %s  %s  (job added)\n", jd.Name, SignedDur(jd.Delta()))
		case diffrun.Removed:
			fmt.Fprintf(w, "job %s  %s  (job removed)\n", jd.Name, SignedDur(jd.Delta()))
		default:
			fmt.Fprintf(w, "job %s  %s  (%s → %s)\n", jd.Name, SignedDur(jd.Delta()),
				Dur(jd.Base), Dur(jd.Head))
		}
		shown, hidden, hiddenNet := diffrun.Fold(jd.Steps, minDelta)
		nw := stepDiffNameWidth(shown)
		for _, sd := range shown {
			fmt.Fprintf(w, "  %s %s  %9s → %-9s %9s  %s\n",
				kindSigil(sd.Kind),
				Pad(Truncate(sd.Name, nw), nw),
				sideDur(sd, false), sideDur(sd, true),
				SignedDur(sd.Delta()),
				deltaBar(sd.Delta(), scale))
		}
		if hidden > 0 {
			fmt.Fprintf(w, "  · %s within ±%s (net %s)\n",
				Count(hidden, "step"), Dur(minDelta), SignedDur(hiddenNet))
		}
	}
}

// pctSuffix renders a relative change like " (+72.9%)" when a baseline
// exists to compare against.
func pctSuffix(delta, base time.Duration) string {
	if base <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%+.1f%%)", float64(delta)/float64(base)*100)
}

func kindSigil(k diffrun.Kind) string {
	switch k {
	case diffrun.Added:
		return "+"
	case diffrun.Removed:
		return "-"
	default:
		return "~"
	}
}

// sideDur formats one side of a step diff, with "—" for the side an
// added/removed step does not exist on.
func sideDur(sd diffrun.StepDiff, head bool) string {
	if head && sd.Kind == diffrun.Removed {
		return "—"
	}
	if !head && sd.Kind == diffrun.Added {
		return "—"
	}
	if head {
		return Dur(sd.Head)
	}
	return Dur(sd.Base)
}

func stepDiffNameWidth(steps []diffrun.StepDiff) int {
	w := 12
	for _, s := range steps {
		if n := len([]rune(s.Name)); n > w {
			w = n
		}
	}
	if w > 36 {
		w = 36
	}
	return w
}

func maxShownDelta(rep *diffrun.Report, minDelta time.Duration) time.Duration {
	var max time.Duration
	for _, jd := range rep.Jobs {
		shown, _, _ := diffrun.Fold(jd.Steps, minDelta)
		for _, sd := range shown {
			d := sd.Delta()
			if d < 0 {
				d = -d
			}
			if d > max {
				max = d
			}
		}
	}
	return max
}

// deltaBar draws delta magnitude on a 16-cell scale shared by the whole
// diff, so bar lengths compare across jobs. Nonzero deltas always get one
// cell — invisible regressions defeat the point.
func deltaBar(delta, scale time.Duration) string {
	const width = 16
	if scale <= 0 || delta == 0 {
		return ""
	}
	if delta < 0 {
		delta = -delta
	}
	n := int(float64(delta) / float64(scale) * width)
	if n < 1 {
		n = 1
	}
	if n > width {
		n = width
	}
	out := ""
	for i := 0; i < n; i++ {
		out += "█"
	}
	return out
}
