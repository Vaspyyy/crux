package main

import (
	"strings"

	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/work"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
	"github.com/Vaspyyy/crux/internal/render"
)

// result is a finished run as JSON: Prism's report.Crux (docs/output.md in
// Prism). It has every error, failure and warning with its refs, not just
// those the text's budget shows, unless -b was given: then the ones the
// text report lists. Lists are capped at report.MaxItems; truncated says
// what a limit left out (nil if nothing).
//
// Locations carry the context marks of the symbol they're in (st may be
// off). reserve is the lines the text report keeps for next: lines, so
// that with -b the result lists what the text does.
func result(run render.Run, r *digest.Report, rs *refs.Resolver, o opts, st *work.State, reserve int) (*report.Crux, *report.Truncated) {
	c := &report.Crux{
		Command:    run.Argv,
		Exit:       run.Exit,
		DurationMS: run.Duration.Milliseconds(),
		Tools:      append([]string{}, r.Tools...),
		Lines:      r.Lines,
		Log:        run.LogPath,
	}
	if run.Signal != 0 {
		c.Signal = render.SignalName(run.Signal)
	}
	if r.HasTests {
		c.Tests = tests(r)
	}
	nErr, nFail, nWarn := len(r.Errors), len(r.Failures), len(r.Warnings)
	if o.budgetSet {
		nErr, nFail, nWarn = render.Listed(run, r, render.Options{Budget: o.budget, Refs: rs.Lines, Reserve: reserve})
	}
	var cut cut
	c.Errors = items(r.Errors, nErr, rs, st, &cut)
	c.Failures = items(r.Failures, nFail, rs, st, &cut)
	c.Warnings = items(r.Warnings, nWarn, rs, st, &cut)
	c.Notes = r.Notes[:cut.keep(len(r.Notes), len(r.Notes))]
	if tailWanted(r, run.Exit) {
		c.Tail = r.Tail
	}
	return c, cut.truncated()
}

// tests are the counts as the text's tests: line has them.
func tests(r *digest.Report) *report.CruxTests {
	t := r.Tests
	failed := max(t.Failed, len(r.Failures))
	pkgs := t.PkgOK + t.PkgFailed
	return &report.CruxTests{
		Passed: t.Passed, Failed: failed, Skipped: t.Skipped,
		XFailed: t.XFailed, XPassed: t.XPassed, Errors: t.Errors,
		PkgOK: t.PkgOK, PkgFailed: t.PkgFailed, PkgNoTests: t.PkgNoTests, PkgNoRun: t.PkgNoRun,
		CountsUnknown: t.Unknown,
		// whenever the text says "no tests ran": also when a -run filter
		// matched nothing in any package, or only errors outside tests came
		NoneRan: t.NoneRan || t.Passed == 0 && failed == 0 &&
			(t.Errors == 0 && pkgs > 0 && t.PkgNoRun == pkgs || t.Errors > 0 && pkgs == 0),
	}
}

// items turns the first n of a report's items into JSON ones.
func items(its []*digest.Item, n int, rs *refs.Resolver, st *work.State, cut *cut) []report.CruxItem {
	out := []report.CruxItem{}
	for _, it := range its[:cut.keep(len(its), n)] {
		out = append(out, item(it, rs, st, cut))
	}
	return out
}

// item is one error, failure or warning. loc.in and test are the refs the
// text prints as "in" and "test" lines (a failure without a location has
// no loc, and its test names it); folded errors' other locations get
// theirs too, each location the context marks of the symbol in in, and
// the test its own (test_marks).
func item(it *digest.Item, rs *refs.Resolver, st *work.State, cut *cut) report.CruxItem {
	ci := report.CruxItem{Kind: it.Kind, Title: it.Title}
	if it.Count > 1 && it.Norm != "" {
		ci.Title = it.Norm // folded warnings: the message without the names, which go in names
	}
	in, test := rs.Refs(it)
	ci.Test, ci.TestMarks = test, marks(st, test)
	if ci.Loc = rs.Location(it.Loc, it.Missing); ci.Loc != nil {
		ci.Loc.In, ci.Loc.Marks = in, marks(st, in)
		ci.Missing = it.Missing
	} else if it.Pkg != "" && it.Kind == "fail" {
		ci.Pkg = rs.Path(it.Pkg, true) // go test: no line, only the package
	}
	ci.Body = it.Body[:cut.keep(len(it.Body), len(it.Body))]
	if it.Count > 1 {
		ci.Count = it.Count
		names := distinct(it.Names)
		ci.Names = names[:cut.keep(len(names), len(names))]
	}
	for _, at := range it.Locs[:cut.keep(len(it.Locs), len(it.Locs))] {
		at, missing := strings.CutSuffix(at, " (not found)") // how tidy marks a folded location that's missing
		if l := rs.Location(at, missing); l != nil {
			if it.Kind != "warning" {
				l.In = rs.In(at, missing)
				l.Marks = marks(st, l.In)
			}
			ci.Locs = append(ci.Locs, *l)
		}
	}
	return ci
}

// marks returns the context marks of a symbol the result names, counted
// as shown.
func marks(st *work.State, sym string) []string {
	if sym == "" {
		return nil
	}
	return st.Marks(sym)
}

// distinct returns folded warnings' names once each, in order, without
// their quote marks (`a`, ‘a’, "a").
func distinct(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if r := []rune(n); len(r) > 2 {
			n = string(r[1 : len(r)-1])
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// tailWanted reports whether the end of the output belongs in the result:
// when the text report shows it, for a failure nothing structured explains,
// or for a successful command that isn't a test run (its output may be
// the point).
func tailWanted(r *digest.Report, exit int) bool {
	if exit != 0 {
		explained := len(r.Errors)+len(r.Failures) > 0 && !r.Generic || len(r.Notes) > 0
		return !explained
	}
	return !r.HasTests && len(r.Errors)+len(r.Warnings) == 0
}

// cut counts what limits left out of the result's lists, for the
// envelope's "truncated".
type cut struct{ shown, total int }

// keep returns how many of a list's n entries the result keeps: at most
// limit, and never more than report.MaxItems.
func (c *cut) keep(n, limit int) int {
	k := min(n, limit, report.MaxItems)
	if k < n {
		c.shown += k
		c.total += n
	}
	return k
}

func (c *cut) truncated() *report.Truncated {
	if c.total == 0 {
		return nil
	}
	return &report.Truncated{Shown: c.shown, Total: c.total}
}
