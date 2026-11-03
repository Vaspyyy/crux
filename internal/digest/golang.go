package digest

import (
	"regexp"
	"strings"
)

var (
	goPkgHdrRe   = regexp.MustCompile(`^# (\S+)( \[.*\])?$`)
	goFileErrRe  = regexp.MustCompile(`^(\S+\.go):(\d+)(?::(\d+))?: (.+)$`)
	goFailRe     = regexp.MustCompile(`^(\s*)--- FAIL: (\S+) \([\d.]+s\)$`)
	goPassRe     = regexp.MustCompile(`^\s*--- PASS: `)
	goSkipRe     = regexp.MustCompile(`^\s*--- SKIP: `)
	goRunRe      = regexp.MustCompile(`^=== (RUN|PAUSE|CONT|NAME)\s+(\S+)`)
	goLogLocRe   = regexp.MustCompile(`^\s*(\S+\.go:\d+):`)
	goOkRe       = regexp.MustCompile(`^ok\s+\S+`)
	goPkgFailRe  = regexp.MustCompile(`^FAIL\s+(\S+)(\s+[\d.]+s|\s+\[(build|setup) failed\])?\s*$`)
	goPanicRe    = regexp.MustCompile(`^panic: (.+?)( \[recovered.*\])?$`)
	goFrameFile  = regexp.MustCompile(`^\t(\S+\.go:\d+)`)
	goGoroutine  = regexp.MustCompile(`^goroutine \d+ \[`)
	goStdlibPath = regexp.MustCompile(`/(src/(runtime|testing|reflect|sync|internal)/|toolchain@|go/src/)`)
)

// goBuildErrors handles "# pkg" followed by file.go:line:col: message lines.
func goBuildErrors(s *state, i int) int {
	l := s.lines[i]
	if goPkgHdrRe.MatchString(l) {
		end := i + 1
		for end < len(s.lines) && goFileErrRe.MatchString(s.lines[end]) {
			m := goFileErrRe.FindStringSubmatch(s.lines[end])
			loc := m[1] + ":" + m[2]
			if m[3] != "" {
				loc += ":" + m[3]
			}
			s.r.Errors = append(s.r.Errors, &Item{Kind: "error", Title: m[4], Loc: loc})
			end++
		}
		if end > i+1 {
			s.tool("go")
			return end
		}
		return 0
	}
	// vet-style lines without a header
	if m := goFileErrRe.FindStringSubmatch(l); m != nil && !strings.HasPrefix(m[4], "error:") && !strings.HasPrefix(m[4], "warning:") {
		loc := m[1] + ":" + m[2]
		if m[3] != "" {
			loc += ":" + m[3]
		}
		s.tool("go")
		s.r.Errors = append(s.r.Errors, &Item{Kind: "error", Title: m[4], Loc: loc})
		return i + 1
	}
	return 0
}

// goTestFailure handles "--- FAIL: TestX (0.00s)" with the log lines that
// belong to it (printed before the line with -v, after it without).
func goTestFailure(s *state, i int) int {
	m := goFailRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	s.tool("go test")
	indent, name := len(m[1]), m[2]
	it := &Item{Kind: "fail", Title: name}
	// -v: the test's log lines come between "=== RUN name" and this line
	// only trust them if we actually reach "=== RUN name": without -v the
	// indented lines above belong to the previous failure
	var before []string
	for j := i - 1; j >= 0 && j > i-200; j-- {
		l := s.lines[j]
		if r := goRunRe.FindStringSubmatch(l); r != nil {
			if r[2] != name {
				before = nil
			}
			break
		}
		if !strings.HasPrefix(l, "    ") || goPassRe.MatchString(l) || goFailRe.MatchString(l) {
			before = nil
			break
		}
		before = append([]string{l}, before...)
	}
	end := i + 1
	var after []string
	for end < len(s.lines) {
		l := s.lines[end]
		if goFailRe.MatchString(l) || goPassRe.MatchString(l) || !strings.HasPrefix(l, strings.Repeat(" ", indent+4)) {
			break
		}
		after = append(after, l)
		end++
	}
	for _, l := range append(before, after...) {
		if loc := goLogLocRe.FindStringSubmatch(l); loc != nil && it.Loc == "" {
			it.Loc = loc[1]
		}
		it.Body = append(it.Body, strings.TrimSpace(l))
	}
	// a panic right after the test belongs to it
	if end < len(s.lines) && goPanicRe.MatchString(s.lines[end]) {
		p, next := parseGoPanic(s.lines, end)
		it.Body = append(it.Body, p.Title)
		if p.Loc != "" {
			it.Body = append(it.Body, "at "+p.Loc)
			if it.Loc == "" {
				it.Loc = p.Loc
			}
		}
		end = next
	}
	it.Body = capLines(it.Body, 6)
	s.r.Failures = append(s.r.Failures, it)
	return end
}

// parseGoPanic reads a panic and its goroutine dump, keeping the first
// frame that is in the user's code.
func parseGoPanic(lines []string, i int) (*Item, int) {
	m := goPanicRe.FindStringSubmatch(lines[i])
	it := &Item{Kind: "error", Title: "panic: " + m[1]}
	end := i + 1
	for end < len(lines) {
		l := lines[end]
		t := strings.TrimSpace(l)
		switch {
		case t == "" || goGoroutine.MatchString(l) || strings.HasPrefix(l, "created by ") || strings.HasPrefix(l, "\t"):
		case strings.HasSuffix(t, ")") || strings.HasSuffix(t, "(...)"): // a function frame
		default:
			return it, end
		}
		if f := goFrameFile.FindStringSubmatch(l); f != nil && it.Loc == "" && !goStdlibPath.MatchString(f[1]) {
			it.Loc = f[1]
		}
		end++
	}
	return it, end
}

func goPanic(s *state, i int) int {
	if !goPanicRe.MatchString(s.lines[i]) {
		return 0
	}
	s.tool("go")
	it, end := parseGoPanic(s.lines, i)
	s.r.Errors = append(s.r.Errors, it)
	return end
}

// goSummary counts packages and -v results and swallows harness chatter.
func goSummary(s *state, i int) int {
	l := s.lines[i]
	switch {
	case goOkRe.MatchString(l):
		s.tool("go test")
		s.r.Tests.PkgOK++
	case goPkgFailRe.MatchString(l):
		s.tool("go test")
		s.r.Tests.PkgFailed++
		if m := goPkgFailRe.FindStringSubmatch(l); m[3] != "" {
			s.r.Notes = append(s.r.Notes, "package "+m[1]+": "+m[3]+" failed")
		}
	case goPassRe.MatchString(l):
		s.tool("go test")
		s.r.Tests.Passed++
	case goSkipRe.MatchString(l):
		s.r.Tests.Skipped++
	case s.tools["go test"] && strings.HasPrefix(l, "    ") && goLogLocRe.MatchString(l):
		// t.Log output; a failing test's lines are picked up by goTestFailure
	case goRunRe.MatchString(l):
		s.tool("go test")
	case l == "PASS", l == "FAIL", strings.HasPrefix(l, "exit status "),
		strings.HasPrefix(l, "?   \t"), strings.HasPrefix(l, "warning: GOPATH set to GOROOT"):
	default:
		return 0
	}
	return i + 1
}
