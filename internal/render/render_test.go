package render

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Vaspyyy/crux/internal/digest"
)

func TestBudgetLimitsTheReport(t *testing.T) {
	r := &digest.Report{}
	for i := range 40 {
		r.Errors = append(r.Errors, &digest.Item{Kind: "error", Title: fmt.Sprintf("problem %d", i), Loc: fmt.Sprintf("a.c:%d:1", i),
			Body: []string{"  frame", "  ^"}, Count: 1})
	}
	out := Report(Run{Command: "make", Exit: 2, Duration: time.Second, Lines: make([]string, 500)}, r, Options{Budget: 30})
	if n := strings.Count(out, "\n"); n > 34 {
		t.Errorf("report has %d lines, budget 30:\n%s", n, out)
	}
	if !strings.Contains(out, "more not shown") {
		t.Errorf("no note about hidden items:\n%s", out)
	}
}

func TestShortOutputIsShownWhole(t *testing.T) {
	lines := []string{"hello", "world"}
	out := Report(Run{Command: "echo", Lines: lines}, digest.Digest(lines, 0), Options{Budget: 60})
	if !strings.Contains(out, "  hello\n  world\n") || !strings.HasPrefix(out, "OK  echo") {
		t.Errorf("got:\n%s", out)
	}
}

func TestNoColorsForAgents(t *testing.T) {
	r := &digest.Report{Errors: []*digest.Item{{Kind: "error", Title: "boom", Count: 1}}}
	if out := Report(Run{Command: "x", Exit: 1, Lines: make([]string, 50)}, r, Options{Budget: 60}); strings.Contains(out, "\x1b") {
		t.Errorf("escape codes in plain output: %q", out)
	}
}
