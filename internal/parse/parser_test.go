package parse_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pbrazdil/onlava/internal/model"
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

func TestParseRuntimeDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/runtimedecls\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"runtimedecls"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "jobs/runtime.go", `package jobs

import (
	"context"
	"time"

	"github.com/pbrazdil/onlava/cron"
	"github.com/pbrazdil/onlava/temporal"
)

type In struct{ ID string }
type Out struct{ ID string }

var _ = temporal.NewWorkflow[In, Out]("orders.Fulfill/v1", temporal.WorkflowConfig{}, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{ID: in.ID}, nil
})
var _ = temporal.NewActivity[In, Out]("orders.Capture/v1", temporal.ActivityConfig{TaskQueue: "orders.activities.go"}, func(ctx context.Context, in In) (Out, error) {
	return Out{ID: in.ID}, nil
})
var _ = cron.NewJob("tick", cron.JobConfig{
	Every: time.Second,
	Handler: func(context.Context) error { return nil },
})
`)

	app, err := parse.App(dir, "runtimedecls")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	if len(app.Runtime) != 3 {
		t.Fatalf("runtime declarations = %#v", app.Runtime)
	}
	got := make(map[model.RuntimeDeclarationKind]string)
	for _, decl := range app.Runtime {
		got[decl.Kind] = decl.Name
		if decl.Package == nil || decl.File == nil || decl.CallName == "" {
			t.Fatalf("incomplete runtime declaration: %#v", decl)
		}
		if decl.Kind == model.RuntimeDeclarationTemporalActivity && (!decl.TaskQueueExplicit || decl.TaskQueue != "orders.activities.go") {
			t.Fatalf("activity task queue = %q explicit=%v", decl.TaskQueue, decl.TaskQueueExplicit)
		}
		if decl.Kind == model.RuntimeDeclarationTemporalWorkflow && !decl.TaskQueueResolved {
			t.Fatalf("workflow task queue should be resolved as defaultable")
		}
	}
	want := map[model.RuntimeDeclarationKind]string{
		model.RuntimeDeclarationTemporalWorkflow: "orders.Fulfill/v1",
		model.RuntimeDeclarationTemporalActivity: "orders.Capture/v1",
		model.RuntimeDeclarationCronJob:          "tick",
	}
	for kind, name := range want {
		if got[kind] != name {
			t.Fatalf("runtime declaration %s = %q, want %q (all: %#v)", kind, got[kind], name, got)
		}
	}
}

func TestParseRejectsEmptyTemporalActivityTaskQueue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/emptyactivityqueue\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"emptyactivityqueue"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
func Ping(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "jobs/runtime.go", `package jobs

import (
	"context"

	"github.com/pbrazdil/onlava/temporal"
)

type In struct{}
type Out struct{}

var _ = temporal.NewActivity[In, Out]("orders.Capture/v1", temporal.ActivityConfig{}, func(ctx context.Context, in In) (Out, error) {
	return Out{}, nil
})
var blank = temporal.ActivityConfig{TaskQueue: "   "}
var _ = temporal.NewActivity[In, Out]("orders.Refund/v1", blank, func(ctx context.Context, in In) (Out, error) {
	return Out{}, nil
})
var zero temporal.ActivityConfig
var _ = temporal.NewActivity[In, Out]("orders.Zero/v1", zero, func(ctx context.Context, in In) (Out, error) {
	return Out{}, nil
})
`)

	_, err := parse.App(dir, "emptyactivityqueue")
	if err == nil {
		t.Fatal("expected empty activity task queue error")
	}
	if got := err.Error(); strings.Count(got, "temporal.NewActivity requires temporal.ActivityConfig.TaskQueue") != 3 {
		t.Fatalf("expected two activity task queue diagnostics, got %v", err)
	}
}

func TestParseAcceptsUnkeyedTemporalActivityConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/unkeyedactivityqueue\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"unkeyedactivityqueue"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
func Ping(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "jobs/runtime.go", `package jobs

import (
	"context"
	"time"

	"github.com/pbrazdil/onlava/temporal"
)

type In struct{}
type Out struct{}

var _ = temporal.NewActivity[In, Out]("orders.Capture/v1", temporal.ActivityConfig{"orders.activities.go", time.Minute, 0, temporal.RetryPolicy{}}, func(ctx context.Context, in In) (Out, error) {
	return Out{}, nil
})
`)

	app, err := parse.App(dir, "unkeyedactivityqueue")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	var found bool
	for _, decl := range app.Runtime {
		if decl.Kind == model.RuntimeDeclarationTemporalActivity {
			found = true
			if decl.TaskQueue != "orders.activities.go" || !decl.TaskQueueExplicit || !decl.TaskQueueResolved {
				t.Fatalf("activity declaration = %#v", decl)
			}
		}
	}
	if !found {
		t.Fatal("expected temporal activity declaration")
	}
}

func TestParseRejectsLegacyTemporalStartCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/legacystart\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"legacystart"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import (
	"context"

	"github.com/pbrazdil/onlava/temporal"
)

type In struct{}
type Out struct{}

var wf = temporal.NewWorkflow[In, Out]("orders.Fulfill/v1", temporal.WorkflowConfig{}, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{}, nil
})

//onlava:api public
func Ping(ctx context.Context) error {
	_, err := temporal.Start(ctx, wf, In{})
	return err
}
`)

	_, err := parse.App(dir, "legacystart")
	if err == nil || !strings.Contains(err.Error(), "temporal.Start requires a workflow identity argument") {
		t.Fatalf("expected legacy temporal.Start diagnostic, got %v", err)
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

func TestParseAllowsNestedPackageServiceStruct(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/nestedservice\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"nestedservice"}`)
	writeFile(t, dir, "solar/projects/api.go", `package projects

import "context"

//onlava:service
type Service struct{}

type ListProjectsResponse struct {
	Items []string
}

//onlava:api public method=GET path=/tenants/:tenant_id/projects
func (s *Service) ListProjects(ctx context.Context, tenant_id string) (*ListProjectsResponse, error) {
	return &ListProjectsResponse{}, nil
}
`)

	app, err := parse.App(dir, "nestedservice")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	if len(app.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(app.Services))
	}
	svc := app.Services[0]
	if svc.Name != "projects" {
		t.Fatalf("service name = %q, want %q", svc.Name, "projects")
	}
	if svc.RootRelDir != filepath.Join("solar", "projects") {
		t.Fatalf("service root = %q, want %q", svc.RootRelDir, filepath.Join("solar", "projects"))
	}
	if svc.Struct == nil || svc.Struct.TypeName != "Service" {
		t.Fatalf("expected Service struct, got %+v", svc.Struct)
	}
	if len(svc.Endpoints) != 1 || svc.Endpoints[0].Name != "ListProjects" {
		t.Fatalf("unexpected endpoints: %+v", svc.Endpoints)
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
