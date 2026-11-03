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
	pyLocRe      = regexp.MustCompile(`^(\S+\.py):(\d+): ?(\w*)$`)
	pyShortRe    = regexp.MustCompile(`^(FAILED|ERROR) (\S+)(?: - (.*))?$`)
	pyFinalRe    = regexp.MustCompile(`^=+ (.+?) in [\d.]+s(?: \([^)]*\))? =+$`)
	pyCountRe    = regexp.MustCompile(`(\d+) (passed|failed|skipped|errors?|xfailed|xpassed|deselected|warnings?)`)
	pyProgressRe = regexp.MustCompile(`^\S+\.py [.FEsxX]+\s*(\[\s*\d+%\])?$`)
	pyFrameRe    = regexp.MustCompile(`^  File "(.+)", line (\d+), in (.+)$`)
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
		for end < len(s.lines) {
			b := pyBlockRe.FindStringSubmatch(s.lines[end])
			if b == nil {
				break
			}
			it := &Item{Kind: "fail", Title: b[1]}
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
					if e := strings.TrimSpace(l[1:]); e != "" {
						it.Body = append(it.Body, "E "+e)
					}
				default:
					if loc := pyLocRe.FindStringSubmatch(t); loc != nil {
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
			it.Body = capLines(it.Body, 6)
			s.r.Failures = append(s.r.Failures, it)
		}
		return end
	case m[1] == "short test summary info":
		s.tool("pytest")
		end := i + 1
		have := len(s.r.Failures) > 0
		for end < len(s.lines) {
			sm := pyShortRe.FindStringSubmatch(s.lines[end])
			if sm == nil {
				break
			}
			if !have { // -q / --tb=no runs only have this list
				it := &Item{Kind: "fail", Title: sm[2]}
				if sm[3] != "" {
					it.Body = []string{sm[3]}
				}
				s.r.Failures = append(s.r.Failures, it)
			}
			end++
		}
		return end
	case strings.Contains(m[1], "warnings summary"):
		s.tool("pytest")
		end := i + 1 // keep the count from the final line, drop the details
		for end < len(s.lines) && !pyBannerRe.MatchString(s.lines[end]) && !strings.HasPrefix(s.lines[end], "-- Docs:") {
			end++
		}
		if end < len(s.lines) && strings.HasPrefix(s.lines[end], "-- Docs:") {
			end++
		}
		return end
	}
	return 0
}

// pytestSummary parses "=== 3 failed, 1 passed in 0.03s ===" and progress lines.
func pytestSummary(s *state, i int) int {
	l := s.lines[i]
	if pyProgressRe.MatchString(l) {
		s.tool("pytest")
		return i + 1
	}
	m := pyFinalRe.FindStringSubmatch(l)
	if m == nil {
		return 0
	}
	s.tool("pytest")
	for _, c := range pyCountRe.FindAllStringSubmatch(m[1], -1) {
		n, _ := strconv.Atoi(c[1])
		switch c[2] {
		case "passed", "xpassed":
			s.r.Tests.Passed += n
		case "failed", "error", "errors":
			s.r.Tests.Failed += n
		case "skipped", "xfailed", "deselected":
			s.r.Tests.Skipped += n
		}
	}
	return i + 1
}

// pyTraceback handles a Python traceback: the exception plus the frame
// that raised it.
func pyTraceback(s *state, i int) int {
	if s.lines[i] != "Traceback (most recent call last):" {
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
	// the innermost frames tell the story; keep the last two
	if len(frames) > 0 {
		it.Loc = frames[len(frames)-1].loc
		for _, f := range frames[max(0, len(frames)-2):] {
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
