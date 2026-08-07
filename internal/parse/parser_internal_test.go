package parse

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
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

	missing, err := MissingHermeticModulePackages(GoTargetContext{
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

func TestGoTargetEnvironmentSelectsDeclaredToolchain(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go9.9.9+auto")
	t.Setenv("GOMAXPROCS", "99")
	t.Setenv("CC", "/ambient/cc")
	t.Setenv("PKG_CONFIG", "/ambient/pkg-config")

	environment := GoTargetEnvironment(GoTargetContext{
		ToolchainVersion: "1.26.3",
		GOOS:             "linux",
		GOARCH:           "amd64",
		NativeToolEnv:    map[string]string{"CC": "/declared/cc", "PKG_CONFIG": "/declared/pkg-config-disabled"},
	})
	values := map[string]string{}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	if values["GOTOOLCHAIN"] != "go1.26.3" {
		t.Fatalf("GOTOOLCHAIN = %q, want exact declared toolchain", values["GOTOOLCHAIN"])
	}
	if values["GOOS"] != "linux" || values["GOARCH"] != "amd64" {
		t.Fatalf("target environment = GOOS=%q GOARCH=%q", values["GOOS"], values["GOARCH"])
	}
	if values["CC"] != "/declared/cc" || values["PKG_CONFIG"] != "/declared/pkg-config-disabled" {
		t.Fatalf("native tools leaked ambient environment: CC=%q PKG_CONFIG=%q", values["CC"], values["PKG_CONFIG"])
	}
	if want := strconv.Itoa(min(runtime.GOMAXPROCS(0), goAnalysisMaxProcs)); values["GOMAXPROCS"] != want {
		t.Fatalf("Go analysis concurrency = %q, want bounded subprocess concurrency", values["GOMAXPROCS"])
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
