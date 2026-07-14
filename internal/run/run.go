// Package run assembles a whole workflow run from an Actions log archive:
// it enumerates the archive, parses every job and step log, and produces
// the Run/Job/Step model the rest of ciblame reports on.
package run

import (
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/JaydenCJ/ciblame/internal/archive"
	"github.com/JaydenCJ/ciblame/internal/logparse"
)

// Step is one executed workflow step, timed from the first and last
// timestamps of its log file.
type Step struct {
	Number   int
	Name     string
	Start    time.Time
	End      time.Time
	Timed    bool // false when the log carried no parseable timestamps
	Lines    int
	Errors   int
	Warnings int
	Commands int
	Failed   bool
	FailLine string
	Groups   []logparse.Group
}

// Duration is the step's wall-clock time (zero when untimed).
func (s *Step) Duration() time.Duration {
	if !s.Timed {
		return 0
	}
	return s.End.Sub(s.Start)
}

// Job is one job of the run. When the archive contains per-step logs the
// job is timed from its steps; when only the combined `N_job.txt` log is
// present, LogOnly is true and timing comes from that file instead (no step
// breakdown is possible).
type Job struct {
	Index     int // order from the top-level job log; -1 when absent
	Name      string
	Steps     []*Step
	LogOnly   bool
	Start     time.Time
	End       time.Time
	Timed     bool
	Failed    bool
	RunnerVer string // e.g. "2.325.0", from the Set up job step
	Image     string // e.g. "ubuntu-24.04"
	ImageVer  string // e.g. "20260630.1.0"
}

// Duration is the job's wall-clock time from first step start to last step
// end (or across the combined log for LogOnly jobs).
func (j *Job) Duration() time.Duration {
	if !j.Timed {
		return 0
	}
	return j.End.Sub(j.Start)
}

// Run is a fully parsed log archive.
type Run struct {
	Label   string
	Jobs    []*Job
	Skipped int // archive entries that were not recognizable logs
}

// Wall is the run's wall-clock span: earliest job start to latest job end.
// Jobs overlap when they run in parallel, so Wall ≤ JobTime.
func (r *Run) Wall() time.Duration {
	var start, end time.Time
	for _, j := range r.Jobs {
		if !j.Timed {
			continue
		}
		if start.IsZero() || j.Start.Before(start) {
			start = j.Start
		}
		if j.End.After(end) {
			end = j.End
		}
	}
	if start.IsZero() {
		return 0
	}
	return end.Sub(start)
}

// JobTime is the sum of all job durations — the quantity GitHub bills
// runner minutes against, and the number that matters for CI cost.
func (r *Run) JobTime() time.Duration {
	var total time.Duration
	for _, j := range r.Jobs {
		total += j.Duration()
	}
	return total
}

// StepCount is the total number of steps across all jobs.
func (r *Run) StepCount() int {
	n := 0
	for _, j := range r.Jobs {
		n += len(j.Steps)
	}
	return n
}

// Load opens the archive at p (zip or extracted directory) and assembles
// the run. It fails only when the archive is unreadable or contains no
// recognizable job logs at all; individual malformed entries are skipped
// and counted in Run.Skipped.
func Load(p string) (*Run, error) {
	src, err := archive.Open(p)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return assemble(src)
}

func assemble(src *archive.Source) (*Run, error) {
	r := &Run{Label: path.Base(src.Label)}
	jobs := map[string]*Job{}
	jobFor := func(name string) *Job {
		if j, ok := jobs[name]; ok {
			return j
		}
		j := &Job{Index: -1, Name: name}
		jobs[name] = j
		return j
	}

	type jobLog struct {
		index int
		entry archive.Entry
	}
	var jobLogs []jobLog

	for _, e := range src.Entries {
		kind, jobName, num, name := classify(e.Path)
		switch kind {
		case kindStepLog:
			res, err := parseEntry(e)
			if err != nil {
				return nil, err
			}
			j := jobFor(jobName)
			j.Steps = append(j.Steps, newStep(num, name, res))
			absorbMeta(j, res)
		case kindJobLog:
			jobLogs = append(jobLogs, jobLog{index: num, entry: e})
			jobFor(jobName).Index = num
		default:
			r.Skipped++
		}
	}

	// Combined job logs fill in jobs that have no per-step files (older
	// archives, or partial downloads). For jobs that do have steps, only
	// the index above is taken — steps are the better clock.
	for _, jl := range jobLogs {
		_, jobName, _, _ := classify(jl.entry.Path)
		j := jobs[jobName]
		if len(j.Steps) > 0 {
			continue
		}
		res, err := parseEntry(jl.entry)
		if err != nil {
			return nil, err
		}
		j.LogOnly = true
		j.Start, j.End, j.Timed = res.Start, res.End, res.HasTime
		j.Failed = res.Failed
		absorbMeta(j, res)
	}

	if len(jobs) == 0 {
		return nil, fmt.Errorf("no GitHub Actions job logs found in %s (expected N_job.txt files or job/N_step.txt directories)", src.Label)
	}

	for _, j := range jobs {
		finishJob(j)
		r.Jobs = append(r.Jobs, j)
	}
	// Indexed jobs first in workflow order, then unindexed ones by name, so
	// output order matches what the Actions UI shows.
	sort.Slice(r.Jobs, func(a, b int) bool {
		ja, jb := r.Jobs[a], r.Jobs[b]
		if (ja.Index >= 0) != (jb.Index >= 0) {
			return ja.Index >= 0
		}
		if ja.Index != jb.Index {
			return ja.Index < jb.Index
		}
		return ja.Name < jb.Name
	})
	return r, nil
}

func parseEntry(e archive.Entry) (logparse.Result, error) {
	rc, err := e.Open()
	if err != nil {
		return logparse.Result{}, fmt.Errorf("open %s: %w", e.Path, err)
	}
	defer rc.Close()
	res, err := logparse.Parse(rc)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", e.Path, err)
	}
	return res, nil
}

func newStep(num int, name string, res logparse.Result) *Step {
	return &Step{
		Number:   num,
		Name:     name,
		Start:    res.Start,
		End:      res.End,
		Timed:    res.HasTime,
		Lines:    res.Lines,
		Errors:   res.Errors,
		Warnings: res.Warnings,
		Commands: res.Commands,
		Failed:   res.Failed,
		FailLine: res.FailLine,
		Groups:   res.Groups,
	}
}

// absorbMeta copies runner identity facts onto the job. They appear only in
// the "Set up job" log, so at most one source per job ever has them.
func absorbMeta(j *Job, res logparse.Result) {
	if res.RunnerVer != "" {
		j.RunnerVer = res.RunnerVer
	}
	if res.Image != "" {
		j.Image = res.Image
	}
	if res.ImageVer != "" {
		j.ImageVer = res.ImageVer
	}
}

// finishJob orders steps and derives the job clock and verdict from them.
func finishJob(j *Job) {
	sort.Slice(j.Steps, func(a, b int) bool {
		sa, sb := j.Steps[a], j.Steps[b]
		if sa.Number != sb.Number {
			return sa.Number < sb.Number
		}
		return sa.Name < sb.Name
	})
	if j.LogOnly {
		return
	}
	for _, s := range j.Steps {
		if s.Failed {
			j.Failed = true
		}
		if !s.Timed {
			continue
		}
		if !j.Timed || s.Start.Before(j.Start) {
			j.Start = s.Start
			j.Timed = true
		}
		if s.End.After(j.End) {
			j.End = s.End
		}
	}
}
