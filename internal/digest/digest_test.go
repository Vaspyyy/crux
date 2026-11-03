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
	for _, l := range r.Tail {
		fmt.Fprintf(&b, "TAIL %s\n", l)
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
			"test_add_wrong @ test_calc.py:7", "E assert 3 == 4", "raised at calc.py:5"},
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
