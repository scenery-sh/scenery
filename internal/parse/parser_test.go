package parse_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pbrazdil/onlava/internal/parse"
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
	writeFile(t, dir, "go.mod", "module example.com/rawcall\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"rawcall"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import (
	"net/http"
)

//onlava:api public raw
func Raw(w http.ResponseWriter, req *http.Request) {}

//onlava:api public
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
	writeFile(t, dir, "go.mod", "module example.com/pathmismatch\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"pathmismatch"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public path=/hello/:name
func Hello(ctx context.Context, wrong string) error { return nil }
`)

	_, err := parse.App(dir, "pathmismatch")
	if err == nil || !strings.Contains(err.Error(), "path param name must match") && !strings.Contains(err.Error(), "must match function param") {
		t.Fatalf("expected path param mismatch error, got %v", err)
	}
}

func TestParseRejectsNonOnlavaDirectives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/otherdirective\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"otherdirective"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//other:api public
func Hello(ctx context.Context) error { return nil }
`)

	_, err := parse.App(dir, "otherdirective")
	if err == nil || !strings.Contains(err.Error(), "no onlava directives found in application") {
		t.Fatalf("expected no onlava directives error, got %v", err)
	}
}

func TestParseMiddlewareTargetsAndTags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/middlewareapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"middlewareapp"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public tag:foo
func Hello(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "svc/mw/mw.go", `package mw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware target=tag:foo
func ServiceTag(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`)
	writeFile(t, dir, "globalmw/mw.go", `package globalmw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware global target=all
func Global(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`)

	app, err := parse.App(dir, "middlewareapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	if len(app.Middleware) != 2 {
		t.Fatalf("expected 2 middleware declarations, got %d", len(app.Middleware))
	}
	var svcFound bool
	var svc = app.Services[0]
	for _, candidate := range app.Services {
		if candidate.Name == "svc" {
			svc = candidate
			svcFound = true
			break
		}
	}
	if !svcFound {
		t.Fatalf("expected to find svc service, got %+v", app.Services)
	}
	if len(svc.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(svc.Endpoints))
	}
	ep := svc.Endpoints[0]
	if got := strings.Join(ep.Tags, ","); got != "foo" {
		t.Fatalf("unexpected endpoint tags: %s", got)
	}
	if len(ep.Middleware) != 2 {
		t.Fatalf("expected endpoint middleware to include global and service match, got %d", len(ep.Middleware))
	}
	if !ep.Middleware[0].Global || ep.Middleware[1].Global {
		t.Fatalf("expected global middleware to sort before service middleware: %+v", ep.Middleware)
	}
}

func TestParseRejectsAppsWithoutOnlavaDirectives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/noonlava\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"noonlava"}`)
	writeFile(t, dir, "svc/api.go", `package svc

func Helper() {}
`)

	_, err := parse.App(dir, "noonlava")
	if err == nil || !strings.Contains(err.Error(), "no onlava directives found in application") {
		t.Fatalf("expected no onlava directives error, got %v", err)
	}
}

func TestParseRejectsInvalidServiceShutdownSignature(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/badshutdown\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"badshutdown"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:service
type Service struct{}

func (s *Service) Shutdown() {}

//onlava:api public
func (s *Service) Hello(ctx context.Context) error { return nil }
`)

	_, err := parse.App(dir, "badshutdown")
	if err == nil || !strings.Contains(err.Error(), "Shutdown method must have signature func(context.Context)") {
		t.Fatalf("expected invalid shutdown signature error, got %v", err)
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
