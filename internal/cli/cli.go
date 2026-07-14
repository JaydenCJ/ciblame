// Package cli implements the ciblame command-line interface. Run takes
// argv and two writers and returns an exit code, so the whole surface is
// testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JaydenCJ/ciblame/internal/diffrun"
	"github.com/JaydenCJ/ciblame/internal/render"
	"github.com/JaydenCJ/ciblame/internal/run"
	"github.com/JaydenCJ/ciblame/internal/timeline"
	"github.com/JaydenCJ/ciblame/internal/version"
)

// Exit codes. Documented in the README; `diff --fail-over` uses ExitBreach
// as its machine-readable verdict.
const (
	ExitOK      = 0
	ExitBreach  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "slow":
		return runSlow(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ciblame %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(stderr, "ciblame: unknown flag %q before a subcommand\n\n", args[0])
			usage(stderr)
			return ExitUsage
		}
		// Bare archive path: treat as `report <path>`.
		return runReport(args, stdout, stderr)
	}
}

// jobFilter is the repeatable --job flag: case-insensitive substring match
// against job names, keeping a job when any pattern matches.
type jobFilter []string

func (f *jobFilter) String() string     { return strings.Join(*f, ",") }
func (f *jobFilter) Set(v string) error { *f = append(*f, v); return nil }

func (f jobFilter) keep(name string) bool {
	if len(f) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, pat := range f {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return true
		}
	}
	return false
}

// applyFilter narrows a run to matching jobs. It errors when the filter
// eats everything — a silent empty report would read as "no time spent".
func applyFilter(r *run.Run, filter jobFilter) error {
	if len(filter) == 0 {
		return nil
	}
	var kept []*run.Job
	for _, j := range r.Jobs {
		if filter.keep(j.Name) {
			kept = append(kept, j)
		}
	}
	if len(kept) == 0 {
		return fmt.Errorf("--job %q matches none of the %s in %s", filter.String(), render.Count(len(r.Jobs), "job"), r.Label)
	}
	r.Jobs = kept
	return nil
}

func checkFormat(stderr io.Writer, format string, allowed ...string) bool {
	for _, a := range allowed {
		if format == a {
			return true
		}
	}
	fmt.Fprintf(stderr, "ciblame: unknown --format %q (want %s)\n", format, strings.Join(allowed, ", "))
	return false
}

func loadRun(path string, filter jobFilter, stderr io.Writer) (*run.Run, int) {
	r, err := run.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "ciblame: %v\n", err)
		return nil, ExitRuntime
	}
	if err := applyFilter(r, filter); err != nil {
		fmt.Fprintf(stderr, "ciblame: %v\n", err)
		return nil, ExitRuntime
	}
	return r, ExitOK
}

func runReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text, json, or markdown")
	width := fs.Int("width", 28, "waterfall track width in cells (8-120)")
	groups := fs.Bool("groups", false, "show the slowest log folds under each step")
	var filter jobFilter
	fs.Var(&filter, "job", "only report jobs whose name contains this (repeatable)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, code := onePath(fs.Args(), stderr)
	if code != ExitOK {
		return code
	}
	if !checkFormat(stderr, *format, "text", "json", "markdown") {
		return ExitUsage
	}
	if *width < 8 || *width > 120 {
		fmt.Fprintf(stderr, "ciblame: --width %d out of range (want 8-120)\n", *width)
		return ExitUsage
	}
	r, code := loadRun(path, filter, stderr)
	if code != ExitOK {
		return code
	}
	switch *format {
	case "json":
		if err := render.JSON(stdout, r); err != nil {
			fmt.Fprintf(stderr, "ciblame: %v\n", err)
			return ExitRuntime
		}
	case "markdown":
		render.Markdown(stdout, r)
	default:
		render.Text(stdout, r, render.TextOptions{Width: *width, Groups: *groups})
	}
	return ExitOK
}

func runSlow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("slow", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or json")
	top := fs.Int("top", 10, "how many steps to list")
	var filter jobFilter
	fs.Var(&filter, "job", "only rank jobs whose name contains this (repeatable)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	path, code := onePath(fs.Args(), stderr)
	if code != ExitOK {
		return code
	}
	if !checkFormat(stderr, *format, "text", "json") {
		return ExitUsage
	}
	if *top < 1 {
		fmt.Fprintf(stderr, "ciblame: --top must be at least 1\n")
		return ExitUsage
	}
	r, code := loadRun(path, filter, stderr)
	if code != ExitOK {
		return code
	}
	entries := timeline.Slowest(r, *top)
	if *format == "json" {
		if err := render.SlowJSON(stdout, r, entries); err != nil {
			fmt.Fprintf(stderr, "ciblame: %v\n", err)
			return ExitRuntime
		}
		return ExitOK
	}
	render.SlowText(stdout, r, entries)
	return ExitOK
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text, json, or markdown")
	minDelta := fs.Duration("min-delta", 2*time.Second, "fold steps whose |delta| is below this (text/markdown)")
	failOver := fs.Duration("fail-over", 0, "exit 1 when total job time grows by more than this")
	var filter jobFilter
	fs.Var(&filter, "job", "only diff jobs whose name contains this (repeatable)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintf(stderr, "ciblame diff: expected exactly two archives (base head), got %d\n", len(rest))
		return ExitUsage
	}
	if !checkFormat(stderr, *format, "text", "json", "markdown") {
		return ExitUsage
	}
	if *minDelta < 0 {
		fmt.Fprintf(stderr, "ciblame: --min-delta must not be negative\n")
		return ExitUsage
	}
	base, code := loadRun(rest[0], filter, stderr)
	if code != ExitOK {
		return code
	}
	head, code := loadRun(rest[1], filter, stderr)
	if code != ExitOK {
		return code
	}
	rep := diffrun.Diff(base, head)
	switch *format {
	case "json":
		if err := render.DiffJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "ciblame: %v\n", err)
			return ExitRuntime
		}
	case "markdown":
		render.DiffMarkdown(stdout, rep, *minDelta)
	default:
		render.DiffText(stdout, rep, *minDelta)
	}
	if *failOver > 0 && rep.JobTimeDelta() > *failOver {
		fmt.Fprintf(stderr, "ciblame diff: job time grew %s, over the --fail-over budget of %s\n",
			render.SignedDur(rep.JobTimeDelta()), render.Dur(*failOver))
		return ExitBreach
	}
	return ExitOK
}

// onePath extracts the single required positional archive argument.
func onePath(rest []string, stderr io.Writer) (string, int) {
	switch len(rest) {
	case 1:
		return rest[0], ExitOK
	case 0:
		fmt.Fprintf(stderr, "ciblame: missing archive path (a downloaded logs .zip or an unzipped directory)\n")
		return "", ExitUsage
	default:
		fmt.Fprintf(stderr, "ciblame: expected one archive path, got %d arguments\n", len(rest))
		return "", ExitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, `ciblame %s — where did the CI minutes go?

Usage:
  ciblame [report] [flags] <archive>        step waterfall per job (default)
  ciblame slow     [flags] <archive>        slowest steps across the run
  ciblame diff     [flags] <base> <head>    what changed between two runs
  ciblame version                           print the version

<archive> is a GitHub Actions log archive: the .zip from the run page's
"Download log archive" button (or the /actions/runs/{id}/logs API), either
as the zip file itself or an already-unzipped directory.

Report flags:
  --format FORMAT     text (default), json, or markdown
  --job SUBSTR        only report matching jobs (repeatable)
  --width N           waterfall track width in cells (8-120, default 28)
  --groups            show the slowest log folds under each step

Slow flags:
  --format FORMAT     text (default) or json
  --top N             how many steps to list (default 10)
  --job SUBSTR        only rank matching jobs (repeatable)

Diff flags:
  --format FORMAT     text (default), json, or markdown
  --job SUBSTR        only diff matching jobs (repeatable)
  --min-delta DUR     fold steps changing less than this (default 2s)
  --fail-over DUR     exit 1 when total job time grows by more than this

Exit codes: 0 ok · 1 --fail-over breach · 2 usage error · 3 runtime error
`, version.Version)
}
