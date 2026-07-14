// Package testarc fabricates deterministic GitHub Actions log archives for
// tests: a fluent builder that emits either a real zip file or an extracted
// directory with identical content, plus line helpers that write the exact
// timestamp format the runner uses (RFC 3339 UTC, seven fractional digits).
//
// It lives outside the _test files because the archive, run, render, and
// cli test suites all need it; it is never imported by production code.
package testarc

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Base is the fixed instant fixtures count from, so every generated
// archive is byte-identical across machines and runs.
var Base = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

// At renders an offset from Base in the runner's timestamp format.
func At(offset time.Duration) string {
	return Base.Add(offset).Format("2006-01-02T15:04:05.0000000Z")
}

// Line builds one runner log line: timestamp, space, text.
func Line(offset time.Duration, text string) string {
	return At(offset) + " " + text
}

// StepLines builds a minimal well-formed step log spanning [start, end]:
// a first line at start, a filler line midway, and a last line at end.
func StepLines(start, end time.Duration, what string) string {
	mid := start + (end-start)/2
	return strings.Join([]string{
		Line(start, "##[group]Run "+what),
		Line(mid, what+" output"),
		Line(end, "##[endgroup]"),
	}, "\n") + "\n"
}

// Builder accumulates archive files and writes them out in either shape.
type Builder struct {
	files map[string]string
}

// New returns an empty archive builder.
func New() *Builder { return &Builder{files: map[string]string{}} }

// File adds a raw file at an arbitrary archive path.
func (b *Builder) File(path, content string) *Builder {
	b.files[path] = content
	return b
}

// Step adds "job/num_name.txt" with the given content.
func (b *Builder) Step(job string, num int, name, content string) *Builder {
	return b.File(fmt.Sprintf("%s/%d_%s.txt", job, num, name), content)
}

// TimedStep adds a step whose log spans [start, end] relative to Base.
func (b *Builder) TimedStep(job string, num int, name string, start, end time.Duration) *Builder {
	return b.Step(job, num, name, StepLines(start, end, name))
}

// JobLog adds a top-level "index_job.txt" combined log.
func (b *Builder) JobLog(index int, job, content string) *Builder {
	return b.File(fmt.Sprintf("%d_%s.txt", index, job), content)
}

// paths returns file paths in sorted order for deterministic zips.
func (b *Builder) paths() []string {
	out := make([]string, 0, len(b.files))
	for p := range b.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// WriteZip writes the archive as a zip file at path.
func (b *Builder) WriteZip(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for _, p := range b.paths() {
		w, err := zw.Create(p)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(b.files[p])); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// WriteDir writes the archive as an extracted directory rooted at dir.
func (b *Builder) WriteDir(dir string) error {
	for _, p := range b.paths() {
		abs := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(b.files[p]), 0o644); err != nil {
			return err
		}
	}
	return nil
}
