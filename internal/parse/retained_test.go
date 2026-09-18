package parse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/model"
)

func retainedTestWrite(t *testing.T, root, path, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// retainedTestAnalysis is an analysis of: app imports service and store;
// service imports store; tool imports nothing.
func retainedTestAnalysis(stamp analysisStamp) *retainedAnalysis {
	pkg := func(path, dir string, imports ...string) *retainedPackage {
		return &retainedPackage{model: &model.Package{ImportPath: path, RelDir: dir}, dir: dir, imports: imports}
	}
	return &retainedAnalysis{stamp: stamp, modulePath: "example.test/app", packages: map[string]*retainedPackage{
		"example.test/app":         pkg("example.test/app", "", "example.test/app/service", "example.test/app/store", "fmt"),
		"example.test/app/service": pkg("example.test/app/service", "service", "example.test/app/store"),
		"example.test/app/store":   pkg("example.test/app/store", "store"),
		"example.test/app/tool":    pkg("example.test/app/tool", "tool"),
	}}
}

func TestPlanAnalysisLoadsOnlyPackagesWhoseOwnSourcesChanged(t *testing.T) {
	t.Parallel()
	base := analysisStamp{identity: "id", dirs: map[string]string{"": "a", "service": "b", "store": "c", "tool": "d", "assets": "e"}}
	with := func(edit func(*analysisStamp)) analysisStamp {
		next := analysisStamp{identity: base.identity, dirs: map[string]string{}}
		for dir, digest := range base.dirs {
			next.dirs[dir] = digest
		}
		edit(&next)
		return next
	}
	retained := retainedTestAnalysis(base)
	for _, test := range []struct {
		name  string
		stamp analysisStamp
		full  bool
		dirs  []string
	}{
		{name: "nothing changed", stamp: with(func(*analysisStamp) {}), dirs: []string{}},
		{name: "a leaf", stamp: with(func(s *analysisStamp) { s.dirs["tool"] = "d2" }), dirs: []string{"tool"}},
		{name: "a package others import", stamp: with(func(s *analysisStamp) { s.dirs["store"] = "c2" }), dirs: []string{"store"}},
		{name: "two packages", stamp: with(func(s *analysisStamp) { s.dirs["service"], s.dirs["tool"] = "b2", "d2" }), dirs: []string{"service", "tool"}},
		{name: "another target, module file or replacement", stamp: with(func(s *analysisStamp) { s.identity = "other" }), full: true},
		{name: "a new source directory", stamp: with(func(s *analysisStamp) { s.dirs["added"] = "x" }), full: true},
		{name: "a removed source directory", stamp: with(func(s *analysisStamp) { delete(s.dirs, "tool") }), full: true},
		{name: "a renamed source directory", stamp: with(func(s *analysisStamp) { delete(s.dirs, "tool"); s.dirs["tools"] = "d" }), full: true},
		{name: "a changed directory that is no loaded package", stamp: with(func(s *analysisStamp) { s.dirs["assets"] = "e2" }), full: true},
	} {
		plan := planAnalysis(retained, test.stamp)
		if plan.full != test.full || (!test.full && !slices.Equal(plan.dirs, test.dirs)) {
			t.Fatalf("%s: plan = %+v, want full=%v dirs=%v", test.name, plan, test.full, test.dirs)
		}
	}
	if plan := planAnalysis(nil, base); !plan.full {
		t.Fatal("an analysis without a retained one was planned incrementally")
	}
	// A package whose observable declarations changed invalidates every package
	// that imports it, directly or not, and nothing else.
	for _, test := range []struct {
		changed []string
		loaded  []string
		want    []string
	}{
		{changed: []string{"example.test/app/store"}, loaded: []string{"store"}, want: []string{"", "service"}},
		{changed: []string{"example.test/app/service"}, loaded: []string{"service"}, want: []string{""}},
		{changed: []string{"example.test/app/tool"}, loaded: []string{"tool"}, want: []string{}},
		{changed: []string{"example.test/app/store", "example.test/app/service"}, loaded: []string{"service", "store"}, want: []string{""}},
		{changed: nil, loaded: []string{"store"}, want: []string{}},
	} {
		loaded := map[string]bool{}
		for _, dir := range test.loaded {
			loaded[dir] = true
		}
		if got := retained.importerDirs(test.changed, loaded); !slices.Equal(got, test.want) {
			t.Fatalf("importers of %v = %q, want %q", test.changed, got, test.want)
		}
	}
}

func TestMergeAnalysisAcceptsOnlyExactlyThePlannedPackages(t *testing.T) {
	t.Parallel()
	stamp := analysisStamp{identity: "id", dirs: map[string]string{"": "a", "service": "b", "store": "c2", "tool": "d"}}
	retained := retainedTestAnalysis(analysisStamp{identity: "id", dirs: map[string]string{"": "a", "service": "b", "store": "c", "tool": "d"}})
	plan := []string{"", "service", "store"}
	fresh := func(path, dir string) *retainedPackage {
		return &retainedPackage{model: &model.Package{ImportPath: path, RelDir: dir}, dir: dir}
	}
	loaded := []*retainedPackage{fresh("example.test/app", ""), fresh("example.test/app/service", "service"), fresh("example.test/app/store", "store")}
	merged, ok := mergeAnalysis(retained, stamp, plan, loaded)
	if !ok {
		t.Fatal("the planned packages were refused")
	}
	if merged.packages["example.test/app/store"] != loaded[2] || merged.packages["example.test/app/tool"] != retained.packages["example.test/app/tool"] {
		t.Fatal("merge did not replace the loaded packages and keep the others")
	}
	if merged.stamp.dirs["store"] != "c2" || retained.packages["example.test/app/store"] == loaded[2] {
		t.Fatal("merge did not take the new stamp, or changed the retained analysis in place")
	}
	app := merged.app("app", "/root")
	var dirs []string
	for _, pkg := range app.Packages {
		dirs = append(dirs, pkg.RelDir)
	}
	if !slices.IsSorted(dirs) || len(dirs) != 4 || app.ModulePath != "example.test/app" {
		t.Fatalf("application model = %v %q", dirs, app.ModulePath)
	}
	for name, packages := range map[string][]*retainedPackage{
		"a planned package is missing":     loaded[:2],
		"a package was returned twice":     append(slices.Clone(loaded), fresh("example.test/app/store", "store")),
		"an unplanned package":             append(slices.Clone(loaded), fresh("example.test/app/tool", "tool")),
		"a package the analysis never had": {loaded[0], loaded[1], fresh("example.test/app/other", "store")},
		"a package that moved":             {loaded[0], loaded[1], fresh("example.test/app/store", "elsewhere")},
	} {
		if _, ok := mergeAnalysis(retained, stamp, plan, packages); ok {
			t.Fatalf("%s: merge accepted it", name)
		}
	}
}

func TestAnalysisSourceDirsFollowBytesAndMembershipNotTimes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for path, data := range map[string]string{
		"main.go":                      "package app\n",
		"main_test.go":                 "package app\n",
		"service/service.go":           "package service\n",
		"service/templates/page.html":  "<p>",
		"service/templates/deep/a.txt": "a",
		"docs/readme.md":               "docs",
		".hidden/x.go":                 "package x\n",
		"_skipped/y.go":                "package y\n",
		"service/testdata/z.go":        "package z\n",
		"nested/go.mod":                "module nested\n",
		"nested/n.go":                  "package nested\n",
	} {
		retainedTestWrite(t, root, path, data)
	}
	first, err := analysisSourceDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for dir := range first {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	if !slices.Equal(dirs, []string{"", "service"}) {
		t.Fatalf("source directories = %q", dirs)
	}
	changes := func(name string, edit func(), want ...string) {
		t.Helper()
		edit()
		next, err := analysisSourceDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		var changed []string
		for dir, digest := range next {
			if first[dir] != digest {
				changed = append(changed, dir)
			}
		}
		slices.Sort(changed)
		if !slices.Equal(changed, want) || len(next) != len(first) {
			t.Fatalf("%s: changed directories = %q, want %q", name, changed, want)
		}
		first = next
	}
	changes("an unchanged tree", func() {})
	changes("a test file", func() { retainedTestWrite(t, root, "service/service_test.go", "package service\n") })
	changes("a same-size body edit", func() { retainedTestWrite(t, root, "service/service.go", "package servicf\n") }, "service")
	changes("an embeddable file beneath a package", func() { retainedTestWrite(t, root, "service/templates/deep/b.txt", "b") }, "service")
	changes("the content of an embeddable file", func() { retainedTestWrite(t, root, "service/templates/page.html", "<div>") })
	changes("a file of a directory the root package owns", func() { retainedTestWrite(t, root, "docs/more.md", "more") }, "")
	changes("hidden, underscore, testdata and nested modules", func() {
		for _, path := range []string{".hidden/x.go", "_skipped/y.go", "service/testdata/z.go", "nested/n.go"} {
			retainedTestWrite(t, root, path, "package changed\n")
		}
	})
	if !contentAddressedDir("/x/.scenery/framework/source/"+strings.Repeat("ab", 32)) || contentAddressedDir("/x/source/latest") || contentAddressedDir("/x/"+strings.Repeat("AB", 32)) {
		t.Fatal("content-addressed directory detection is wrong")
	}
}

func retainedTestTypes(t *testing.T, source string) *types.Package {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "p.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := (&types.Config{Importer: nil}).Check("example.test/p", files, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func TestPackageAPIDigestIgnoresBodiesAndSeesEveryObservableChange(t *testing.T) {
	t.Parallel()
	const base = `package p
const Limit = 10
type Store struct { name string; Size int }
type reader interface { Read() string }
func (s *Store) Open(path string) error { return nil }
func (s *Store) internal() int { return 1 }
func New() *Store { return &Store{name: "a"} }
var Default = New()
`
	want := packageAPIDigest(retainedTestTypes(t, base))
	body := strings.NewReplacer(`return &Store{name: "a"}`, `s := &Store{name: "b"}; s.Size = 2; return s`, `return 1`, `return 2`).Replace(base)
	if packageAPIDigest(retainedTestTypes(t, body)) != want {
		t.Fatal("a function body edit changed the package's observable declarations")
	}
	for name, replacement := range map[string][2]string{
		"a changed signature":         {`Open(path string) error`, `Open(path string, flag int) error`},
		"a changed constant value":    {`Limit = 10`, `Limit = 11`},
		"a changed exported field":    {`Size int`, `Size int64`},
		"a changed unexported field":  {`name string`, `name []byte`},
		"a removed method":            {`func (s *Store) internal() int { return 1 }`, ``},
		"a changed interface":         {`Read() string`, `Read() []byte`},
		"a changed variable type":     {`var Default = New()`, `var Default any = New()`},
		"a new exported declaration":  {`var Default = New()`, "var Default = New()\nfunc Extra() {}"},
		"a renamed exported function": {`func New() *Store`, `func Make() *Store`},
	} {
		changed := strings.Replace(base, replacement[0], replacement[1], 1)
		if name == "a changed unexported field" {
			changed = strings.Replace(changed, `&Store{name: "a"}`, `&Store{}`, 1)
		}
		if name == "a renamed exported function" {
			changed = strings.Replace(changed, `var Default = New()`, `var Default = Make()`, 1)
		}
		if packageAPIDigest(retainedTestTypes(t, changed)) == want {
			t.Fatalf("%s left the package's observable declarations unchanged", name)
		}
	}
}
