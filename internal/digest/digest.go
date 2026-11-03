// Package digest turns the output of a build, test or lint command into a
// short report: what failed, where, and the few lines that explain why.
package digest

import (
	"regexp"
	"strings"
)

// Item is one error, warning or failed test.
type Item struct {
	Kind  string   // "error", "warning" or "fail"
	Title string   // the message, or the test name for failures
	Loc   string   // file:line[:col], if known
	Body  []string // the few lines that explain it (code frame, assertion, panic message)
	Count int      // identical items folded into this one
	Locs  []string // locations of the folded duplicates
}

// Tests counts test results; -1 means the tool didn't say.
type Tests struct {
	Passed, Failed, Skipped int
	PkgOK, PkgFailed        int // go test packages
}

func (t *Tests) any() bool {
	return t.Passed > 0 || t.Failed > 0 || t.Skipped > 0 || t.PkgOK > 0 || t.PkgFailed > 0
}

// Report is the digest of one command run.
type Report struct {
	Tools    []string // recognised formats, e.g. "cargo", "go test", "pytest"
	Tests    Tests
	HasTests bool
	Errors   []*Item
	Failures []*Item
	Warnings []*Item
	Notes    []string // summary lines worth keeping ("make: *** [all] Error 1")
	Tail     []string // last meaningful lines, for output nothing else explains
	Lines    int      // lines of output in total
	Shown    []string // the whole cleaned output, when it is short enough to show as is
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// Clean strips escape sequences and resolves carriage returns the way a
// terminal would: only the text after the last \r survives (progress bars).
func Clean(line string) string {
	line = strings.TrimRight(line, "\r\n")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	if strings.IndexByte(line, 0x1b) >= 0 {
		line = ansiRe.ReplaceAllString(line, "")
	}
	return strings.TrimRight(line, " \t")
}

// state is shared by the extractors while they walk the output.
type state struct {
	lines []string
	used  []bool
	r     *Report
	tools map[string]bool
}

func (s *state) tool(name string) {
	if !s.tools[name] {
		s.tools[name] = true
		s.r.Tools = append(s.r.Tools, name)
	}
}

func (s *state) consume(from, to int) {
	for i := from; i < to && i < len(s.used); i++ {
		s.used[i] = true
	}
}

// extractor looks at line i and, if it recognises something, records it
// and returns the index after the block it consumed. 0 means "not mine".
type extractor func(s *state, i int) int

var extractors = []extractor{
	rustDiag, rustTestFailure, rustPanic, rustSummary,
	goBuildErrors, goTestFailure, goPanic, goSummary,
	pytestSections, pytestSummary,
	tscError, tscSummary,
	gccStyle, javacError, javacSummary,
	eslint,
	pyTraceback, nodeError,
	makeError, npmError,
}

// Digest analyses cleaned output lines. exit is the command's exit code.
func Digest(lines []string, exit int) *Report {
	r := &Report{Lines: len(lines)}
	s := &state{lines: lines, used: make([]bool, len(lines)), r: r, tools: map[string]bool{}}
	for i := 0; i < len(lines); {
		if s.used[i] {
			i++
			continue
		}
		next := 0
		for _, ex := range extractors {
			if next = ex(s, i); next > i {
				break
			}
		}
		if next > i {
			s.consume(i, next)
			i = next
		} else {
			i++
		}
	}
	r.Errors = fold(r.Errors)
	r.Warnings = fold(r.Warnings)
	for _, group := range [][]*Item{r.Errors, r.Failures, r.Warnings} {
		for _, it := range group {
			tidy(it)
		}
	}
	r.HasTests = r.Tests.any() || len(r.Failures) > 0

	// leftovers: lines nothing recognised and that aren't known noise
	var rest []string
	for i, l := range lines {
		if !s.used[i] && strings.TrimSpace(l) != "" && !isNoise(l) {
			rest = append(rest, l)
		}
	}
	explained := len(r.Errors) > 0 || len(r.Failures) > 0
	if exit != 0 && !explained {
		r.Errors = append(r.Errors, genericErrors(rest)...)
	}
	r.Tail = lastLines(rest, 6)
	return r
}

// Relative is set by the caller to the working directory; locations under it
// are shown relative to it.
var Relative string

// tidy shortens locations and drops a body line that only repeats the location.
func tidy(it *Item) {
	it.Loc = rel(it.Loc)
	for i, l := range it.Locs {
		it.Locs[i] = rel(l)
	}
	for i, l := range it.Body {
		it.Body[i] = rel(l)
		if it.Loc != "" && strings.HasPrefix(it.Body[i], it.Loc+": ") {
			it.Body[i] = strings.TrimPrefix(it.Body[i], it.Loc+": ")
		}
	}
}

func rel(s string) string {
	if Relative == "" || Relative == "/" {
		return s
	}
	return strings.ReplaceAll(s, Relative+"/", "")
}

// fold merges items with the same kind and title ("unused variable ×7").
func fold(items []*Item) []*Item {
	var out []*Item
	seen := map[string]*Item{}
	for _, it := range items {
		key := it.Kind + "\x00" + it.Title
		if it.Kind == "warning" { // "unused variable: `a`" and "… `b`" are the same warning
			key = it.Kind + "\x00" + quotedRe.ReplaceAllString(it.Title, "…")
		}
		if first, ok := seen[key]; ok {
			first.Count++
			if it.Loc != "" {
				first.Locs = append(first.Locs, it.Loc)
			}
			continue
		}
		it.Count = 1
		seen[key] = it
		out = append(out, it)
	}
	return out
}

// quoted names in messages: `x`, 'x', ‘x’, »x«, "x"
var quotedRe = regexp.MustCompile("`[^`]*`|'[^']*'|‘[^’]*’|»[^«]*«|\"[^\"]*\"")

func lastLines(ls []string, n int) []string {
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return append([]string(nil), ls...)
}

var genericErrRe = regexp.MustCompile(`(?i)(^|\s|\[)(error|fatal|failed|failure|panic|exception|traceback|cannot|could not|not found|denied|refused|segmentation fault|aborted)\b`)

// genericErrors picks error-looking lines (with one line of context) from
// output no extractor understood.
func genericErrors(rest []string) []*Item {
	var out []*Item
	for i, l := range rest {
		if !genericErrRe.MatchString(l) {
			continue
		}
		it := &Item{Kind: "error", Title: strings.TrimSpace(l)}
		if i+1 < len(rest) && strings.HasPrefix(rest[i+1], " ") {
			it.Body = []string{rest[i+1]}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return fold(out)
}
