package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulse.dev/internal/codegen"
	"pulse.dev/internal/parse"
)

func TestGenerateBasicGolden(t *testing.T) {
	root := filepath.Join(repoRoot(t), "testdata", "apps", "basic")
	app, err := parse.App(root, "basicapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_service_pulse.gen.go"), out.Generated["service/pulse.gen.go"])
	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_main.go"), out.Generated["pulse_internal_main/main.go"])
}

func TestGenerateSanitizesBlankIdentifiers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/blankident\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"blankident"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//pulse:api public
func Hello(_ context.Context) error { return nil }
`)

	app, err := parse.App(dir, "blankident")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/pulse.gen.go"])
	if !strings.Contains(got, "pulseArg0 context.Context") {
		t.Fatalf("expected sanitized context param, got:\n%s", got)
	}
	if strings.Contains(got, "CallEndpoint(_,") {
		t.Fatalf("expected blank identifier to be sanitized, got:\n%s", got)
	}
	if !strings.Contains(got, "pulseInternalImplHello(ctx)") {
		t.Fatalf("expected invoke closure to use ctx, got:\n%s", got)
	}
}

func TestGenerateRawOnlyPackageOmitsContextImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/rawonly\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"rawonly"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "net/http"

//pulse:api public raw
func Hook(w http.ResponseWriter, req *http.Request) {}
`)

	app, err := parse.App(dir, "rawonly")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/pulse.gen.go"])
	if strings.Contains(got, "\"context\"") {
		t.Fatalf("expected raw-only package to omit context import, got:\n%s", got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", filepath.Base(path), want, got)
	}
}

func writeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
