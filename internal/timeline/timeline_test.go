// Tests for waterfall geometry: step offsets and gaps, overhead totals,
// bar rendering edge cases, and the cross-job slowest-steps ranking. Model
// values are constructed directly — no archives needed at this layer.
package timeline

import (
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/ciblame/internal/logparse"
	"github.com/JaydenCJ/ciblame/internal/run"
)

const sec = time.Second

var base = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

// step builds a timed step spanning [from, to] relative to base.
func step(num int, name string, from, to time.Duration) *run.Step {
	return &run.Step{
		Number: num, Name: name,
		Start: base.Add(from), End: base.Add(to), Timed: true,
	}
}

// job wraps steps into a job with a clock derived the same way assembly
// does (min start, max end).
func job(name string, steps ...*run.Step) *run.Job {
	j := &run.Job{Name: name, Steps: steps}
	for _, s := range steps {
		if !s.Timed {
			continue
		}
		if !j.Timed || s.Start.Before(j.Start) {
			j.Start, j.Timed = s.Start, true
		}
		if s.End.After(j.End) {
			j.End = s.End
		}
	}
	return j
}

func TestSpanOffsetsRelativeToJobStart(t *testing.T) {
	j := job("build",
		step(1, "a", 0, 2*sec),
		step(2, "b", 5*sec, 9*sec),
	)
	spans := Spans(j)
	if spans[0].Offset != 0 || spans[1].Offset != 5*sec {
		t.Fatalf("offsets = %v, %v", spans[0].Offset, spans[1].Offset)
	}
}

func TestSpanGapsBetweenSteps(t *testing.T) {
	j := job("build",
		step(1, "a", 0, 2*sec),
		step(2, "b", 5*sec, 9*sec), // 3s idle before this step
		step(3, "c", 9*sec, 10*sec),
	)
	spans := Spans(j)
	if spans[0].Gap != 0 || spans[1].Gap != 3*sec || spans[2].Gap != 0 {
		t.Fatalf("gaps = %v, %v, %v", spans[0].Gap, spans[1].Gap, spans[2].Gap)
	}
}

func TestOverlappingStepsProduceNoNegativeGap(t *testing.T) {
	// Post-steps can interleave oddly; a step starting before the previous
	// one ended must yield gap 0, never negative time.
	j := job("build",
		step(1, "a", 0, 6*sec),
		step(2, "b", 4*sec, 8*sec),
	)
	if g := Spans(j)[1].Gap; g != 0 {
		t.Fatalf("gap = %v, want 0", g)
	}
}

func TestUntimedStepGetsPlaceholderSpan(t *testing.T) {
	j := job("build",
		step(1, "a", 0, 2*sec),
		&run.Step{Number: 2, Name: "untimed"},
		step(3, "c", 2*sec, 3*sec),
	)
	spans := Spans(j)
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	if spans[1].Gap != 0 || spans[1].Offset != 0 {
		t.Fatalf("untimed span = %+v", spans[1])
	}
}

func TestOverheadSumsAllGaps(t *testing.T) {
	j := job("build",
		step(1, "a", 0, 2*sec),
		step(2, "b", 3*sec, 5*sec), // +1s
		step(3, "c", 7*sec, 9*sec), // +2s
	)
	if oh := Overhead(j); oh != 3*sec {
		t.Fatalf("overhead = %v, want 3s", oh)
	}
}

func TestBarGeometry(t *testing.T) {
	// A full-span step fills the whole track.
	if got := Bar(0, 10*sec, 10*sec, 10); got != strings.Repeat("█", 10) {
		t.Fatalf("full bar = %q", got)
	}
	// A sub-cell step still gets one visible cell.
	if got := Bar(0, time.Millisecond, time.Hour, 10); !strings.Contains(got, "█") {
		t.Fatalf("tiny bar = %q, want at least one filled cell", got)
	}
	// A zero-duration job renders an empty track instead of dividing by zero.
	if got := Bar(0, 0, 0, 8); got != strings.Repeat("░", 8) {
		t.Fatalf("zero-total bar = %q", got)
	}
	// Clock skew can put a step "past" the job end; the mark is clamped
	// inside the track.
	if got := Bar(15*sec, 10*sec, 10*sec, 10); !strings.HasSuffix(got, "█") {
		t.Fatalf("clamped bar = %q", got)
	}
	// Whatever the inputs, the track is exactly width cells.
	for _, w := range []int{1, 8, 28, 120} {
		if got := Bar(2*sec, 3*sec, 10*sec, w); len([]rune(got)) != w {
			t.Fatalf("width %d rendered %q", w, got)
		}
	}
}

func TestSlowestRanksAcrossJobs(t *testing.T) {
	r := &run.Run{Jobs: []*run.Job{
		job("build", step(1, "fast", 0, sec), step(2, "slowest", sec, 61*sec)),
		job("lint", step(1, "medium", 0, 30*sec)),
	}}
	got := Slowest(r, 0)
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	if got[0].Step.Name != "slowest" || got[1].Step.Name != "medium" || got[2].Step.Name != "fast" {
		t.Fatalf("order = %s, %s, %s", got[0].Step.Name, got[1].Step.Name, got[2].Step.Name)
	}
}

func TestSlowestTruncatesToN(t *testing.T) {
	r := &run.Run{Jobs: []*run.Job{
		job("build", step(1, "a", 0, sec), step(2, "b", sec, 3*sec), step(3, "c", 3*sec, 6*sec)),
	}}
	if got := Slowest(r, 2); len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
}

func TestSlowestSharesOfJobTime(t *testing.T) {
	// One 60s step in a 90s run (60s + 30s jobs) is 66.7% of job time.
	r := &run.Run{Jobs: []*run.Job{
		job("build", step(1, "big", 0, 60*sec)),
		job("lint", step(1, "small", 0, 30*sec)),
	}}
	got := Slowest(r, 1)
	if got[0].Share < 66.6 || got[0].Share > 66.8 {
		t.Fatalf("share = %.2f, want ~66.7", got[0].Share)
	}
}

func TestTopGroupsOrdersByDuration(t *testing.T) {
	g := func(title string, d time.Duration) logparse.Group {
		return logparse.Group{Title: title, Start: base, End: base.Add(d)}
	}
	s := &run.Step{Groups: []logparse.Group{
		g("setup", 2*sec), g("main", 50*sec), g("teardown", 5*sec),
	}}
	idx := TopGroups(s, 2)
	if len(idx) != 2 || s.Groups[idx[0]].Title != "main" || s.Groups[idx[1]].Title != "teardown" {
		t.Fatalf("top groups = %v", idx)
	}
}
