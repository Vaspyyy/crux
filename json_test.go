package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/report"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
	"github.com/Vaspyyy/crux/internal/render"
)

// The JSON tests run crux on the refs fixtures (real output of go, g++ and
// cargo), printed by sh in a copy of the project they came from. The copy
// is a git repository of its own; "outside" lies beside it.

// testdata is absolute: the tests change directory.
var testdata, _ = filepath.Abs(filepath.Join("internal", "refs", "testdata"))

// project copies the fixtures' project to a temporary git repository and
// changes into dir (relative to it). It returns the repository's root.
func project(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"proj", "outside"} {
		if err := os.CopyFS(filepath.Join(tmp, d), os.DirFS(filepath.Join(testdata, d))); err != nil {
			t.Fatal(err)
		}
	}
	proj := filepath.Join(tmp, "proj")
	if out, err := exec.Command("git", "-C", proj, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(filepath.Join(proj, filepath.FromSlash(dir)))
	t.Setenv("XDG_STATE_HOME", t.TempDir()) // logs
	return proj
}

// output writes a fixture with its /work/proj paths pointing at proj, then
// extra lines, beside the project, and returns its path.
func output(t *testing.T, proj, name string, extra ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, name))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(b), "/work/proj", proj) + strings.Join(append(extra, ""), "\n")
	p := filepath.Join(proj, "..", name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// capture runs crux and returns its exit code, stdout and stderr.
func capture(t *testing.T, args ...string) (int, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, errOut
	code := run(args)
	os.Stdout, os.Stderr = stdout, stderr
	out.Close()
	errOut.Close()
	o, _ := os.ReadFile(out.Name())
	e, _ := os.ReadFile(errOut.Name())
	return code, o, string(e)
}

// runJSON runs crux --json on a fixture, printed by sh with the given exit
// code, and checks that stdout is one envelope whose refs all parse.
func runJSON(t *testing.T, flags []string, file string, exit int) (int, *report.Decoded, report.Crux) {
	t.Helper()
	args := append(append([]string{"--json"}, flags...), "sh", "-c", `cat "$1"; exit "$2"`, "sh", file, fmt.Sprint(exit))
	code, out, stderr := capture(t, args...)
	d := check(t, out)
	if stderr != "" {
		t.Errorf("stderr: %q", stderr)
	}
	if !reflect.DeepEqual(d.Args, args) {
		t.Errorf("args = %q, want %q", d.Args, args)
	}
	var c report.Crux
	if err := json.Unmarshal(d.Result, &c); err != nil {
		t.Fatalf("result: %v\n%s", err, out)
	}
	return code, d, c
}

// check validates an envelope and parses every ref in it.
func check(t *testing.T, out []byte) *report.Decoded {
	t.Helper()
	d, err := report.Check(out)
	if err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	if d.Tool != "crux" || d.Version != version {
		t.Errorf("tool %q %q", d.Tool, d.Version)
	}
	rs, err := report.Refs(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rs {
		if r := ref.Parse(s, nil); r.Name == "" || r.String() != s {
			t.Errorf("ref %q parses as %+v", s, r)
		}
	}
	return d
}

func find(items []report.CruxItem, title string) report.CruxItem {
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	return report.CruxItem{Title: "(no item " + title + ")"}
}

func loc(l *report.Location) string {
	if l == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s:%d:%d in %q", l.Path, l.Line, l.Col, l.In)
}

func TestJSONGoTest(t *testing.T) {
	proj := project(t, ".")
	code, d, c := runJSON(t, nil, output(t, proj, "go-test.out"), 1)
	if code != 1 || c.Exit != 1 {
		t.Errorf("exit %d, result %d", code, c.Exit)
	}
	if d.Repo.Root != proj || !d.Repo.Git || d.Truncated != nil {
		t.Errorf("repo %+v, truncated %v", d.Repo, d.Truncated)
	}
	if c.Tests == nil || c.Tests.Failed != 3 || c.Tests.PkgFailed != 1 || len(c.Failures) != 3 {
		t.Errorf("tests %+v, %d failures", c.Tests, len(c.Failures))
	}
	if !reflect.DeepEqual(c.Tools, []string{"go test"}) || c.Command[0] != "sh" || len(c.Tail) != 0 {
		t.Errorf("tools %q, command %q, tail %q", c.Tools, c.Command, c.Tail)
	}
	// the assertion is in a helper: in names it, test the test
	add := find(c.Failures, "TestAdd")
	if loc(add.Loc) != `calc/calc_test.go:7:0 in "calc/calc_test.go:check"` || add.Test != "calc/calc_test.go:TestAdd" ||
		!reflect.DeepEqual(add.Body, []string{"got 2, want 3"}) {
		t.Errorf("TestAdd: %s, test %q, body %q", loc(add.Loc), add.Test, add.Body)
	}
	// no line, only the package directory: no loc, and test names it
	none := find(c.Failures, "TestNoMessage")
	if none.Loc != nil || none.Test != "calc/calc_test.go:TestNoMessage" {
		t.Errorf("TestNoMessage: %s, test %q", loc(none.Loc), none.Test)
	}
	if !strings.HasPrefix(c.Log, os.Getenv("XDG_STATE_HOME")) {
		t.Errorf("log %q", c.Log)
	} else if b, err := os.ReadFile(c.Log); err != nil || !strings.Contains(string(b), "--- FAIL: TestAdd") {
		t.Errorf("log %q: %v", c.Log, err)
	}
}

func TestJSONLocations(t *testing.T) {
	proj := project(t, ".")
	file := output(t, proj, "gcc.out",
		"gone/stale.c:3:1: error: a file that isn't there",
		// three of one message fold into the first, with the others' locations
		"lib.cpp:4:5: error: one cause", "lib.cpp:8:5: error: one cause", "../outside/far.h:2:1: error: one cause",
		"lib.cpp:4:9: warning: unused variable ‘a’ [-Wunused-variable]", "lib.cpp:8:9: warning: unused variable ‘b’ [-Wunused-variable]")
	code, _, c := runJSON(t, []string{"--no-log"}, file, 1)
	if code != 1 || c.Log != "" || c.Tests != nil || len(c.Failures) != 0 {
		t.Errorf("exit %d, log %q, tests %+v, failures %d", code, c.Log, c.Tests, len(c.Failures))
	}
	far := filepath.Join(filepath.Dir(proj), "outside", "far.h")
	for title, want := range map[string]string{
		"‘undefined_count’ was not declared in this scope": `lib.cpp:8:12 in "lib.cpp:parse#2"`,
		"‘missing’ was not declared in this scope":         `lib.cpp:11:24 in ""`, // global scope
		"‘unknown_thing’ was not declared in this scope":   far + `:2:12 in ""`,   // outside the repository: absolute
		"a file that isn't there":                          `gone/stale.c:3:1 in ""`,
	} {
		if got := loc(find(c.Errors, title).Loc); got != want {
			t.Errorf("%s: %s, want %s", title, got, want)
		}
	}
	folded := find(c.Errors, "one cause")
	var locs []string
	for _, l := range folded.Locs {
		locs = append(locs, loc(&l))
	}
	if want := []string{`lib.cpp:8:5 in "lib.cpp:parse#2"`, far + `:2:1 in ""`}; folded.Count != 3 ||
		loc(folded.Loc) != `lib.cpp:4:5 in "lib.cpp:parse#1"` || !reflect.DeepEqual(locs, want) {
		t.Errorf("folded: count %d, %s, locs %q", folded.Count, loc(folded.Loc), locs)
	}
	if len(c.Warnings) != 1 || c.Warnings[0].Count != 2 || c.Warnings[0].Title != "unused variable [-Wunused-variable]" ||
		c.Warnings[0].Loc.In != "" || len(c.Warnings[0].Locs) != 1 {
		t.Errorf("warnings: %+v", c.Warnings)
	}
}

// Paths and refs are relative to the repository, also from a subdirectory
// (the text's locations are relative to the working directory).
func TestJSONSubdirectory(t *testing.T) {
	proj := project(t, "shapes")
	_, _, c := runJSON(t, nil, output(t, proj, "cargo-test.out"), 101)
	neg := find(c.Failures, "shape::tests::negative")
	if loc(neg.Loc) != `shapes/src/shape.rs:8:13 in "shapes/src/shape.rs:Circle.area"` || neg.Test != "shapes/src/shape.rs:tests.negative" {
		t.Errorf("%s, test %q", loc(neg.Loc), neg.Test)
	}
	if c.Exit != 101 || c.Tests == nil || c.Tests.Failed != 3 {
		t.Errorf("exit %d, tests %+v", c.Exit, c.Tests)
	}
}

// --json lists everything; -b limits it to what the text report lists.
func TestJSONBudget(t *testing.T) {
	proj := project(t, ".")
	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("lib.cpp:%d:1: error: problem %d", i%11+1, i))
	}
	file := filepath.Join(proj, "..", "many.out")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, d, c := runJSON(t, []string{"--no-log"}, file, 1)
	if len(c.Errors) != 40 || d.Truncated != nil {
		t.Errorf("without -b: %d errors, truncated %+v", len(c.Errors), d.Truncated)
	}
	_, d, c = runJSON(t, []string{"--no-log", "-b", "10"}, file, 1)
	if len(c.Errors) != 30 || d.Truncated == nil || *d.Truncated != (report.Truncated{Shown: 30, Total: 40}) {
		t.Errorf("with -b 10: %d errors, truncated %+v", len(c.Errors), d.Truncated)
	}
}

func TestJSONFailures(t *testing.T) {
	project(t, ".")
	for _, c := range []struct {
		args []string
		exit int
		code string
	}{
		{[]string{"--json", "crux-no-such-command"}, 127, report.CodeFailed},
		{[]string{"--json", "./calc"}, 126, report.CodeFailed}, // a directory
		{[]string{"--json", "-r", "true"}, 2, report.CodeUsage},
		{[]string{"-x", "--json", "true"}, 2, report.CodeUsage},
		{[]string{"-b", "1", "--json", "true"}, 2, report.CodeUsage},
		{[]string{"--json"}, 2, report.CodeUsage},
	} {
		code, out, stderr := capture(t, c.args...)
		d := check(t, out)
		if code != c.exit || d.Error == nil || d.Error.Code != c.code || stderr != "" {
			t.Errorf("%q: exit %d, error %+v, stderr %q", c.args, code, d.Error, stderr)
		}
	}
}

// -v streams the output to stderr, so stdout stays one JSON document.
func TestJSONVerbose(t *testing.T) {
	project(t, ".")
	code, out, stderr := capture(t, "--json", "-v", "--no-log", "sh", "-c", "echo hello")
	d := check(t, out)
	var c report.Crux
	if err := json.Unmarshal(d.Result, &c); err != nil {
		t.Fatal(err)
	}
	// a successful command that isn't a test run: its output may be the point
	if code != 0 || stderr != "hello\n" || !reflect.DeepEqual(c.Tail, []string{"hello"}) || c.Lines != 1 {
		t.Errorf("exit %d, stderr %q, result %+v", code, stderr, c)
	}
}

func TestJSONCapsLists(t *testing.T) {
	r := &digest.Report{Structured: true}
	for range report.MaxItems + 5 {
		r.Warnings = append(r.Warnings, &digest.Item{Kind: "warning", Title: "w", Count: 1})
	}
	c, tr := result(render.Run{Argv: []string{"make"}}, r, refs.New(t.TempDir()), opts{})
	if len(c.Warnings) != report.MaxItems || tr == nil || *tr != (report.Truncated{Shown: report.MaxItems, Total: report.MaxItems + 5}) {
		t.Errorf("%d warnings, truncated %+v", len(c.Warnings), tr)
	}
	if c.Errors == nil || c.Failures == nil || c.Tools == nil {
		t.Errorf("empty lists must be [], not null: %+v", c)
	}
}
