// Tests for two-run matching and delta arithmetic: name-based pairing
// (surviving renumbering and duplicates), added/removed jobs and steps,
// impact ordering, totals, and the noise-floor fold.
package diffrun

import (
	"testing"
	"time"

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

func job(name string, steps ...*run.Step) *run.Job {
	j := &run.Job{Name: name, Steps: steps}
	for _, s := range steps {
		if !j.Timed || s.Start.Before(j.Start) {
			j.Start, j.Timed = s.Start, true
		}
		if s.End.After(j.End) {
			j.End = s.End
		}
	}
	return j
}

func mkRun(label string, jobs ...*run.Job) *run.Run {
	return &run.Run{Label: label, Jobs: jobs}
}

// findStep locates a step diff by name, failing the test if absent.
func findStep(t *testing.T, jd JobDiff, name string) StepDiff {
	t.Helper()
	for _, sd := range jd.Steps {
		if sd.Name == name {
			return sd
		}
	}
	t.Fatalf("step %q not in diff %+v", name, jd.Steps)
	return StepDiff{}
}

func TestMatchedStepDelta(t *testing.T) {
	baseRun := mkRun("base", job("build", step(1, "Run tests", 0, 60*sec)))
	headRun := mkRun("head", job("build", step(1, "Run tests", 0, 90*sec)))
	rep := Diff(baseRun, headRun)
	sd := findStep(t, rep.Jobs[0], "Run tests")
	if sd.Kind != Matched || sd.Delta() != 30*sec {
		t.Fatalf("diff = %+v", sd)
	}
}

func TestRenumberedStepStillMatches(t *testing.T) {
	// Inserting a step shifts every later step number; matching is by name
	// so the pairing must survive.
	baseRun := mkRun("base", job("build",
		step(1, "Checkout", 0, 2*sec),
		step(2, "Run tests", 2*sec, 62*sec)))
	headRun := mkRun("head", job("build",
		step(1, "Checkout", 0, 2*sec),
		step(2, "Restore cache", 2*sec, 10*sec),
		step(3, "Run tests", 10*sec, 70*sec)))
	rep := Diff(baseRun, headRun)
	sd := findStep(t, rep.Jobs[0], "Run tests")
	if sd.Kind != Matched || sd.Delta() != 0 {
		t.Fatalf("renumbered step diff = %+v", sd)
	}
	if added := findStep(t, rep.Jobs[0], "Restore cache"); added.Kind != Added {
		t.Fatalf("inserted step = %+v", added)
	}
}

func TestAddedStepCostsItsFullDuration(t *testing.T) {
	baseRun := mkRun("base", job("build", step(1, "Build", 0, 10*sec)))
	headRun := mkRun("head", job("build",
		step(1, "Build", 0, 10*sec),
		step(2, "Coverage", 10*sec, 22*sec)))
	rep := Diff(baseRun, headRun)
	sd := findStep(t, rep.Jobs[0], "Coverage")
	if sd.Kind != Added || sd.Delta() != 12*sec || sd.Base != 0 {
		t.Fatalf("added step = %+v", sd)
	}
}

func TestRemovedStepCreditsItsFullDuration(t *testing.T) {
	baseRun := mkRun("base", job("build",
		step(1, "Build", 0, 10*sec),
		step(2, "Lint", 10*sec, 40*sec)))
	headRun := mkRun("head", job("build", step(1, "Build", 0, 10*sec)))
	rep := Diff(baseRun, headRun)
	sd := findStep(t, rep.Jobs[0], "Lint")
	if sd.Kind != Removed || sd.Delta() != -30*sec || sd.Head != 0 {
		t.Fatalf("removed step = %+v", sd)
	}
}

func TestDuplicateStepNamesPairByOccurrence(t *testing.T) {
	// Two steps both named "Run make": first pairs with first, second with
	// second, so the 5s→50s regression lands on the right row.
	baseRun := mkRun("base", job("build",
		step(1, "Run make", 0, 10*sec),
		step(2, "Run make", 10*sec, 15*sec)))
	headRun := mkRun("head", job("build",
		step(1, "Run make", 0, 10*sec),
		step(2, "Run make", 10*sec, 60*sec)))
	rep := Diff(baseRun, headRun)
	if len(rep.Jobs[0].Steps) != 2 {
		t.Fatalf("steps = %+v", rep.Jobs[0].Steps)
	}
	// Sorted by |delta|: the regressed occurrence first.
	if rep.Jobs[0].Steps[0].Delta() != 45*sec || rep.Jobs[0].Steps[1].Delta() != 0 {
		t.Fatalf("deltas = %v, %v", rep.Jobs[0].Steps[0].Delta(), rep.Jobs[0].Steps[1].Delta())
	}
}

func TestAddedJob(t *testing.T) {
	baseRun := mkRun("base", job("build", step(1, "Build", 0, 10*sec)))
	headRun := mkRun("head",
		job("build", step(1, "Build", 0, 10*sec)),
		job("e2e", step(1, "Run e2e", 0, 120*sec)))
	rep := Diff(baseRun, headRun)
	if rep.Jobs[0].Name != "e2e" || rep.Jobs[0].Kind != Added || rep.Jobs[0].Delta() != 120*sec {
		t.Fatalf("added job = %+v", rep.Jobs[0])
	}
	if sd := findStep(t, rep.Jobs[0], "Run e2e"); sd.Kind != Added {
		t.Fatalf("added job's step = %+v", sd)
	}
	if Matched.String() != "matched" || Added.String() != "added" || Removed.String() != "removed" {
		t.Fatalf("kind labels = %s/%s/%s", Matched, Added, Removed)
	}
}

func TestRemovedJob(t *testing.T) {
	baseRun := mkRun("base",
		job("build", step(1, "Build", 0, 10*sec)),
		job("docs", step(1, "Build docs", 0, 45*sec)))
	headRun := mkRun("head", job("build", step(1, "Build", 0, 10*sec)))
	rep := Diff(baseRun, headRun)
	if rep.Jobs[0].Name != "docs" || rep.Jobs[0].Kind != Removed || rep.Jobs[0].Delta() != -45*sec {
		t.Fatalf("removed job = %+v", rep.Jobs[0])
	}
}

func TestJobsSortedByImpact(t *testing.T) {
	baseRun := mkRun("base",
		job("small", step(1, "s", 0, 10*sec)),
		job("big", step(1, "b", 0, 10*sec)))
	headRun := mkRun("head",
		job("small", step(1, "s", 0, 12*sec)),
		job("big", step(1, "b", 0, 70*sec)))
	rep := Diff(baseRun, headRun)
	if rep.Jobs[0].Name != "big" || rep.Jobs[1].Name != "small" {
		t.Fatalf("order = %s, %s", rep.Jobs[0].Name, rep.Jobs[1].Name)
	}
}

func TestStepsSortedByAbsoluteImpact(t *testing.T) {
	// A -20s improvement outranks a +5s regression: sorting is by |delta|.
	baseRun := mkRun("base", job("build",
		step(1, "faster now", 0, 30*sec),
		step(2, "bit slower", 30*sec, 35*sec)))
	headRun := mkRun("head", job("build",
		step(1, "faster now", 0, 10*sec),
		step(2, "bit slower", 10*sec, 20*sec)))
	rep := Diff(baseRun, headRun)
	if rep.Jobs[0].Steps[0].Name != "faster now" {
		t.Fatalf("first step = %+v", rep.Jobs[0].Steps[0])
	}
}

func TestTotals(t *testing.T) {
	baseRun := mkRun("base",
		job("build", step(1, "b", 0, 60*sec)),
		job("lint", step(1, "l", 0, 30*sec)))
	headRun := mkRun("head",
		job("build", step(1, "b", 0, 90*sec)),
		job("lint", step(1, "l", 0, 30*sec)))
	rep := Diff(baseRun, headRun)
	if rep.BaseJobTime != 90*sec || rep.HeadJobTime != 120*sec || rep.JobTimeDelta() != 30*sec {
		t.Fatalf("job time = %v→%v (%v)", rep.BaseJobTime, rep.HeadJobTime, rep.JobTimeDelta())
	}
	if rep.WallDelta() != 30*sec {
		t.Fatalf("wall delta = %v", rep.WallDelta())
	}
}

func TestFoldSplitsAtThreshold(t *testing.T) {
	steps := []StepDiff{
		{Name: "big", Kind: Matched, Base: 10 * sec, Head: 40 * sec},
		{Name: "tiny up", Kind: Matched, Base: 10 * sec, Head: 10*sec + 500*time.Millisecond},
		{Name: "tiny down", Kind: Matched, Base: 10 * sec, Head: 10*sec - 200*time.Millisecond},
	}
	shown, hidden, net := Fold(steps, 2*sec)
	if len(shown) != 1 || shown[0].Name != "big" {
		t.Fatalf("shown = %+v", shown)
	}
	if hidden != 2 || net != 300*time.Millisecond {
		t.Fatalf("hidden = %d net = %v", hidden, net)
	}
	// A zero threshold hides nothing.
	shown, hidden, _ = Fold(steps, 0)
	if len(shown) != 3 || hidden != 0 {
		t.Fatalf("zero threshold: shown = %d hidden = %d", len(shown), hidden)
	}
}
