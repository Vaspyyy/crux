package digest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	rustDiagRe    = regexp.MustCompile(`^(error|warning)(\[(E\d+)\])?: (.+)$`)
	rustArrowRe   = regexp.MustCompile(`^\s*--> (.+)$`)
	rustCodeRe    = regexp.MustCompile(`^\s*\d+\s*\|`)
	rustMarkRe    = regexp.MustCompile(`^\s*\|.*[\^\-~]`)
	rustSuggestRe = regexp.MustCompile(`^\s*\d+ - `) // the old line of a suggestion; the new one (+) is kept
	rustAddRe     = regexp.MustCompile(`^\s*\d+ \+ `)
	rustLabelRe   = regexp.MustCompile(`^\s*\|\s+\S`)
	rustTestHdrRe = regexp.MustCompile(`^---- (.+) stdout ----$`)
	rustPanicRe   = regexp.MustCompile(`^thread '([^']+)'(?: \(\d+\))? panicked at (.+):$`)
	rustResultRe  = regexp.MustCompile(`^test result: (ok|FAILED)\. (\d+) passed; (\d+) failed; (\d+) ignored`)
	rustTestLine  = regexp.MustCompile(`^test (\S+) \.\.\. (ok|FAILED|ignored)`)
)

// rustDiag handles rustc diagnostics (error[E0308]: … --> file:line:col …)
// and cargo's one-line summaries.
func rustDiag(s *state, i int) int {
	m := rustDiagRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	kind, code, msg := m[1], m[3], m[4]
	loc := ""
	if i+1 < len(s.lines) {
		if a := rustArrowRe.FindStringSubmatch(s.lines[i+1]); a != nil {
			loc = a[1]
		}
	}
	if loc == "" {
		// cargo's own lines: "error: could not compile `x` …", "warning: `x` generated 3 warnings"
		switch {
		case strings.HasPrefix(msg, "test failed, to rerun"):
			s.tool("cargo")
			if !CargoNoFailFast {
				s.r.Notes = append(s.r.Notes, "cargo stops at the first failing target: later targets and doctests didn't run (--no-fail-fast)")
			}
			return i + 1
		case strings.HasPrefix(msg, "could not compile"),
			strings.Contains(msg, "generated ") && strings.Contains(msg, "warning"),
			strings.HasSuffix(msg, "warnings emitted"), strings.HasSuffix(msg, "warning emitted"):
			s.tool("cargo")
			return i + 1
		case kind == "error" && (strings.HasPrefix(msg, "Recipe `") || strings.Contains(msg, "aborting due to")):
			return i + 1
		}
		return 0 // a generic "error: …" line; let other extractors or the fallback have it
	}
	s.tool("cargo")
	end := i + 1
	for end < len(s.lines) && strings.TrimSpace(s.lines[end]) != "" {
		end++
	}
	var body []string
	for _, l := range s.lines[i+2 : end] {
		t := strings.TrimSpace(l)
		switch {
		case t == "|" || t == "":
		case rustSuggestRe.MatchString(l):
		case rustAddRe.MatchString(l):
			body = append(body, strings.TrimRight(l, " ")+"   (suggested)")
		case rustCodeRe.MatchString(l), rustMarkRe.MatchString(l), rustLabelRe.MatchString(l),
			strings.HasPrefix(t, "= help:"), strings.HasPrefix(t, "help:"), strings.HasPrefix(t, "= note:"):
			body = append(body, strings.TrimRight(l, " "))
		}
	}
	title := msg
	if code != "" {
		title = code + ": " + msg
	}
	it := &Item{Kind: kind, Title: title, Loc: loc, Body: capLines(body, 8)}
	for _, l := range body { // the primary label says what differs between errors with one message
		if d := rustDetailRe.FindStringSubmatch(l); d != nil {
			it.Detail = d[1]
			break
		}
	}
	if kind == "error" {
		s.r.Errors = append(s.r.Errors, it)
	} else {
		s.r.Warnings = append(s.r.Warnings, it)
	}
	return end
}

// rustTestFailure handles the "---- name stdout ----" blocks of cargo test.
func rustTestFailure(s *state, i int) int {
	m := rustTestHdrRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	s.tool("cargo test")
	it := &Item{Kind: "fail", Title: m[1], Runner: "cargo test"}
	end := i + 1
	for ; end < len(s.lines); end++ {
		l := s.lines[end]
		if rustTestHdrRe.MatchString(l) || l == "failures:" || rustResultRe.MatchString(l) {
			break
		}
		t := strings.TrimSpace(l)
		switch {
		case t == "", strings.HasPrefix(t, "note: run with `RUST_BACKTRACE"):
		case rustPanicRe.MatchString(l):
			it.Loc = rustPanicRe.FindStringSubmatch(l)[2]
		default:
			it.Body = append(it.Body, strings.TrimRight(l, " "))
		}
	}
	// doctests panic inside a temporary file; the test name has the real place
	if strings.Contains(it.Loc, "rustdoctest") || strings.Contains(it.Loc, "doctest_bundle") {
		if d := rustDoctestRe.FindStringSubmatch(it.Title); d != nil {
			it.Loc = d[1] + ":" + d[2]
		}
	}
	it.Body = capLines(it.Body, 15)
	s.r.Failures = append(s.r.Failures, it)
	return end
}

var rustDetailRe = regexp.MustCompile(`\^+ (.+)$`)

var rustDoctestRe = regexp.MustCompile(`^(\S+) - .*\(line (\d+)\)$`)

// rustPanic handles a panic outside the test harness (cargo run, a binary).
func rustPanic(s *state, i int) int {
	m := rustPanicRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	it := &Item{Kind: "error", Title: "panic in thread '" + m[1] + "'", Loc: m[2]}
	end := i + 1
	for ; end < len(s.lines) && strings.TrimSpace(s.lines[end]) != ""; end++ {
		if strings.HasPrefix(s.lines[end], "note: run with `RUST_BACKTRACE") {
			end++
			break
		}
		it.Body = append(it.Body, s.lines[end])
	}
	if len(it.Body) > 0 {
		it.Title = "panic: " + strings.TrimSpace(it.Body[0])
		it.Body = it.Body[1:]
	}
	it.Body = capLines(it.Body, 4)
	s.r.Errors = append(s.r.Errors, it)
	return end
}

// rustSummary counts "test result:" lines and swallows the harness chatter.
func rustSummary(s *state, i int) int {
	l := s.lines[i]
	if m := rustResultRe.FindStringSubmatch(l); m != nil {
		s.tool("cargo test")
		p, _ := strconv.Atoi(m[2])
		f, _ := strconv.Atoi(m[3])
		g, _ := strconv.Atoi(m[4])
		s.r.Tests.Passed += p
		s.r.Tests.Failed += f
		s.r.Tests.Skipped += g
		return i + 1
	}
	if l == "failures:" { // the list of failed test names repeats the blocks above
		end := i + 1
		for end < len(s.lines) && (strings.HasPrefix(s.lines[end], "    ") || strings.TrimSpace(s.lines[end]) == "") {
			end++
		}
		return end
	}
	if rustTestLine.MatchString(l) {
		return i + 1
	}
	return 0
}

func capLines(ls []string, n int) []string {
	if len(ls) <= n {
		return ls
	}
	return append(ls[:n:n], fmt.Sprintf("… +%d more lines", len(ls)-n))
}
