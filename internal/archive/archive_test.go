// Tests for archive opening: zip vs directory equivalence, magic-byte
// detection, litter filtering, and deterministic entry ordering. Fixtures
// are built with the shared testarc builder into t.TempDir().
package archive

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/ciblame/internal/testarc"
)

func demoBuilder() *testarc.Builder {
	return testarc.New().
		File("0_build.txt", "job log\n").
		File("build/1_Set up job.txt", "step one\n").
		File("build/2_Checkout.txt", "step two\n")
}

func entryPaths(src *Source) []string {
	out := make([]string, 0, len(src.Entries))
	for _, e := range src.Entries {
		out = append(out, e.Path)
	}
	return out
}

func readEntry(t *testing.T, e Entry) string {
	t.Helper()
	rc, err := e.Open()
	if err != nil {
		t.Fatalf("open %s: %v", e.Path, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", e.Path, err)
	}
	return string(b)
}

func TestOpenZip(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "run.zip")
	if err := demoBuilder().WriteZip(zipPath); err != nil {
		t.Fatal(err)
	}
	src, err := Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	want := []string{"0_build.txt", "build/1_Set up job.txt", "build/2_Checkout.txt"}
	if got := entryPaths(src); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if got := readEntry(t, src.Entries[1]); got != "step one\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestOpenDirMatchesZip(t *testing.T) {
	dir := t.TempDir()
	if err := demoBuilder().WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	src, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if len(src.Entries) != 3 {
		t.Fatalf("entries = %v", entryPaths(src))
	}
	if got := readEntry(t, src.Entries[2]); got != "step two\n" {
		t.Fatalf("content = %q", got)
	}
	if src.Entries[0].Size != int64(len("job log\n")) {
		t.Fatalf("size = %d", src.Entries[0].Size)
	}
}

func TestZipDetectedByMagicNotExtension(t *testing.T) {
	// `gh api …/logs > logs` produces a zip with no extension at all.
	p := filepath.Join(t.TempDir(), "logs-without-extension")
	if err := demoBuilder().WriteZip(p); err != nil {
		t.Fatal(err)
	}
	src, err := Open(p)
	if err != nil {
		t.Fatalf("magic sniffing failed: %v", err)
	}
	defer src.Close()
	if len(src.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(src.Entries))
	}
}

func TestNonZipFileRejectedWithHint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(p, []byte("just text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(p)
	if err == nil || !strings.Contains(err.Error(), "neither a directory nor a zip") {
		t.Fatalf("err = %v, want the format hint", err)
	}
	// A zip with zero files starts with the end-of-central-directory magic,
	// not the local-file-header one — and holds no logs anyway.
	empty := filepath.Join(t.TempDir(), "empty.zip")
	if err := testarc.New().WriteZip(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(empty); err == nil {
		t.Fatal("empty zip should be rejected")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "gone.zip")); err == nil {
		t.Fatal("missing path should error")
	}
}

func TestMacOSLitterSkipped(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "run.zip")
	b := demoBuilder().
		File("__MACOSX/build/1_Set up job.txt", "resource fork junk").
		File("build/.DS_Store", "junk").
		File("build/._2_Checkout.txt", "apple double")
	if err := b.WriteZip(zipPath); err != nil {
		t.Fatal(err)
	}
	src, err := Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, p := range entryPaths(src) {
		if strings.Contains(p, "__MACOSX") || strings.Contains(p, ".DS_Store") || strings.Contains(p, "._2") {
			t.Fatalf("litter %s survived", p)
		}
	}
	if len(src.Entries) != 3 {
		t.Fatalf("entries = %v, want the 3 real files", entryPaths(src))
	}
}

func TestBackslashZipPathsNormalized(t *testing.T) {
	// Zips produced by some Windows tools use backslash separators.
	zipPath := filepath.Join(t.TempDir(), "run.zip")
	b := testarc.New().File(`build\1_Set up job.txt`, "content\n")
	if err := b.WriteZip(zipPath); err != nil {
		t.Fatal(err)
	}
	src, err := Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if len(src.Entries) != 1 || src.Entries[0].Path != "build/1_Set up job.txt" {
		t.Fatalf("entries = %v", entryPaths(src))
	}
}

func TestEntriesSortedDeterministically(t *testing.T) {
	// Insertion order into the builder is scrambled; Open must sort.
	zipPath := filepath.Join(t.TempDir(), "run.zip")
	b := testarc.New().
		File("z/9_Last.txt", "z").
		File("0_a.txt", "a").
		File("m/1_Mid.txt", "m")
	if err := b.WriteZip(zipPath); err != nil {
		t.Fatal(err)
	}
	src, err := Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	got := entryPaths(src)
	want := []string{"0_a.txt", "m/1_Mid.txt", "z/9_Last.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
