package parse_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pulse.dev/internal/parse"
)

func TestParseBasicApp(t *testing.T) {
	appRoot := filepath.Join(repoRoot(t), "testdata", "apps", "basic")
	app, err := parse.App(appRoot, "basicapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	if len(app.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(app.Services))
	}
	svc := app.Services[0]
	if svc.Name != "service" {
		t.Fatalf("expected service name %q, got %q", "service", svc.Name)
	}
	if svc.Struct == nil {
		t.Fatal("expected service struct")
	}
	if svc.AuthHandler == nil {
		t.Fatal("expected auth handler")
	}

	var foundEcho, foundCallPrivate bool
	for _, ep := range svc.Endpoints {
		switch ep.Name {
		case "Echo":
			foundEcho = true
			if ep.Path != "/echo/:name" {
				t.Fatalf("unexpected Echo path: %s", ep.Path)
			}
			if got := strings.Join(ep.Methods, ","); got != "GET,POST" {
				t.Fatalf("unexpected Echo methods: %s", got)
			}
			if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "name" {
				t.Fatalf("unexpected Echo path params: %+v", ep.PathParams)
			}
		case "CallPrivate":
			foundCallPrivate = true
			if ep.Path != "/service.CallPrivate" {
				t.Fatalf("unexpected CallPrivate path: %s", ep.Path)
			}
		}
	}
	if !foundEcho || !foundCallPrivate {
		t.Fatalf("missing expected endpoints, Echo=%v CallPrivate=%v", foundEcho, foundCallPrivate)
	}
}

func TestParseRejectsRawEndpointCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/rawcall\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"rawcall"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import (
	"net/http"
)

//pulse:api public raw
func Raw(w http.ResponseWriter, req *http.Request) {}

//pulse:api public
func Call(w http.ResponseWriter, req *http.Request) {
	Raw(w, req)
}
`)

	_, err := parse.App(dir, "rawcall")
	if err == nil || !strings.Contains(err.Error(), "raw endpoint calls are not supported") {
		t.Fatalf("expected raw endpoint call error, got %v", err)
	}
}

func TestParseRejectsPathParamMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/pathmismatch\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"pathmismatch"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//pulse:api public path=/hello/:name
func Hello(ctx context.Context, wrong string) error { return nil }
`)

	_, err := parse.App(dir, "pathmismatch")
	if err == nil || !strings.Contains(err.Error(), "path param name must match") && !strings.Contains(err.Error(), "must match function param") {
		t.Fatalf("expected path param mismatch error, got %v", err)
	}
}

func TestParseAcceptsEncoreDirectives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/encoreapp\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"encoreapp"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//encore:api public
func Hello(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "encoreapp")
	if err != nil {
		t.Fatalf("expected Encore directives to parse, got %v", err)
	}
	if len(app.Services) != 1 || len(app.Services[0].Endpoints) != 1 {
		t.Fatalf("expected one service with one endpoint, got %+v", app.Services)
	}
}

func TestParseRejectsAppsWithoutPulseDirectives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/nopulse\n\ngo 1.26.0\n\nrequire pulse.dev v0.0.0\n\nreplace pulse.dev => "+repoRoot(t)+"\n")
	writeFile(t, dir, "pulse.app", `{"name":"nopulse"}`)
	writeFile(t, dir, "svc/api.go", `package svc

func Helper() {}
`)

	_, err := parse.App(dir, "nopulse")
	if err == nil || !strings.Contains(err.Error(), "no Pulse or Encore directives found in application") {
		t.Fatalf("expected no Pulse directives error, got %v", err)
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
