package digest

import (
	"regexp"
	"strings"
)

var (
	tscPlainRe   = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\): (error|warning) (TS\d+): (.+)$`)
	tscPrettyRe  = regexp.MustCompile(`^(\S+?):(\d+):(\d+) - (error|warning) (TS\d+): (.+)$`)
	tscFoundRe   = regexp.MustCompile(`^Found (\d+) errors?\b`)
	gccRe        = regexp.MustCompile(`^(.+?):(\d+):(\d+): (fatal error|error|warning|note): (.+)$`)
	gccFrameRe   = regexp.MustCompile(`^\s*\d*\s*\|`)
	gccContextRe = regexp.MustCompile(`^(.+?): (In (function|member function|constructor|destructor|instantiation of)|At top level)|^In file included from|^\s+from \S+:\d+`)
	gccCountRe   = regexp.MustCompile(`^\d+ (errors?|warnings?)( and \d+ (errors?|warnings?))? generated\.$`)
	javacRe      = regexp.MustCompile(`^(.+\.java):(\d+): (error|warning): (.+)$`)
	javacCountRe = regexp.MustCompile(`^\d+ (errors?|warnings?)$`)
	eslintFileRe = regexp.MustCompile(`^(/\S+|\S+\.(?:[cm]?[jt]sx?|vue|svelte))$`)
	eslintItemRe = regexp.MustCompile(`^\s+(\d+):(\d+)\s+(error|warning)\s+(.+?)\s{2,}(\S+)$`)
	eslintSumRe  = regexp.MustCompile(`^✖ \d+ problems?`)
	nodeErrRe    = regexp.MustCompile(`^(?:Uncaught )?((?:[A-Z]\w*)?(?:Error|Exception))(?:: (.*))?$`)
	nodeAtRe     = regexp.MustCompile(`^\s+at (?:.*\()?(.+?:\d+:\d+)\)?$`)
	nodeHdrRe    = regexp.MustCompile(`^(\S+):(\d+)$`)
	makeErrRe    = regexp.MustCompile(`^(g?make|ninja)(\[\d+\])?: (\*\*\* .+|build stopped.*)$`)
	makeEnterRe  = regexp.MustCompile("Entering directory ['‘`](.+?)['’]$")
	makeDirRe    = regexp.MustCompile(`^g?make(\[\d+\])?: (Entering|Leaving|Nothing to be done)`)
	npmErrRe     = regexp.MustCompile(`^npm (ERR!|error)( |$)`)
	npmWarnRe    = regexp.MustCompile(`^npm (WARN|warn) (.*)$`)
)

func loc3(file, line, col string) string {
	if col == "" {
		return file + ":" + line
	}
	return file + ":" + line + ":" + col
}

func addItem(s *state, it *Item) {
	switch it.Kind {
	case "error":
		s.r.Errors = append(s.r.Errors, it)
	case "warning":
		s.r.Warnings = append(s.r.Warnings, it)
	}
}

// tscError handles TypeScript diagnostics, plain and --pretty.
func tscError(s *state, i int) int {
	if m := tscPlainRe.FindStringSubmatch(s.lines[i]); m != nil {
		s.tool("tsc")
		addItem(s, &Item{Kind: m[4], Title: m[5] + ": " + m[6], Loc: loc3(m[1], m[2], m[3])})
		return i + 1
	}
	m := tscPrettyRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	s.tool("tsc")
	it := &Item{Kind: m[4], Title: m[5] + ": " + m[6], Loc: loc3(m[1], m[2], m[3])}
	end := i + 1
	blank := 0
	for end < len(s.lines) && !tscPrettyRe.MatchString(s.lines[end]) && !tscFoundRe.MatchString(s.lines[end]) {
		t := strings.TrimSpace(s.lines[end])
		if t == "" {
			if blank++; blank == 2 {
				break
			}
		} else {
			blank = 0
			it.Body = append(it.Body, strings.TrimRight(s.lines[end], " "))
		}
		end++
	}
	it.Body = capLines(it.Body, 8) // related information ("'x' is declared here.") comes last
	addItem(s, it)
	return end
}

func tscSummary(s *state, i int) int {
	if tscFoundRe.MatchString(s.lines[i]) {
		s.tool("tsc")
		return i + 1
	}
	return 0
}

// gccStyle handles file:line:col: error|warning|note: … (gcc, clang, many
// others) plus the code frame below it. Notes attach to the item before.
func gccStyle(s *state, i int) int {
	l := s.lines[i]
	if gccContextRe.MatchString(l) || gccCountRe.MatchString(l) {
		return i + 1
	}
	m := gccRe.FindStringSubmatch(l)
	if m == nil {
		return 0
	}
	if strings.HasSuffix(m[1], ".go") || strings.HasPrefix(m[1], " ") {
		return 0
	}
	s.tool("c/c++")
	end := i + 1
	var frame []string
	for end < len(s.lines) && gccFrameRe.MatchString(s.lines[end]) {
		frame = append(frame, strings.TrimRight(s.lines[end], " "))
		end++
	}
	loc := loc3(m[1], m[2], m[3])
	if m[4] == "note" {
		var last *Item
		if n := len(s.r.Errors); n > 0 {
			last = s.r.Errors[n-1]
		}
		if last != nil && len(last.Body) < 8 {
			last.Body = append(last.Body, "note: "+m[5]+" ("+loc+")")
		}
		return end
	}
	kind := "error"
	if m[4] == "warning" {
		kind = "warning"
	}
	addItem(s, &Item{Kind: kind, Title: m[5], Loc: loc, Body: capLines(frame, 4)})
	return end
}

// javacError handles File.java:12: error: … plus its frame.
func javacError(s *state, i int) int {
	m := javacRe.FindStringSubmatch(s.lines[i])
	if m == nil {
		return 0
	}
	s.tool("javac")
	it := &Item{Kind: m[3], Title: m[4], Loc: m[1] + ":" + m[2]}
	end := i + 1
	for end < len(s.lines) && strings.HasPrefix(s.lines[end], " ") && !javacRe.MatchString(s.lines[end]) {
		it.Body = append(it.Body, strings.TrimRight(s.lines[end], " "))
		end++
	}
	it.Body = capLines(it.Body, 5)
	addItem(s, it)
	return end
}

func javacSummary(s *state, i int) int {
	if javacCountRe.MatchString(s.lines[i]) && len(s.tools) > 0 {
		return i + 1
	}
	return 0
}

// eslint handles the default "stylish" format.
func eslint(s *state, i int) int {
	l := s.lines[i]
	if eslintSumRe.MatchString(l) {
		s.tool("eslint")
		return i + 1
	}
	m := eslintFileRe.FindStringSubmatch(l)
	if m == nil || i+1 >= len(s.lines) || !eslintItemRe.MatchString(s.lines[i+1]) {
		return 0
	}
	s.tool("eslint")
	end := i + 1
	for end < len(s.lines) {
		it := eslintItemRe.FindStringSubmatch(s.lines[end])
		if it == nil {
			break
		}
		addItem(s, &Item{Kind: it[3], Title: it[4] + " (" + it[5] + ")", Loc: loc3(m[1], it[1], it[2])})
		end++
	}
	return end
}

// nodeError handles an uncaught JS error with its stack, keeping the first
// frames outside node internals and node_modules.
func nodeError(s *state, i int) int {
	m := nodeErrRe.FindStringSubmatch(s.lines[i])
	if m == nil || i+1 >= len(s.lines) || !nodeAtRe.MatchString(s.lines[i+1]) {
		return 0
	}
	s.tool("node")
	title := m[1]
	if m[2] != "" {
		title += ": " + m[2]
	}
	it := &Item{Kind: "error", Title: title}
	end := i + 1
	for end < len(s.lines) && nodeAtRe.MatchString(s.lines[end]) {
		frame := strings.TrimSpace(s.lines[end])
		if !strings.Contains(frame, "node:internal") && !strings.Contains(frame, "node_modules") && len(it.Body) < 3 {
			if it.Loc == "" {
				it.Loc = nodeAtRe.FindStringSubmatch(s.lines[end])[1]
			}
			it.Body = append(it.Body, frame)
		}
		end++
	}
	// node prints "file:line / code / ^" above the error: show the code, and
	// use it as the location when the stack has none of the user's frames
	// (syntax errors)
	for j := max(0, i-5); j < i; j++ {
		if h := nodeHdrRe.FindStringSubmatch(s.lines[j]); h != nil && j+1 < i && !s.used[j] {
			it.Body = append([]string{strings.TrimRight(s.lines[j+1], " ")}, it.Body...)
			if it.Loc == "" {
				it.Loc = h[1] + ":" + h[2]
			}
			s.consume(j, i)
			break
		}
	}
	if end < len(s.lines) && strings.HasPrefix(s.lines[end], "Node.js v") {
		end++
	}
	if end+1 < len(s.lines) && strings.TrimSpace(s.lines[end]) == "" && strings.HasPrefix(s.lines[end+1], "Node.js v") {
		end += 2
	}
	s.r.Errors = append(s.r.Errors, it)
	return end
}

func makeError(s *state, i int) int {
	l := s.lines[i]
	if makeDirRe.MatchString(l) {
		if m := makeEnterRe.FindStringSubmatch(l); m != nil {
			s.roots = append(s.roots, m[1])
		}
		return i + 1
	}
	if makeErrRe.MatchString(l) {
		return i + 1 // the exit code already says it; "last lines" shows it when nothing else explains the failure
	}
	return 0
}

// npmError folds a run of "npm error" lines into one item.
func npmError(s *state, i int) int {
	if w := npmWarnRe.FindStringSubmatch(s.lines[i]); w != nil {
		s.tool("npm")
		addItem(s, &Item{Kind: "warning", Title: w[2]})
		return i + 1
	}
	if !npmErrRe.MatchString(s.lines[i]) {
		return 0
	}
	s.tool("npm")
	it := &Item{Kind: "error"}
	end := i
	for end < len(s.lines) && npmErrRe.MatchString(s.lines[end]) {
		t := strings.TrimSpace(npmErrRe.ReplaceAllString(s.lines[end], ""))
		switch {
		case t == "", strings.HasPrefix(t, "A complete log of this run"), strings.HasPrefix(t, "code "):
		case it.Title == "":
			it.Title = t
		case len(it.Body) < 5:
			it.Body = append(it.Body, t)
		}
		end++
	}
	if it.Title == "" {
		it.Title = "npm failed"
	}
	s.r.Errors = append(s.r.Errors, it)
	return end
}
