package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Vaspyyy/prism/focus"
	"github.com/Vaspyyy/prism/repo"
	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/work"
)

// The context tests run crux in temporary git repositories, with context
// on and an environment of their own: no git configuration, a fixed
// author, and no agent session, so records are shared per worktree.

func contextEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	for k, v := range map[string]string{
		"PRISM_NO_CONTEXT": "", "PRISM_SESSION": "", "CLAUDE_CODE_SESSION_ID": "", "T3CODE_THREAD_ID": "",
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "crux test", "GIT_AUTHOR_EMAIL": "crux@example.com",
		"GIT_COMMITTER_NAME": "crux test", "GIT_COMMITTER_EMAIL": "crux@example.com",
		"GOWORK": "off", "GOFLAGS": "",
		"XDG_STATE_HOME": t.TempDir(), // logs
	} {
		t.Setenv(k, v)
	}
}

// The v1 acceptance fixture (Prism's docs/roadmap.md): List, and TestList,
// which checks List's result. Line numbers matter to the tests below.
const (
	filesGo = `// Package files lists files.
package files

import "sort"

// List returns the names, sorted.
func List(names []string) []string {
	out := append([]string{}, names...)
	sort.Strings(out)
	return out
}
`
	filesTestGo = `package files

import (
	"reflect"
	"testing"
)

func TestList(t *testing.T) {
	got := List([]string{"b", "a", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}
`
	filesPath = "internal/files/files.go"
	testPath  = "internal/files/files_test.go"
	listRef   = filesPath + ":List"
	testRef   = testPath + ":TestList"
)

// fixture writes the acceptance module to a new git repository, commits
// it on main and changes into it. It returns the repository's root.
func fixture(t *testing.T) string {
	t.Helper()
	contextEnv(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// the toolchain running the tests, so go doesn't look for another one
	v := strings.TrimPrefix(runtime.Version(), "go")
	if !regexp.MustCompile(`^1\.\d+(\.\d+)?$`).MatchString(v) {
		v = "1.22"
	}
	write(t, root, "go.mod", "module example.com/fixture\n\ngo "+v+"\n")
	write(t, root, filesPath, filesGo)
	write(t, root, testPath, filesTestGo)
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "files")
	t.Chdir(root)
	return root
}

func write(t *testing.T, root, rel, text string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// edit replaces old with new in a file of the repository.
func edit(t *testing.T, root, rel, old, new string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || !strings.Contains(string(b), old) {
		t.Fatalf("%s: no %q (%v)", rel, old, err)
	}
	write(t, root, rel, strings.Replace(string(b), old, new, 1))
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func info(t *testing.T, root string) *repo.Info {
	t.Helper()
	in, err := repo.Detect(root)
	if err != nil || !in.Git {
		t.Fatalf("repo %s: %+v, %v", root, in, err)
	}
	return in
}

// lastRunOf reads crux's last-run record the way seek and recap do.
func lastRunOf(t *testing.T, root string) *focus.LastRun {
	t.Helper()
	return focus.Open(info(t, root)).LastRun()
}

// text runs crux with a plain report and no log, and checks that the
// report never claims coverage.
func text(t *testing.T, args ...string) (int, string) {
	t.Helper()
	code, out, _ := capture(t, append([]string{"-A", "--no-log"}, args...)...)
	noCovers(t, out)
	return code, string(out)
}

// jsonRun runs crux --json and returns the envelope and its result.
func jsonRun(t *testing.T, args ...string) (*report.Decoded, report.Crux) {
	t.Helper()
	_, out, _ := capture(t, append([]string{"--json", "--no-log"}, args...)...)
	noCovers(t, out)
	d := check(t, out)
	var c report.Crux
	if err := json.Unmarshal(d.Result, &c); err != nil {
		t.Fatalf("result: %v\n%s", err, out)
	}
	return d, c
}

// noCovers checks the evidence rule: without coverage data, nothing may
// say a test covers code.
func noCovers(t *testing.T, out []byte) {
	t.Helper()
	if strings.Contains(strings.ToLower(string(out)), "cover") {
		t.Errorf("output claims coverage:\n%s", out)
	}
}

// printed runs crux on output printed by sh, as a tool in the repository
// would have printed it.
func printed(lines ...string) []string {
	return append([]string{"sh", "-c", `printf '%s\n' "$@"; exit 1`, "sh"}, lines...)
}

// The roadmap's v1 acceptance step for crux, with the real go test: List
// broken on purpose in the working tree, so TestList fails.
func TestContextAcceptance(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs go")
	}
	root := fixture(t)
	edit(t, root, filesPath, "return out\n", "return out[:2]\n")
	cmd := []string{"go", "test", "-run", "^TestList$", "./internal/files/"}

	// The failure is in TestList, which didn't change: no mark on it. The
	// change to List, which TestList mentions, is the suggestion.
	code, on := text(t, cmd...)
	if code != 1 || !strings.HasPrefix(on, "context: 1 changed symbol vs HEAD\nFAILED  go test -run '^TestList$' ./internal/files/  (exit 1, ") ||
		!strings.Contains(on, "\n  TestList  internal/files/files_test.go:12\n    in "+testRef+"\n    List = [a b], want [a b c]\n") {
		t.Errorf("exit %d, report:\n%s", code, on)
	}
	wantNext := []string{
		"next: recap --show " + listRef + "    the uncommitted change to List, which TestList mentions",
		"next: seek " + testRef + "   the failed test's definition",
	}
	if got := nextOf(on); !reflect.DeepEqual(got, wantNext) {
		t.Errorf("next lines %q, want %q", got, wantNext)
	}
	lr := lastRunOf(t, root)
	if lr == nil || lr.By != "crux" || lr.Cmd != "go test -run '^TestList$' ./internal/files/" || lr.Exit != 1 ||
		!reflect.DeepEqual(lr.Tests, []string{testRef}) || len(lr.In) != 0 {
		t.Errorf("last run %+v", lr)
	}

	d, c := jsonRun(t, cmd...)
	f := find(c.Failures, "TestList")
	if f.Loc == nil || f.Loc.In != testRef || f.Loc.Marks != nil || f.Test != testRef {
		t.Errorf("failure %+v, loc %+v", f, f.Loc)
	}
	// nothing printed carries a mark: context only gave the suggestion
	if d.Context == nil || !reflect.DeepEqual(d.Context.Applied, []string{"suggest"}) || d.Context.Changed == nil ||
		d.Context.Changed.Uncommitted != 1 || d.Context.Focus != nil || d.Context.LastRun != nil {
		t.Errorf("context %+v", d.Context)
	}
	if f.TestMarks != nil {
		t.Errorf("test marks %q", f.TestMarks)
	}
	if len(d.Next) != 2 || d.Next[0].Cmd != "recap --show "+listRef || d.Next[1].Cmd != "seek "+testRef {
		t.Errorf("next %+v", d.Next)
	}

	// Without context: the same report minus the context: line and the
	// suggestion from context, and nothing recorded.
	if err := os.RemoveAll(filepath.Join(root, ".git", "prism")); err != nil {
		t.Fatal(err)
	}
	_, off := text(t, append([]string{"--no-context"}, cmd...)...)
	if got, want := comparable(off), comparable(withoutContext(on)); got != want {
		t.Errorf("--no-context:\n%s\nwant the report with context minus context:\n%s", got, want)
	}
	dOff, cOff := jsonRun(t, append([]string{"--no-context"}, cmd...)...)
	if dOff.Context != nil || !reflect.DeepEqual(dOff.Next, d.Next[1:]) || !reflect.DeepEqual(sameRun(cOff), sameRun(unmarked(c))) {
		t.Errorf("--json --no-context: context %+v, next %+v, result %+v", dOff.Context, dOff.Next, cOff)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "prism")); !os.IsNotExist(err) {
		t.Errorf("--no-context wrote context: %v", err)
	}

	// The test changed and fails inside its own change: marked.
	edit(t, root, filesPath, "return out[:2]\n", "return out\n")
	edit(t, root, testPath, `want := []string{"a", "b", "c"}`, `want := []string{"a", "b", "c", "d"}`)
	_, on = text(t, cmd...)
	if !strings.Contains(on, "\n    in "+testRef+" [changed]\n") ||
		!strings.Contains(on, "next: recap --show "+testRef+"   the uncommitted change to TestList, where the failure is\n") {
		t.Errorf("failure inside a changed test:\n%s", on)
	}
	d, c = jsonRun(t, cmd...)
	if f := find(c.Failures, "TestList"); f.Loc == nil || !reflect.DeepEqual(f.Loc.Marks, []string{"changed"}) || !reflect.DeepEqual(f.TestMarks, []string{"changed"}) {
		t.Errorf("failure %+v, loc %+v", f, f.Loc)
	}
	if !reflect.DeepEqual(d.Context.Applied, []string{"mark", "suggest"}) {
		t.Errorf("applied %q", d.Context.Applied)
	}

	// A pass replaces the failure: nothing fails any more.
	git(t, root, "checkout", "-q", ".")
	code, on = text(t, cmd...)
	if lr := lastRunOf(t, root); code != 0 || lr == nil || lr.Exit != 0 || len(lr.Tests) != 0 || len(lr.In) != 0 {
		t.Errorf("after a pass: exit %d, last run %+v", code, lr)
	}
	if strings.Contains(on, "context:") || strings.Contains(on, "next:") {
		t.Errorf("a clean pass has nothing to suggest:\n%s", on)
	}
}

// An error inside a symbol changed in the working tree: [changed] in the
// text and the JSON, named in the context: line, and recap suggested.
func TestContextMarksChangedError(t *testing.T) {
	root := fixture(t)
	edit(t, root, filesPath, "return out\n", "return outt\n")
	build := printed("# example.com/fixture/internal/files", "internal/files/files.go:10:9: undefined: outt")
	code, out := text(t, build...)
	want := "context: 1 changed symbol vs HEAD\nFAILED  "
	if code != 1 || !strings.HasPrefix(out, want) || !strings.Contains(out, "  internal/files/files.go:10:9  undefined: outt\n    in "+listRef+" [changed]\n") {
		t.Errorf("report:\n%s", out)
	}
	if got := nextOf(out); !reflect.DeepEqual(got, []string{"next: recap --show " + listRef + "   the uncommitted change to List, where the error is"}) {
		t.Errorf("next %q", got)
	}
	if lr := lastRunOf(t, root); lr == nil || len(lr.Tests) != 0 || !reflect.DeepEqual(lr.In, []string{listRef}) {
		t.Errorf("last run %+v", lr)
	}
	d, c := jsonRun(t, build...)
	if e := find(c.Errors, "undefined: outt"); e.Loc == nil || e.Loc.In != listRef || !reflect.DeepEqual(e.Loc.Marks, []string{"changed"}) {
		t.Errorf("error %+v", e.Loc)
	}
	if d.Context == nil || !reflect.DeepEqual(d.Context.Applied, []string{"mark", "suggest"}) || len(d.Next) != 1 {
		t.Errorf("context %+v, next %+v", d.Context, d.Next)
	}
}

// A symbol the branch changed in a commit: [branch], and recap shows the
// branch's change from where it left main.
func TestContextMarksBranch(t *testing.T) {
	root := fixture(t)
	base := git(t, root, "rev-parse", "HEAD")
	git(t, root, "switch", "-q", "-c", "feature")
	edit(t, root, filesPath, "sort.Strings(out)", "sort.Sort(sort.Reverse(sort.StringSlice(out)))")
	git(t, root, "commit", "-q", "-am", "reverse")
	_, out := text(t, printed("# example.com/fixture/internal/files", "internal/files/files.go:10:9: undefined: outt")...)
	if !strings.HasPrefix(out, "context: 1 symbol changed on this branch since "+base[:7]+"\n") || !strings.Contains(out, "    in "+listRef+" [branch]\n") ||
		!strings.Contains(out, "next: recap --show "+listRef+" "+base[:7]+"   this branch's change to List, where the error is\n") {
		t.Errorf("report:\n%s", out)
	}
}

// The focus recap last showed marks a failure in a symbol it changed.
func TestContextMarksFocus(t *testing.T) {
	root := fixture(t)
	old := git(t, root, "rev-parse", "HEAD")
	edit(t, root, testPath, `"b", "a", "c"`, `"c", "b", "a"`)
	git(t, root, "commit", "-q", "-am", "reorder")
	head := git(t, root, "rev-parse", "HEAD")
	if err := focus.Open(info(t, root)).SetFocus("recap", focus.Focus{Range: head[:7], Old: old, New: head, Symbols: []string{testRef}}); err != nil {
		t.Fatal(err)
	}
	goTest := printed("--- FAIL: TestList (0.00s)", "    files_test.go:12: List = [a b], want [a b c]", "FAIL",
		"FAIL\texample.com/fixture/internal/files\t0.002s", "FAIL")
	_, out := text(t, goTest...)
	if !strings.HasPrefix(out, "context: focus: commit "+head[:7]+" (recap, just now)\n") || !strings.Contains(out, "    in "+testRef+" [focus]\n") ||
		!strings.Contains(out, "next: recap show "+head[:7]+" --show "+testRef+"   the change to TestList in focus commit "+head[:7]+", where the failure is\n") {
		t.Errorf("report:\n%s", out)
	}
	d, c := jsonRun(t, goTest...)
	if f := find(c.Failures, "TestList"); f.Loc == nil || !reflect.DeepEqual(f.Loc.Marks, []string{"focus"}) {
		t.Errorf("failure loc %+v", f.Loc)
	}
	if d.Context == nil || d.Context.Focus == nil || d.Context.Focus.New != head || d.Context.Focus.By != "recap" {
		t.Errorf("context %+v", d.Context)
	}
}

// --no-context and PRISM_NO_CONTEXT read nothing and write nothing: an
// old record stays as it was, and nothing is marked.
func TestNoContextWritesNothing(t *testing.T) {
	root := fixture(t)
	edit(t, root, filesPath, "return out\n", "return outt\n")
	build := printed("# example.com/fixture/internal/files", "internal/files/files.go:10:9: undefined: outt")
	store := focus.Open(info(t, root))
	if err := store.SetLastRun("crux", focus.LastRun{Cmd: "make", Exit: 2, In: []string{"Makefile:all"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		env  string
		args []string
	}{
		{"", append([]string{"--no-context"}, build...)},
		{"1", build},
		{"yes", append([]string{"-v", "--no-context", "--json"}, build...)},
	} {
		t.Setenv("PRISM_NO_CONTEXT", c.env)
		_, out, _ := capture(t, append([]string{"-A", "--no-log"}, c.args...)...)
		if o := string(out); strings.Contains(o, "[changed]") || strings.Contains(o, `"marks"`) || strings.Contains(o, "context:") ||
			strings.Contains(o, `"context": {`) || strings.Contains(o, "recap") {
			t.Errorf("%q with PRISM_NO_CONTEXT=%q used context:\n%s", c.args, c.env, out)
		}
		if lr := store.LastRun(); lr == nil || lr.Cmd != "make" {
			t.Errorf("%q with PRISM_NO_CONTEXT=%q: last run %+v", c.args, c.env, lr)
		}
	}
	// after the command, --no-context is the command's
	t.Setenv("PRISM_NO_CONTEXT", "")
	_, out := text(t, "sh", "-c", `echo "$1"`, "sh", "--no-context")
	if !strings.Contains(out, "  --no-context\n") {
		t.Errorf("the command's own --no-context:\n%s", out)
	}
	if lr := store.LastRun(); lr == nil || lr.Cmd != "sh -c 'echo \"$1\"' sh --no-context" || lr.Exit != 0 {
		t.Errorf("last run %+v", lr)
	}
}

// Outside git there's nowhere to keep a record, and nothing to mark.
func TestContextOutsideGit(t *testing.T) {
	contextEnv(t)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	code, out := text(t, "sh", "-c", "echo hello")
	if code != 0 || strings.Contains(out, "context:") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	// nothing may be written anywhere in the working directory (no prism/, no .git/)
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("wrote into %s: %v %v", dir, entries, err)
	}
}

// The record names every failed test once and the other symbols failures
// are in, with refs relative to the repository of the working directory.
func TestLastRunRecord(t *testing.T) {
	contextEnv(t)
	proj := project(t, "calc") // a subdirectory: refs stay repository-relative
	file := output(t, proj, "go-test.out")
	code, _, _ := capture(t, "-A", "--no-log", "sh", "-c", `cat "$1"; exit 1`, "sh", file)
	lr := lastRunOf(t, proj)
	if code != 1 || lr == nil {
		t.Fatalf("exit %d, last run %+v", code, lr)
	}
	// TestAdd fails in its helper check; TestDiv and TestNoMessage in themselves
	want := []string{"calc/calc_test.go:TestAdd", "calc/calc_test.go:TestDiv", "calc/calc_test.go:TestNoMessage"}
	if !reflect.DeepEqual(lr.Tests, want) || !reflect.DeepEqual(lr.In, []string{"calc/calc_test.go:check"}) || lr.Exit != 1 {
		t.Errorf("tests %q, in %q, exit %d", lr.Tests, lr.In, lr.Exit)
	}
}

// nextOf returns a report's next: lines.
func nextOf(out string) []string {
	var next []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "next: ") {
			next = append(next, l)
		}
	}
	return next
}

var marksRe = regexp.MustCompile(` \[(changed|branch|focus|failing)\]`)

// withoutContext is a report minus what context adds to it: the context:
// line, marks, and the next: lines that come from context (recap's).
func withoutContext(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "context: ") || strings.HasPrefix(l, "next: recap ") {
			continue
		}
		keep = append(keep, marksRe.ReplaceAllString(l, ""))
	}
	return strings.Join(keep, "\n")
}

var timingRe = regexp.MustCompile(`\([^()]*lines? of output\)`)

// comparable leaves out what differs between two runs of one command: the
// timing, and how next: lines are lined up (by the longest command).
func comparable(out string) string {
	out = timingRe.ReplaceAllString(out, "(…)")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "next: ") {
			lines[i] = strings.Join(strings.Fields(l), " ")
		}
	}
	return strings.Join(lines, "\n")
}

// unmarked is a result without the context marks on its locations.
func unmarked(c report.Crux) report.Crux {
	for _, list := range [][]report.CruxItem{c.Errors, c.Failures, c.Warnings} {
		for i := range list {
			if l := list[i].Loc; l != nil {
				cp := *l
				cp.Marks = nil
				list[i].Loc = &cp
			}
			for j := range list[i].Locs {
				list[i].Locs[j].Marks = nil
			}
		}
	}
	return c
}

// sameRun is a result without what differs between two runs of one
// command.
func sameRun(c report.Crux) report.Crux {
	c.DurationMS, c.Log = 0, ""
	return c
}

func TestCandidatesNeedAUniqueName(t *testing.T) {
	for _, c := range []struct{ refs, want []string }{
		{[]string{"a.go:List", "b.go:Repo.Open", "c.go:File.Open"}, []string{"a.go:List"}},
		{[]string{"a.go:List", "a.go:List"}, []string{"a.go:List"}},
		// "#2" means the file has another parse: the name doesn't say which
		{[]string{"a.go:Queue.Push", "lib.cpp:parse#2"}, []string{"a.go:Queue.Push"}},
	} {
		var got []string
		for _, cand := range candidatesOf(c.refs) {
			got = append(got, cand.ref)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.refs, got, c.want)
		}
	}
}

// Context never takes room from results: with a tight budget, the report
// with context lists exactly what the one without does, and its context:
// line and suggestions come on top. With -b, the JSON lists the same too.
func TestContextNeverHides(t *testing.T) {
	root := fixture(t)
	edit(t, root, filesPath, "return out\n", "return outt\n")
	lines := []string{"# example.com/fixture/internal/files"}
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("internal/files/files.go:%d:2: problem %d", 7+i%5, i))
	}
	build := printed(lines...)
	for _, budget := range []string{"10", "30", "60"} {
		_, on := text(t, append([]string{"-b", budget}, build...)...)
		_, off := text(t, append([]string{"-b", budget, "--no-context"}, build...)...)
		if !strings.Contains(on, "[changed]") || !strings.HasPrefix(on, "context: ") || len(nextOf(on)) != 1 {
			t.Errorf("-b %s: context unused:\n%s", budget, on)
		}
		if got, want := comparable(off), comparable(withoutContext(on)); got != want {
			t.Errorf("-b %s without context:\n%s\nwant:\n%s", budget, got, want)
		}
		_, cOn := jsonRun(t, append([]string{"-b", budget}, build...)...)
		_, cOff := jsonRun(t, append([]string{"-b", budget, "--no-context"}, build...)...)
		if !reflect.DeepEqual(sameRun(cOff), sameRun(unmarked(cOn))) {
			t.Errorf("-b %s: JSON lists %d errors with context, %d without", budget, len(cOn.Errors), len(cOff.Errors))
		}
	}
}

// --raw prints the output and nothing else, and still records the run.
func TestContextRaw(t *testing.T) {
	root := fixture(t)
	edit(t, root, filesPath, "return out\n", "return outt\n")
	code, out, _ := capture(t, append([]string{"-r", "--no-log"}, printed("# example.com/fixture/internal/files", "internal/files/files.go:10:9: undefined: outt")...)...)
	if code != 1 || string(out) != "# example.com/fixture/internal/files\ninternal/files/files.go:10:9: undefined: outt\n" {
		t.Errorf("exit %d, stdout:\n%s", code, out)
	}
	if lr := lastRunOf(t, root); lr == nil || lr.Exit != 1 || !reflect.DeepEqual(lr.In, []string{listRef}) {
		t.Errorf("last run %+v", lr)
	}
}

// An error in a symbol that didn't change, but mentions symbols that did:
// recap for those, worded as a mention, with the name the code uses.
func TestContextSuggestsMentionedChanges(t *testing.T) {
	root := fixture(t)
	write(t, root, "internal/files/run.go", `package files

type Queue struct{ items []string }

func (q *Queue) Push(s string) { q.items = append(q.items, s) }

func run(q *Queue) []string {
	q.Push("x")
	return List(q.items)
}
`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "run")
	edit(t, root, filesPath, "return out\n", "return out[:1]\n")
	edit(t, root, "internal/files/run.go", "append(q.items, s)", "append(q.items, s, s)")
	_, out := text(t, printed("# example.com/fixture/internal/files", "internal/files/run.go:9:9: not enough arguments in call to List")...)
	want := []string{
		"next: recap --show " + listRef + "       the uncommitted change to List, which run mentions",
		"next: recap --show internal/files/run.go:Queue.Push   the uncommitted change to Queue.Push; run mentions Queue and Push",
	}
	if got := nextOf(out); !strings.Contains(out, "    in internal/files/run.go:run\n") || !reflect.DeepEqual(got, want) {
		t.Errorf("next %q, want %q, report:\n%s", got, want, out)
	}
	if !strings.HasPrefix(out, "context: 2 changed symbols vs HEAD\n") {
		t.Errorf("the context: line names the layer the suggestions come from:\n%s", out)
	}
}

// The context loads beside the command without git's optional index lock:
// it leaves crux's environment alone, and a stale index isn't written
// back (a command writing the index meanwhile would fail on the lock).
func TestContextLoadTakesNoLocks(t *testing.T) {
	root := fixture(t)
	t.Setenv("GIT_OPTIONAL_LOCKS", "1") // locks allowed: what a plain git status does
	old := time.Now().Add(-time.Hour)   // a stale index: the file's times changed, its content didn't
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(filesPath)), old, old); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	c := loadContext(root, work.Changes|work.Focus, false)
	if v := os.Getenv("GIT_OPTIONAL_LOCKS"); v != "1" {
		t.Errorf("while loading, GIT_OPTIONAL_LOCKS=%q", v)
	}
	_, st := c.wait()
	if v := os.Getenv("GIT_OPTIONAL_LOCKS"); v != "1" {
		t.Errorf("after loading, GIT_OPTIONAL_LOCKS=%q", v)
	}
	if st.Changes == nil || len(st.Changes.Files) != 0 || st.Store == nil {
		t.Errorf("changes %+v, store %v", st.Changes, st.Store)
	}
	if after, _ := os.ReadFile(index); !bytes.Equal(before, after) {
		t.Error("loading the context wrote the index")
	}
	// a plain git status does write it, so the check above can fail
	git(t, root, "status", "--porcelain")
	if after, _ := os.ReadFile(index); bytes.Equal(before, after) {
		t.Error("git status didn't refresh the stale index; the test checks nothing")
	}
}

// A failed test's own marks: on its test line (the assertion is in a
// helper) and on the in line of a failure without a location, and as
// test_marks in JSON.
func TestContextMarksTests(t *testing.T) {
	root := fixture(t)
	write(t, root, "internal/files/helper_test.go", `package files

import "testing"

func check(t *testing.T, ok bool) {
	if !ok {
		t.Error("not ok")
	}
}

func TestHelper(t *testing.T) {
	check(t, len(List(nil)) == 1)
}
`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "helper")
	edit(t, root, "internal/files/helper_test.go", "== 1", "== 2")
	edit(t, root, testPath, `want := []string{"a", "b", "c"}`, `want := []string{"a", "b", "c", "d"}`)
	goTest := printed("--- FAIL: TestHelper (0.00s)", "    helper_test.go:7: not ok", "--- FAIL: TestList (0.00s)", "FAIL",
		"FAIL\texample.com/fixture/internal/files\t0.002s", "FAIL")
	helperRef := "internal/files/helper_test.go:TestHelper"
	_, out := text(t, goTest...)
	if !strings.Contains(out, "\n  TestHelper  internal/files/helper_test.go:7\n    in internal/files/helper_test.go:check\n    test "+helperRef+" [changed]\n") ||
		!strings.Contains(out, "\n  TestList  internal/files/\n    in "+testRef+" [changed]\n") {
		t.Errorf("report:\n%s", out)
	}
	d, c := jsonRun(t, goTest...)
	h, l := find(c.Failures, "TestHelper"), find(c.Failures, "TestList")
	if h.Loc == nil || h.Loc.In != "internal/files/helper_test.go:check" || h.Loc.Marks != nil || h.Test != helperRef ||
		!reflect.DeepEqual(h.TestMarks, []string{"changed"}) {
		t.Errorf("TestHelper: %+v, loc %+v", h, h.Loc)
	}
	if l.Loc != nil || l.Pkg != "internal/files" || l.Test != testRef || !reflect.DeepEqual(l.TestMarks, []string{"changed"}) {
		t.Errorf("TestList: %+v", l)
	}
	if !reflect.DeepEqual(d.Context.Applied, []string{"mark", "suggest"}) {
		t.Errorf("applied %q", d.Context.Applied)
	}
	_, cOff := jsonRun(t, append([]string{"--no-context"}, goTest...)...)
	if f := find(cOff.Failures, "TestList"); f.TestMarks != nil {
		t.Errorf("--no-context test marks %q", f.TestMarks)
	}
}

// A mention is a name in the symbol's text that isn't its own: a method
// needs its type's name too, and a symbol never mentions another of its
// own name by being declared.
func TestMentioned(t *testing.T) {
	cands := candidatesOf([]string{"server.go:Server.Start", "files.go:List", "q.go:Queue.Push", "a.go:Run"})
	words := func(s string) map[string]bool {
		w := map[string]bool{}
		for _, f := range strings.Fields(s) {
			w[f] = true
		}
		return w
	}
	for _, c := range []struct {
		sym, text string
		want      []string
	}{
		{"client.go:Client.Start", "func c Client Start error", nil}, // declares Start, never names Server
		{"client.go:Client.Start", "Client Start Server", nil},       // its own name is Start
		{"x_test.go:TestList", "TestList List got want", []string{"files.go:List"}},
		{"x.go:run", "run q Push", nil}, // a Push, but whose?
		{"x.go:run", "run q Queue Push", []string{"q.go:Queue.Push"}},
		{"x.go:Queue.Len", "Queue Len Push", []string{"q.go:Queue.Push"}}, // a sibling method's call
		{"q.py:Queue", "Queue items Push", nil},                           // a class holds the names of its members
		{"a.go:Run", "Run Run", nil},                                      // itself
		{"b.go:Run", "Run List", []string{"files.go:List"}},
	} {
		var got []string
		for _, m := range mentioned(c.sym, words(c.text), cands) {
			got = append(got, m.ref)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s with %q: got %q, want %q", c.sym, c.text, got, c.want)
		}
	}
}

// The reviewer's case: a compile error in Client.Start, which shares the
// name of the changed Server.Start but never refers to it, suggests
// nothing.
func TestContextNoMentionByOwnName(t *testing.T) {
	root := fixture(t)
	write(t, root, "internal/net/net.go", `package net

type Server struct{}

func (s *Server) Start() error { return nil }

type Client struct{}

func (c *Client) Start() error {
	return dial()
}
`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "net")
	edit(t, root, "internal/net/net.go", "func (s *Server) Start() error { return nil }", "func (s *Server) Start() error { return start() }")
	_, out := text(t, printed("# example.com/fixture/internal/net", "internal/net/net.go:10:9: undefined: dial")...)
	if !strings.Contains(out, "    in internal/net/net.go:Client.Start\n") || len(nextOf(out)) != 0 || strings.Contains(out, "context:") {
		t.Errorf("report:\n%s", out)
	}
}

// The context: line names only layers whose marks the report prints: a
// mark on in or test lines the budget leaves out doesn't count.
func TestContextLineNamesOnlyPrintedMarks(t *testing.T) {
	root := fixture(t)
	write(t, root, "internal/files/run.go", "package files\n\nfunc Other() int {\n\treturn 1\n}\n")
	write(t, root, "internal/check/check_test.go", `package check

import "testing"

func check(t *testing.T, ok bool) {
	if !ok {
		t.Error("not ok")
	}
}

func TestF(t *testing.T) {
	check(t, false)
}
`)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "more")
	head := git(t, root, "rev-parse", "HEAD")
	if err := focus.Open(info(t, root)).SetFocus("recap", focus.Focus{Range: head[:7], Old: head, New: head, Symbols: []string{"internal/check/check_test.go:TestF"}}); err != nil {
		t.Fatal(err)
	}
	edit(t, root, filesPath, "return out\n", "return outt\n")
	edit(t, root, "internal/files/run.go", "return 1", "return one")
	goTest := printed("# example.com/fixture/internal/files", "internal/files/files.go:10:9: undefined: outt",
		"internal/files/run.go:4:9: undefined: one", "--- FAIL: TestF (0.00s)", "    check_test.go:7: not ok", "FAIL",
		"FAIL\texample.com/fixture/internal/check\t0.002s", "FAIL\texample.com/fixture/internal/files [build failed]", "FAIL")
	// tight budgets plan the failure's lines but drop them; roomy ones print them
	seen := map[bool]bool{}
	for budget := 10; budget <= 30; budget++ {
		_, out := text(t, append([]string{"-b", fmt.Sprint(budget)}, goTest...)...)
		printedFocus := strings.Contains(out, "[focus]")
		named := strings.Contains(strings.SplitN(out, "\n", 2)[0], "focus:")
		if printedFocus != named || !strings.HasPrefix(out, "context: 2 changed symbols vs HEAD") {
			t.Errorf("-b %d: [focus] printed %v, named %v:\n%s", budget, printedFocus, named, out)
		}
		seen[printedFocus] = true
	}
	if !seen[true] || !seen[false] {
		t.Errorf("the budgets should both show and leave out the failure's lines: %v", seen)
	}
}

// Once the report is out, the suggestions and the record only parse a
// little more source, so many errors in big files don't delay the exit:
// the record names what was affordable, failed tests first.
func TestRecordParsesLittleAfterTheReport(t *testing.T) {
	root := fixture(t)
	var body strings.Builder
	for j := range 400 { // about 20 KB a file
		fmt.Fprintf(&body, "func F%d(x int) int {\n\treturn x + %d\n}\n\n", j, j)
	}
	lines := []string{"# example.com/fixture/internal/big"}
	for i := range 40 {
		write(t, root, fmt.Sprintf("internal/big/f%02d.go", i), "package big\n\n"+body.String())
		lines = append(lines, fmt.Sprintf("internal/big/f%02d.go:4:9: undefined: z%d", i, i))
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "big")
	lines = append(lines, "--- FAIL: TestList (0.00s)", "    files_test.go:12: List = [a b], want [a b c]", "FAIL",
		"FAIL\texample.com/fixture/internal/files\t0.002s", "FAIL\texample.com/fixture/internal/big [build failed]", "FAIL")
	text(t, printed(lines...)...)
	lr := lastRunOf(t, root)
	if lr == nil || !slices.Contains(lr.Tests, testRef) || !slices.Contains(lr.In, "internal/big/f00.go:F0") {
		t.Fatalf("last run %+v", lr)
	}
	// 40 files of 20 KB: the report's own and about six more fit, never all
	if n := len(lr.In); n >= 20 {
		t.Errorf("the record parsed %d files after the report", n)
	}
}

// A run that was killed by a signal or interrupted may have stopped early:
// its record would drop failures it never got to, so the old one stays.
func TestNoRecordForSignalsAndInterrupts(t *testing.T) {
	root := fixture(t)
	store := focus.Open(info(t, root))
	if err := store.SetLastRun("crux", focus.LastRun{Cmd: "go test ./...", Exit: 1, Tests: []string{testRef}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := text(t, "sh", "-c", "kill -TERM $$"); code != 143 {
		t.Errorf("exit %d", code)
	}
	if lr := store.LastRun(); lr == nil || lr.Cmd != "go test ./..." {
		t.Errorf("after a signal: %+v", lr)
	}
	// Ctrl+C reaches crux, which passes it on; the command catches it and
	// exits normally (as pytest does), but the run was still cut short.
	// crux runs in this process, so the command interrupts its parent.
	code, _ := text(t, "sh", "-c", `trap 'exit 2' INT; sleep 0.3; kill -INT $PPID; sleep 5`)
	if lr := store.LastRun(); code != 2 || lr == nil || lr.Cmd != "go test ./..." {
		t.Errorf("after an interrupt: exit %d, %+v", code, lr)
	}
}
