package main

import (
	"path/filepath"
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
// test". It needs every failure's test, since a rerun that left some out
// would read as all of them, and none when Rerun can't make the command
// exact (another runner, a flag it doesn't know). It comes from the run,
// not from context, so --no-context keeps it.
func rerunNext(argv []string, cwd string, r *digest.Report, rs *refs.Resolver) []report.Next {
	if len(r.Failures) == 0 || len(r.Failures) > maxRerun {
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
