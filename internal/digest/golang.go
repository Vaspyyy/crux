package digest

import (
	"path"
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
	goOkRe       = regexp.MustCompile(`^ok\s+(\S+)\s+([\d.]+s|\(cached\))`)
	goPkgFailRe  = regexp.MustCompile(`^FAIL\s+(\S+)\s+([\d.]+s|\[(build|setup) failed\])\s*$`)
	goNoTestsRe  = regexp.MustCompile(`^\?\s+\S+\s+\[no test files\]$`)
	goPanicRe    = regexp.MustCompile(`^panic: (.+?)( \[recovered.*\])?$`)
	goFrameFile  = regexp.MustCompile(`^\t(\S+\.go:\d+)`)
	goGoroutine  = regexp.MustCompile(`^goroutine \d+ \[`)
	goStdlibPath = regexp.MustCompile(`/(src/(runtime|testing|reflect|sync|internal)/|toolchain@|go/src/)|_testmain\.go`)
	goRunningRe  = regexp.MustCompile(`^\t\t(\S+) \(`)
)

// GoModule and GoModuleDir describe the Go module the command ran in (set
// by the caller): failures in package GoModule/x/y get locations under
// GoModuleDir/x/y, since go test prints only the file's base name.
var GoModule, GoModuleDir string

func goPkgDir(pkg string) string {
	if GoModule == "" || (pkg != GoModule && !strings.HasPrefix(pkg, GoModule+"/")) {
		return ""
	}
	return path.Join(GoModuleDir, strings.TrimPrefix(strings.TrimPrefix(pkg, GoModule), "/"))
}

// goBuildErrors handles "# pkg" followed by file.go:line:col: message lines.
func goBuildErrors(s *state, i int) int {
	l := s.lines[i]
	if goPkgHdrRe.MatchString(l) {
		end := i + 1
		for end < len(s.lines) && goFileErrRe.MatchString(s.lines[end]) {
			goFileError(s, s.lines[end])
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
		s.tool("go")
		goFileError(s, l)
		return i + 1
	}
	return 0
}

func goFileError(s *state, l string) {
	m := goFileErrRe.FindStringSubmatch(l)
	if m[4] == "too many errors" {
		s.r.Notes = append(s.r.Notes, "go: too many errors (the compiler stopped listing them)")
		return
	}
	s.r.Errors = append(s.r.Errors, &Item{Kind: "error", Title: m[4], Loc: loc3(m[1], m[2], m[3])})
}

// goTestFailure handles "--- FAIL: TestX (0.00s)" with the log lines that
// belong to it (collected while streaming with -v, or printed after it).
func goTestFailure(s *state, i int) int {
	m := goFailRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	s.tool("go test")
	indent, name := len(m[1]), m[2]
	it := &Item{Kind: "fail", Title: name}
	body := append([]string(nil), s.goLogs[name]...)
	end := i + 1
	for end < len(s.lines) {
		l := s.lines[end]
		if goFailRe.MatchString(l) || goPassRe.MatchString(l) || !strings.HasPrefix(l, strings.Repeat(" ", indent+4)) {
			break
		}
		body = append(body, l)
		end++
	}
	for _, l := range body {
		if loc := goLogLocRe.FindStringSubmatch(l); loc != nil && it.Loc == "" {
			it.Loc = loc[1]
		}
		it.Body = append(it.Body, strings.TrimSpace(l))
	}
	// a panic right after the test belongs to it, unless it's the timeout panic.
	// The failure is located at the test's own line, like every other failure;
	// where it panicked goes into the message.
	if end < len(s.lines) && goPanicRe.MatchString(s.lines[end]) && !strings.Contains(s.lines[end], "test timed out") {
		p, next := parseGoPanic(s.lines, end)
		it.Body = append(it.Body, p.Title)
		if p.Loc != "" && p.Loc != p.testLoc {
			it.Body = append(it.Body, "panicked at "+p.Loc)
		}
		if it.Loc == "" {
			it.Loc = p.testLoc
			if it.Loc == "" {
				it.Loc = p.Loc
			}
		}
		s.goPanicked = name
		end = next
	}
	it.Body = capLines(it.Body, 6)
	s.r.Failures = append(s.r.Failures, it)
	s.goPending = append(s.goPending, it)
	return end
}

type goPanicInfo struct {
	*Item
	testLoc string // the first frame in a _test.go file
}

// parseGoPanic reads a panic and its goroutine dump, keeping the first
// frame in the user's code. A test timeout names the tests that were running.
func parseGoPanic(lines []string, i int) (goPanicInfo, int) {
	m := goPanicRe.FindStringSubmatch(lines[i])
	it := goPanicInfo{Item: &Item{Kind: "error", Title: "panic: " + m[1]}}
	end := i + 1
	var running []string
	for end < len(lines) {
		l := lines[end]
		t := strings.TrimSpace(l)
		switch {
		case t == "running tests:":
		case goRunningRe.MatchString(l):
			running = append(running, goRunningRe.FindStringSubmatch(l)[1])
		case t == "" || goGoroutine.MatchString(l) || strings.HasPrefix(l, "created by ") || strings.HasPrefix(l, "\t"):
		case strings.HasSuffix(t, ")") || strings.HasSuffix(t, "(...)"): // a function frame
		default:
			goto done
		}
		if f := goFrameFile.FindStringSubmatch(l); f != nil && !goStdlibPath.MatchString(f[1]) {
			if it.Loc == "" {
				it.Loc = f[1]
			}
			if it.testLoc == "" && strings.Contains(f[1], "_test.go:") {
				it.testLoc = f[1]
			}
		}
		end++
	}
done:
	if len(running) > 0 {
		it.Title += " while running " + strings.Join(running, ", ")
	}
	return it, end
}

func goPanic(s *state, i int) int {
	if !goPanicRe.MatchString(s.lines[i]) {
		return 0
	}
	s.tool("go")
	it, end := parseGoPanic(s.lines, i)
	s.r.Errors = append(s.r.Errors, it.Item)
	return end
}

// goPackageDone attributes pending failures to the package that just ended,
// so their locations get the package directory.
func goPackageDone(s *state, pkg string) {
	if s.goPanicked != "" {
		s.r.Notes = append(s.r.Notes, "package "+pkg+": "+s.goPanicked+" panicked, so the tests after it in this package didn't run")
		s.goPanicked = ""
	}
	dir := goPkgDir(pkg)
	for _, it := range s.goPending {
		switch {
		case it.Loc != "" && !strings.Contains(it.Loc, "/") && dir != "":
			it.Loc = path.Join(dir, it.Loc)
			for k, b := range it.Body { // the panic site is in the same package
				if p, ok := strings.CutPrefix(b, "panicked at "); ok && !strings.Contains(p, "/") {
					it.Body[k] = "panicked at " + path.Join(dir, p)
				}
			}
		case it.Loc == "" && dir != "":
			it.Loc = dir + "/"
		case it.Loc == "" || !strings.Contains(it.Loc, "/"):
			it.Body = append(it.Body, "in package "+pkg)
		}
	}
	s.goPending = nil
}

// goSummary counts packages and -v results, keeps -v log lines per test,
// and swallows harness chatter.
func goSummary(s *state, i int) int {
	l := s.lines[i]
	switch {
	case goOkRe.MatchString(l):
		s.tool("go test")
		s.r.Tests.PkgOK++
		goPackageDone(s, goOkRe.FindStringSubmatch(l)[1])
	case goPkgFailRe.MatchString(l):
		s.tool("go test")
		m := goPkgFailRe.FindStringSubmatch(l)
		s.r.Tests.PkgFailed++
		if m[3] != "" {
			s.r.Notes = append(s.r.Notes, "package "+m[1]+": "+m[3]+" failed")
		}
		goPackageDone(s, m[1])
	case goNoTestsRe.MatchString(l):
		s.r.Tests.PkgNoTests++
	case goPassRe.MatchString(l):
		s.tool("go test")
		s.r.Tests.Passed++
	case goSkipRe.MatchString(l):
		s.r.Tests.Skipped++
	case goRunRe.MatchString(l):
		s.tool("go test")
		s.goRun = goRunRe.FindStringSubmatch(l)[2]
	case s.tools["go test"] && strings.HasPrefix(l, "    ") && goLogLocRe.MatchString(l):
		// t.Log / t.Error output while a test runs (-v)
		if s.goRun != "" {
			s.goLogs[s.goRun] = append(s.goLogs[s.goRun], l)
		}
	case s.tools["go test"] && (l == "PASS" || l == "FAIL" || strings.HasPrefix(l, "exit status ")):
	case strings.HasPrefix(l, "warning: GOPATH set to GOROOT"):
	default:
		return 0
	}
	return i + 1
}
