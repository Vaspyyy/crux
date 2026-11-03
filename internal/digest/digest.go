// Package digest turns the output of a build, test or lint command into a
// short report: what failed, where, and the few lines that explain why.
package digest

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Item is one error, warning or failed test.
type Item struct {
	Kind   string   // "error", "warning" or "fail"
	Title  string   // the message, or the test name for failures
	Loc    string   // file:line[:col], if known
	Body   []string // the few lines that explain it (code frame, assertion, panic message)
	Count  int      // occurrences folded into this one
	Locs   []string // locations of the folded occurrences
	Names  []string // folded warnings: the distinct names (`a`, `b`, …)
	Norm   string   // folded warnings: the message without the names
	Detail string   // what tells two errors with one message apart ("expected `bool`, found `&str`")
	// Missing: the location's file doesn't exist relative to the working
	// directory (a stale cache, a path relative to another root)
	Missing bool
}

// Tests counts test results; -1 means the tool didn't say.
type Tests struct {
	Passed, Failed, Skipped int
	XFailed, Errors         int  // pytest: expected failures, errors outside tests
	PkgOK, PkgFailed        int  // go test packages
	PkgNoTests              int  // go test packages without test files
	XPassed                 int  // pytest: expected to fail but passed
	NoneRan                 bool // the tool said no tests ran
	PkgNoRun                int  // go test packages where no test matched (-run)
	Unknown                 bool // go test without -v: only packages are known
}

func (t *Tests) any() bool {
	return t.NoneRan || t.Passed > 0 || t.Failed > 0 || t.Skipped > 0 || t.XFailed > 0 || t.Errors > 0 || t.PkgOK > 0 || t.PkgFailed > 0
}

// Report is the digest of one command run.
type Report struct {
	Tools      []string // recognised formats, e.g. "cargo", "go test", "pytest"
	Tests      Tests
	HasTests   bool
	Errors     []*Item
	Failures   []*Item
	Warnings   []*Item
	Notes      []string // summary lines worth keeping ("make: *** [all] Error 1")
	Tail       []string // last meaningful lines, for output nothing else explains
	Lines      int      // lines of output in total
	Meaningful []string // non-empty, non-noise lines
	Structured bool     // a known tool format was recognised
	Generic    bool     // errors come from the error-looking-line fallback
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

	goRun      string              // go test -v: the test currently running
	goLogs     map[string][]string // go test -v: log lines per test
	goPending  []*Item             // go failures waiting for their "FAIL pkg" line
	goPanicked string              // the test whose panic ended the current package
	pyTbLocs   []string            // pytest --tb=line: one location per failure, in order
	roots      []string            // directories make entered: tools' paths may be relative to them
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
	s := &state{lines: lines, used: make([]bool, len(lines)), r: r, tools: map[string]bool{}, goLogs: map[string][]string{}}
	for i := 0; i < len(lines); {
		if s.used[i] || len(strings.TrimSpace(lines[i])) < 3 { // nothing we know is that short
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
	r.Errors = foldRepeated(fold(r.Errors))
	r.Warnings = foldWarnings(r.Warnings)
	if s.tools["cargo test"] { // cargo prints failures in completion order: make it stable
		sort.SliceStable(r.Failures, func(i, j int) bool { return naturalLess(r.Failures[i].Title, r.Failures[j].Title) })
	}
	dropParentFailures(r)
	for _, group := range [][]*Item{r.Errors, r.Failures, r.Warnings} {
		for _, it := range group {
			tidy(s, it)
		}
	}
	for i, n := range r.Notes {
		r.Notes[i] = rel(n)
	}
	r.HasTests = r.Tests.any() || len(r.Failures) > 0
	// leftovers: lines nothing recognised and that aren't known noise
	var rest, meaningful []string
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || isNoise(l) {
			continue
		}
		meaningful = append(meaningful, l)
		if !s.used[i] {
			rest = append(rest, l)
		}
	}
	r.Structured = len(r.Errors)+len(r.Failures)+len(r.Warnings) > 0 || r.HasTests
	explained := len(r.Errors) > 0 || len(r.Failures) > 0
	if exit != 0 && !explained {
		r.Errors = append(r.Errors, genericErrors(rest)...)
		r.Generic = len(r.Errors) > 0
	}
	// the true end of the output: what an unexplained failure usually ends with
	r.Tail = lastLines(meaningful, 8)
	r.Meaningful = meaningful
	return r
}

// dropParentFailures removes "TestX" when only its subtests "TestX/…" carry
// the failure (go test reports both).
func dropParentFailures(r *Report) {
	var out []*Item
	for _, f := range r.Failures {
		parent := false
		for _, g := range r.Failures {
			if g != f && strings.HasPrefix(g.Title, f.Title+"/") {
				parent = true
				break
			}
		}
		own := 0
		for _, b := range f.Body {
			if !strings.HasPrefix(b, "in package ") {
				own++
			}
		}
		if parent && own == 0 {
			continue
		}
		out = append(out, f)
	}
	r.Failures = out
}

// Relative is set by the caller to the working directory; locations under it
// are shown relative to it.
var Relative string

// CheckFiles makes tidy look up each location's file (off in tests, whose
// fixtures come from elsewhere).
var CheckFiles bool

var locSuffixRe = regexp.MustCompile(`(:\d+){1,2}$`)

// resolve checks that loc's file exists. Tools print paths relative to
// their own root (cargo: the workspace, make -C: its directory), so a path
// that isn't found from here is looked up in the directories make entered
// and in the parent directories, and rewritten to work from here.
func resolve(loc string, roots []string) (string, bool) {
	suffix := locSuffixRe.FindString(loc)
	file := strings.TrimSuffix(loc, suffix)
	if !CheckFiles || suffix == "" || file == "" || strings.ContainsAny(file[:1], "<[") ||
		strings.Contains(file, "://") || !strings.Contains(file, ".") {
		return loc, false
	}
	if filepath.IsAbs(file) {
		_, err := os.Stat(file)
		return loc, os.IsNotExist(err)
	}
	if _, err := os.Stat(filepath.Join(Relative, file)); err == nil {
		return loc, false
	}
	cands := append([]string(nil), roots...)
	for dir, k := filepath.Dir(Relative), 0; k < 8 && dir != filepath.Dir(dir); dir, k = filepath.Dir(dir), k+1 {
		cands = append(cands, dir)
	}
	for _, root := range cands {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			if r, err := filepath.Rel(Relative, filepath.Join(root, file)); err == nil {
				return r + suffix, false
			}
		}
	}
	return loc, true
}

// tidy shortens locations and drops a body line that only repeats the location.
func tidy(s *state, it *Item) {
	it.Loc = rel(it.Loc)
	if it.Loc != "" {
		it.Loc, it.Missing = resolve(it.Loc, s.roots)
	}
	for i, l := range it.Locs {
		if l2, miss := resolve(rel(l), s.roots); miss {
			it.Locs[i] = l2 + " (not found)"
		} else {
			it.Locs[i] = l2
		}
	}
	for i, l := range it.Body {
		it.Body[i] = rel(l)
		for _, prefix := range []string{it.Loc, path.Base(it.Loc)} {
			if prefix != "" && prefix != "." && strings.HasPrefix(it.Body[i], prefix+": ") {
				it.Body[i] = strings.TrimPrefix(it.Body[i], prefix+": ")
				break
			}
		}
	}
}

func rel(s string) string {
	if Relative == "" || Relative == "/" {
		return s
	}
	return strings.ReplaceAll(s, Relative+"/", "")
}

// fold merges true duplicates: same message at the same place (rustc and
// gcc can report one problem twice). Different places stay separate.
func fold(items []*Item) []*Item {
	var out []*Item
	seen := map[string]*Item{}
	for _, it := range items {
		key := it.Kind + "\x00" + it.Title + "\x00" + it.Loc
		if first, ok := seen[key]; ok && it.Loc != "" {
			first.Count++
			continue
		}
		it.Count = 1
		seen[key] = it
		out = append(out, it)
	}
	return out
}

// foldRepeated merges errors that share a message when there are three or
// more of them: one cause, many places. The first keeps its code frame and
// the others are listed by location.
func foldRepeated(items []*Item) []*Item {
	n := map[string]int{}
	for _, it := range items {
		n[it.Kind+"\x00"+it.Title+"\x00"+it.Detail]++
	}
	var out []*Item
	first := map[string]*Item{}
	for _, it := range items {
		key := it.Kind + "\x00" + it.Title + "\x00" + it.Detail
		if n[key] < 3 {
			out = append(out, it)
			continue
		}
		if f, ok := first[key]; ok {
			f.Count += it.Count
			if it.Loc != "" {
				f.Locs = append(f.Locs, it.Loc)
			}
			continue
		}
		first[key] = it
		out = append(out, it)
	}
	return out
}

// naturalLess orders "case_2" before "case_10".
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		da, db := digitsPrefix(a), digitsPrefix(b)
		if da != "" && db != "" {
			ta, tb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(ta) != len(tb) {
				return len(ta) < len(tb)
			}
			if ta != tb {
				return ta < tb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digitsPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// foldWarnings merges warnings of the same kind ("unused variable") and
// keeps every name and location, so nothing is attributed to the wrong one.
func foldWarnings(items []*Item) []*Item {
	var out []*Item
	seen := map[string]*Item{}
	for _, it := range items {
		norm := quotedRe.ReplaceAllString(it.Title, "…")
		name := quotedRe.FindString(it.Title)
		if first, ok := seen[norm]; ok {
			first.Count++
			if it.Loc != "" {
				first.Locs = append(first.Locs, it.Loc)
			}
			if name != "" {
				first.Names = append(first.Names, name)
			}
			continue
		}
		it.Count, it.Norm = 1, withoutNames(it.Title)
		if name != "" {
			it.Names = []string{name}
		}
		seen[norm] = it
		out = append(out, it)
	}
	for _, it := range out {
		if it.Count > 1 {
			it.Body = nil // one instance's code frame would misrepresent the others
		}
	}
	return out
}

// withoutNames drops the quoted names from a message, for the line that
// lists them after it: "unused variable [-Wunused-variable] ×2: ‘a’, ‘b’".
func withoutNames(title string) string {
	t := strings.Join(strings.Fields(quotedRe.ReplaceAllString(title, "")), " ")
	t = strings.ReplaceAll(t, "( )", "")
	t = strings.ReplaceAll(t, "()", "")
	return strings.TrimRight(strings.Join(strings.Fields(t), " "), " :,")
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
	}
	return fold(out)
}
