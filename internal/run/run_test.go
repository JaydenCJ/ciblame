// Tests for run assembly: loading zip and directory archives, job/step
// ordering, clock derivation, combined-log fallback, and the run-level
// aggregates. Fixtures are deterministic archives built with testarc.
package run

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/ciblame/internal/testarc"
)

const sec = time.Second

// twoJobArchive is the canonical fixture: an indexed `build` job with three
// steps (one leaving a 2s gap) and a parallel `lint` job starting 5s later.
func twoJobArchive() *testarc.Builder {
	return testarc.New().
		JobLog(0, "build", testarc.Line(0, "combined")+"\n"+testarc.Line(70*sec, "end")+"\n").
		TimedStep("build", 1, "Set up job", 0, 2*sec).
		TimedStep("build", 2, "Checkout", 4*sec, 10*sec). // 2s gap after step 1
		TimedStep("build", 3, "Run tests", 10*sec, 70*sec).
		JobLog(1, "lint", testarc.Line(5*sec, "combined")+"\n").
		TimedStep("lint", 1, "Set up job", 5*sec, 7*sec).
		TimedStep("lint", 2, "Run linter", 7*sec, 30*sec)
}

func loadZip(t *testing.T, b *testarc.Builder) *Run {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run-4213.zip")
	if err := b.WriteZip(p); err != nil {
		t.Fatal(err)
	}
	r, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLoadZipAndDirAgree(t *testing.T) {
	// The same archive must parse identically whether it is still zipped
	// or already extracted.
	zr := loadZip(t, twoJobArchive())
	if zr.Label != "run-4213.zip" {
		t.Fatalf("label = %q, want the archive basename", zr.Label)
	}
	dir := t.TempDir()
	if err := twoJobArchive().WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	dr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.Jobs) != len(dr.Jobs) {
		t.Fatalf("job counts differ: %d vs %d", len(zr.Jobs), len(dr.Jobs))
	}
	for i := range zr.Jobs {
		zj, dj := zr.Jobs[i], dr.Jobs[i]
		if zj.Name != dj.Name || zj.Duration() != dj.Duration() || len(zj.Steps) != len(dj.Steps) {
			t.Fatalf("job %d differs: %+v vs %+v", i, zj, dj)
		}
	}
}

func TestJobsOrderedByRootIndex(t *testing.T) {
	r := loadZip(t, twoJobArchive())
	if len(r.Jobs) != 2 || r.Jobs[0].Name != "build" || r.Jobs[1].Name != "lint" {
		t.Fatalf("job order = %v", jobNames(r))
	}
	if r.Jobs[0].Index != 0 || r.Jobs[1].Index != 1 {
		t.Fatalf("indexes = %d, %d", r.Jobs[0].Index, r.Jobs[1].Index)
	}
}

func TestUnindexedJobSortsAfterIndexed(t *testing.T) {
	// A job directory can arrive without its top-level combined log (a
	// partial download); it must still load, ordered after indexed jobs.
	matrix := "aaa (macos-14, 1.22, race)" // matrix names sort first alphabetically
	b := twoJobArchive().TimedStep(matrix, 1, "Only step", 0, sec)
	r := loadZip(t, b)
	if got := jobNames(r); strings.Join(got, ",") != "build,lint,"+matrix {
		t.Fatalf("order = %v", got)
	}
	if r.Jobs[2].Index != -1 {
		t.Fatalf("unindexed job Index = %d, want -1", r.Jobs[2].Index)
	}
}

func TestStepsSortedByNumber(t *testing.T) {
	// Zip entry order is alphabetical ("10_" sorts before "2_"); assembly
	// must re-sort numerically.
	b := testarc.New().
		TimedStep("job", 10, "Tenth", 20*sec, 21*sec).
		TimedStep("job", 2, "Second", 2*sec, 3*sec).
		TimedStep("job", 1, "First", 0, sec)
	r := loadZip(t, b)
	steps := r.Jobs[0].Steps
	if steps[0].Number != 1 || steps[1].Number != 2 || steps[2].Number != 10 {
		t.Fatalf("step order = %d,%d,%d", steps[0].Number, steps[1].Number, steps[2].Number)
	}
}

func TestJobClockSpansItsSteps(t *testing.T) {
	r := loadZip(t, twoJobArchive())
	j := r.Jobs[0]
	if j.Duration() != 70*sec {
		t.Fatalf("build duration = %v, want 70s", j.Duration())
	}
	if !j.Start.Equal(testarc.Base) {
		t.Fatalf("build start = %v", j.Start)
	}
}

func TestLogOnlyFallback(t *testing.T) {
	// Only the combined job log is present: the job is timed from it and
	// flagged LogOnly with zero steps.
	content := testarc.Line(0, "start") + "\n" + testarc.Line(42*sec, "end") + "\n"
	r := loadZip(t, testarc.New().JobLog(0, "deploy", content))
	j := r.Jobs[0]
	if !j.LogOnly || len(j.Steps) != 0 {
		t.Fatalf("job = %+v, want LogOnly with no steps", j)
	}
	if j.Duration() != 42*sec {
		t.Fatalf("duration = %v, want 42s", j.Duration())
	}
}

func TestCombinedLogIgnoredWhenStepsExist(t *testing.T) {
	// The build job's combined log spans 70s but claims nothing extra;
	// steps are the clock. Shrink the combined log to prove it is unused.
	b := twoJobArchive().JobLog(0, "build", testarc.Line(0, "tiny")+"\n")
	r := loadZip(t, b)
	j := r.Jobs[0]
	if j.LogOnly {
		t.Fatal("job with steps must not be LogOnly")
	}
	if j.Duration() != 70*sec {
		t.Fatalf("duration = %v, want 70s from steps", j.Duration())
	}
}

func TestFailedStepMarksJob(t *testing.T) {
	b := twoJobArchive().Step("build", 4, "Deploy",
		testarc.Line(70*sec, "##[error]Process completed with exit code 2.")+"\n")
	r := loadZip(t, b)
	j := r.Jobs[0]
	if !j.Failed {
		t.Fatal("job should inherit step failure")
	}
	if !j.Steps[3].Failed || j.Steps[3].FailLine != "Process completed with exit code 2." {
		t.Fatalf("step = %+v", j.Steps[3])
	}
}

func TestRunnerMetadataLandsOnJob(t *testing.T) {
	setup := strings.Join([]string{
		testarc.Line(0, "Current runner version: '2.325.0'"),
		testarc.Line(sec, "##[group]Runner Image"),
		testarc.Line(sec, "Image: ubuntu-24.04"),
		testarc.Line(sec, "Version: 20260622.1.0"),
		testarc.Line(2*sec, "##[endgroup]"),
	}, "\n") + "\n"
	r := loadZip(t, testarc.New().Step("build", 1, "Set up job", setup))
	j := r.Jobs[0]
	if j.RunnerVer != "2.325.0" || j.Image != "ubuntu-24.04" || j.ImageVer != "20260622.1.0" {
		t.Fatalf("meta = %q/%q/%q", j.RunnerVer, j.Image, j.ImageVer)
	}
}

func TestUntimedStepIncludedWithZeroDuration(t *testing.T) {
	b := twoJobArchive().Step("build", 4, "Weird step", "no timestamps in here\n")
	r := loadZip(t, b)
	s := r.Jobs[0].Steps[3]
	if s.Timed || s.Duration() != 0 {
		t.Fatalf("step = %+v, want untimed zero-duration", s)
	}
	if r.Jobs[0].Duration() != 70*sec {
		t.Fatal("untimed step must not disturb the job clock")
	}
}

func TestUnrecognizedEntriesCountedAsSkipped(t *testing.T) {
	b := twoJobArchive().
		File("README.md", "stray file").
		File("build/checksums", "stray file")
	r := loadZip(t, b)
	if r.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2", r.Skipped)
	}
}

func TestArchiveWithNoJobLogsErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "run.zip")
	if err := testarc.New().File("README.md", "nothing useful").WriteZip(p); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "no GitHub Actions job logs") {
		t.Fatalf("err = %v", err)
	}
}

func TestWallVersusJobTime(t *testing.T) {
	// build spans 0-70s, lint 5-30s in parallel: wall is 70s but the
	// billable job time is 70+25 = 95s.
	r := loadZip(t, twoJobArchive())
	if r.Wall() != 70*sec {
		t.Fatalf("wall = %v, want 70s", r.Wall())
	}
	if r.JobTime() != 95*sec {
		t.Fatalf("job time = %v, want 95s", r.JobTime())
	}
	if r.StepCount() != 5 {
		t.Fatalf("steps = %d, want 5", r.StepCount())
	}
}

func jobNames(r *Run) []string {
	out := make([]string, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		out = append(out, j.Name)
	}
	return out
}
