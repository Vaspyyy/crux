// Package render prints a digest, as plain text for agents or in color for
// people.
package render

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/Vaspyyy/crux/internal/digest"
)

type Run struct {
	Argv     []string
	Exit     int
	Signal   syscall.Signal // non-zero if the command was killed by a signal
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
type pal struct{ red, green, yellow, cyan, bold, faint, reset string }

func palette(on bool) pal {
	if !on {
		return pal{}
	}
	return pal{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[36m", "\x1b[1m", "\x1b[2m", "\x1b[0m"}
}

const (
	maxLine     = 300 // characters per printed line
	tinyOutput  = 8   // output this small is printed as is, without the frame
	shortOutput = 20  // unrecognised output this small is printed whole
)

type report struct {
	b     strings.Builder
	c     pal
	lines int
}

func (w *report) line(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	fmt.Fprintln(&w.b, s)
	w.lines += strings.Count(s, "\n") + 1
}

var trailingLocRe = regexp.MustCompile(`\(?[^\s(]+:\d+(:\d+)?\)?$`)

// clip keeps one line of output within maxLine characters. A location at
// the end of the line (a stack frame, "at file:line") survives the cut.
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxLine {
		return s
	}
	tail := []rune(trailingLocRe.FindString(s))
	if len(tail) > 120 {
		tail = nil
	}
	keep := maxLine - len(tail)
	out := string(r[:keep]) + fmt.Sprintf("…[+%d chars]", len(r)-keep-len(tail))
	if len(tail) > 0 {
		out += "… " + string(tail)
	}
	return out
}

// Report builds the text for a finished run.
func Report(run Run, r *digest.Report, o Options) string {
	w := &report{c: palette(o.Color)}
	c := w.c
	ok := run.Exit == 0

	// tiny output says it best itself: verdict line plus the output
	if !o.Raw && !r.HasTests && len(r.Meaningful) <= tinyOutput && run.Exit >= 0 && len(run.Lines) <= tinyOutput+4 {
		w.header(run, ok)
		for _, l := range r.Meaningful {
			w.line("  %s", clip(l))
		}
		return w.b.String()
	}

	w.header(run, ok)
	if o.Raw {
		for _, l := range run.Lines {
			w.line("%s", clip(l))
		}
		return w.b.String()
	}
	if r.HasTests {
		w.line("tests: %s", testCounts(r, c))
	}

	// output nothing recognised, and short: show it whole
	if !r.Structured && len(r.Meaningful) <= shortOutput {
		w.line("")
		for _, l := range r.Meaningful {
			w.line("  %s", clip(l))
		}
		return w.b.String()
	}

	budget := max(o.Budget, 10)
	w.items(r, budget, ok)

	if len(r.Notes) > 0 {
		w.line("\nnotes:")
		for _, n := range r.Notes {
			w.line("  %s", clip(n))
		}
	}
	explained := len(r.Errors)+len(r.Failures) > 0 && !r.Generic
	switch {
	case !ok && !explained:
		// nothing structured explains the failure: the end of the output usually does
		w.line("\n%slast lines:%s", c.faint, c.reset)
		for _, l := range lastN(r.Tail, max(3, budget-w.lines-3)) {
			w.line("  %s", clip(l))
		}
	case ok && !r.HasTests && len(r.Errors)+len(r.Warnings) == 0:
		// a successful command that isn't a test run: its output may be the point
		show := r.Meaningful
		label := "output:"
		if len(show) > budget/2 {
			show, label = lastN(show, 10), fmt.Sprintf("last 10 of %d non-empty lines:", len(r.Meaningful))
		}
		if len(show) > 0 {
			w.line("\n%s%s%s", c.faint, label, c.reset)
			for _, l := range show {
				w.line("  %s", clip(l))
			}
		}
	}
	w.footer(run)
	return w.b.String()
}

func lastN(ls []string, n int) []string {
	if len(ls) > n {
		return ls[len(ls)-n:]
	}
	return ls
}

func (w *report) header(run Run, ok bool) {
	c := w.c
	status := c.green + c.bold + "OK" + c.reset
	if !ok {
		status = c.red + c.bold + "FAILED" + c.reset
	}
	if w.c.bold != "" {
		if ok {
			status = c.green + c.bold + "✓ ok" + c.reset
		} else {
			status = c.red + c.bold + "✗ failed" + c.reset
		}
	}
	var meta []string
	if !ok {
		if run.Signal != 0 {
			meta = append(meta, "killed by "+signalName(run.Signal))
		} else {
			meta = append(meta, "exit "+strconv.Itoa(run.Exit))
		}
	}
	meta = append(meta, fmtDuration(run.Duration), plural(len(run.Lines), "line")+" of output")
	w.line("%s  %s%s%s  %s(%s)%s", status, c.bold, Command(run.Argv), c.reset, c.faint, strings.Join(meta, ", "), c.reset)
}

func (w *report) footer(run Run) {
	if run.LogPath != "" {
		w.line("\n%sfull log: %s%s", w.c.faint, run.LogPath, w.c.reset)
	}
}

func testCounts(r *digest.Report, c pal) string {
	t := r.Tests
	failed := max(t.Failed, len(r.Failures))
	pkgs := t.PkgOK + t.PkgFailed
	var parts []string
	add := func(n int, word, color string) {
		if n > 0 {
			parts = append(parts, color+strconv.Itoa(n)+" "+word+c.reset)
		}
	}
	switch {
	case t.Passed > 0:
		add(t.Passed, "passed", c.green)
	case failed == 0 && t.Errors > 0 && pkgs == 0:
		parts = append(parts, c.red+"no tests ran"+c.reset)
	case failed == 0 && t.Errors == 0 && pkgs == 0:
		parts = append(parts, c.green+"0 passed"+c.reset)
	}
	add(failed, "failed", c.red)
	add(t.Errors, plural0(t.Errors, "error"), c.red)
	add(t.XPassed, "xpassed", c.yellow)
	add(t.Skipped, "skipped", c.yellow)
	add(t.XFailed, "xfailed", c.yellow)
	if pkgs > 0 {
		pk := fmt.Sprintf("%d/%d packages ok", t.PkgOK, pkgs)
		if t.PkgNoTests > 0 {
			pk += fmt.Sprintf(", %s without tests", plural(t.PkgNoTests, "package"))
		}
		if t.Passed == 0 && failed == 0 {
			pk += " (use -v for per-test counts)"
		}
		parts = append(parts, c.faint+pk+c.reset)
	}
	return strings.Join(parts, ", ")
}

// items prints errors, failed tests and warnings within the budget: every
// error and failure gets its line before any code frame or warning does.
func (w *report) items(r *digest.Report, budget int, ok bool) {
	c := w.c
	important := append(append([]*digest.Item{}, r.Errors...), r.Failures...)
	room := budget - w.lines - 4 // header and footer
	nWarn := len(r.Warnings)
	sections := 0
	if len(r.Errors) > 0 {
		sections++
	}
	if len(r.Failures) > 0 {
		sections++
	}
	room -= sections * 2 // section titles and blank lines

	// 1. one line per error / failure, as many as fit (at least one), keeping
	// room for the first one's explanation: with 50 failures of one cause,
	// the cause matters more than the 50th name
	lines := func(it *digest.Item) int {
		if len(it.Locs) > 0 {
			return 2
		}
		return 1
	}
	reserve := 0
	if len(important) > 0 {
		if b := len(important[0].Body); b > 0 && b <= room/2 {
			reserve = b
		}
	}
	shown := 0
	for _, it := range important {
		if shown > 0 && lines(it) > room-reserve {
			break
		}
		room -= lines(it)
		shown++
	}
	// 2. their bodies, in order, while they fit
	withBody := map[*digest.Item]bool{}
	for _, it := range important[:shown] {
		if len(it.Body) > 0 && len(it.Body) <= room {
			withBody[it] = true
			room -= len(it.Body)
		}
	}
	// 3. warnings: one or two lines each, with what's left; at least one
	// when every error and failure is already listed
	warnShown := 0
	if nWarn > 0 {
		room -= 2
		for _, it := range r.Warnings {
			need := lines(it)
			if need > room && (warnShown > 0 || shown < len(important)) {
				break
			}
			room -= need
			warnShown++
		}
		if ok && warnShown > 0 { // a clean run with a few warnings: show their frames too
			for _, it := range r.Warnings[:warnShown] {
				if len(it.Body) > 0 && len(it.Body) <= room {
					withBody[it] = true
					room -= len(it.Body)
				}
			}
		}
	}

	emit := func(it *digest.Item, color string) {
		loc := it.Loc
		if it.Missing {
			loc += c.reset + c.faint + " (file not found from here)"
		}
		var head string
		switch {
		case it.Kind == "fail":
			head = "  " + c.bold + it.Title + c.reset
			if it.Loc != "" {
				head += "  " + c.cyan + loc + c.reset
			}
		default:
			head = "  "
			if it.Loc != "" {
				head += c.cyan + loc + c.reset + "  "
			}
			title := it.Title
			if it.Count > 1 && it.Norm != "" {
				title = it.Norm
			}
			head += color + title + c.reset
			if it.Count > 1 {
				head += c.faint + fmt.Sprintf(" ×%d", it.Count) + c.reset
			}
			if names := distinct(it.Names); len(names) > 1 {
				if len(names) > 8 {
					names = append(names[:8], fmt.Sprintf("+%d more", len(names)-8))
				}
				head += c.faint + ": " + strings.Join(names, ", ") + c.reset
			}
		}
		w.line("%s", clip(head))
		if withBody[it] {
			for _, l := range it.Body {
				w.line("    %s%s%s", c.faint, clip(l), c.reset)
			}
		}
		if len(it.Locs) > 0 {
			locs, extra := it.Locs, ""
			if len(locs) > 6 {
				locs, extra = locs[:6], fmt.Sprintf(" +%d more", len(locs)-6)
			}
			w.line("    %salso at %s%s%s", c.faint, strings.Join(locs, ", "), extra, c.reset)
		}
	}

	errShown := min(len(r.Errors), shown)
	failShown := shown - errShown
	if len(r.Errors) > 0 {
		w.line("\n%s%s:%s", c.red, plural(total(r.Errors), "error"), c.reset)
		for _, it := range r.Errors[:errShown] {
			emit(it, c.red)
		}
		if n := len(r.Errors) - errShown; n > 0 {
			w.line("  %s… and %s not shown (full log below)%s", c.yellow, plural(n, "more error"), c.reset)
		}
	}
	if len(r.Failures) > 0 {
		w.line("\n%s%s:%s", c.red, plural(len(r.Failures), "failed test"), c.reset)
		for _, it := range r.Failures[:failShown] {
			emit(it, c.red)
		}
		if n := len(r.Failures) - failShown; n > 0 {
			w.line("  %s… and %s not shown (full log below)%s", c.yellow, plural(n, "more failed test"), c.reset)
		}
	}
	if nWarn > 0 {
		w.line("\n%s%s:%s", c.yellow, plural(total(r.Warnings), "warning"), c.reset)
		for _, it := range r.Warnings[:warnShown] {
			emit(it, c.yellow)
		}
		if n := total(r.Warnings[warnShown:]); n > 0 {
			w.line("  %s… and %s not shown (full log below)%s", c.faint, plural(n, "more warning"), c.reset)
		}
	}
}

func total(items []*digest.Item) int {
	n := 0
	for _, it := range items {
		n += max(it.Count, 1)
	}
	return n
}

func distinct(ls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ls {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Command renders argv so it can be pasted back into a shell: arguments
// with spaces or quotes are single-quoted, control characters are escaped,
// and very long commands are shortened.
func Command(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
		if first, _, multi := strings.Cut(a, "\n"); multi && len(parts[i]) > 60 {
			parts[i] = shellQuote(first) + "…" // a script passed with -c: its first line names it
		}
	}
	s := strings.Join(parts, " ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	return s
}

func shellQuote(a string) string {
	if a == "" {
		return "''"
	}
	if strings.IndexFunc(a, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
		return "$" + strings.ReplaceAll(strconv.Quote(a), "'", "\\'") // $"…" with escapes, like bash's $'…'
	}
	if strings.IndexFunc(a, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

func signalName(s syscall.Signal) string {
	names := map[syscall.Signal]string{syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
		syscall.SIGILL: "SIGILL", syscall.SIGABRT: "SIGABRT", syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL",
		syscall.SIGSEGV: "SIGSEGV", syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
		syscall.SIGBUS: "SIGBUS"}
	if n, ok := names[s]; ok {
		return n
	}
	return "signal " + strconv.Itoa(int(s))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func plural0(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
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
