// Command make-demo-archive fabricates two deterministic GitHub Actions
// log archives — demo/base.zip and demo/head.zip — shaped exactly like the
// zip the run page's "Download log archive" button serves: one combined
// `N_job.txt` per job plus a `job/N_step.txt` file per step, every line
// prefixed with an RFC 3339 UTC timestamp.
//
// The head run recreates the classic mystery: CI got about four minutes
// slower. `ciblame diff demo/base.zip demo/head.zip` names the step.
//
// Usage: go run ./examples/make-demo-archive [output-dir]
package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// arc collects files and writes a zip; tiny on purpose so the example
// doubles as documentation of the archive layout.
type arc struct {
	start time.Time
	files map[string][]string
}

func newArc(start time.Time) *arc {
	return &arc{start: start, files: map[string][]string{}}
}

// add appends timestamped lines to a file. Offsets are relative to the
// archive's start instant.
func (a *arc) add(path string, at time.Duration, lines ...string) {
	for i, l := range lines {
		ts := a.start.Add(at + time.Duration(i)*20*time.Millisecond)
		a.files[path] = append(a.files[path], ts.Format("2006-01-02T15:04:05.0000000Z")+" "+l)
	}
}

// step writes a realistic step log spanning [from, to]: the runner's fold
// around the action, an echoed command, and a closing line.
func (a *arc) step(job string, num int, name string, from, to time.Duration, body ...string) {
	p := fmt.Sprintf("%s/%d_%s.txt", job, num, name)
	a.add(p, from, "##[group]Run "+name)
	a.add(p, from+100*time.Millisecond, body...)
	a.add(p, to, "##[endgroup]")
}

func (a *arc) write(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(a.files))
	for n := range a.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(strings.Join(a.files[n], "\n") + "\n")); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

const (
	s  = time.Second
	ms = time.Millisecond
)

// setup writes a faithful "Set up job" log with runner identity lines.
func setup(a *arc, job string, from, to time.Duration, image string) {
	p := fmt.Sprintf("%s/1_Set up job.txt", job)
	a.add(p, from,
		"Current runner version: '2.325.0'",
		"##[group]Operating System",
		"Ubuntu",
		"24.04.2",
		"LTS",
		"##[endgroup]",
		"##[group]Runner Image",
		"Image: "+image,
		"Version: 20260622.1.0",
		"Included Software: https://example.test/images",
		"##[endgroup]")
	a.add(p, to, "Complete job name: "+job)
}

// buildJob lays out the `build` job. The three tunable durations are the
// ones the head run regresses: module-cache restore, unit tests, and the
// coverage step (absent in base).
func buildJob(a *arc, cache, tests time.Duration, coverage bool) {
	setup(a, "build", 0, 2400*ms, "ubuntu-24.04")
	a.step("build", 2, "Checkout", 2600*ms, 4400*ms,
		"[command]/usr/bin/git version",
		"[command]/usr/bin/git fetch --no-tags --prune --depth=1")
	a.step("build", 3, "Set up Go", 4600*ms, 12100*ms,
		"Setup go version spec 1.22")
	a.step("build", 4, "Restore module cache", 12300*ms, 12300*ms+cache,
		"Received 96468992 of 96468992 bytes")
	from := 12500*ms + cache
	a.step("build", 5, "Build", from, from+38*s,
		"[command]go build ./...")
	from += 38200 * ms
	a.step("build", 6, "Run unit tests", from, from+tests,
		"[command]go test -count=1 ./...",
		"##[warning]retrying flaky package example.test/pkg/net once")
	from += tests + 200*ms
	num := 7
	if coverage {
		a.step("build", num, "Generate coverage report", from, from+12500*ms,
			"[command]go tool cover -func=cover.out")
		from += 12700 * ms
		num++
	}
	a.step("build", num, "Upload artifact", from, from+8500*ms,
		"Artifact app-linux-amd64 uploaded, 12 MiB")
	from += 8700 * ms
	a.step("build", num+1, "Post Checkout", from, from+500*ms,
		"Cleaning up orphan processes")
	// The combined job log GitHub also puts at the archive root. ciblame
	// prefers the per-step files; this is here for layout fidelity.
	a.add("0_build.txt", 0, "##[group]Run build")
	a.add("0_build.txt", from+500*ms, "##[endgroup]")
}

// lintJob runs in parallel with build, starting a few seconds later.
func lintJob(a *arc, lint time.Duration) {
	setup(a, "lint (ubuntu-latest)", 5*s, 7*s, "ubuntu-24.04")
	a.step("lint (ubuntu-latest)", 2, "Checkout", 7200*ms, 9000*ms,
		"[command]/usr/bin/git version")
	a.step("lint (ubuntu-latest)", 3, "Run golangci-lint", 9200*ms, 9200*ms+lint,
		"[command]golangci-lint run ./...")
	a.add("1_lint (ubuntu-latest).txt", 5*s, "##[group]Run lint")
	a.add("1_lint (ubuntu-latest).txt", 9400*ms+lint, "##[endgroup]")
}

func main() {
	out := "demo"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fail(err)
	}

	// Base: the fast run everyone remembers. Head: a week later, the module
	// cache restore has bloated and the test step carries a new integration
	// suite — together almost exactly four minutes of extra job time.
	base := newArc(time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	buildJob(base, 8*s, 62*s, false)
	lintJob(base, 46*s)

	head := newArc(time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC))
	buildJob(head, 31800*ms, 266*s, true)
	lintJob(head, 47800*ms)

	for name, a := range map[string]*arc{"base.zip": base, "head.zip": head} {
		p := filepath.Join(out, name)
		if err := a.write(p); err != nil {
			fail(err)
		}
		fmt.Printf("wrote %s\n", p)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "make-demo-archive:", err)
	os.Exit(1)
}
