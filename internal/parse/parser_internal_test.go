package parse

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"golang.org/x/tools/go/packages"

	"scenery.sh/internal/gotarget"
)

func TestMissingHermeticModulePackages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOMODCACHE", filepath.Join(root, "module-cache"))
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", `module example.test/cacheprobe

go 1.24.0

require github.com/google/uuid v1.6.0
`)
	write("go.sum", `github.com/google/uuid v1.6.0 h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=
github.com/google/uuid v1.6.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=
`)
	write("cacheprobe.go", `package cacheprobe

import _ "github.com/google/uuid"
import _ "example.test/cacheprobe/generated"
`)

	missing, err := MissingHermeticModulePackages(gotarget.Context{
		ModuleRoot: root,
		Patterns:   []string{"./..."},
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"github.com/google/uuid"}; !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing packages = %#v, want %#v", missing, want)
	}
}

func TestCanonicalizeAnalysisOverlayUsesPhysicalRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	if err := os.Mkdir(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	logical := filepath.Join(root, "logical")
	if err := os.Symlink(physical, logical); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	inside := filepath.Join(logical, "generated", "contract.go")
	outside := filepath.Join(root, "outside.go")
	data := []byte("package generated\n")
	got := canonicalizeAnalysisOverlay(logical, physical, map[string][]byte{inside: data, outside: data})
	if _, ok := got[filepath.Join(physical, "generated", "contract.go")]; !ok {
		t.Fatalf("inside overlay did not use physical root: %#v", got)
	}
	if _, ok := got[outside]; !ok {
		t.Fatalf("outside overlay path changed: %#v", got)
	}
}

func TestAnalyzeTargetOverlayUsesPrivateWritableModFile(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", `module example.test/app

go 1.27.0

replace example.test/runtime => ./runtime
`)
	write("app.go", "package app\n")
	write("runtime/go.mod", "module example.test/runtime\n\ngo 1.27.0\n")
	write("runtime/runtime.go", "package runtime\n")

	before, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	overlay := map[string][]byte{
		filepath.Join(root, "app.go"): []byte("package app\n\nimport _ \"example.test/runtime\"\n"),
	}
	model, err := AnalyzeTargetContext(t.Context(), root, "test", overlay, gotarget.Context{
		ModuleRoot: root,
		Patterns:   []string{"./..."},
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Packages) != 1 || model.Packages[0].ImportPath != "example.test/app" {
		t.Fatalf("packages = %#v", model.Packages)
	}
	after, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("source go.mod changed:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("source go.sum was created: %v", err)
	}
}

func TestPackageFilePathsPrefersCompiledGoFiles(t *testing.T) {
	t.Parallel()

	pkg := &packages.Package{
		GoFiles:         []string{"api.go"},
		CompiledGoFiles: []string{"api.go", "cgo_gen.go"},
	}

	got := packageFilePaths(pkg)
	want := []string{"api.go", "cgo_gen.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("packageFilePaths() = %v, want %v", got, want)
	}
}

func TestPackageFilePathsFallsBackToGoFiles(t *testing.T) {
	t.Parallel()

	pkg := &packages.Package{
		GoFiles: []string{"api.go", "extra.go"},
	}

	got := packageFilePaths(pkg)
	want := []string{"api.go", "extra.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("packageFilePaths() = %v, want %v", got, want)
	}
}
