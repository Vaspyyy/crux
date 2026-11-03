package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Vaspyyy/crux/internal/digest"
)

func TestBudgetLimitsTheReport(t *testing.T) {
	r := &digest.Report{Structured: true, Meaningful: make([]string, 500)}
	for i := range 40 {
		r.Errors = append(r.Errors, &digest.Item{Kind: "error", Title: fmt.Sprintf("problem %d", i), Loc: fmt.Sprintf("a.c:%d:1", i),
			Body: []string{"  frame", "  ^"}, Count: 1})
	}
	out := Report(Run{Argv: []string{"make"}, Exit: 2, Duration: time.Second, Lines: make([]string, 500)}, r, Options{Budget: 30})
	if n := strings.Count(out, "\n"); n > 34 {
		t.Errorf("report has %d lines, budget 30:\n%s", n, out)
	}
	if !strings.Contains(out, "17 more errors not shown") {
		t.Errorf("no note about hidden items:\n%s", out)
	}
}

func TestShortOutputIsShownWhole(t *testing.T) {
	lines := []string{"hello", "world"}
	out := Report(Run{Argv: []string{"echo"}, Lines: lines}, digest.Digest(lines, 0), Options{Budget: 60})
	if !strings.Contains(out, "  hello\n  world\n") || !strings.HasPrefix(out, "OK  echo") {
		t.Errorf("got:\n%s", out)
	}
}

func TestNoColorsForAgents(t *testing.T) {
	r := &digest.Report{Errors: []*digest.Item{{Kind: "error", Title: "boom", Count: 1}}}
	if out := Report(Run{Argv: []string{"x"}, Exit: 1, Lines: make([]string, 50)}, r, Options{Budget: 60}); strings.Contains(out, "\x1b") {
		t.Errorf("escape codes in plain output: %q", out)
	}
}

// A tight budget must never show warnings while hiding errors, nor print a
// section title with nothing under it.
func TestTightBudgetKeepsErrorsFirst(t *testing.T) {
	r := &digest.Report{Structured: true, Meaningful: make([]string, 40)}
	for i := range 3 {
		r.Errors = append(r.Errors, &digest.Item{Kind: "error", Title: fmt.Sprintf("problem %d", i), Loc: fmt.Sprintf("main.c:%d:1", i+1),
			Body: []string{"  4 |   int y = 1", "    |       ^"}, Count: 1})
	}
	r.Warnings = []*digest.Item{{Kind: "warning", Title: "unused variable ‘a’", Norm: "unused variable …", Count: 2,
		Names: []string{"‘a’", "‘b’"}, Locs: []string{"main.c:9:1"}}}
	out := Report(Run{Argv: []string{"make"}, Exit: 2, Lines: make([]string, 40)}, r, Options{Budget: 10})
	if !strings.Contains(out, "main.c:1:1  problem 0") {
		t.Errorf("first error missing:\n%s", out)
	}
	if strings.Contains(out, "warning") && !strings.Contains(out, "problem 2") {
		t.Errorf("warnings shown while errors are hidden:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, ":") && strings.Contains(out, l+"\n\n") {
			t.Errorf("empty section %q:\n%s", l, out)
		}
	}
}

func TestFoldedWarningsListEveryName(t *testing.T) {
	r := &digest.Report{Meaningful: make([]string, 30), Structured: true, Warnings: []*digest.Item{{
		Kind: "warning", Title: "unused variable ‘unused’", Norm: "unused variable …", Count: 2,
		Names: []string{"‘unused’", "‘y’"}, Loc: "main.c:2:25", Locs: []string{"main.c:4:9"}}}}
	out := Report(Run{Argv: []string{"make"}, Lines: make([]string, 30)}, r, Options{Budget: 60})
	if !strings.Contains(out, "unused variable … ×2: ‘unused’, ‘y’") || !strings.Contains(out, "also at main.c:4:9") {
		t.Errorf("got:\n%s", out)
	}
}

func TestCommandIsPasteable(t *testing.T) {
	for argv, want := range map[string]string{
		"sh\x00-c\x00exit 7":            `sh -c 'exit 7'`,
		"echo\x00it's":                  `echo 'it'\''s'`,
		"echo\x00line1\nOK  fake":       `echo $"line1\nOK  fake"`,
		"go\x00test\x00./...\x00-run=X": `go test ./... -run=X`,
	} {
		if got := Command(strings.Split(argv, "\x00")); got != want {
			t.Errorf("Command(%q) = %s, want %s", argv, got, want)
		}
	}
}

func TestLongLinesAreClipped(t *testing.T) {
	long := strings.Repeat("x", 100000)
	lines := []string{"error: " + long}
	out := Report(Run{Argv: []string{"sh"}, Exit: 1, Lines: lines}, digest.Digest(lines, 1), Options{Budget: 60})
	if len(out) > 1000 || !strings.Contains(out, "…[+") {
		t.Errorf("report is %d bytes", len(out))
	}
}
