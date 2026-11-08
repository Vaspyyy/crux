package main

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/testcmd"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
)

// repoFixture writes files to a new git repository, commits them and
// changes into it. It returns the repository's root.
func repoFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	contextEnv(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for p, s := range files {
		write(t, root, p, s)
	}
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "fixture")
	t.Chdir(root)
	return root
}

// checkRerun runs cmd through crux and checks that the first next: line,
// in the text and in --json, with and without context, is the rerun
// (argv, worded by execution). Then it runs the rerun as printed and
// checks that only the failed tests ran, and failed again. It returns the
// rerun's result.
func checkRerun(t *testing.T, cmd, argv []string, why string, failed ...string) report.Crux {
	t.Helper()
	want := report.Next{Cmd: "crux " + report.Cmd(argv...), Why: why}
	line := regexp.MustCompile(`^next: ` + regexp.QuoteMeta(want.Cmd) + ` {3,}` + regexp.QuoteMeta(why) + `$`)
	for _, flags := range [][]string{nil, {"--no-context"}} {
		code, out := text(t, append(flags, cmd...)...)
		if next := nextOf(out); code == 0 || len(next) == 0 || !line.MatchString(next[0]) {
			t.Fatalf("crux %v: exit %d, next: lines %q, want first %s   %s\n%s", flags, code, next, want.Cmd, why, out)
		}
		if d, _ := jsonRun(t, append(flags, cmd...)...); len(d.Next) == 0 || d.Next[0] != want {
			t.Errorf("crux --json %v: next %+v, want first %+v", flags, d.Next, want)
		}
	}
	_, c := jsonRun(t, argv...)
	var got []string
	for _, f := range c.Failures {
		got = append(got, f.Title)
	}
	if !reflect.DeepEqual(got, failed) || c.Tests == nil || c.Tests.Failed != len(failed) || c.Tests.Passed != 0 {
		t.Errorf("the rerun %s: failures %q, tests %+v", want.Cmd, got, c.Tests)
	}
	return c
}

// After a go test run, the rerun keeps the run's flags and names only the
// failed test and its package.
func TestRerunGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs go")
	}
	root := fixture(t)
	write(t, root, "internal/other/other_test.go", "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
	edit(t, root, filesPath, "return out\n", "return out[:2]\n")
	c := checkRerun(t, []string{"go", "test", "-count=1", "-timeout", "2m", "-v", "./..."},
		[]string{"go", "test", "-count=1", "-timeout", "2m", "-v", "-run", "^TestList$", "./internal/files/"},
		"reruns the 1 failed test", "TestList")
	if c.Tests.PkgOK+c.Tests.PkgFailed != 1 {
		t.Errorf("the rerun tested %d packages, want internal/files only", c.Tests.PkgOK+c.Tests.PkgFailed)
	}

	// still three next: lines at most: with the test changed too, context
	// has two suggestions, and the rerun and seek leave room for one
	edit(t, root, testPath, "got := List(", "got := List( // changed\n\t\t")
	_, out := text(t, "go", "test", "./internal/files/")
	if next := nextOf(out); len(next) != report.MaxNext || !strings.HasPrefix(next[0], "next: crux go test -run '^TestList$' ./internal/files/ ") ||
		!strings.HasPrefix(next[1], "next: recap --show ") || !strings.HasPrefix(next[2], "next: seek "+testRef+" ") {
		t.Errorf("next: lines %q", next)
	}
}

// After a pytest run, the rerun names the parametrization that failed, as
// pytest printed it.
func TestRerunPytest(t *testing.T) {
	if _, err := exec.LookPath("pytest"); err != nil {
		t.Skip("needs pytest")
	}
	t.Setenv("PYTEST_ADDOPTS", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1") // no plugins of the machine's
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	repoFixture(t, map[string]string{
		"tests/test_x.py": "import pytest\n\n\n@pytest.mark.parametrize(\"n\", [1, 2, 3])\ndef test_p(n):\n    assert n != 2\n\n\ndef test_other():\n    pass\n",
	})
	checkRerun(t, []string{"pytest", "-p", "no:cacheprovider", "tests"},
		[]string{"pytest", "-p", "no:cacheprovider", "tests/test_x.py::test_p[2]"},
		"reruns the 1 failed test", "test_p[2]")
}

// After a cargo test run, the rerun selects the test path cargo printed:
// the module moved by #[path] has another one than its file's place.
func TestRerunCargo(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("needs cargo")
	}
	t.Setenv("CARGO_TARGET_DIR", t.TempDir())
	t.Setenv("CARGO_NET_OFFLINE", "true")
	t.Setenv("CARGO_TERM_COLOR", "never")
	repoFixture(t, map[string]string{
		"Cargo.toml":        "[package]\nname = \"fixture\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":        "#[path = \"parser/imp.rs\"]\nmod parser;\n\n#[cfg(test)]\nmod tests {\n    #[test]\n    fn other() {}\n}\n",
		"src/parser/imp.rs": "#[cfg(test)]\nmod tests {\n    #[test]\n    fn works() {\n        assert_eq!(1 + 1, 3);\n    }\n}\n",
	})
	checkRerun(t, []string{"cargo", "test", "--no-fail-fast"},
		[]string{"cargo", "test", "--no-fail-fast", "--", "--exact", "parser::tests::works"},
		"reruns the 1 failed test", "parser::tests::works")
}

// Every failure needs its test, and at most maxRerun of them.
func TestRerunNextNeedsEveryTest(t *testing.T) {
	root := fixture(t)
	rs := refs.New(root)
	rs.SetRepo(info(t, root))
	list := &digest.Item{Kind: "fail", Title: "TestList", Runner: "go test", Pkg: "internal/files"}
	argv := []string{"go", "test", "./..."}
	if got := rerunNext(argv, root, &digest.Report{Failures: []*digest.Item{list}}, rs); len(got) != 1 ||
		got[0] != (report.Next{Cmd: "crux go test -run '^TestList$' ./internal/files/", Why: "reruns the 1 failed test"}) {
		t.Errorf("rerunNext = %+v", got)
	}
	ghost := &digest.Item{Kind: "fail", Title: "TestGhost", Runner: "go test", Pkg: "internal/files"} // no definition found
	for name, fs := range map[string][]*digest.Item{
		"a failure without its test": {list, ghost},
		"too many failures":          repeat(list, maxRerun+1),
		"no failures":                nil,
	} {
		if got := rerunNext(argv, root, &digest.Report{Failures: fs}, rs); got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if got := rerunNext([]string{"make", "test"}, root, &digest.Report{Failures: []*digest.Item{list}}, rs); got != nil {
		t.Errorf("make: %+v", got)
	}
}

func repeat(it *digest.Item, n int) []*digest.Item {
	out := make([]*digest.Item, n)
	for i := range out {
		out[i] = it
	}
	return out
}

// failedTest passes each runner's printed name: pytest's as a node id with
// the parametrization, whichever way the title gave it.
func TestFailedTest(t *testing.T) {
	for _, c := range []struct {
		runner, title, test string
		want                testcmd.Failed
	}{
		{"go test", "TestList/empty", "internal/files/files_test.go:TestList",
			testcmd.Failed{Target: ref.Target{Path: "internal/files/files_test.go", Name: "TestList", Kind: "func"}, Printed: "TestList/empty"}},
		{"cargo test", "parser::tests::works", "src/parser/imp.rs:tests.works",
			testcmd.Failed{Target: ref.Target{Path: "src/parser/imp.rs", Name: "tests.works", Kind: "fn"}, Printed: "parser::tests::works"}},
		{"pytest", "test_p[2]", "tests/test_x.py:test_p",
			testcmd.Failed{Target: ref.Target{Path: "tests/test_x.py", Name: "test_p", Kind: "def"}, Printed: "tests/test_x.py::test_p[2]"}},
		{"pytest", "sub/tests/test_x.py::TestA::test_b[a-[1]]", "sub/tests/test_x.py:TestA.test_b",
			testcmd.Failed{Target: ref.Target{Path: "sub/tests/test_x.py", Name: "TestA.test_b", Kind: "def"}, Printed: "sub/tests/test_x.py::TestA::test_b[a-[1]]"}},
		{"pytest", "TestA.test_b", "tests/test_x.py:TestA.test_b",
			testcmd.Failed{Target: ref.Target{Path: "tests/test_x.py", Name: "TestA.test_b", Kind: "def"}, Printed: "tests/test_x.py::TestA::test_b"}},
	} {
		got, ok := failedTest(&digest.Item{Kind: "fail", Title: c.title, Runner: c.runner}, c.test)
		if !ok || got != c.want {
			t.Errorf("failedTest(%s %q) = %+v, %v", c.runner, c.title, got, ok)
		}
	}
	for _, c := range [][2]string{{"go test", ""}, {"jest", "a.test.js:works"}, {"go test", "internal/files/files_test.go"}} {
		if _, ok := failedTest(&digest.Item{Kind: "fail", Title: "x", Runner: c[0]}, c[1]); ok {
			t.Errorf("failedTest(%s, %q): ok", c[0], c[1])
		}
	}
}
