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
