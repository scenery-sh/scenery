package parse

import (
	"go/ast"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

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

func TestSyntaxFilePathsPrefersCompiledGoFilesWhenTheyMatchSyntax(t *testing.T) {
	t.Parallel()

	pkg := &packages.Package{
		GoFiles:         []string{"api.go"},
		CompiledGoFiles: []string{"api.go", "cgo_gen.go"},
		Syntax:          make([]*ast.File, 2),
	}

	got := syntaxFilePaths(pkg)
	want := []string{"api.go", "cgo_gen.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("syntaxFilePaths() = %v, want %v", got, want)
	}
}

func TestServiceRootForPackagePrefersExplicitNestedServiceRoot(t *testing.T) {
	t.Parallel()

	projectsRoot := filepath.Join("solar", "projects")
	explicit := map[string]bool{projectsRoot: true}

	tests := map[string]string{
		projectsRoot:                             projectsRoot,
		filepath.Join(projectsRoot, "db", "gen"): projectsRoot,
		filepath.Join("solar", "tasks"):          "solar",
		"billing":                                "billing",
	}

	for relDir, want := range tests {
		if got := serviceRootForPackage(relDir, explicit); got != want {
			t.Fatalf("serviceRootForPackage(%q) = %q, want %q", relDir, got, want)
		}
	}
}

func TestSyntaxFilePathsFallsBackToGoFilesWhenTheyMatchSyntax(t *testing.T) {
	t.Parallel()

	pkg := &packages.Package{
		GoFiles:         []string{"api.go", "extra.go"},
		CompiledGoFiles: []string{"api.go"},
		Syntax:          make([]*ast.File, 2),
	}

	got := syntaxFilePaths(pkg)
	want := []string{"api.go", "extra.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("syntaxFilePaths() = %v, want %v", got, want)
	}
}
