// End-to-end tests: build real (temporary, offline) log archives with
// pinned timestamps, then run the CLI in-process and assert on stdout,
// stderr, and exit codes. Nothing here touches the network or the wall
// clock, so results are byte-identical on every machine.
package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/ciblame/internal/testarc"
)

const sec = time.Second

// runCLI invokes the CLI in-process and captures both streams.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// baseArchive fabricates the canonical two-job fixture as base.zip:
// build (Set up job 2s, Checkout 2s, Run tests 60s) + lint (48s linter).
func baseArchive(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "base.zip")
	b := testarc.New().
		JobLog(0, "build", testarc.Line(0, "start")+"\n").
		TimedStep("build", 1, "Set up job", 0, 2*sec).
		TimedStep("build", 2, "Checkout", 2*sec+500*time.Millisecond, 4*sec+500*time.Millisecond).
		TimedStep("build", 3, "Run tests", 5*sec, 65*sec).
		JobLog(1, "lint", testarc.Line(3*sec, "start")+"\n").
		TimedStep("lint", 1, "Set up job", 3*sec, 5*sec).
		TimedStep("lint", 2, "Run linter", 5*sec, 53*sec)
	if err := b.WriteZip(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// headArchive is baseArchive a week later: tests regressed 60s → 4m30s and
// a coverage step appeared.
func headArchive(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "head.zip")
	b := testarc.New().
		JobLog(0, "build", testarc.Line(0, "start")+"\n").
		TimedStep("build", 1, "Set up job", 0, 2*sec).
		TimedStep("build", 2, "Checkout", 2*sec+500*time.Millisecond, 4*sec+500*time.Millisecond).
		TimedStep("build", 3, "Run tests", 5*sec, 275*sec).
		TimedStep("build", 4, "Coverage", 275*sec+500*time.Millisecond, 290*sec).
		JobLog(1, "lint", testarc.Line(3*sec, "start")+"\n").
		TimedStep("lint", 1, "Set up job", 3*sec, 5*sec).
		TimedStep("lint", 2, "Run linter", 5*sec, 54*sec)
	if err := b.WriteZip(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVersionAndHelp(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := runCLI(t, arg)
		if code != ExitOK || strings.TrimSpace(out) != "ciblame 0.1.0" {
			t.Fatalf("%s → code %d, out %q", arg, code, out)
		}
	}
	code, out, _ := runCLI(t, "help")
	if code != ExitOK || !strings.Contains(out, "Usage:") {
		t.Fatalf("help → code %d, out %q", code, out)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	// Every malformed invocation must exit 2 with a pointed message, so
	// scripts can distinguish "you called it wrong" from "the archive is bad".
	cases := []struct {
		args []string
		want string
	}{
		{nil, "Usage:"},
		{[]string{"--bogus"}, "unknown flag"},
		{[]string{"report"}, "missing archive path"},
		{[]string{"report", "--format", "yaml", "x.zip"}, `unknown --format "yaml"`},
		{[]string{"report", "--width", "4", "x.zip"}, "--width"},
		{[]string{"diff", "only-one.zip"}, "exactly two archives"},
		{[]string{"slow", "--top", "0", "x.zip"}, "--top"},
	}
	for _, c := range cases {
		code, _, errOut := runCLI(t, c.args...)
		if code != ExitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%v → code %d, err %q (want exit 2 mentioning %q)", c.args, code, errOut, c.want)
		}
	}
}

func TestNonexistentArchiveIsRuntimeError(t *testing.T) {
	code, _, errOut := runCLI(t, "report", filepath.Join(t.TempDir(), "gone.zip"))
	if code != ExitRuntime || errOut == "" {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

func TestBarePathDefaultsToReport(t *testing.T) {
	code, out, _ := runCLI(t, baseArchive(t))
	if code != ExitOK || !strings.Contains(out, "ciblame report — base.zip") {
		t.Fatalf("code %d, out:\n%s", code, out)
	}
}

func TestReportTextWaterfall(t *testing.T) {
	code, out, _ := runCLI(t, "report", baseArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{
		"2 jobs · 5 steps",
		"job build · 1m05s",
		"Run tests",
		"1m00s",
		"█",
		"job lint · 50.0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
	code, out, _ = runCLI(t, "report", "--format", "markdown", baseArchive(t))
	if code != ExitOK || !strings.Contains(out, "| # | step | start | dur | share |") {
		t.Fatalf("markdown format: code %d, out:\n%s", code, out)
	}
}

func TestReportJSON(t *testing.T) {
	code, out, _ := runCLI(t, "report", "--format", "json", baseArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["kind"] != "report" || doc["job_time_s"] != float64(115) {
		t.Fatalf("doc = %v", doc)
	}
	if len(doc["jobs"].([]any)) != 2 {
		t.Fatal("expected 2 jobs")
	}
}

func TestReportJobFilter(t *testing.T) {
	code, out, _ := runCLI(t, "report", "--job", "LINT", baseArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out, "job build") || !strings.Contains(out, "job lint") {
		t.Fatalf("filter not applied (matching is case-insensitive):\n%s", out)
	}
}

func TestJobFilterMatchingNothingErrors(t *testing.T) {
	code, _, errOut := runCLI(t, "report", "--job", "deploy", baseArchive(t))
	if code != ExitRuntime || !strings.Contains(errOut, "matches none") {
		t.Fatalf("code %d, err %q", code, errOut)
	}
}

func TestSlowRanking(t *testing.T) {
	code, out, _ := runCLI(t, "slow", "--top", "2", baseArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// Header, blank, column header, then exactly 2 ranked rows.
	if !strings.Contains(lines[len(lines)-2], "Run tests") || !strings.Contains(lines[len(lines)-1], "Run linter") {
		t.Fatalf("ranking rows wrong:\n%s", out)
	}
	if !strings.Contains(out, "top 2 of 5 steps") {
		t.Fatalf("header wrong:\n%s", out)
	}
}

func TestDiffFindsTheCulprit(t *testing.T) {
	code, out, _ := runCLI(t, "diff", baseArchive(t), headArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	// The regressed step must be the first step line printed.
	var firstStep string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "~ ") || strings.HasPrefix(trimmed, "+ ") {
			firstStep = trimmed
			break
		}
	}
	if !strings.HasPrefix(firstStep, "~ Run tests") {
		t.Fatalf("first step line = %q, want the Run tests regression:\n%s", firstStep, out)
	}
	if !strings.Contains(out, "+ Coverage") {
		t.Fatalf("added step missing:\n%s", out)
	}
}

func TestDiffJSONOutput(t *testing.T) {
	code, out, _ := runCLI(t, "diff", "--format", "json", baseArchive(t), headArchive(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["kind"] != "diff" {
		t.Fatalf("kind = %v", doc["kind"])
	}
	if doc["job_time_delta_s"].(float64) < 200 {
		t.Fatalf("delta = %v, want a big regression", doc["job_time_delta_s"])
	}
}

func TestDiffFailOverBreaches(t *testing.T) {
	code, _, errOut := runCLI(t, "diff", "--fail-over", "60s", baseArchive(t), headArchive(t))
	if code != ExitBreach {
		t.Fatalf("code %d, want %d", code, ExitBreach)
	}
	if !strings.Contains(errOut, "over the --fail-over budget") {
		t.Fatalf("err = %q", errOut)
	}
	// A generous budget passes with exit 0.
	if code, _, _ := runCLI(t, "diff", "--fail-over", "10m", baseArchive(t), headArchive(t)); code != ExitOK {
		t.Fatalf("within budget: code %d, want 0", code)
	}
}

func TestDiffIdenticalRunsIsQuiet(t *testing.T) {
	// Diffing an archive against itself: zero deltas everywhere and every
	// step folded under the default 2s threshold.
	p := baseArchive(t)
	code, out, _ := runCLI(t, "diff", p, p)
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "+0.0s") {
		t.Fatalf("expected zero delta:\n%s", out)
	}
	if strings.Contains(out, "~ ") {
		t.Fatalf("no step should surface on an identical diff:\n%s", out)
	}
}

func TestReportOnExtractedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "extracted")
	b := testarc.New().TimedStep("build", 1, "Set up job", 0, 2*sec)
	if err := b.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, "report", dir)
	if code != ExitOK || !strings.Contains(out, "job build") {
		t.Fatalf("code %d, out:\n%s", code, out)
	}
}
