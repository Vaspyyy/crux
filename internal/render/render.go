// Package render prints a digest, as plain text for agents or in color for
// people.
package render

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Vaspyyy/crux/internal/digest"
)

type Run struct {
	Command  string
	Exit     int
	Signal   string
	Duration time.Duration
	LogPath  string
	Lines    []string // cleaned output
}

type Options struct {
	Color  bool
	Budget int // lines of report, roughly
	Raw    bool
}

// palette, ANSI 16 colors so it follows the terminal theme
type pal struct{ red, green, yellow, blue, magenta, cyan, bold, faint, reset string }

func palette(on bool) pal {
	if !on {
		return pal{}
	}
	return pal{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[34m", "\x1b[35m", "\x1b[36m", "\x1b[1m", "\x1b[2m", "\x1b[0m"}
}

const shortOutput = 20 // at or below this many meaningful lines, just show them

// Report builds the text for a finished run.
func Report(run Run, r *digest.Report, o Options) string {
	c := palette(o.Color)
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	ok := run.Exit == 0

	// header: verdict, command, exit, time, size
	status := c.green + c.bold + "OK" + c.reset
	if o.Color {
		status = c.green + c.bold + "✓ ok" + c.reset
	}
	if !ok {
		status = c.red + c.bold + "FAILED" + c.reset
		if o.Color {
			status = c.red + c.bold + "✗ failed" + c.reset
		}
	}
	meta := []string{}
	if !ok {
		e := "exit " + strconv.Itoa(run.Exit)
		if run.Signal != "" {
			e = "killed by " + run.Signal
		}
		meta = append(meta, e)
	}
	meta = append(meta, fmtDuration(run.Duration), plural(len(run.Lines), "line")+" of output")
	line("%s  %s%s%s  %s(%s)%s", status, c.bold, run.Command, c.reset, c.faint, strings.Join(meta, ", "), c.reset)

	if o.Raw {
		for _, l := range run.Lines {
			line("%s", l)
		}
		footer(&b, run, c)
		return b.String()
	}

	// test counts
	failed := max(r.Tests.Failed, len(r.Failures))
	if r.HasTests {
		var parts []string
		if r.Tests.Passed > 0 || failed == 0 {
			parts = append(parts, c.green+strconv.Itoa(r.Tests.Passed)+" passed"+c.reset)
		}
		if failed > 0 {
			parts = append(parts, c.red+strconv.Itoa(failed)+" failed"+c.reset)
		}
		if r.Tests.Skipped > 0 {
			parts = append(parts, c.yellow+strconv.Itoa(r.Tests.Skipped)+" skipped"+c.reset)
		}
		if r.Tests.PkgOK+r.Tests.PkgFailed > 0 {
			pk := fmt.Sprintf("%d/%d packages ok", r.Tests.PkgOK, r.Tests.PkgOK+r.Tests.PkgFailed)
			parts = append(parts, c.faint+pk+c.reset)
		}
		if r.Tests.Passed == 0 && failed > 0 && r.Tests.PkgOK+r.Tests.PkgFailed > 0 {
			parts = parts[1:] // "0 passed" is just unknown without -v
		}
		line("tests: %s", strings.Join(parts, ", "))
	}

	// short output that nothing structured explains: show it whole
	meaningful := meaningfulLines(run.Lines)
	structured := len(r.Errors)+len(r.Failures)+len(r.Warnings) > 0 || r.HasTests
	if !structured && len(meaningful) <= shortOutput {
		if len(meaningful) > 0 {
			line("")
			for _, l := range meaningful {
				line("  %s", l)
			}
		}
		footer(&b, run, c)
		return b.String()
	}

	budget := o.Budget
	used := strings.Count(b.String(), "\n") + 2
	more := 0
	emit := func(it *digest.Item, color string, withBody bool) bool {
		need := 1
		if withBody {
			need += len(it.Body)
		}
		if used+need > budget && used > 4 {
			more++
			return false
		}
		head := "  "
		if it.Loc != "" {
			head += c.cyan + it.Loc + c.reset + "  "
		}
		title := it.Title
		if it.Kind == "fail" && it.Loc != "" {
			head = "  " + c.bold + it.Title + c.reset + "  " + c.cyan + it.Loc + c.reset
			title = ""
		} else {
			title = color + title + c.reset
		}
		if it.Count > 1 {
			title += c.faint + fmt.Sprintf(" ×%d", it.Count) + c.reset
		}
		line("%s%s", head, title)
		used++
		if withBody {
			for _, l := range it.Body {
				line("    %s%s%s", c.faint, l, c.reset)
				used++
			}
		}
		return true
	}

	if len(r.Errors) > 0 {
		line("\n%s%s:%s", c.red, plural(len(r.Errors), "error"), c.reset)
		used += 2
		for _, it := range r.Errors {
			emit(it, c.red, true)
		}
	}
	if len(r.Failures) > 0 {
		line("\n%s%s:%s", c.red, plural(len(r.Failures), "failed test"), c.reset)
		used += 2
		for _, it := range r.Failures {
			emit(it, c.red, true)
		}
	}
	if len(r.Warnings) > 0 {
		total := 0
		for _, w := range r.Warnings {
			total += w.Count
		}
		line("\n%s%s:%s", c.yellow, plural(total, "warning"), c.reset)
		used += 2
		// warnings matter less when something failed: one line each
		withBody := ok && len(r.Warnings) <= 3
		for _, it := range r.Warnings {
			emit(it, c.yellow, withBody)
			if len(it.Locs) > 0 && used < budget {
				locs := it.Locs
				extra := ""
				if len(locs) > 4 {
					extra = fmt.Sprintf(" +%d more", len(locs)-4)
					locs = locs[:4]
				}
				line("    %salso at %s%s%s", c.faint, strings.Join(locs, ", "), extra, c.reset)
				used++
			}
		}
	}
	for _, n := range r.Notes {
		line("%s%s%s", c.faint, n, c.reset)
	}
	if more > 0 {
		line("%s… %d more not shown (budget %d lines): see the full log%s", c.yellow, more, budget, c.reset)
	}

	// nothing explains the failure: the end of the output usually does
	explained := len(r.Errors)+len(r.Failures) > 0
	if (!ok && !explained) || (ok && !r.HasTests && len(r.Tail) > 0) {
		line("\n%slast lines:%s", c.faint, c.reset)
		for _, l := range r.Tail {
			line("  %s", l)
		}
	}
	footer(&b, run, c)
	return b.String()
}

func footer(b *strings.Builder, run Run, c pal) {
	if run.LogPath != "" {
		fmt.Fprintf(b, "\n%sfull log: %s%s\n", c.faint, pretty(run.LogPath), c.reset)
	}
}

func meaningfulLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return strconv.Itoa(int(d.Milliseconds())) + "ms"
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func pretty(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
