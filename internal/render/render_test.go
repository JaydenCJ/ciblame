// Tests for the renderers: duration formatting rules, waterfall text,
// JSON envelopes and values, Markdown tables, and diff output including the
// noise-floor fold line. Assertions target the load-bearing fragments of
// each format rather than full golden files, so cosmetic tweaks don't
// invalidate the suite.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/ciblame/internal/diffrun"
	"github.com/JaydenCJ/ciblame/internal/logparse"
	"github.com/JaydenCJ/ciblame/internal/run"
	"github.com/JaydenCJ/ciblame/internal/timeline"
)

const sec = time.Second

var base = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

func step(num int, name string, from, to time.Duration) *run.Step {
	return &run.Step{
		Number: num, Name: name,
		Start: base.Add(from), End: base.Add(to), Timed: true,
	}
}

func job(name string, steps ...*run.Step) *run.Job {
	j := &run.Job{Name: name, Steps: steps, Index: 0}
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
		if s.Failed {
			j.Failed = true
		}
	}
	return j
}

func demoRun() *run.Run {
	return &run.Run{Label: "run.zip", Jobs: []*run.Job{
		job("build",
			step(1, "Set up job", 0, 2*sec),
			step(2, "Run tests", 4*sec, 64*sec), // 2s gap → overhead
		),
	}}
}

func TestDurFormats(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{800 * time.Millisecond, "0.8s"},
		{2400 * time.Millisecond, "2.4s"},
		{59 * sec, "59.0s"},
		{62 * sec, "1m02s"},
		{59*time.Minute + 59*sec, "59m59s"},
		{time.Hour + 2*time.Minute + 3*sec, "1h02m03s"},
	}
	for _, c := range cases {
		if got := Dur(c.d); got != c.want {
			t.Errorf("Dur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	// Deltas carry an explicit sign; zero is "+0.0s" — a delta, not absence.
	if got := SignedDur(30 * sec); got != "+30.0s" {
		t.Errorf("SignedDur(30s) = %q", got)
	}
	if got := SignedDur(-90 * sec); got != "-1m30s" {
		t.Errorf("SignedDur(-90s) = %q", got)
	}
	if got := SignedDur(0); got != "+0.0s" {
		t.Errorf("SignedDur(0) = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("a very long step name", 10); got != "a very lo…" || len([]rune(got)) != 10 {
		t.Fatalf("got %q", got)
	}
}

func TestTextReportShape(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, demoRun(), TextOptions{Width: 20})
	out := buf.String()
	for _, want := range []string{
		"ciblame report — run.zip",
		"1 job · 2 steps",
		"job build · 1m04s",
		"Run tests",
		"█",
		"between-step overhead: 2.0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestTextMarksFailedStep(t *testing.T) {
	failed := step(1, "Deploy", 0, 5*sec)
	failed.Failed = true
	r := &run.Run{Label: "x.zip", Jobs: []*run.Job{job("deploy", failed)}}
	var buf bytes.Buffer
	Text(&buf, r, TextOptions{})
	if !strings.Contains(buf.String(), "✗ failed") || !strings.Contains(buf.String(), "FAILED") {
		t.Fatalf("failed markers missing:\n%s", buf.String())
	}
}

func TestTextLogOnlyNote(t *testing.T) {
	j := &run.Job{Name: "old-style", LogOnly: true, Timed: true, Start: base, End: base.Add(30 * sec)}
	r := &run.Run{Label: "x.zip", Jobs: []*run.Job{j}}
	var buf bytes.Buffer
	Text(&buf, r, TextOptions{})
	if !strings.Contains(buf.String(), "combined job log only") {
		t.Fatalf("log-only note missing:\n%s", buf.String())
	}
}

func TestTextGroupsDrilldown(t *testing.T) {
	s := step(1, "Run tests", 0, 60*sec)
	s.Groups = []logparse.Group{{Title: "Run go test ./...", Start: base, End: base.Add(55 * sec)}}
	r := &run.Run{Label: "x.zip", Jobs: []*run.Job{job("build", s)}}
	var buf bytes.Buffer
	Text(&buf, r, TextOptions{Groups: true})
	if !strings.Contains(buf.String(), "└─") || !strings.Contains(buf.String(), "Run go test ./...") {
		t.Fatalf("group drill-down missing:\n%s", buf.String())
	}
}

func TestJSONReport(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, demoRun()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["tool"] != "ciblame" || doc["schema_version"] != float64(1) || doc["kind"] != "report" {
		t.Fatalf("envelope = %v", doc)
	}
	jobs := doc["jobs"].([]any)
	j := jobs[0].(map[string]any)
	if j["duration_s"] != float64(64) || j["overhead_s"] != float64(2) {
		t.Fatalf("job values = %v", j)
	}
	steps := j["steps"].([]any)
	s := steps[1].(map[string]any)
	if s["offset_s"] != float64(4) || s["gap_s"] != float64(2) || s["duration_s"] != float64(60) {
		t.Fatalf("step values = %v", s)
	}
	if s["start"] != "2026-07-01T10:00:04Z" {
		t.Fatalf("step start = %v", s["start"])
	}
	// An untimed step serializes null timestamps, not zero-value garbage.
	untimed := &run.Run{Label: "x.zip", Jobs: []*run.Job{
		job("build", &run.Step{Number: 1, Name: "untimed"}),
	}}
	buf.Reset()
	if err := JSON(&buf, untimed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"start": null`) {
		t.Fatalf("untimed step should serialize null start:\n%s", buf.String())
	}
}

func TestMarkdownReportTable(t *testing.T) {
	var buf bytes.Buffer
	Markdown(&buf, demoRun())
	out := buf.String()
	for _, want := range []string{
		"## ciblame report — run.zip",
		"| # | step | start | dur | share |",
		"| 2 | Run tests | +4.0s | 1m00s | 93.8% |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q:\n%s", want, out)
		}
	}
	// Pipes in step names must not break the table.
	piped := &run.Run{Label: "x.zip", Jobs: []*run.Job{
		job("build", step(1, "Run a | b", 0, sec)),
	}}
	buf.Reset()
	Markdown(&buf, piped)
	if !strings.Contains(buf.String(), `Run a \| b`) {
		t.Fatalf("pipe not escaped:\n%s", buf.String())
	}
}

func demoDiff() *diffrun.Report {
	baseRun := &run.Run{Label: "base.zip", Jobs: []*run.Job{
		job("build",
			step(1, "Checkout", 0, 2*sec),
			step(2, "Run tests", 2*sec, 62*sec)),
	}}
	headRun := &run.Run{Label: "head.zip", Jobs: []*run.Job{
		job("build",
			step(1, "Checkout", 0, 2500*time.Millisecond),
			step(2, "Run tests", 3*sec, 303*sec)),
	}}
	return diffrun.Diff(baseRun, headRun)
}

func TestDiffTextShape(t *testing.T) {
	var buf bytes.Buffer
	DiffText(&buf, demoDiff(), 2*sec)
	out := buf.String()
	for _, want := range []string{
		"ciblame diff — base.zip → head.zip",
		"job time",
		"+4m01s",              // job time delta (62s → 303s job)
		"~ Run tests",         // matched sigil
		"1m00s → 5m00s",       // per-step before/after
		"1 step within ±2.0s", // folded Checkout
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiffTextAddedRemovedSigils(t *testing.T) {
	baseRun := &run.Run{Label: "b", Jobs: []*run.Job{job("build", step(1, "Old step", 0, 30*sec))}}
	headRun := &run.Run{Label: "h", Jobs: []*run.Job{job("build", step(1, "New step", 0, 40*sec))}}
	var buf bytes.Buffer
	DiffText(&buf, diffrun.Diff(baseRun, headRun), 0)
	out := buf.String()
	if !strings.Contains(out, "+ New step") || !strings.Contains(out, "- Old step") {
		t.Fatalf("sigils missing:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Fatalf("missing-side dash absent:\n%s", out)
	}
}

func TestDiffJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := DiffJSON(&buf, demoDiff()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["kind"] != "diff" || doc["base"] != "base.zip" || doc["head"] != "head.zip" {
		t.Fatalf("envelope = %v", doc)
	}
	if doc["job_time_delta_s"] != float64(241) {
		t.Fatalf("job_time_delta_s = %v", doc["job_time_delta_s"])
	}
	jobs := doc["jobs"].([]any)
	steps := jobs[0].(map[string]any)["steps"].([]any)
	first := steps[0].(map[string]any)
	if first["name"] != "Run tests" || first["delta_s"] != float64(240) {
		t.Fatalf("first step = %v", first)
	}
}

func TestDiffMarkdown(t *testing.T) {
	var buf bytes.Buffer
	DiffMarkdown(&buf, demoDiff(), 2*sec)
	out := buf.String()
	for _, want := range []string{
		"## ciblame diff — base.zip → head.zip",
		"| job time |",
		"| ~ Run tests | 1m00s | 5m00s | +4m00s |",
		"_1 step within ±2.0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff markdown missing %q:\n%s", want, out)
		}
	}
}

func TestSlowOutputs(t *testing.T) {
	r := demoRun()
	entries := timeline.Slowest(r, 1)
	var buf bytes.Buffer
	SlowText(&buf, r, entries)
	if !strings.Contains(buf.String(), "Run tests") || !strings.Contains(buf.String(), "93.8%") {
		t.Fatalf("slow text:\n%s", buf.String())
	}
	buf.Reset()
	if err := SlowJSON(&buf, r, entries); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["kind"] != "slow" {
		t.Fatalf("kind = %v", doc["kind"])
	}
	s := doc["steps"].([]any)[0].(map[string]any)
	if s["step"] != "Run tests" || s["share_pct"] != float64(93.8) {
		t.Fatalf("entry = %v", s)
	}
}
