// Package diffrun answers "our CI got slower — which step?": it matches two
// parsed runs job-by-job and step-by-step by *name* (numbers shift whenever
// a step is inserted), computes per-step deltas, and ranks everything by
// impact so the culprit is the first line of output.
package diffrun

import (
	"sort"
	"time"

	"github.com/JaydenCJ/ciblame/internal/run"
)

// Kind classifies how a job or step relates across the two runs.
type Kind int

const (
	Matched Kind = iota // present in both runs
	Added               // only in the head run
	Removed             // only in the base run
)

// String returns the JSON/text label for the kind.
func (k Kind) String() string {
	switch k {
	case Added:
		return "added"
	case Removed:
		return "removed"
	default:
		return "matched"
	}
}

// StepDiff compares one step across runs. For Added/Removed steps the
// missing side's duration is zero and the delta is the surviving side's
// full duration (an added step costs its whole runtime).
type StepDiff struct {
	Name string
	Kind Kind
	Base time.Duration
	Head time.Duration
}

// Delta is head minus base: positive means slower.
func (d StepDiff) Delta() time.Duration { return d.Head - d.Base }

// JobDiff compares one job across runs. Steps are sorted by absolute delta,
// descending, so the biggest mover is always first.
type JobDiff struct {
	Name  string
	Kind  Kind
	Base  time.Duration
	Head  time.Duration
	Steps []StepDiff
}

// Delta is head minus base for the whole job.
func (d JobDiff) Delta() time.Duration { return d.Head - d.Base }

// Report is the full two-run comparison.
type Report struct {
	BaseLabel string
	HeadLabel string
	Jobs      []JobDiff
	// JobTime totals are the billable sums of job durations; Wall totals
	// are elapsed time (jobs overlap when parallel). Both matter: wall is
	// what developers wait, job time is what the invoice counts.
	BaseJobTime time.Duration
	HeadJobTime time.Duration
	BaseWall    time.Duration
	HeadWall    time.Duration
}

// JobTimeDelta is the change in billable job time, head minus base.
func (r *Report) JobTimeDelta() time.Duration { return r.HeadJobTime - r.BaseJobTime }

// WallDelta is the change in elapsed wall-clock time, head minus base.
func (r *Report) WallDelta() time.Duration { return r.HeadWall - r.BaseWall }

// Diff compares two runs. Jobs pair by exact name; steps pair by name with
// duplicates matched in occurrence order (the k-th "Run tests" in base
// pairs with the k-th in head), which survives step renumbering.
func Diff(base, head *run.Run) *Report {
	rep := &Report{
		BaseLabel:   base.Label,
		HeadLabel:   head.Label,
		BaseJobTime: base.JobTime(),
		HeadJobTime: head.JobTime(),
		BaseWall:    base.Wall(),
		HeadWall:    head.Wall(),
	}

	baseJobs := byName(base.Jobs)
	seen := map[string]bool{}
	for _, hj := range head.Jobs {
		if bj, ok := baseJobs[hj.Name]; ok {
			seen[hj.Name] = true
			rep.Jobs = append(rep.Jobs, diffJob(bj, hj))
		} else {
			rep.Jobs = append(rep.Jobs, JobDiff{
				Name: hj.Name, Kind: Added, Head: hj.Duration(),
				Steps: soloSteps(hj, Added),
			})
		}
	}
	for _, bj := range base.Jobs {
		if !seen[bj.Name] {
			rep.Jobs = append(rep.Jobs, JobDiff{
				Name: bj.Name, Kind: Removed, Base: bj.Duration(),
				Steps: soloSteps(bj, Removed),
			})
		}
	}
	sortByImpact(rep.Jobs)
	return rep
}

func byName(jobs []*run.Job) map[string]*run.Job {
	m := make(map[string]*run.Job, len(jobs))
	for _, j := range jobs {
		if _, dup := m[j.Name]; !dup { // first occurrence wins on freak duplicates
			m[j.Name] = j
		}
	}
	return m
}

func diffJob(base, head *run.Job) JobDiff {
	jd := JobDiff{
		Name: head.Name,
		Kind: Matched,
		Base: base.Duration(),
		Head: head.Duration(),
	}
	// Pair duplicate step names by occurrence: consume base steps of the
	// same name in order as head steps of that name appear.
	baseByName := map[string][]*run.Step{}
	for _, s := range base.Steps {
		baseByName[s.Name] = append(baseByName[s.Name], s)
	}
	for _, hs := range head.Steps {
		if q := baseByName[hs.Name]; len(q) > 0 {
			bs := q[0]
			baseByName[hs.Name] = q[1:]
			jd.Steps = append(jd.Steps, StepDiff{
				Name: hs.Name, Kind: Matched,
				Base: bs.Duration(), Head: hs.Duration(),
			})
		} else {
			jd.Steps = append(jd.Steps, StepDiff{Name: hs.Name, Kind: Added, Head: hs.Duration()})
		}
	}
	// Whatever base steps were never consumed are gone in head.
	for _, s := range base.Steps {
		if q := baseByName[s.Name]; len(q) > 0 && q[0] == s {
			baseByName[s.Name] = q[1:]
			jd.Steps = append(jd.Steps, StepDiff{Name: s.Name, Kind: Removed, Base: s.Duration()})
		}
	}
	sortStepsByImpact(jd.Steps)
	return jd
}

// soloSteps renders an added or removed job's steps as one-sided diffs.
func soloSteps(j *run.Job, kind Kind) []StepDiff {
	steps := make([]StepDiff, 0, len(j.Steps))
	for _, s := range j.Steps {
		sd := StepDiff{Name: s.Name, Kind: kind}
		if kind == Added {
			sd.Head = s.Duration()
		} else {
			sd.Base = s.Duration()
		}
		steps = append(steps, sd)
	}
	sortStepsByImpact(steps)
	return steps
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func sortByImpact(jobs []JobDiff) {
	sort.SliceStable(jobs, func(a, b int) bool {
		da, db := abs(jobs[a].Delta()), abs(jobs[b].Delta())
		if da != db {
			return da > db
		}
		return jobs[a].Name < jobs[b].Name
	})
}

func sortStepsByImpact(steps []StepDiff) {
	sort.SliceStable(steps, func(a, b int) bool {
		da, db := abs(steps[a].Delta()), abs(steps[b].Delta())
		if da != db {
			return da > db
		}
		return steps[a].Name < steps[b].Name
	})
}

// Fold splits a job's steps into the ones at or above the minDelta
// threshold and a summary of the ones below it, so renderers can hide the
// noise floor without losing its net effect.
func Fold(steps []StepDiff, minDelta time.Duration) (shown []StepDiff, hidden int, hiddenNet time.Duration) {
	for _, s := range steps {
		if abs(s.Delta()) >= minDelta {
			shown = append(shown, s)
		} else {
			hidden++
			hiddenNet += s.Delta()
		}
	}
	return shown, hidden, hiddenNet
}
