package digest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		out = append(out, Clean(l))
	}
	return out
}

func dump(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tools=%v tests=%+v lines=%d\n", r.Tools, r.Tests, r.Lines)
	for _, group := range []struct {
		name  string
		items []*Item
	}{{"ERR", r.Errors}, {"FAIL", r.Failures}, {"WARN", r.Warnings}} {
		for _, it := range group.items {
			fmt.Fprintf(&b, "%s %s @ %s ×%d %v\n", group.name, it.Title, it.Loc, it.Count, it.Locs)
			for _, l := range it.Body {
				fmt.Fprintf(&b, "    | %s\n", l)
			}
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "NOTE %s\n", n)
	}
	return b.String()
}

func TestDump(t *testing.T) {
	if os.Getenv("DUMP") == "" {
		t.Skip("set DUMP=1 to print every fixture's digest")
	}
	files, _ := filepath.Glob("testdata/*.out")
	for _, f := range files {
		name := filepath.Base(f)
		fmt.Printf("===== %s\n%s\n", name, dump(Digest(load(t, name), 1)))
	}
}

type want struct {
	errors, failures, warnings int
	passed, failed             int
	contains                   []string // must appear in the dump
	absent                     []string // must not
}

func TestFixtures(t *testing.T) {
	cases := map[string]want{
		"rust-build.out": {errors: 2, warnings: 1, contains: []string{
			"E0425: cannot find value `z` in this scope @ src/main.rs:6:20",
			"E0308: mismatched types @ src/main.rs:4:18", "expected `i32`, found `&str`"}},
		"rust-test.out": {failures: 3, passed: 4, failed: 3, contains: []string{
			"tests::adds_wrong @ src/lib.rs:7:31", "left: 4", "index out of bounds"}},
		"rust-big.out": {failures: 2, warnings: 1, passed: 400, failed: 2, contains: []string{
			"tests::sq_overflow @ src/lib.rs:406:32", "3 squared"}, absent: []string{"sq_17"}},
		"rust-test-ok.out": {passed: 1},
		"go-test.out": {errors: 1, failures: 2, contains: []string{
			"undefined: undefinedThing @ strs/strs.go:3:38", "TestAdd @ calc_test.go:7",
			"TestDiv @ /work/go-test/calc/calc_test.go:10", "panicked at /work/go-test/calc/calc.go:4", "integer divide by zero"},
			absent: []string{"testing.tRunner"}},
		"go-big.out": {failures: 1, passed: 250, contains: []string{
			"TestBroken @ big_test.go:255", "expected port 8080"}, absent: []string{"checking case"}},
		"pytest.out": {failures: 3, passed: 1, failed: 3, contains: []string{
			"test_add_wrong @ test_calc.py:7", "E       assert 3 == 4", "raised at calc.py:5"},
			absent: []string{"platform linux", "plugins:"}},
		"tsc.out":        {errors: 3, contains: []string{"TS2322: Type 'string' is not assignable to type 'number'. @ a.ts:1:7"}},
		"tsc-pretty.out": {errors: 3, contains: []string{"TS2741", "@ a.ts:5:7"}},
		"make-gcc.out": {errors: 3, warnings: 1, contains: []string{
			"‘z’ undeclared (first use in this function) @ main.c:5:20", "×2 [main.c:4:9]",
			"note: expected ‘int’"}},
		"javac.out":    {errors: 2, contains: []string{"cannot find symbol @ Main.java:4", "symbol:   method undefined()"}},
		"py-crash.out": {errors: 1, contains: []string{"KeyError: 'b' @ /work/crash.py:4", "in g: return"}},
		"node-crash.out": {errors: 1, contains: []string{
			"TypeError: Cannot read properties of null (reading 'x') @ /work/crash.js:2:20", "function b(){ null.x }"},
			absent: []string{"node:internal", "Node.js v"}},
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			r := Digest(load(t, name), 1)
			d := dump(r)
			if len(r.Errors) != w.errors || len(r.Failures) != w.failures || len(r.Warnings) != w.warnings {
				t.Errorf("errors/failures/warnings = %d/%d/%d, want %d/%d/%d\n%s",
					len(r.Errors), len(r.Failures), len(r.Warnings), w.errors, w.failures, w.warnings, d)
			}
			if r.Tests.Passed != w.passed || r.Tests.Failed != w.failed {
				t.Errorf("passed/failed = %d/%d, want %d/%d", r.Tests.Passed, r.Tests.Failed, w.passed, w.failed)
			}
			for _, c := range w.contains {
				if !strings.Contains(d, c) {
					t.Errorf("missing %q in\n%s", c, d)
				}
			}
			for _, a := range w.absent {
				if strings.Contains(d, a) {
					t.Errorf("should not contain %q:\n%s", a, d)
				}
			}
		})
	}
}

func TestCleanResolvesCarriageReturns(t *testing.T) {
	if got := Clean("\x1b[32mDownloading 10%\rDownloading 100%\x1b[0m\r\n"); got != "Downloading 100%" {
		t.Errorf("got %q", got)
	}
}

// Regressions found by the first round of model reviews.
func TestReviewRegressions(t *testing.T) {
	cases := map[string]want{
		// 12 errors with one message: one item ×12 that keeps all 12 locations
		"rustc-many.out": {errors: 1, contains: []string{"@ many.rs:2:19 ×12", "many.rs:3:19", "many.rs:13:20"}},
		// pytest -q has no === border around its summary
		"pytest-q.out": {failures: 1, errors: 1, warnings: 1, passed: 3, failed: 1,
			contains: []string{"fixture exploded", "DeprecationWarning: old api"}},
		"pytest-mixed.out": {failures: 1, errors: 1, warnings: 1, passed: 3, failed: 1},
		// a collection error is an error, not a failed test
		"pytest-collect.out": {errors: 1, contains: []string{"SyntaxError", "test_bad.py:1"}},
		// go -v subtests: the message is printed before the --- FAIL line
		"go-subtests.out": {failures: 1, contains: []string{"TestSame/sub_one @ x_test.go:6", "fail in a"},
			absent: []string{"FAIL TestSame @"}},
		// the timeout panic names the test that hung, not the previous failure
		"go-timeout.out": {errors: 1, failures: 1, contains: []string{
			"TestFails @ x_test.go:5", "test timed out after 1s while running TestSlow"},
			absent: []string{"TestFails @ x_test.go:5 ×0 []\n    | plain failure\n    | panic"}},
		// "too many errors" is the compiler giving up, not an eleventh error
		"go-many.out":     {errors: 10, contains: []string{"NOTE go: too many errors"}},
		"node-throw.out":  {errors: 1, contains: []string{"Error: boom @ [eval]:1:7"}},
		"node-syntax.out": {errors: 1, contains: []string{"SyntaxError: Unexpected number @ /work/bad.js:2"}},
		// a prompt without a newline must not hide the traceback
		"py-eof.out": {errors: 1, contains: []string{"EOFError: EOF when reading a line"}},
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			r := Digest(load(t, name), 1)
			d := dump(r)
			if len(r.Errors) != w.errors || len(r.Failures) != w.failures || len(r.Warnings) != w.warnings {
				t.Errorf("errors/failures/warnings = %d/%d/%d, want %d/%d/%d\n%s",
					len(r.Errors), len(r.Failures), len(r.Warnings), w.errors, w.failures, w.warnings, d)
			}
			if w.passed+w.failed > 0 && (r.Tests.Passed != w.passed || r.Tests.Failed != w.failed) {
				t.Errorf("passed/failed = %d/%d, want %d/%d\n%s", r.Tests.Passed, r.Tests.Failed, w.passed, w.failed, d)
			}
			for _, c := range w.contains {
				if !strings.Contains(d, c) {
					t.Errorf("missing %q in\n%s", c, d)
				}
			}
			for _, a := range w.absent {
				if strings.Contains(d, a) {
					t.Errorf("should not contain %q:\n%s", a, d)
				}
			}
		})
	}
}

func TestWarningsKeepTheirNames(t *testing.T) {
	r := Digest(load(t, "make-gcc.out"), 1)
	if len(r.Warnings) != 1 || r.Warnings[0].Count != 2 {
		t.Fatalf("want one folded warning ×2, got %s", dump(r))
	}
	if got := strings.Join(r.Warnings[0].Names, " "); got != "‘unused’ ‘y’" {
		t.Errorf("names = %q", got)
	}
}

func TestPlainFailLineIsNotGo(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprint(i))
	}
	lines = append(lines, "FAIL x")
	r := Digest(lines, 1)
	if r.HasTests || r.Tail[len(r.Tail)-1] != "FAIL x" {
		t.Errorf("a plain FAIL line was taken for go test output: %s / tail %q", dump(r), r.Tail)
	}
}

func TestGenericErrorsAreNotCapped(t *testing.T) {
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("error: e%d at f.c:%d", i, i))
	}
	if r := Digest(lines, 1); len(r.Errors) != 12 {
		t.Errorf("got %d errors, want 12", len(r.Errors))
	}
}

// Regressions found by the second round of model reviews.
func TestSecondRoundRegressions(t *testing.T) {
	t.Run("two different errors are never folded", func(t *testing.T) {
		r := Digest([]string{"a.c:1:1: error: x", "a.c:2:1: error: x", "a.c:3:1: error: y"}, 1)
		if len(r.Errors) != 3 {
			t.Errorf("got %s", dump(r))
		}
	})
	t.Run("folded warning text has no placeholder", func(t *testing.T) {
		if got := withoutNames("unused variable ‘unused’ [-Wunused-variable]"); got != "unused variable [-Wunused-variable]" {
			t.Errorf("got %q", got)
		}
		if got := withoutNames("unused variable: `x`"); got != "unused variable" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("natural order", func(t *testing.T) {
		if !naturalLess("tests::case_2", "tests::case_10") || naturalLess("tests::case_10", "tests::case_2") {
			t.Error("case_2 should sort before case_10")
		}
	})
	t.Run("capped bodies say how much is cut", func(t *testing.T) {
		got := capLines([]string{"a", "b", "c", "d"}, 2)
		if got[len(got)-1] != "… +2 more lines" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("missing files are marked", func(t *testing.T) {
		CheckFiles, Relative = true, t.TempDir()
		defer func() { CheckFiles, Relative = false, "" }()
		os.WriteFile(filepath.Join(Relative, "here.py"), nil, 0o644)
		r := Digest([]string{"here.py:3:1: error: a", "gone.py:4:1: error: b", "[eval]:1:7: error: c"}, 1)
		var got []bool
		for _, e := range r.Errors {
			got = append(got, e.Missing)
		}
		if fmt.Sprint(got) != "[false true false]" {
			t.Errorf("missing = %v", got)
		}
	})
	t.Run("pytest xpassed is not passed", func(t *testing.T) {
		r := Digest([]string{"==== 2 passed, 1 xpassed in 0.01s ===="}, 0)
		if r.Tests.Passed != 2 || r.Tests.XPassed != 1 {
			t.Errorf("tests = %+v", r.Tests)
		}
	})
	t.Run("pytest interrupted", func(t *testing.T) {
		r := Digest([]string{"!!!!!!!!!!!!!!!!!!!! Interrupted: 1 error during collection !!!!!!!!!!!!!!!!!!!!"}, 2)
		if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "no tests ran") {
			t.Errorf("notes = %q", r.Notes)
		}
	})
	t.Run("cargo stopped early", func(t *testing.T) {
		r := Digest([]string{"error: test failed, to rerun pass `--lib`"}, 101)
		if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "--no-fail-fast") {
			t.Errorf("notes = %q", r.Notes)
		}
	})
	t.Run("go packages without tests", func(t *testing.T) {
		r := Digest([]string{"?   \texample.com/x/cmd\t[no test files]", "ok  \texample.com/x/lib\t0.01s"}, 0)
		if r.Tests.PkgNoTests != 1 || r.Tests.PkgOK != 1 {
			t.Errorf("tests = %+v", r.Tests)
		}
	})
}

// Regressions found by the final review round.
func TestFinalRoundRegressions(t *testing.T) {
	cases := map[string]struct {
		w     want
		notes []string
	}{
		// Ctrl+C is an interruption with a location, not an error called "(to show a full traceback …)"
		"pytest-kbint.out": {want{passed: 1, absent: []string{"full traceback"}},
			[]string{"interrupted (KeyboardInterrupt at /work/pk/test_kb.py:2)"}},
		"pytest-x.out": {want{failures: 1, failed: 1}, []string{"stopped after 1 failure (-x or --maxfail)"}},
		"pytest-tbline.out": {want{failures: 2, passed: 1, failed: 2,
			contains: []string{"test_x.py::test_1 @ /work/pk/test_x.py:1", "test_x.py::test_2 @ /work/pk/test_x.py:2"}}, nil},
		// a panic in a goroutine is the failure of the test that started it, and ends the package
		"go-goroutine.out": {want{failures: 1, contains: []string{"TestB @ /work/gor/p_test.go:10", "assignment to entry in nil map"}},
			[]string{"TestB panicked, so the tests after it"}},
		"go-norun.out": {want{}, []string{"no test matched"}},
		// one message, different details: the bool mismatch stays separate
		"rust-fold.out": {want{errors: 2, contains: []string{"@ src/main.rs:2:18 ×3", "expected `bool`, found `&str`"}}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := Digest(load(t, name), 1)
			d := dump(r)
			if len(r.Errors) != c.w.errors || len(r.Failures) != c.w.failures {
				t.Errorf("errors/failures = %d/%d, want %d/%d\n%s", len(r.Errors), len(r.Failures), c.w.errors, c.w.failures, d)
			}
			if r.Tests.Passed != c.w.passed || r.Tests.Failed != c.w.failed {
				t.Errorf("passed/failed = %d/%d, want %d/%d\n%s", r.Tests.Passed, r.Tests.Failed, c.w.passed, c.w.failed, d)
			}
			for _, s := range c.w.contains {
				if !strings.Contains(d, s) {
					t.Errorf("missing %q in\n%s", s, d)
				}
			}
			for _, s := range c.w.absent {
				if strings.Contains(d, s) {
					t.Errorf("should not contain %q:\n%s", s, d)
				}
			}
			for _, n := range c.notes {
				if !strings.Contains(d, n) {
					t.Errorf("missing note %q in\n%s", n, d)
				}
			}
		})
	}
	t.Run("go test without -v reports no per-test counts", func(t *testing.T) {
		for name, want := range map[string]bool{"go-test.out": true, "go-subtests.out": false, "go-norun.out": false} {
			if r := Digest(load(t, name), 1); r.Tests.Unknown != want {
				t.Errorf("%s: unknown = %v, want %v", name, r.Tests.Unknown, want)
			}
		}
	})
	t.Run("zero tests", func(t *testing.T) {
		if r := Digest(load(t, "pytest-none.out"), 5); !r.HasTests || !r.Tests.NoneRan {
			t.Errorf("tests = %+v", r.Tests)
		}
	})
	t.Run("paths relative to a parent are resolved", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "src"), 0o755)
		os.WriteFile(filepath.Join(root, "src", "main.rs"), nil, 0o644)
		CheckFiles, Relative = true, filepath.Join(root, "src")
		defer func() { CheckFiles, Relative = false, "" }()
		r := Digest([]string{"src/main.rs:3:1: error: a"}, 1)
		if e := r.Errors[0]; e.Loc != "main.rs:3:1" || e.Missing {
			t.Errorf("loc %q missing=%v", e.Loc, e.Missing)
		}
	})
}

// Failed tests say which runner reported them, and go test failures their
// package's directory, so their definitions can be found.
func TestFailuresNameTheirRunner(t *testing.T) {
	GoModule, GoModuleDir = "example.com/demo", "."
	defer func() { GoModule, GoModuleDir = "", "" }()
	for name, want := range map[string]string{
		"go-test.out":     "TestAdd go test calc, TestDiv go test calc",
		"go-subtests.out": "TestSame/sub_one go test ",
		"pytest.out":      "test_add_wrong pytest , test_load_missing pytest , test_dict pytest ",
		"rust-test.out":   "tests::adds_wrong cargo test , tests::panics cargo test , tests::parses_empty cargo test ",
	} {
		var got []string
		for _, f := range Digest(load(t, name), 1).Failures {
			got = append(got, f.Title+" "+f.Runner+" "+f.Pkg)
		}
		if strings.Join(got, ", ") != want {
			t.Errorf("%s: got %q, want %q", name, strings.Join(got, ", "), want)
		}
	}
}
