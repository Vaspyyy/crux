package main

// What crux does with Prism's context (docs/context.md in Prism): it reads
// what changed and the focus recap last showed, marks the symbols errors
// and failures are in, suggests next steps, and records its run so seek
// and recap can mark what failed. Context only marks and suggests: it
// never changes which errors and failures the report lists.

import (
	"strings"

	"github.com/Vaspyyy/prism/changes"
	"github.com/Vaspyyy/prism/focus"
	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/repo"
	"github.com/Vaspyyy/prism/report"
	"github.com/Vaspyyy/prism/work"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/refs"
)

const (
	maxRecorded    = focus.MaxSymbols // errors and failures the last-run record looks at
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
// (git add in a script) would fail on the lock, so it runs without the
// optional lock (changes.Options.NoLocks), as editors run their background
// git status.
func loadContext(cwd string, layers work.Layer, off bool) *runContext {
	c := &runContext{done: make(chan struct{})}
	if off {
		c.st = work.LoadWith(nil, work.Options{Off: true})
		close(c.done)
		return c
	}
	go func() {
		defer close(c.done)
		c.info, _ = repo.Detect(cwd)
		c.st = work.LoadWith(c.info, work.Options{Layers: layers, Changes: changes.Options{NoLocks: true}})
	}()
	return c
}

// wait returns the context once it's loaded.
func (c *runContext) wait() (*repo.Info, *work.State) {
	<-c.done
	return c.info, c.st
}

// markedLines is the report's "in" and "test" lines with the context marks
// of their symbols: "in internal/group/group.go:multiline [changed]".
func markedLines(rs *refs.Resolver, st *work.State) func(*digest.Item) []string {
	return func(it *digest.Item) []string {
		ls := rs.Lines(it)
		for i, l := range ls {
			if word, r, ok := strings.Cut(l, " "); ok && (word == "in" || word == "test") {
				ls[i] = l + work.Suffix(st.Marks(r))
			}
		}
		return ls
	}
}

// lastRun is the record of a finished run that seek and recap read: the
// command, its exit code, the failed tests and the symbols errors and
// failures are in. The store drops duplicates (and a failed test that
// contains its own failure from In) and caps the lists; this only bounds
// the lookups.
func lastRun(argv []string, exit int, r *digest.Report, rs *refs.Resolver) focus.LastRun {
	lr := focus.LastRun{Cmd: report.Cmd(argv...), Exit: exit}
	looked := 0
	add := func(list *[]string, ref string) {
		if ref != "" {
			*list = append(*list, ref)
		}
	}
	for _, it := range errorsAndFailures(r, maxRecorded) {
		in, test := rs.Refs(it)
		add(&lr.Tests, test)
		add(&lr.In, in)
		for _, at := range it.Locs { // the other locations of folded errors
			if looked++; looked > 2*maxRecorded {
				break
			}
			at, missing := strings.CutSuffix(at, " (not found)")
			add(&lr.In, rs.In(at, missing))
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
		if cmd, change, mark := recap(st, sym); cmd != "" {
			seen[sym] = true
			st.Used(mark) // the context: line names the layer the suggestion rests on
			st.Use(work.Suggest)
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
// that context marks, that change in words, and the mark it rests on; ""
// when the symbol isn't marked. It only peeks: a suggestion isn't a
// printed mark.
func recap(st *work.State, sym string) (cmd, change, mark string) {
	n := name(sym)
	for _, m := range st.Peek(sym) {
		switch f := st.Focus; {
		case m == work.MarkChanged:
			return report.Cmd("recap", "--show", sym), "the uncommitted change to " + n, m
		case m == work.MarkBranch && st.Changes.BranchBase != "":
			return report.Cmd("recap", "--show", sym, short(st.Changes.BranchBase)), "this branch's change to " + n, m
		case m == work.MarkFocus && f.New != "" && strings.Contains(f.Range, "..") && f.Old != "":
			return report.Cmd("recap", "--show", sym, short(f.Old)+".."+short(f.New)), "the change to " + n + " in focus " + f.Name(), m
		case m == work.MarkFocus && f.New != "":
			return report.Cmd("recap", "show", short(f.New), "--show", sym), "the change to " + n + " in focus " + f.Name(), m
		}
	}
	return "", "", ""
}

// name is a symbol ref's qualified name, without path and ordinal.
func name(sym string) string { return ref.Parse(sym, nil).Name }

// short abbreviates a commit hash as the context: line does.
func short(h string) string { return h[:min(len(h), 7)] }
