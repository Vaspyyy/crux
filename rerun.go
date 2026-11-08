package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/testcmd"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
)

// maxRerun is how many failures a rerun suggestion takes on at most.
const maxRerun = nextItems

// rerunNext is the next: line that reruns only the failed tests with the
// run's own flags (Prism's testcmd.Rerun), worded by execution: "crux go
// test -race -run '^TestList$' ./internal/files/   reruns the 1 failed
// test". A rerun that left a test out would read as all of them, so it
// needs every failed test: none when the run had errors (a setup or
// teardown error, a test that timed out), when the failures crux read
// aren't all the run says failed (lines are the run's output), or when a
// failure's test wasn't found; and none when Rerun can't make the command
// exact (another runner, a flag it doesn't know). It comes from the run,
// not from context, so --no-context keeps it.
func rerunNext(argv []string, cwd string, lines []string, r *digest.Report, rs *refs.Resolver) []report.Next {
	if len(r.Failures) == 0 || len(r.Failures) > maxRerun || len(r.Errors) > 0 || r.Tests.Errors > 0 || !allFailures(r, lines) {
		return nil
	}
	info := rs.Repo()
	if info == nil {
		return nil
	}
	var failed []testcmd.Failed
	for _, it := range r.Failures {
		_, test := rs.Refs(it)
		f, ok := failedTest(it, test)
		if !ok {
			return nil
		}
		failed = append(failed, f)
	}
	// the refs are relative to the repository's root as git resolves it
	n, ok := testcmd.RerunNext(resolved(info.Root), resolved(cwd), argv, failed)
	if !ok {
		return nil
	}
	return []report.Next{n}
}

// goFailRe is a failed test as go test prints it, read loosely so a line
// the digest missed still counts.
var goFailRe = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)

// allFailures reports whether the failures are all the tests the run
// says failed: pytest's and cargo's own count, and for go every "--- FAIL"
// a failure or the parent of one (a parent fails with its subtests).
// (Failures from another runner than the command's, Rerun refuses.)
func allFailures(r *digest.Report, lines []string) bool {
	if r.Failures[0].Runner != "go test" {
		return r.Tests.Failed == len(r.Failures)
	}
	for _, l := range lines {
		m := goFailRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if !slices.ContainsFunc(r.Failures, func(f *digest.Item) bool { return f.Title == m[1] || strings.HasPrefix(f.Title, m[1]+"/") }) {
			return false
		}
	}
	return true
}

// failedTest is a failure as Rerun takes it: its test's symbol, and the
// test as the runner printed it. That's go's test name and cargo's test
// path as the title has them; for pytest, the node id from the symbol
// with the parametrization the title has ("[2]"), since a traceback's
// header names the test without its file. ok is false for a failure
// without a test ref.
func failedTest(it *digest.Item, test string) (testcmd.Failed, bool) {
	pr := ref.Parse(test, nil)
	if test == "" || pr.Name == "" {
		return testcmd.Failed{}, false
	}
	t := ref.Target{Path: pr.Path, Name: pr.Name}
	printed := it.Title
	switch it.Runner {
	case "go test":
		t.Kind = "func"
	case "cargo test":
		t.Kind = "fn"
	case "pytest":
		t.Kind = "def"
		printed = pr.Path + "::" + strings.ReplaceAll(pr.Name, ".", "::")
		if i := strings.IndexByte(it.Title, '['); i > 0 && strings.HasSuffix(it.Title, "]") {
			printed += it.Title[i:]
		}
	default:
		return testcmd.Failed{}, false
	}
	return testcmd.Failed{Target: t, Printed: printed}, true
}

// resolved is a directory with symlinks resolved, or as given if it can't be.
func resolved(dir string) string {
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		return d
	}
	return dir
}
