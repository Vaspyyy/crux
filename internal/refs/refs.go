// Package refs names the symbol an error or failure is in, and the failing
// test's own definition, as Prism refs (internal/group/group.go:multiline).
// seek and recap accept these as is, so crux's report can be handed on
// without working out which function a file:line belongs to.
//
// "in" only says that the location lies inside the symbol's lines, as
// outline finds them (basis "location" in Prism's evidence rules).
package refs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Vaspyyy/prism/outline"
	"github.com/Vaspyyy/prism/ref"
	"github.com/Vaspyyy/prism/repo"
	"github.com/Vaspyyy/prism/report"

	"github.com/Vaspyyy/crux/internal/digest"
)

const (
	maxSymbols    = 1 << 20 // outline.Parse's limit: every symbol of a file
	maxCrateFiles = 5000    // .rs files read to find a cargo test; more and the test gets no ref
)

// Resolver finds the refs for a report's items. It runs nothing up front:
// the repository is detected and each file parsed (once) only when an item
// needs it (the text report asks only for the items it shows).
type Resolver struct {
	cwd     string     // item locations are relative to it
	repo    *repo.Info // nil until needed
	noRepo  bool       // detecting it failed
	files   map[string]*file
	crates  map[string][]sym
	dirTest map[string][]string // go package dir: its _test.go files
}

// file is a parsed source file in the repository.
type file struct {
	rel  string // repo-relative path
	ts   []ref.Target
	refs []ref.Ref // with ordinals over the whole file
}

type sym struct {
	t   ref.Target
	ref ref.Ref
}

// New returns a resolver for locations relative to cwd.
func New(cwd string) *Resolver {
	return &Resolver{cwd: cwd, files: map[string]*file{}, crates: map[string][]sym{}, dirTest: map[string][]string{}}
}

// SetRepo hands the resolver the repository cwd is in, when the caller
// has already detected it, so it isn't detected twice.
func (r *Resolver) SetRepo(info *repo.Info) {
	if info != nil {
		r.repo, r.noRepo = info, false
	}
}

// Lines returns what to print under an error or failure: "in <ref>" for
// the innermost symbol containing its location, and for a failed test
// "test <ref>" when the test is a different symbol. A failure without a
// location gets "in <test ref>". Warnings get nothing.
func (r *Resolver) Lines(it *digest.Item) []string {
	if it.Kind != "error" && it.Kind != "fail" {
		return nil
	}
	in, located := r.in(it.Loc, it.Missing)
	test := r.test(it)
	switch {
	case in != "" && (test == "" || test == in):
		return []string{"in " + in}
	case in != "":
		return []string{"in " + in, "test " + test}
	case test != "" && !located:
		return []string{"in " + test}
	case test != "":
		// the location lies outside any definition, or outside the repository
		return []string{"test " + test}
	}
	return nil
}

// Refs returns an error's or failure's refs without the text's rules for
// which to print: in, the innermost symbol containing its location, and
// test, a failed test's own definition. Each is "" when unknown; warnings
// get neither.
func (r *Resolver) Refs(it *digest.Item) (in, test string) {
	if it.Kind != "error" && it.Kind != "fail" {
		return "", ""
	}
	in, _ = r.in(it.Loc, it.Missing)
	return in, r.test(it)
}

// In returns the ref of the innermost symbol containing a location (one of
// an item's folded locations), or "".
func (r *Resolver) In(loc string, missing bool) string {
	in, _ := r.in(loc, missing)
	return in
}

// Repo returns the repository the working directory is in (outside git:
// the directory itself), or nil when it couldn't be read.
func (r *Resolver) Repo() *repo.Info { return r.repoInfo() }

// Words returns the identifiers that appear as whole words in the lines
// of a symbol, given by a ref that Lines, Refs or In returned. nil when
// the symbol can't be found. A name in the set is mentioned by the symbol
// in Prism's sense (basis "reference"): textually, maybe a different
// symbol of the same name.
func (r *Resolver) Words(symRef string) map[string]bool {
	info := r.repoInfo()
	sr := ref.Parse(symRef, nil)
	if info == nil || sr.Name == "" {
		return nil
	}
	abs := filepath.Join(info.Root, filepath.FromSlash(sr.Path))
	f := r.file(abs)
	if f == nil {
		return nil
	}
	for i, fr := range f.refs {
		if fr.String() != symRef {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		t := f.ts[i]
		if t.Line < 1 || t.Line > len(lines) {
			return nil
		}
		words := map[string]bool{}
		for _, l := range lines[t.Line-1 : max(min(t.End, len(lines)), t.Line)] {
			for _, w := range strings.FieldsFunc(l, notWord) {
				words[w] = true
			}
		}
		return words
	}
	return nil
}

func notWord(c rune) bool { return !(c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c)) }

// locRe takes ":line[:col]" off the end of a location, so its path may
// contain ':'.
var locRe = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?$`)

// Location turns a location as the report has it (relative to the working
// directory, or as the tool printed it) into a Prism location: its path
// repo-relative inside the repository and absolute outside it. A file that
// doesn't exist ("<stdin>", or missing) keeps the path as reported. nil
// when loc has no line (go test points some failures at a package
// directory). In is left for the caller.
func (r *Resolver) Location(loc string, missing bool) *report.Location {
	m := locRe.FindStringSubmatch(loc)
	if m == nil {
		return nil
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	if line < 1 {
		return nil
	}
	l := &report.Location{Path: m[1], Line: line, Col: col}
	if !missing {
		l.Path = r.Path(m[1], false)
	}
	return l
}

// Path turns a file's (or with dir, a directory's) path from the report
// into the form JSON has: repo-relative inside the repository, absolute
// outside it, and as reported when there's no such file or directory.
func (r *Resolver) Path(p string, dir bool) string {
	abs := r.abs(p)
	if st, err := os.Stat(abs); err != nil || st.IsDir() != dir {
		return p
	}
	if info := r.repoInfo(); info != nil {
		if rel, ok := info.Rel(abs); ok {
			return rel
		}
	}
	return abs
}

// in returns the ref of the innermost symbol containing a location.
// located reports whether it is a file:line location at all (go test
// points failures without one at the package directory).
func (r *Resolver) in(at string, missing bool) (in string, located bool) {
	loc := ref.Parse(at, nil)
	if !loc.IsLocation() {
		return "", false
	}
	if missing {
		return "", true
	}
	f := r.file(loc.Path)
	if f == nil {
		return "", true
	}
	if i := ref.Innermost(f.ts, f.rel, loc.Line); i >= 0 {
		return f.refs[i].String(), true
	}
	return "", true
}

// abs makes a path from the tool's output absolute.
func (r *Resolver) abs(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(r.cwd, p)
}

func (r *Resolver) repoInfo() *repo.Info {
	if r.repo == nil && !r.noRepo {
		in, err := repo.Detect(r.cwd)
		r.repo, r.noRepo = in, err != nil
	}
	return r.repo
}

// file parses p (relative to cwd, or absolute) once. It is nil for files
// that don't exist, have no outline rules, or lie outside the repository
// (outside git: outside the working directory). Installed packages count as
// outside even in a virtualenv or node_modules inside the repository.
func (r *Resolver) file(p string) *file {
	abs := r.abs(p)
	if f, ok := r.files[abs]; ok {
		return f
	}
	r.files[abs] = nil
	if !outline.Supported(abs) || dependency(abs) {
		return nil
	}
	info := r.repoInfo()
	if info == nil {
		return nil
	}
	rel, ok := info.Rel(abs)
	if !ok {
		return nil
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() || st.Size() > outline.MaxSize {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	ts := ref.Targets(rel, outline.Parse(abs, data, maxSymbols))
	f := &file{rel: rel, ts: ts, refs: ref.Refs(ts)}
	r.files[abs] = f
	return f
}

// dependency reports whether p is in an installed package rather than the
// project's own code.
func dependency(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		switch part {
		case "node_modules", "site-packages", "dist-packages":
			return true
		}
	}
	return false
}

// exact returns the ref of the one symbol in f with exactly this qualified
// name, or "" if there is none or more than one.
func (f *file) exact(name, kind string) string {
	found := -1
	for i, t := range f.ts {
		if t.Name == name && (kind == "" || t.Kind == kind) {
			if found >= 0 {
				return ""
			}
			found = i
		}
	}
	if found < 0 {
		return ""
	}
	return f.refs[found].String()
}

// test returns the ref of a failed test's definition, or "" when it can't
// be found with certainty.
func (r *Resolver) test(it *digest.Item) string {
	if it.Kind != "fail" {
		return ""
	}
	switch it.Runner {
	case "go test":
		return r.goTest(it)
	case "pytest":
		return r.pyTest(it)
	case "cargo test":
		return r.cargoTest(it)
	}
	return ""
}

// goTest finds "func TestX" in the _test.go files of the package's
// directory. A subtest (TestX/case) is defined by its parent.
func (r *Resolver) goTest(it *digest.Item) string {
	if it.Pkg == "" {
		return ""
	}
	name, _, _ := strings.Cut(it.Title, "/")
	dir := r.abs(it.Pkg)
	files, ok := r.dirTest[dir]
	if !ok {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
		r.dirTest[dir] = files
	}
	var found []string
	for _, p := range files {
		if f := r.file(p); f != nil {
			if t := f.exact(name, "func"); t != "" {
				found = append(found, t)
			}
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	// defined in several files (build tags): the one the failure points into
	loc := ref.Parse(it.Loc, nil)
	if f := r.file(loc.Path); f != nil && loc.IsLocation() && !it.Missing {
		for _, t := range found {
			if strings.HasPrefix(t, f.rel+":") {
				return t
			}
		}
	}
	return ""
}

// pyTest finds a pytest test: "path::Class::test" names it with its file
// (relative to pytest's rootdir), "Class.test" (the header of a long
// traceback) without, so it is looked up in the file of the failure's first
// frame, which is the test's own. Parameters ("[1-2]") are dropped.
func (r *Resolver) pyTest(it *digest.Item) string {
	title := it.Title
	if i := strings.IndexByte(title, '['); i > 0 && strings.HasSuffix(title, "]") {
		title = title[:i]
	}
	if path, rest, ok := strings.Cut(title, "::"); ok {
		if f := r.pyFile(path, it); f != nil {
			return f.exact(strings.ReplaceAll(rest, "::", "."), "")
		}
		return ""
	}
	loc := ref.Parse(it.Loc, nil)
	if !loc.IsLocation() || it.Missing {
		return ""
	}
	if f := r.file(loc.Path); f != nil {
		return f.exact(title, "")
	}
	return ""
}

// pyFile finds the file of a pytest node id. Its path is relative to
// pytest's rootdir: usually the working directory, else the failure's own
// file or a parent directory within the repository.
func (r *Resolver) pyFile(p string, it *digest.Item) *file {
	cands := []string{r.abs(p)}
	if loc := ref.Parse(it.Loc, nil); loc.IsLocation() && !it.Missing {
		if a := r.abs(loc.Path); strings.HasSuffix(filepath.ToSlash(a), "/"+filepath.ToSlash(filepath.Clean(p))) {
			cands = append(cands, a)
		}
	}
	if info := r.repoInfo(); info != nil {
		for dir := filepath.Dir(r.cwd); ; dir = filepath.Dir(dir) {
			if _, ok := info.Rel(dir); !ok {
				break
			}
			cands = append(cands, filepath.Join(dir, p))
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return r.file(c)
		}
	}
	return nil
}

// cargoTest finds a cargo test ("module::tests::name") among the functions
// of its crate: the one whose qualified name ends in "tests.name" (the
// last two parts of the test's path, since the file names the module).
// More than one candidate means no ref: a wrong ref is worse than none.
func (r *Resolver) cargoTest(it *digest.Item) string {
	if strings.ContainsAny(it.Title, " \t") { // doctests: "src/lib.rs - add (line 5)"
		return ""
	}
	parts := strings.Split(it.Title, "::")
	want := parts[len(parts)-1]
	if len(parts) >= 2 {
		want = parts[len(parts)-2] + "." + want
	}
	start := r.cwd
	if loc := ref.Parse(it.Loc, nil); loc.IsLocation() && !it.Missing {
		start = filepath.Dir(r.abs(loc.Path))
	}
	root := r.crateRoot(start)
	if root == "" {
		return ""
	}
	found := ""
	for _, s := range r.crate(root) {
		if s.t.Kind == "fn" && (s.t.Name == want || len(parts) >= 2 && strings.HasSuffix(s.t.Name, "."+want)) {
			if found != "" {
				return ""
			}
			found = s.ref.String()
		}
	}
	return found
}

// crateRoot is the directory of the nearest Cargo.toml at or above dir,
// within the repository.
func (r *Resolver) crateRoot(dir string) string {
	info := r.repoInfo()
	if info == nil {
		return ""
	}
	for {
		if _, ok := info.Rel(dir); !ok {
			return ""
		}
		if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}

// crate returns the symbols of every .rs file under root, skipping build
// output and hidden directories.
func (r *Resolver) crate(root string) []sym {
	if syms, ok := r.crates[root]; ok {
		return syms
	}
	var paths []string
	tooMany := false
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && p != root && (d.Name() == "target" || strings.HasPrefix(d.Name(), ".")):
			return filepath.SkipDir
		case !d.IsDir() && strings.HasSuffix(p, ".rs"):
			if len(paths) >= maxCrateFiles {
				tooMany = true
				return filepath.SkipAll
			}
			paths = append(paths, p)
		}
		return nil
	})
	var syms []sym
	if !tooMany {
		slices.Sort(paths)
		for _, p := range paths {
			if f := r.file(p); f != nil {
				for i, t := range f.ts {
					syms = append(syms, sym{t, f.refs[i]})
				}
			}
		}
	}
	r.crates[root] = syms
	return syms
}
