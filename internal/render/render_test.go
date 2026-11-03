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
	if n := strings.Count(out, "\n"); n > 40 { // 30 errors always get a line
		t.Errorf("report has %d lines, budget 30:\n%s", n, out)
	}
	if !strings.Contains(out, "10 more errors not shown") || !strings.Contains(out, "problem 0\n      frame") {
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
		"echo\x00line1\nOK  fake":       `echo line1…`,
		"printf\x00a\tb\x00it's\x1b":    `printf $'a\tb' $'it\'s\x1b'`,
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

func TestSecondRoundRendering(t *testing.T) {
	run := func(lines []string, exit int, o Options) string {
		return Report(Run{Argv: []string{"x"}, Exit: exit, Lines: lines, LogPath: "/l.log"}, digest.Digest(lines, exit), o)
	}
	t.Run("short passing test runs get a tests line", func(t *testing.T) {
		out := run([]string{"==== 3 passed in 0.01s ===="}, 0, Options{Budget: 60})
		if !strings.Contains(out, "tests: 3 passed") {
			t.Errorf("got:\n%s", out)
		}
	})
	t.Run("only collection errors", func(t *testing.T) {
		out := run([]string{"==== 1 error in 0.01s ===="}, 2, Options{Budget: 60})
		if !strings.Contains(out, "tests: no tests ran, 1 error") {
			t.Errorf("got:\n%s", out)
		}
	})
	t.Run("raw has no log line", func(t *testing.T) {
		if out := run([]string{"a"}, 0, Options{Budget: 60, Raw: true}); strings.Contains(out, "full log") {
			t.Errorf("got:\n%s", out)
		}
	})
	t.Run("one warning at a tiny budget", func(t *testing.T) {
		r := &digest.Report{Structured: true, Meaningful: make([]string, 30),
			Warnings: []*digest.Item{{Kind: "warning", Title: "unused variable: `x`", Loc: "src/main.rs:1:5", Count: 1}}}
		for b := 5; b <= 12; b++ {
			out := Report(Run{Argv: []string{"cargo"}, Lines: make([]string, 30)}, r, Options{Budget: b})
			if !strings.Contains(out, "src/main.rs:1:5  unused variable") || strings.Contains(out, "not shown") {
				t.Errorf("budget %d:\n%s", b, out)
			}
		}
	})
	t.Run("hidden warnings are counted, not kinds", func(t *testing.T) {
		r := &digest.Report{Structured: true, Meaningful: make([]string, 30)}
		for i := range 30 {
			r.Warnings = append(r.Warnings, &digest.Item{Kind: "warning", Title: fmt.Sprintf("w%d", i), Count: 2, Locs: []string{"a:1"}})
		}
		out := Report(Run{Argv: []string{"x"}, Lines: make([]string, 30)}, r, Options{Budget: 20})
		if !strings.Contains(out, "more warnings not shown") || strings.Contains(out, "kinds") {
			t.Errorf("got:\n%s", out)
		}
	})
	t.Run("clip keeps the location at the end", func(t *testing.T) {
		got := clip(strings.Repeat("x", 500) + " at f (/src/app.js:12:5)")
		if !strings.HasSuffix(got, "(/src/app.js:12:5)") || len([]rune(got)) > maxLine+40 {
			t.Errorf("got %q", got)
		}
	})
	t.Run("multi-line scripts show their first line", func(t *testing.T) {
		got := Command([]string{"python3", "-c", "import sys\n" + strings.Repeat("print(1)\n", 20)})
		if got != "python3 -c 'import sys'…" {
			t.Errorf("got %s", got)
		}
	})
}

func TestLiveCountsOnlyRealDiagnostics(t *testing.T) {
	l := &Live{}
	for _, s := range []string{
		"warning: unused variable: `rounding`",
		"warning: `ledger` (lib) generated 1 warning",
		"warning: `ledger` (lib test) generated 1 warning (1 duplicate)",
		"error: test failed, to rerun pass `--lib`",
		"error[E0308]: mismatched types",
		"error: could not compile `ledger` (lib) due to 1 previous error; 1 warning emitted",
	} {
		l.Line(s)
	}
	if l.warns != 1 || l.errs != 1 {
		t.Errorf("warns=%d errs=%d, want 1 and 1", l.warns, l.errs)
	}
}

func TestFinalRoundBudget(t *testing.T) {
	r := &digest.Report{Structured: true, Meaningful: make([]string, 60)}
	for i := range 12 {
		r.Errors = append(r.Errors, &digest.Item{Kind: "error", Title: fmt.Sprintf("problem %d", i), Loc: fmt.Sprintf("e.c:%d:1", i+1),
			Body: []string{"  frame", "  ^"}, Count: 1})
	}
	r.Warnings = []*digest.Item{{Kind: "warning", Title: "unused variable 'lone'", Loc: "e.c:2:21", Count: 1}}
	out := Report(Run{Argv: []string{"gcc"}, Exit: 1, Lines: make([]string, 60)}, r, Options{Budget: 5})
	if !strings.Contains(out, "problem 11") || !strings.Contains(out, "problem 0\n      frame") {
		t.Errorf("every error gets a line and the first keeps its frame:\n%s", out)
	}
	if strings.Contains(out, "1 more warning") {
		t.Errorf("a lone warning reported as 'one more':\n%s", out)
	}
}

func TestRecognisedTinyOutputIsDigested(t *testing.T) {
	lines := []string{"a.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'."}
	out := Report(Run{Argv: []string{"tsc"}, Exit: 2, Lines: lines}, digest.Digest(lines, 2), Options{Budget: 60})
	if !strings.Contains(out, "1 error:\n  a.ts:1:7  TS2322") {
		t.Errorf("got:\n%s", out)
	}
}

func TestRawIsJustTheOutput(t *testing.T) {
	lines := []string{"a", "b"}
	if out := Report(Run{Argv: []string{"x"}, Lines: lines}, digest.Digest(lines, 0), Options{Raw: true}); out != "a\nb\n" {
		t.Errorf("got %q", out)
	}
}

// A long t.Errorf message (a whole dumped output) must reach the report:
// the first failure keeps its explanation, up to 15 lines.
func TestLongGoFailureMessageIsKept(t *testing.T) {
	lines := []string{"--- FAIL: TestOverview (0.02s)"}
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("    main_test.go:138: dump line %d", i))
	}
	lines = append(lines, "FAIL", "FAIL\texample.com/x\t0.03s", "FAIL")
	out := Report(Run{Argv: []string{"go"}, Exit: 1, Lines: lines}, digest.Digest(lines, 1), Options{Budget: 60})
	if !strings.Contains(out, "dump line 12") {
		t.Errorf("message cut short:\n%s", out)
	}
}
