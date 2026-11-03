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
			"TestDiv @ /work/go-test/calc/calc.go:4", "integer divide by zero"},
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
			"NOTE make: *** [Makefile:2: all] Error 1", "note: expected ‘int’"}},
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
		// 12 errors with one message on 12 lines are 12 errors, not "1 error ×12"
		"rustc-many.out": {errors: 12, contains: []string{"@ many.rs:2:", "@ many.rs:13:"}},
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
