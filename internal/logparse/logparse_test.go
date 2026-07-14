// Unit tests for the log-line parser: timestamp grammar, marker counting,
// fold pairing, failure detection, and runner metadata. Every fixture is an
// inline string shaped exactly like a downloaded Actions log.
package logparse

import (
	"strings"
	"testing"
	"time"
)

func parse(t *testing.T, content string) Result {
	t.Helper()
	res, err := Parse(strings.NewReader(content))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

func TestTimestampGrammar(t *testing.T) {
	// GitHub emits seven fractional digits and a Z; RFC 3339 variants from
	// re-serialized logs must parse too, and mid-line timestamps must not.
	cases := []struct {
		line    string
		hasTime bool
		want    time.Time
	}{
		{"2026-07-01T10:00:02.1234567Z npm ci", true,
			time.Date(2026, 7, 1, 10, 0, 2, 123456700, time.UTC)},
		{"2026-07-01T10:00:02Z hello", true,
			time.Date(2026, 7, 1, 10, 0, 2, 0, time.UTC)},
		{"2026-07-01T12:00:02.5+02:00 hello", true,
			time.Date(2026, 7, 1, 10, 0, 2, 500000000, time.UTC)},
		{"at 2026-07-01T10:00:00.0000000Z something happened", false, time.Time{}},
	}
	for _, c := range cases {
		res := parse(t, c.line+"\n")
		if res.HasTime != c.hasTime {
			t.Errorf("%q: HasTime = %v, want %v", c.line, res.HasTime, c.hasTime)
			continue
		}
		if c.hasTime && !res.Start.Equal(c.want) {
			t.Errorf("%q: start = %v, want %v", c.line, res.Start, c.want)
		}
	}
}

func TestDurationIsFirstToLastTimestamp(t *testing.T) {
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z start",
		"2026-07-01T10:00:03.5000000Z middle",
		"2026-07-01T10:00:10.0000000Z end",
	}, "\n"))
	if got := res.Duration(); got != 10*time.Second {
		t.Fatalf("duration = %v, want 10s", got)
	}
}

func TestDegenerateFiles(t *testing.T) {
	// Single-line, empty, and clock-free files must all yield a calm zero
	// duration instead of garbage or a panic.
	if res := parse(t, "2026-07-01T10:00:00.0000000Z only line\n"); res.Duration() != 0 || !res.HasTime {
		t.Fatalf("single line: %+v", res)
	}
	if res := parse(t, ""); res.HasTime || res.Lines != 0 {
		t.Fatalf("empty file: %+v", res)
	}
	if res := parse(t, "no clock here\nnor here\n"); res.HasTime || res.Lines != 2 {
		t.Fatalf("clock-free file: %+v", res)
	}
}

func TestContinuationLinesDoNotResetTiming(t *testing.T) {
	// Multi-line tool output sometimes lands without a timestamp prefix;
	// it must count as a line but not disturb the clock.
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z one",
		"  continuation without timestamp",
		"2026-07-01T10:00:04.0000000Z two",
	}, "\n"))
	if got := res.Duration(); got != 4*time.Second {
		t.Fatalf("duration = %v, want 4s", got)
	}
	if res.Lines != 3 {
		t.Fatalf("lines = %d, want 3", res.Lines)
	}
}

func TestCRLFAndBOMTolerated(t *testing.T) {
	// Windows runners produce CRLF; the trailing \r must not corrupt the
	// last field of the line.
	res := parse(t, "2026-07-01T10:00:00.0000000Z ##[group]Run build\r\n2026-07-01T10:00:05.0000000Z ##[endgroup]\r\n")
	if len(res.Groups) != 1 || res.Groups[0].Duration() != 5*time.Second {
		t.Fatalf("groups = %+v, want one 5s group", res.Groups)
	}
	// A UTF-8 BOM on the first line must not hide its timestamp either.
	if res := parse(t, "\ufeff2026-07-01T10:00:00.0000000Z hello\n"); !res.HasTime {
		t.Fatal("BOM hid the first timestamp")
	}
}

func TestGroupPairing(t *testing.T) {
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z ##[group]Run actions/checkout@v4",
		"2026-07-01T10:00:01.0000000Z fetching",
		"2026-07-01T10:00:03.0000000Z ##[endgroup]",
		"2026-07-01T10:00:03.5000000Z ##[group]Post-processing",
		"2026-07-01T10:00:04.0000000Z ##[endgroup]",
	}, "\n"))
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(res.Groups))
	}
	if res.Groups[0].Title != "Run actions/checkout@v4" || res.Groups[0].Duration() != 3*time.Second {
		t.Fatalf("first group = %+v", res.Groups[0])
	}
	if res.Groups[1].Duration() != 500*time.Millisecond {
		t.Fatalf("second group duration = %v", res.Groups[1].Duration())
	}
}

func TestGroupEdgeCases(t *testing.T) {
	// Cancelled step: fold left open at EOF is timed to the last timestamp.
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z ##[group]Run tests",
		"2026-07-01T10:00:07.0000000Z still going",
	}, "\n"))
	if len(res.Groups) != 1 || res.Groups[0].Duration() != 7*time.Second {
		t.Fatalf("unclosed group = %+v", res.Groups)
	}
	// Lost endgroup: the next fold closes it, so time is never counted twice.
	res = parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z ##[group]first",
		"2026-07-01T10:00:02.0000000Z ##[group]second",
		"2026-07-01T10:00:05.0000000Z ##[endgroup]",
	}, "\n"))
	if len(res.Groups) != 2 || res.Groups[0].Duration() != 2*time.Second || res.Groups[1].Duration() != 3*time.Second {
		t.Fatalf("lost endgroup = %+v", res.Groups)
	}
	// A stray endgroup with no open fold is ignored.
	if res := parse(t, "2026-07-01T10:00:00.0000000Z ##[endgroup]\n"); len(res.Groups) != 0 {
		t.Fatalf("stray endgroup = %+v", res.Groups)
	}
}

func TestMarkerCounts(t *testing.T) {
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z ##[error]compile failed",
		"2026-07-01T10:00:01.0000000Z ##[warning]deprecated flag",
		"2026-07-01T10:00:02.0000000Z ##[warning]another",
		"2026-07-01T10:00:03.0000000Z ##[notice]heads up",
		"2026-07-01T10:00:04.0000000Z ##[command]go test ./...",
		"2026-07-01T10:00:05.0000000Z [command]/usr/bin/git version",
	}, "\n"))
	if res.Errors != 1 || res.Warnings != 2 || res.Notices != 1 || res.Commands != 2 {
		t.Fatalf("counts = %d/%d/%d/%d, want 1/2/1/2",
			res.Errors, res.Warnings, res.Notices, res.Commands)
	}
}

func TestFailureDetection(t *testing.T) {
	res := parse(t, "2026-07-01T10:00:00.0000000Z ##[error]Process completed with exit code 1.\n")
	if !res.Failed {
		t.Fatal("Failed should be true")
	}
	if res.FailLine != "Process completed with exit code 1." {
		t.Fatalf("FailLine = %q", res.FailLine)
	}
	// Tools print ##[error] annotations for problems a step still survives
	// (continue-on-error); only the runner's exit-code epitaph is failure.
	if res := parse(t, "2026-07-01T10:00:00.0000000Z ##[error]lint: unused variable\n"); res.Failed || res.Errors != 1 {
		t.Fatalf("plain annotation: %+v", res)
	}
}

func TestDebugMarkerIsNeitherContentNorCount(t *testing.T) {
	// ##[debug] inside the Runner Image fold must not be mistaken for the
	// "Image:" metadata line, and must not count as any diagnostic.
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z ##[group]Runner Image",
		"2026-07-01T10:00:01.0000000Z ##[debug]Image: bogus-image",
		"2026-07-01T10:00:02.0000000Z Image: ubuntu-24.04",
		"2026-07-01T10:00:03.0000000Z ##[endgroup]",
	}, "\n"))
	if res.Image != "ubuntu-24.04" {
		t.Fatalf("image = %q, want ubuntu-24.04", res.Image)
	}
	if res.Errors+res.Warnings+res.Notices+res.Commands != 0 {
		t.Fatal("debug marker should not increment any counter")
	}
}

func TestRunnerMetadata(t *testing.T) {
	res := parse(t, strings.Join([]string{
		"2026-07-01T10:00:00.0000000Z Current runner version: '2.325.0'",
		"2026-07-01T10:00:01.0000000Z ##[group]Runner Image",
		"2026-07-01T10:00:02.0000000Z Image: ubuntu-24.04",
		"2026-07-01T10:00:03.0000000Z Version: 20260622.1.0",
		"2026-07-01T10:00:04.0000000Z ##[endgroup]",
	}, "\n"))
	if res.RunnerVer != "2.325.0" || res.Image != "ubuntu-24.04" || res.ImageVer != "20260622.1.0" {
		t.Fatalf("meta = %q/%q/%q", res.RunnerVer, res.Image, res.ImageVer)
	}
}

func TestImageLineOutsideRunnerImageFoldIgnored(t *testing.T) {
	// A build step legitimately logging "Image: …" (docker build output)
	// must not be mistaken for runner metadata.
	res := parse(t, "2026-07-01T10:00:00.0000000Z Image: my-app:latest\n")
	if res.Image != "" {
		t.Fatalf("image = %q, want empty", res.Image)
	}
}

func TestVeryLongLine(t *testing.T) {
	// A 1 MiB single line (base64 blob in output) must not abort parsing.
	long := "2026-07-01T10:00:00.0000000Z " + strings.Repeat("x", 1<<20) + "\n" +
		"2026-07-01T10:00:09.0000000Z done\n"
	res := parse(t, long)
	if res.Duration() != 9*time.Second || res.Lines != 2 {
		t.Fatalf("duration = %v, lines = %d", res.Duration(), res.Lines)
	}
}
