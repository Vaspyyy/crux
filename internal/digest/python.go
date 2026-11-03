package digest

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	pyBannerRe   = regexp.MustCompile(`^=+ (.+?) =+$`)
	pyBlockRe    = regexp.MustCompile(`^_{3,} (.+?) _{3,}$`)
	pySepRe      = regexp.MustCompile(`^(_ )+_?$`)
	pyLocRe      = regexp.MustCompile(`^(\S+\.py):(\d+):(?: in .+| ?\w*)$`)
	pyBangRe     = regexp.MustCompile(`^!{3,} (.+?) !{3,}$`)
	pyTbLineRe   = regexp.MustCompile(`^(\S+\.py):(\d+): (.+)$`)
	pyStopRe     = regexp.MustCompile(`^stopping after (\d+) failures?$`)
	pyShortRe    = regexp.MustCompile(`^(FAILED|ERROR) (\S+)(?: - (.*))?$`)
	pyFinalRe    = regexp.MustCompile(`^=+ (.+?) in [\d.]+s(?: \([^)]*\))? =+$`)
	pyCountRe    = regexp.MustCompile(`(\d+) (passed|failed|skipped|errors?|xfailed|xpassed|deselected|warnings?)`)
	pyProgressRe = regexp.MustCompile(`^(\S+\.py )?[.FEsxX]+\s*\[\s*\d+%\]$|^\S+\.py [.FEsxX]+$`)
	pyFrameRe    = regexp.MustCompile(`^  File "(.+)", line (\d+), in (.+)$`)
	pyQuietRe    = regexp.MustCompile(`^(\d+ (passed|failed|skipped|errors?|xfailed|xpassed|deselected|warnings?)(, )?)+ in [\d.]+s( \([^)]*\))?$`)
	pyWarnRe     = regexp.MustCompile(`^\s+(\S+\.py):(\d+): (\w*Warning): (.+)$`)
	pyEFileRe    = regexp.MustCompile(`File "(.+?)", line (\d+)`)
)

// pytestSections handles the FAILURES / ERRORS sections and the header.
func pytestSections(s *state, i int) int {
	m := pyBannerRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	switch {
	case m[1] == "test session starts":
		s.tool("pytest")
		end := i + 1 // platform, rootdir, plugins, … up to "collected N items"
		for end < len(s.lines) && end < i+15 {
			l := s.lines[end]
			end++
			if strings.HasPrefix(l, "collected ") || strings.HasPrefix(l, "collecting ") {
				break
			}
		}
		return end
	case m[1] == "FAILURES" || m[1] == "ERRORS":
		s.tool("pytest")
		end := i + 1
		if end < len(s.lines) && !pyBlockRe.MatchString(s.lines[end]) {
			// --tb=line: "E   message" and "file.py:12: message" per failure
			for end < len(s.lines) && !pyBannerRe.MatchString(s.lines[end]) {
				if t := pyTbLineRe.FindStringSubmatch(s.lines[end]); t != nil && !pyLibPath(t[1]) {
					s.pyTbLocs = append(s.pyTbLocs, t[1]+":"+t[2])
				}
				end++
			}
			return end
		}
		for end < len(s.lines) {
			b := pyBlockRe.FindStringSubmatch(s.lines[end])
			if b == nil {
				break
			}
			it := &Item{Kind: "fail", Title: b[1]}
			if m[1] == "ERRORS" { // collection or fixture errors: no test ran
				it.Kind = "error"
			}
			end++
			var raised string
			for end < len(s.lines) && !pyBlockRe.MatchString(s.lines[end]) && !pyBannerRe.MatchString(s.lines[end]) {
				l := s.lines[end]
				t := strings.TrimSpace(l)
				switch {
				case pySepRe.MatchString(t):
				case strings.HasPrefix(t, "> "), strings.HasPrefix(t, ">\t"):
					it.Body = append(it.Body, t)
				case strings.HasPrefix(l, "E "):
					if e := strings.TrimRight(l[1:], " "); strings.TrimSpace(e) != "" {
						it.Body = append(it.Body, "E"+e) // keep the alignment of diffs and carets
					}
					if f := pyEFileRe.FindStringSubmatch(l); f != nil && it.Kind == "error" {
						raised = f[1] + ":" + f[2]
					}
				default:
					if loc := pyLocRe.FindStringSubmatch(t); loc != nil && !pyLibPath(loc[1]) {
						if it.Loc == "" {
							it.Loc = loc[1] + ":" + loc[2]
						} else {
							raised = loc[1] + ":" + loc[2]
						}
					}
				}
				end++
			}
			if raised != "" && raised != it.Loc {
				it.Body = append(it.Body, "raised at "+raised)
			}
			if it.Kind == "error" {
				// title: the exception, not "ERROR collecting x.py"
				for k := len(it.Body) - 1; k >= 0; k-- {
					if strings.HasPrefix(it.Body[k], "E ") && strings.Contains(it.Body[k], "Error") {
						it.Body = append([]string{b[1]}, it.Body...)
						it.Title = strings.TrimSpace(it.Body[k+1][1:])
						break
					}
				}
				if raised != "" {
					it.Loc = raised
					for k, b := range it.Body {
						if b == "raised at "+raised {
							it.Body = append(it.Body[:k], it.Body[k+1:]...)
							break
						}
					}
				}
				it.Body = capLines(it.Body, 6)
				s.r.Errors = append(s.r.Errors, it)
				continue
			}
			it.Body = capLines(it.Body, 15)
			s.r.Failures = append(s.r.Failures, it)
		}
		return end
	case m[1] == "short test summary info":
		s.tool("pytest")
		end := i + 1
		have := len(s.r.Failures)+len(s.r.Errors) > 0
		for end < len(s.lines) {
			sm := pyShortRe.FindStringSubmatch(s.lines[end])
			if sm == nil {
				break
			}
			if !have && sm[1] == "FAILED" { // --tb=no/line runs only have this list
				it := &Item{Kind: "fail", Title: sm[2]}
				if sm[3] != "" {
					it.Body = []string{sm[3]}
				}
				if k := len(s.r.Failures); k < len(s.pyTbLocs) {
					it.Loc = s.pyTbLocs[k]
				}
				s.r.Failures = append(s.r.Failures, it)
			}
			end++
		}
		return end
	case strings.Contains(m[1], "warnings summary"):
		s.tool("pytest")
		end := i + 1
		for end < len(s.lines) && !pyBannerRe.MatchString(s.lines[end]) && !strings.HasPrefix(s.lines[end], "-- Docs:") {
			if w := pyWarnRe.FindStringSubmatch(s.lines[end]); w != nil {
				s.r.Warnings = append(s.r.Warnings, &Item{Kind: "warning", Title: w[3] + ": " + w[4], Loc: w[1] + ":" + w[2]})
			}
			end++
		}
		if end < len(s.lines) && strings.HasPrefix(s.lines[end], "-- Docs:") {
			end++
		}
		return end
	}
	return 0
}

// pyLibPath is code that isn't the project's: pytest's own frames, the
// standard library, installed packages.
func pyLibPath(p string) bool {
	return strings.Contains(p, "site-packages/") || strings.Contains(p, "/lib/python") || strings.HasPrefix(p, "<")
}

// pytestSummary parses "=== 3 failed, 1 passed in 0.03s ===" and progress lines.
func pytestSummary(s *state, i int) int {
	l := s.lines[i]
	if pyProgressRe.MatchString(l) {
		s.tool("pytest")
		return i + 1
	}
	if m := pyBangRe.FindStringSubmatch(l); m != nil && (s.tools["pytest"] || strings.HasPrefix(m[1], "Interrupted: ")) {
		// pytest's "!!! … !!!" banners: the run stopped before the end
		s.tool("pytest")
		end := i + 1
		switch msg := m[1]; {
		case strings.HasPrefix(msg, "Interrupted: "):
			s.r.Notes = append(s.r.Notes, "pytest stopped early: "+strings.TrimPrefix(msg, "Interrupted: ")+", so no tests ran")
		case msg == "KeyboardInterrupt":
			where := ""
			if end < len(s.lines) {
				if t := pyTbLineRe.FindStringSubmatch(s.lines[end]); t != nil {
					where = " at " + t[1] + ":" + t[2]
					end++
				}
			}
			if end < len(s.lines) && strings.HasPrefix(s.lines[end], "(to show a full traceback on KeyboardInterrupt") {
				end++
			}
			s.r.Notes = append(s.r.Notes, "pytest was interrupted (KeyboardInterrupt"+where+"), so the remaining tests didn't run")
		case pyStopRe.MatchString(msg):
			n := pyStopRe.FindStringSubmatch(msg)[1]
			word := "failures"
			if n == "1" {
				word = "failure"
			}
			s.r.Notes = append(s.r.Notes, "pytest stopped after "+n+" "+word+" (-x or --maxfail), so the remaining tests didn't run")
		default:
			s.r.Notes = append(s.r.Notes, "pytest: "+msg)
		}
		return end
	}
	summary := ""
	if m := pyFinalRe.FindStringSubmatch(l); m != nil {
		summary = m[1]
	} else if pyQuietRe.MatchString(l) { // pytest -q: no === border
		summary = l
	}
	if summary == "" {
		return 0
	}
	s.tool("pytest")
	if strings.HasPrefix(summary, "no tests ran") {
		s.r.Tests.NoneRan = true
	}
	for _, c := range pyCountRe.FindAllStringSubmatch(summary, -1) {
		n, _ := strconv.Atoi(c[1])
		switch c[2] {
		case "passed":
			s.r.Tests.Passed += n
		case "xpassed":
			s.r.Tests.XPassed += n
		case "failed":
			s.r.Tests.Failed += n
		case "error", "errors":
			s.r.Tests.Errors += n
		case "skipped", "deselected":
			s.r.Tests.Skipped += n
		case "xfailed":
			s.r.Tests.XFailed += n
		}
	}
	return i + 1
}

// pyTraceback handles a Python traceback: the exception plus the frame
// that raised it.
func pyTraceback(s *state, i int) int {
	if !strings.HasSuffix(s.lines[i], "Traceback (most recent call last):") {
		return 0
	}
	s.tool("python")
	type frame struct{ loc, fn, code string }
	var frames []frame
	end := i + 1
	for end < len(s.lines) && strings.HasPrefix(s.lines[end], " ") {
		if f := pyFrameRe.FindStringSubmatch(s.lines[end]); f != nil {
			fr := frame{loc: f[1] + ":" + f[2], fn: f[3]}
			if end+1 < len(s.lines) && strings.HasPrefix(s.lines[end+1], "    ") && !pyFrameRe.MatchString(s.lines[end+1]) {
				fr.code = strings.TrimSpace(s.lines[end+1])
			}
			frames = append(frames, fr)
		}
		end++
	}
	it := &Item{Kind: "error", Title: "Python exception"}
	if end < len(s.lines) {
		it.Title = strings.TrimSpace(s.lines[end])
		end++
	}
	// the innermost frames tell the story; keep the last three
	if len(frames) > 0 {
		it.Loc = frames[len(frames)-1].loc
		if n := len(frames) - 3; n > 0 {
			it.Body = append(it.Body, "(+"+strconv.Itoa(n)+" earlier frames)")
		}
		for _, f := range frames[max(0, len(frames)-3):] {
			line := f.loc + " in " + f.fn
			if f.code != "" {
				line += ": " + f.code
			}
			it.Body = append(it.Body, line)
		}
	}
	s.r.Errors = append(s.r.Errors, it)
	return end
}
