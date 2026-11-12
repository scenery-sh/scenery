package parse

import (
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/gotarget"
)

func TestAnalysisInputDigestFollowsEverythingAnAnalysisConsumes(t *testing.T) {
	root := t.TempDir()
	write := func(relative, contents string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/app\n\ngo 1.27\n")
	write("svc/service.go", "package svc\n\nfunc Handle() string { return \"a\" }\n")
	write("svc/service_test.go", "package svc\n")
	write("svc/notes.txt", "notes\n")
	target := gotarget.Context{ModuleRoot: root, Patterns: []string{"./..."}}
	overlay := map[string][]byte{filepath.Join(root, "svc", "contract.gen.go"): []byte("package svc\n")}
	digest := func(overlay map[string][]byte, target gotarget.Context) string {
		t.Helper()
		value, err := AnalysisInputDigest(root, overlay, target)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	base := digest(overlay, target)
	if base == "" || base != digest(overlay, target) {
		t.Fatal("the same inputs have two digests")
	}
	// A test file and the content of a file that is no source are not inputs.
	write("svc/service_test.go", "package svc\n\nfunc helper() {}\n")
	write("svc/notes.txt", "other notes\n")
	if digest(overlay, target) != base {
		t.Fatal("a file the analysis does not read changed the digest")
	}
	changes := map[string]func() (map[string][]byte, gotarget.Context){
		"overlay bytes": func() (map[string][]byte, gotarget.Context) {
			return map[string][]byte{filepath.Join(root, "svc", "contract.gen.go"): []byte("package svc\n\nvar X int\n")}, target
		},
		"overlay path": func() (map[string][]byte, gotarget.Context) {
			return map[string][]byte{filepath.Join(root, "svc", "other.gen.go"): []byte("package svc\n")}, target
		},
		"no overlay": func() (map[string][]byte, gotarget.Context) { return nil, target },
		"build tags": func() (map[string][]byte, gotarget.Context) {
			changed := target
			changed.BuildTags = []string{"integration"}
			return overlay, changed
		},
		"build flags": func() (map[string][]byte, gotarget.Context) {
			changed := target
			changed.BuildFlags = []string{"-race"}
			return overlay, changed
		},
		"patterns": func() (map[string][]byte, gotarget.Context) {
			changed := target
			changed.Patterns = []string{"./svc"}
			return overlay, changed
		},
	}
	for name, change := range changes {
		if changedOverlay, changedTarget := change(); digest(changedOverlay, changedTarget) == base {
			t.Errorf("a changed %s kept the digest", name)
		}
	}
	for name, edit := range map[string]func(){
		"source body":                  func() { write("svc/service.go", "package svc\n\nfunc Handle() string { return \"b\" }\n") },
		"new source":                   func() { write("svc/more.go", "package svc\n") },
		"new package":                  func() { write("other/other.go", "package other\n") },
		"module file":                  func() { write("go.mod", "module example.test/app\n\ngo 1.27\n\nrequire example.test/dep v1.0.0\n") },
		"module sums":                  func() { write("go.sum", "example.test/dep v1.0.0 h1:abc=\n") },
		"new file kind beside sources": func() { write("svc/asset.bin", "x") },
	} {
		before := digest(overlay, target)
		edit()
		if digest(overlay, target) == before {
			t.Errorf("a changed %s kept the digest", name)
		}
	}
	// Inputs that cannot be named completely have no digest.
	write("vendor/modules.txt", "")
	if _, err := AnalysisInputDigest(root, overlay, target); err == nil {
		t.Fatal("a vendored module has a digest")
	}
	if _, err := AnalysisInputDigest(root, overlay, gotarget.Context{ModuleRoot: root}); err == nil {
		t.Fatal("a target without patterns has a digest")
	}
}
