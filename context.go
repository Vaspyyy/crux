package main

// What crux does with Prism's context (docs/context.md in Prism): it reads
// what changed and the focus recap last showed, marks the symbols errors
// and failures are in, suggests next steps, and records its run so seek
// and recap can mark what failed. Context only marks and suggests: it
// never changes which errors and failures the report lists.

import (
	"os"
	"strings"

	"github.com/Vaspyyy/prism/focus"
	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/repo"
	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/work"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
)

const (
	maxRecorded    = focus.MaxSymbols // errors and failures the last-run record looks at, and refs it keeps per list
	nextItems      = 20               // errors and failures next: lines look at
	maxContextNext = 2                // next: lines that come from context
)

// runContext is the context of one run, loaded while the command runs.
type runContext struct {
	done chan struct{}
	info *repo.Info // the repository of the working directory; nil when context is off
	st   *work.State
}

// loadContext starts loading the context of the repository cwd is in:
// the given layers, and the store the run is recorded in. With off
// (--no-context or PRISM_NO_CONTEXT) nothing is read.
//
// It runs while the command does, so its git calls run beside the
// command's own. git status would take the index lock to write back a
// refreshed index, and a command that writes the index in that moment
// (git add in a script) would fail on the lock, so these calls run with
// GIT_OPTIONAL_LOCKS=0, as editors run their background git status. The
// command's environment is taken before, so it doesn't get the variable.
func loadContext(cwd string, layers work.Layer, off bool) *runContext {
	c := &runContext{done: make(chan struct{})}
	if off {
		c.st = work.Load(nil, 0, true)
		close(c.done)
		return c
	}
	restore := setenv("GIT_OPTIONAL_LOCKS", "0")
	go func() {
		defer close(c.done)
		defer restore()
		c.info, _ = repo.Detect(cwd)
		c.st = work.Load(c.info, layers, false)
	}()
	return c
}

// wait returns the context once it's loaded.
func (c *runContext) wait() (*repo.Info, *work.State) {
	<-c.done
	return c.info, c.st
}

// setenv sets a variable of crux's own environment and returns what puts
// the old value back.
func setenv(k, v string) func() {
	old, had := os.LookupEnv(k)
	os.Setenv(k, v)
	return func() {
		if had {
			os.Setenv(k, old)
		} else {
			os.Unsetenv(k)
		}
	}
}

// markedLines is the report's "in" and "test" lines with the context marks
// of the symbol in the "in" line: "in internal/group/group.go:multiline
// [changed]".
func markedLines(rs *refs.Resolver, st *work.State) func(*digest.Item) []string {
	return func(it *digest.Item) []string {
		ls := rs.Lines(it)
		for i, l := range ls {
			if r, ok := strings.CutPrefix(l, "in "); ok {
				ls[i] = l + work.Suffix(st.Marks(r))
			}
		}
		return ls
	}
}

// lastRun is the record of a finished run that seek and recap read: the
// command, its exit code, the failed tests and the symbols errors and
// failures are in (a failed test that contains its own failure is only
// in Tests).
func lastRun(argv []string, exit int, r *digest.Report, rs *refs.Resolver) focus.LastRun {
	lr := focus.LastRun{Cmd: report.Cmd(argv...), Exit: exit}
	tests, in := map[string]bool{}, []string{}
	for _, it := range errorsAndFailures(r, maxRecorded) {
		i, t := rs.Refs(it)
		if t != "" && !tests[t] && len(lr.Tests) < maxRecorded {
			tests[t] = true
			lr.Tests = append(lr.Tests, t)
		}
		in = append(in, i)
		for _, at := range it.Locs { // the other locations of folded errors
			if len(in) >= 2*maxRecorded {
				break
			}
			at, missing := strings.CutSuffix(at, " (not found)")
			in = append(in, rs.In(at, missing))
		}
	}
	seen := map[string]bool{}
	for _, i := range in {
		if i != "" && !tests[i] && !seen[i] && len(lr.In) < maxRecorded {
			seen[i] = true
			lr.In = append(lr.In, i)
		}
	}
	return lr
}

// errorsAndFailures returns the first n errors and failures, errors first,
// as the report lists them.
func errorsAndFailures(r *digest.Report, n int) []*digest.Item {
	its := append(append([]*digest.Item{}, r.Errors...), r.Failures...)
	return its[:min(len(its), n)]
}

// suggest returns the next: lines, at most report.MaxNext: from context,
// recap for a changed or focus symbol that an error or failure is in, that
// failed, or that the symbol or failed test mentions; and without context,
// seek for the first failed test's definition. The wording follows the
// evidence: "in" for a location, "mentions" for a name found in the text,
// "failed" for a test run.
func suggest(r *digest.Report, rs *refs.Resolver, st *work.State) (fromContext, plain []report.Next) {
	for _, it := range r.Failures[:min(len(r.Failures), nextItems)] {
		if _, test := rs.Refs(it); test != "" {
			plain = append(plain, report.Next{Cmd: report.Cmd("seek", test), Why: "the failed test's definition"})
			break
		}
	}
	if st == nil || st.Off || st.Changes == nil && st.Focus == nil {
		return nil, plain
	}
	room := min(maxContextNext, report.MaxNext-len(plain))
	seen := map[string]bool{}
	add := func(sym, tail string) {
		if len(fromContext) >= room || seen[sym] {
			return
		}
		if cmd, change := recap(st, sym); cmd != "" {
			seen[sym] = true
			st.Marks(sym) // the suggestion shows what context knows about sym: the context: line names its layer
			fromContext = append(fromContext, report.Next{Cmd: cmd, Why: change + tail})
		}
	}
	items := errorsAndFailures(r, nextItems)
	// location and execution: what an error or failure is in, and failed tests
	for _, it := range items {
		in, test := rs.Refs(it)
		where := ", where the error is"
		if it.Kind == "fail" {
			where = ", where the failure is"
		}
		if in != "" {
			add(in, where)
		}
		if test != "" && test != in {
			add(test, ", the failed test")
		}
	}
	// reference: changed and focus symbols mentioned by name in the symbol
	// an error is in, or in a failed test
	cands := candidates(st)
	for _, it := range items {
		if len(fromContext) >= room {
			break
		}
		in, test := rs.Refs(it)
		sym := in
		if test != "" {
			sym = test
		}
		if sym == "" {
			continue
		}
		words := rs.Words(sym)
		for _, c := range cands {
			if c.ref != sym && words[c.short] {
				tail := ", which " + name(sym) + " mentions"
				if c.short != name(c.ref) {
					tail = "; " + name(sym) + " mentions " + c.short
				}
				add(c.ref, tail)
			}
		}
	}
	return fromContext, plain
}

// candidate is a changed or focus symbol a failed test or a symbol with an
// error may mention.
type candidate struct{ ref, short string }

// candidates returns the changed and focus symbols, in order, whose short
// name (the last part of a qualified name, as code calls it) belongs to
// only one of them: a name two symbols share doesn't say which one is
// meant.
func candidates(st *work.State) []candidate {
	var refs []string
	if st.Changes != nil {
		for _, s := range st.Changes.Symbols {
			refs = append(refs, s.Ref)
		}
	}
	if st.Focus != nil {
		refs = append(refs, st.Focus.Symbols...)
	}
	return candidatesOf(refs)
}

func candidatesOf(refs []string) []candidate {
	byShort := map[string]map[string]bool{}
	var out []candidate
	for _, r := range refs {
		n := name(r)
		short := n[strings.LastIndexByte(n, '.')+1:]
		if short == "" {
			continue
		}
		if byShort[short] == nil {
			byShort[short] = map[string]bool{}
		}
		if !byShort[short][r] {
			byShort[short][r] = true
			out = append(out, candidate{r, short})
		}
	}
	unique := out[:0]
	for _, c := range out {
		if len(byShort[c.short]) == 1 {
			unique = append(unique, c)
		}
	}
	return unique
}

// recap returns the recap command that shows what changed in a symbol
// that context marks, and that change in words; "" when it isn't marked.
func recap(st *work.State, sym string) (cmd, change string) {
	n := name(sym)
	switch st.Changes.Mark(sym) {
	case work.MarkChanged:
		return report.Cmd("recap", "--show", sym), "the uncommitted change to " + n
	case work.MarkBranch:
		if st.Changes.BranchBase != "" {
			return report.Cmd("recap", "--show", sym, short(st.Changes.BranchBase)), "this branch's change to " + n
		}
	}
	if f := st.Focus; f.Has(sym) && f.New != "" {
		if strings.Contains(f.Range, "..") && f.Old != "" {
			return report.Cmd("recap", "--show", sym, short(f.Old)+".."+short(f.New)), "the change to " + n + " in focus " + f.Range
		}
		return report.Cmd("recap", "show", short(f.New), "--show", sym), "the change to " + n + " in focus commit " + short(f.New)
	}
	return "", ""
}

// name is a symbol ref's qualified name, without path and ordinal.
func name(sym string) string { return ref.Parse(sym, nil).Name }

// short abbreviates a commit hash as the context: line does.
func short(h string) string { return h[:min(len(h), 7)] }
