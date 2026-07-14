// Package logparse turns a single GitHub Actions log file into timing and
// diagnostic facts. Every line in a downloaded log archive is prefixed with
// an RFC 3339 UTC timestamp (seven fractional digits); the parser reads
// those prefixes plus the runner's `##[...]` workflow command markers and
// nothing else — log *content* is never interpreted.
//
// The package is pure: it reads from an io.Reader and touches neither the
// filesystem nor the network, which keeps it trivially unit-testable.
package logparse

import (
	"bufio"
	"io"
	"strings"
	"time"
)

// Group is a `##[group]…##[endgroup]` fold inside a step log, with the
// wall-clock span between the two markers. GitHub's own log viewer renders
// these as collapsible sections; timing them answers "which part of this
// step was slow" (e.g. the `Run actions/checkout` fold vs. post-processing).
type Group struct {
	Title string
	Start time.Time
	End   time.Time
}

// Duration is the wall-clock span of the fold.
func (g Group) Duration() time.Duration { return g.End.Sub(g.Start) }

// Result is everything logparse extracts from one log file.
type Result struct {
	// Start and End are the first and last parseable line timestamps.
	// HasTime reports whether at least one line carried a timestamp; when
	// false, Start/End are zero and the file contributes no timing.
	Start   time.Time
	End     time.Time
	HasTime bool

	Lines     int // total lines, timestamped or not
	Errors    int // ##[error] lines
	Warnings  int // ##[warning] lines
	Notices   int // ##[notice] lines
	Commands  int // ##[command] / [command] lines (invocations the runner echoed)
	Failed    bool
	FailLine  string // the "Process completed with exit code N" text, if any
	Groups    []Group
	RunnerVer string // from "Current runner version: '…'" (Set up job only)
	Image     string // from "Image: …" inside the "Runner Image" fold
	ImageVer  string // from "Version: …" inside the "Runner Image" fold
}

// Duration is last-timestamp minus first-timestamp. A single-line or
// untimestamped file has duration zero.
func (r Result) Duration() time.Duration {
	if !r.HasTime {
		return 0
	}
	return r.End.Sub(r.Start)
}

// maxLineBytes bounds scanner buffers. Runner logs can carry very long
// single lines (base64 blobs, minified JS in error output), so the default
// 64 KiB bufio limit is not enough; 4 MiB has headroom without being silly.
const maxLineBytes = 4 << 20

// failMarker is the runner's step-failure epitaph. Its presence in a step
// log is the reliable failure signal — exit codes never appear elsewhere.
const failMarker = "Process completed with exit code"

// Parse reads one log file. It never returns a parse error for malformed
// content — unparseable lines are simply lines without timing — so the only
// error is a genuine read failure from r.
func Parse(r io.Reader) (Result, error) {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)

	var openGroup *Group   // currently unclosed fold, if any
	inRunnerImage := false // inside the "Runner Image" fold of Set up job
	first := true
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if first {
			line = strings.TrimPrefix(line, "\ufeff") // tolerate a UTF-8 BOM
			first = false
		}
		res.Lines++

		ts, rest, ok := splitTimestamp(line)
		if ok {
			if !res.HasTime {
				res.Start = ts
				res.HasTime = true
			}
			res.End = ts
		} else {
			// Continuation line (multi-line command output re-flowed by the
			// runner): it belongs to the previous timestamp, nothing to do.
			rest = line
		}

		kind, payload := marker(rest)
		switch kind {
		case "group":
			if openGroup != nil {
				// The runner never nests folds; an unclosed group followed
				// by a new one means the endgroup was lost — close at the
				// new group's timestamp so no time is double-counted.
				closeGroup(&res, openGroup, ts, ok)
			}
			openGroup = &Group{Title: payload, Start: ts}
			if !ok && res.HasTime {
				openGroup.Start = res.End
			}
			inRunnerImage = payload == "Runner Image"
		case "endgroup":
			closeGroup(&res, openGroup, ts, ok)
			openGroup = nil
			inRunnerImage = false
		case "error":
			res.Errors++
			if strings.HasPrefix(payload, failMarker) {
				res.Failed = true
				res.FailLine = payload
			}
		case "warning":
			res.Warnings++
		case "notice":
			res.Notices++
		case "command":
			res.Commands++
		case "":
			scanMeta(&res, rest, inRunnerImage)
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	if openGroup != nil {
		// Fold left open at EOF (step was cancelled, or the endgroup landed
		// in a later file): close it at the last seen timestamp.
		closeGroup(&res, openGroup, res.End, res.HasTime)
	}
	return res, nil
}

// closeGroup finalizes a fold. Groups whose boundaries carried no
// timestamps are dropped rather than reported with garbage spans.
func closeGroup(res *Result, g *Group, end time.Time, endTimed bool) {
	if g == nil {
		return
	}
	if !endTimed {
		end = res.End
	}
	if g.Start.IsZero() || end.IsZero() || end.Before(g.Start) {
		return
	}
	g.End = end
	res.Groups = append(res.Groups, *g)
}

// splitTimestamp splits "2026-07-01T10:00:02.1234567Z npm ci" into the
// parsed time and the remainder. GitHub emits RFC 3339 UTC with seven
// fractional digits, but RFC3339Nano also accepts fewer digits and numeric
// offsets, so re-serialized or hand-edited logs still parse.
func splitTimestamp(line string) (time.Time, string, bool) {
	// Fast rejects: a timestamp is at least "2026-07-01T10:00:02Z" (20
	// bytes) and starts with a digit.
	if len(line) < 20 || line[0] < '0' || line[0] > '9' {
		return time.Time{}, "", false
	}
	field := line
	rest := ""
	if i := strings.IndexByte(line, ' '); i >= 0 {
		field, rest = line[:i], line[i+1:]
	}
	ts, err := time.Parse(time.RFC3339Nano, field)
	if err != nil {
		return time.Time{}, "", false
	}
	return ts, rest, true
}

// marker recognizes the runner's workflow-command prefixes. It accepts both
// the canonical "##[kind]" form and the bare "[command]" echo some runner
// versions emit, and returns the marker kind plus its payload text.
func marker(rest string) (kind, payload string) {
	s := rest
	if strings.HasPrefix(s, "##[") {
		s = s[3:]
	} else if strings.HasPrefix(s, "[command]") {
		return "command", s[len("[command]"):]
	} else {
		return "", rest
	}
	i := strings.IndexByte(s, ']')
	if i < 0 {
		return "", rest
	}
	switch k := s[:i]; k {
	case "group", "endgroup", "error", "warning", "notice", "command":
		return k, s[i+1:]
	default:
		// ##[debug], ##[section], ##[add-mask]… — real markers we do not
		// track individually; swallow so they aren't misread as content.
		return "other", s[i+1:]
	}
}

// scanMeta harvests runner identity lines. They only ever occur in the
// "Set up job" step, but matching is cheap and exact enough to run on every
// line without false positives.
func scanMeta(res *Result, rest string, inRunnerImage bool) {
	const verPrefix = "Current runner version: '"
	if strings.HasPrefix(rest, verPrefix) {
		res.RunnerVer = strings.TrimSuffix(rest[len(verPrefix):], "'")
		return
	}
	if !inRunnerImage {
		return
	}
	trimmed := strings.TrimSpace(rest)
	if v, ok := strings.CutPrefix(trimmed, "Image: "); ok && res.Image == "" {
		res.Image = v
	} else if v, ok := strings.CutPrefix(trimmed, "Version: "); ok && res.ImageVer == "" {
		res.ImageVer = v
	}
}
