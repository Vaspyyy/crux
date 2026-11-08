package main

import (
	"strings"

	"github.com/Vaspyyy/prism/report"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
	"github.com/Vaspyyy/crux/internal/render"
)

// result is a finished run as JSON: Prism's report.Crux (docs/output.md in
// Prism). It has every error, failure and warning with its refs, not just
// those the text's budget shows, unless -b was given: then the ones the
// text report lists. Lists are capped at report.MaxItems; truncated says
// what a limit left out (nil if nothing).
func result(run render.Run, r *digest.Report, rs *refs.Resolver, o opts) (*report.Crux, *report.Truncated) {
	c := &report.Crux{
		Command:    run.Argv,
		Exit:       run.Exit,
		DurationMS: run.Duration.Milliseconds(),
		Tools:      append([]string{}, r.Tools...),
		Lines:      r.Lines,
		Log:        run.LogPath,
	}
	if r.HasTests {
		c.Tests = tests(r)
	}
	nErr, nFail, nWarn := len(r.Errors), len(r.Failures), len(r.Warnings)
	if o.budgetSet {
		nErr, nFail, nWarn = render.Listed(run, r, render.Options{Budget: o.budget, Refs: rs.Lines})
	}
	var cut cut
	c.Errors = items(r.Errors, nErr, rs, &cut)
	c.Failures = items(r.Failures, nFail, rs, &cut)
	c.Warnings = items(r.Warnings, nWarn, rs, &cut)
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
		PkgOK: t.PkgOK, PkgFailed: t.PkgFailed, PkgNoTests: t.PkgNoTests,
		// whenever the text says "no tests ran": also when a -run filter
		// matched nothing in any package, or only errors outside tests came
		NoneRan: t.NoneRan || t.Passed == 0 && failed == 0 &&
			(t.Errors == 0 && pkgs > 0 && t.PkgNoRun == pkgs || t.Errors > 0 && pkgs == 0),
	}
}

// items turns the first n of a report's items into JSON ones.
func items(its []*digest.Item, n int, rs *refs.Resolver, cut *cut) []report.CruxItem {
	out := []report.CruxItem{}
	for _, it := range its[:cut.keep(len(its), n)] {
		out = append(out, item(it, rs, cut))
	}
	return out
}

// item is one error, failure or warning. loc.in and test are the refs the
// text prints as "in" and "test" lines (a failure without a location has
// no loc, and its test names it); folded errors' other locations get
// theirs too.
func item(it *digest.Item, rs *refs.Resolver, cut *cut) report.CruxItem {
	ci := report.CruxItem{Kind: it.Kind, Title: it.Title}
	if it.Count > 1 && it.Norm != "" {
		ci.Title = it.Norm // folded warnings: the message without the one name, as the text has it
	}
	in, test := rs.Refs(it)
	ci.Test = test
	if ci.Loc = rs.Location(it.Loc, it.Missing); ci.Loc != nil {
		ci.Loc.In = in
	}
	ci.Body = it.Body[:cut.keep(len(it.Body), len(it.Body))]
	if it.Count > 1 {
		ci.Count = it.Count
	}
	for _, at := range it.Locs[:cut.keep(len(it.Locs), len(it.Locs))] {
		at, missing := strings.CutSuffix(at, " (not found)") // how tidy marks a folded location that's missing
		if l := rs.Location(at, missing); l != nil {
			if it.Kind != "warning" {
				l.In = rs.In(at, missing)
			}
			ci.Locs = append(ci.Locs, *l)
		}
	}
	return ci
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
