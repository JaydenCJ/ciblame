// Tests for the archive filename grammar: `N_name.txt` splitting and
// path classification into job logs, step logs, and ignorable entries.
package run

import "testing"

func TestSplitIndexedAcceptsWellFormedStems(t *testing.T) {
	// GitHub replaces '/' with '_' when writing filenames, so sanitized
	// action names contain underscores that must stay intact: only the
	// first separator counts.
	cases := []struct {
		stem string
		num  int
		name string
	}{
		{"12_Run tests", 12, "Run tests"},
		{"2_Run actions_checkout@v4", 2, "Run actions_checkout@v4"},
		{"0_build", 0, "build"},
	}
	for _, c := range cases {
		n, name, ok := splitIndexed(c.stem)
		if !ok || n != c.num || name != c.name {
			t.Errorf("%q → %d %q %v", c.stem, n, name, ok)
		}
	}
}

func TestSplitIndexedRejectsMalformedStems(t *testing.T) {
	for _, stem := range []string{"Run tests", "_leading", "12_", "x2_name", "-1_name", ""} {
		if _, _, ok := splitIndexed(stem); ok {
			t.Fatalf("stem %q should be rejected", stem)
		}
	}
}

func TestClassifyStepLog(t *testing.T) {
	kind, job, num, name := classify("build (ubuntu-latest, 1.22)/3_Run tests.txt")
	if kind != kindStepLog || job != "build (ubuntu-latest, 1.22)" || num != 3 || name != "Run tests" {
		t.Fatalf("got %v %q %d %q", kind, job, num, name)
	}
}

func TestClassifyJobLog(t *testing.T) {
	kind, job, num, name := classify("0_build.txt")
	if kind != kindJobLog || job != "build" || num != 0 || name != "build" {
		t.Fatalf("got %v %q %d %q", kind, job, num, name)
	}
}

func TestClassifyIgnoresDeepNesting(t *testing.T) {
	kind, _, _, _ := classify("a/b/1_step.txt")
	if kind != kindIgnored {
		t.Fatalf("nested path classified as %v", kind)
	}
}

func TestClassifyIgnoresNonTxtAndUnindexed(t *testing.T) {
	for _, p := range []string{"README.md", "build/notes.log", "build/summary.txt", "checksum"} {
		if kind, _, _, _ := classify(p); kind != kindIgnored {
			t.Fatalf("%q classified as %v, want ignored", p, kind)
		}
	}
}
