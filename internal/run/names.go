// Filename grammar for Actions log archives. GitHub lays a run archive out
// as:
//
//	0_build.txt                     one combined log per job, index-prefixed
//	build/1_Set up job.txt          one file per step, step-number-prefixed
//	build/2_Run actions_checkout@v4.txt
//
// Characters that are illegal in filenames ('/', ':', '"', …) are replaced
// with '_' by GitHub when it writes the archive, which is why the checkout
// step above reads `actions_checkout`. ciblame keeps the sanitized names
// verbatim — reversing the substitution would be guessing.
package run

import (
	"path"
	"strconv"
	"strings"
)

// entryKind classifies an archive path.
type entryKind int

const (
	kindIgnored entryKind = iota // not a log file we understand
	kindJobLog                   // top-level N_job.txt combined log
	kindStepLog                  // job/N_step.txt per-step log
)

// classify parses one archive path into its role. Deeply nested paths and
// files without the `N_name.txt` shape are ignored rather than rejected, so
// a stray README inside the archive can't fail the whole load.
func classify(p string) (kind entryKind, job string, num int, name string) {
	dir, base := path.Split(p)
	dir = strings.Trim(dir, "/")
	if strings.Contains(dir, "/") {
		return kindIgnored, "", 0, "" // deeper nesting is not part of the format
	}
	stem, ok := strings.CutSuffix(base, ".txt")
	if !ok {
		return kindIgnored, "", 0, ""
	}
	num, name, ok = splitIndexed(stem)
	if !ok {
		return kindIgnored, "", 0, ""
	}
	if dir == "" {
		return kindJobLog, name, num, name
	}
	return kindStepLog, dir, num, name
}

// splitIndexed splits "12_Run tests" into (12, "Run tests"). The prefix must
// be all digits followed by exactly one underscore; names containing their
// own underscores ("2_Run actions_checkout@v4") keep them intact because
// only the first separator counts.
func splitIndexed(stem string) (int, string, bool) {
	i := strings.IndexByte(stem, '_')
	if i <= 0 || i == len(stem)-1 {
		return 0, "", false
	}
	n, err := strconv.Atoi(stem[:i])
	if err != nil || n < 0 {
		return 0, "", false
	}
	return n, stem[i+1:], true
}
