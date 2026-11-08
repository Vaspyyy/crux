package refs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vaspyyy/prism/repo"

	"github.com/Vaspyyy/crux/internal/digest"
	"github.com/Vaspyyy/crux/internal/render"
)

// The fixtures are real output of go, g++, pytest and cargo, run in
// testdata/proj (written there as /work/proj). That directory stands in for
// the repository; testdata/outside lies outside it.

func project(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// run digests a fixture as crux would in cwd (a directory of the project),
// and returns a resolver for it.
func run(t *testing.T, name, cwd string) (*digest.Report, *Resolver) {
	t.Helper()
	proj := project(t)
	dir := filepath.Join(proj, filepath.FromSlash(cwd))
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		lines = append(lines, digest.Clean(strings.ReplaceAll(l, "/work/proj", proj)))
	}
	modDir, _ := filepath.Rel(dir, proj)
	digest.Relative, digest.CheckFiles = dir, true
	digest.GoModule, digest.GoModuleDir = "example.com/proj", filepath.ToSlash(modDir)
	t.Cleanup(func() {
		digest.Relative, digest.CheckFiles = "", false
		digest.GoModule, digest.GoModuleDir = "", ""
	})
	r := New(dir)
	r.repo = &repo.Info{Root: proj} // not a git repository: the project is the root
	return digest.Digest(lines, 1), r
}

func TestFixtures(t *testing.T) {
	// errors by location, failures by title: the lines under each, "" for none
	cases := []struct {
		fixture, cwd string
		want         map[string]string
	}{
		{"go-build.out", ".", map[string]string{
			"broken/broken.go:3:13":  "", // a package-level var: outside any definition
			"broken/broken.go:10:28": "in broken/broken.go:Queue.Push",
			"broken/broken.go:15:15": "in broken/broken.go:Len",
		}},
		{"gcc.out", ".", map[string]string{
			"../outside/far.h:2:12": "", // outside the repository
			"lib.cpp:8:12":          "in lib.cpp:parse#2",
			"lib.cpp:11:24":         "", // at global scope
		}},
		{"go-test.out", ".", map[string]string{
			"TestAdd":          "in calc/calc_test.go:check | test calc/calc_test.go:TestAdd",
			"TestDiv/negative": "in calc/calc_test.go:TestDiv",
			"TestNoMessage":    "in calc/calc_test.go:TestNoMessage", // no location: the test itself
		}},
		{"pytest.out", ".", map[string]string{
			"test_count":             "in tests/test_store.py:test_count",
			"TestStore.test_missing": "in tests/test_store.py:TestStore.test_missing",
		}},
		{"pytest-line.out", ".", map[string]string{
			"tests/test_store.py::test_count":              "in tests/test_store.py:test_count",
			"tests/test_store.py::TestStore::test_missing": "in store.py:Store.load | test tests/test_store.py:TestStore.test_missing",
		}},
		// run in a subdirectory: refs stay relative to the root
		{"cargo-test.out", "shapes", map[string]string{
			"shape::tests::negative": "in shapes/src/shape.rs:Circle.area | test shapes/src/shape.rs:tests.negative",
			"tests::adds_wrong":      "in shapes/src/lib.rs:tests.adds_wrong",
			"tests::returns_err":     "in shapes/src/lib.rs:tests.returns_err", // no location
		}},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			rep, r := run(t, c.fixture, c.cwd)
			got := map[string]string{}
			for _, it := range append(append([]*digest.Item{}, rep.Errors...), rep.Failures...) {
				key := it.Loc
				if it.Kind == "fail" {
					key = it.Title
				}
				got[key] = strings.Join(r.Lines(it), " | ")
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				for k, v := range got {
					if w, ok := c.want[k]; !ok || w != v {
						t.Errorf("%s: got %q, want %q", k, v, w)
					}
				}
				for k := range c.want {
					if _, ok := got[k]; !ok {
						t.Errorf("%s: no such item", k)
					}
				}
			}
		})
	}
}

func TestEdgeCases(t *testing.T) {
	_, r := run(t, "go-test.out", ".")
	for _, c := range []struct {
		name string
		it   digest.Item
		want string
	}{
		{"warnings get nothing", digest.Item{Kind: "warning", Loc: "calc/calc.go:9:2"}, ""},
		{"missing files get nothing", digest.Item{Kind: "error", Loc: "calc/calc.go:9:2", Missing: true}, ""},
		{"no outline rules", digest.Item{Kind: "error", Loc: "go.mod:1"}, ""},
		{"not a file", digest.Item{Kind: "error", Loc: "[eval]:1:7"}, ""},
		{"absolute path in the repository", digest.Item{Kind: "error", Loc: filepath.Join(project(t), "calc", "calc.go") + ":9:2"}, "in calc/calc.go:Acc.Add"},
		{"installed packages are outside", digest.Item{Kind: "error", Loc: "venv/lib/site-packages/x.py:1"}, ""},
		// a location outside any definition still names the failing test, but not as "in"
		{"test outside its location", digest.Item{Kind: "fail", Title: "TestAdd", Runner: "go test", Pkg: "calc", Loc: "calc/calc.go:1"}, "test calc/calc_test.go:TestAdd"},
		{"go test outside a module", digest.Item{Kind: "fail", Title: "TestAdd", Runner: "go test", Loc: "calc/calc_test.go:7"}, "in calc/calc_test.go:check"},
		{"unknown go test", digest.Item{Kind: "fail", Title: "TestGone", Runner: "go test", Pkg: "calc"}, ""},
		{"only failures name a test", digest.Item{Kind: "error", Title: "TestAdd", Runner: "go test", Pkg: "calc"}, ""},
		{"pytest parameters", digest.Item{Kind: "fail", Title: "tests/test_store.py::test_count[a::b]", Runner: "pytest"}, "in tests/test_store.py:test_count"},
		{"cargo: one part is an exact name", digest.Item{Kind: "fail", Title: "area", Runner: "cargo test", Loc: "shapes/src/lib.rs:2"}, ""},
		{"cargo doctest", digest.Item{Kind: "fail", Title: "src/lib.rs - add (line 3)", Runner: "cargo test"}, ""},
	} {
		if got := strings.Join(r.Lines(&c.it), " | "); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// Each file is read once, and only for the items that ask.
func TestParsesLazily(t *testing.T) {
	_, r := run(t, "go-test.out", ".")
	if len(r.files) != 0 {
		t.Fatalf("files read before any item asked: %v", r.files)
	}
	it := &digest.Item{Kind: "error", Loc: "calc/calc.go:9:2"}
	r.Lines(it)
	r.Lines(it)
	if len(r.files) != 1 {
		t.Errorf("files = %v", r.files)
	}
}

// The whole report, as an agent sees it.
func TestReport(t *testing.T) {
	rep, r := run(t, "go-build.out", ".")
	out := render.Report(render.Run{Argv: []string{"go", "build", "./broken/"}, Exit: 1, Lines: make([]string, 4)}, rep,
		render.Options{Budget: 60, Refs: r.Lines})
	want := `FAILED  go build ./broken/  (exit 1, 0ms, 4 lines of output)

3 errors:
  broken/broken.go:3:13  undefined: undefinedLimit
  broken/broken.go:10:28  undefined: y
    in broken/broken.go:Queue.Push
  broken/broken.go:15:15  q.itemz undefined (type *Queue has no field or method itemz)
    in broken/broken.go:Len
`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}
