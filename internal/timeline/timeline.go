// Package timeline computes waterfall geometry and rankings from a parsed
// run: where each step sits relative to its job's start, how much time
// disappears *between* steps (runner overhead), and which steps are the
// slowest across the whole run. Everything here is pure arithmetic on the
// run model, kept out of the renderers so it can be unit-tested exactly.
package timeline

import (
	"sort"
	"strings"
	"time"

	"github.com/JaydenCJ/ciblame/internal/run"
)

// Span places one step on its job's waterfall.
type Span struct {
	Step   *run.Step
	Offset time.Duration // step start minus job start
	Gap    time.Duration // idle time since the previous timed step ended
}

// Spans lays out a job's timed steps in order. Untimed steps get a Span
// with the previous offset and zero gap so table rows still line up.
func Spans(j *run.Job) []Span {
	spans := make([]Span, 0, len(j.Steps))
	var cursor time.Time // end of the previous timed step
	var lastOffset time.Duration
	for _, s := range j.Steps {
		sp := Span{Step: s, Offset: lastOffset}
		if s.Timed && j.Timed {
			sp.Offset = s.Start.Sub(j.Start)
			lastOffset = sp.Offset
			if !cursor.IsZero() && s.Start.After(cursor) {
				sp.Gap = s.Start.Sub(cursor)
			}
			if s.End.After(cursor) {
				cursor = s.End
			}
		}
		spans = append(spans, sp)
	}
	return spans
}

// Overhead sums the positive gaps between consecutive steps of a job — the
// seconds the runner spent between steps (container setup, artifact
// bookkeeping) that no step gets blamed for, yet still bill.
func Overhead(j *run.Job) time.Duration {
	var total time.Duration
	for _, sp := range Spans(j) {
		total += sp.Gap
	}
	return total
}

// Bar renders a step's position as a fixed-width track: '█' cells where the
// step runs, '░' elsewhere. A running step always occupies at least one
// cell so sub-cell steps stay visible; offsets and durations are clamped so
// clock skew can never push the bar out of the track.
func Bar(offset, dur, total time.Duration, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if dur < 0 {
		dur = 0
	}
	if offset+dur > total {
		dur = total - offset
	}
	start := int(float64(offset) / float64(total) * float64(width))
	end := int(float64(offset+dur)/float64(total)*float64(width) + 0.5)
	if start >= width {
		start = width - 1
	}
	if end <= start {
		end = start + 1
	}
	if end > width {
		end = width
	}
	var b strings.Builder
	b.Grow(width * 3)
	for i := 0; i < width; i++ {
		if i >= start && i < end {
			b.WriteString("█")
		} else {
			b.WriteString("░")
		}
	}
	return b.String()
}

// Slow is one entry in the cross-job slowest-steps ranking.
type Slow struct {
	Job   *run.Job
	Step  *run.Step
	Share float64 // percentage of the run's total job time
}

// Slowest ranks every timed step in the run by duration, descending, and
// returns the top n (all of them when n <= 0). Ties break by job then step
// order so the ranking is deterministic.
func Slowest(r *run.Run, n int) []Slow {
	total := r.JobTime()
	var all []Slow
	for _, j := range r.Jobs {
		for _, s := range j.Steps {
			if !s.Timed {
				continue
			}
			e := Slow{Job: j, Step: s}
			if total > 0 {
				e.Share = float64(s.Duration()) / float64(total) * 100
			}
			all = append(all, e)
		}
	}
	sort.SliceStable(all, func(a, b int) bool {
		return all[a].Step.Duration() > all[b].Step.Duration()
	})
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	return all
}

// TopGroups returns a step's k longest folds, ties broken by log order.
// Renderers use it for the `--groups` drill-down.
func TopGroups(s *run.Step, k int) []int {
	idx := make([]int, len(s.Groups))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return s.Groups[idx[a]].Duration() > s.Groups[idx[b]].Duration()
	})
	if k > 0 && len(idx) > k {
		idx = idx[:k]
	}
	return idx
}
