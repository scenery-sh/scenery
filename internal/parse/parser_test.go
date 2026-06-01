package parse_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pbrazdil/onlava/internal/clientgen"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
)

func TestBasicAppParseAndClientgen(t *testing.T) {
	t.Parallel()

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

	out, err := clientgen.GenerateTypeScript(app, clientgen.TypeScriptOptions{AppSlug: "basicapp"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`export namespace service {`,
		`public async Echo(name: string, params: EchoRequest, options?: CallParameters): Promise<EchoResponse> {`,
		`public async EchoWithMeta(name: string, params: EchoRequest, options?: CallParameters): Promise<APIResponse<EchoResponse>> {`,
		`this.EchoWithMeta = this.EchoWithMeta.bind(this)`,
		`title: encodeQueryValue(params.Title),`,
		`"X-Echo": encodeHeaderValue(params.Header),`,
		`body: encodeQueryValue(params.body),`,
		`transport?: OnlavaTransport`,
		`export type CallParameters = Omit<RequestInit, "method" | "body" | "headers"> & {`,
		`export interface APIResponse<T> {`,
		`export type OnlavaTransport = "auto" | "json" | "binary" | "binary-strict" | "wire-json" | "wire-json-strict"`,
		`const ONLAVA_WIRE_SCHEMA_HASH = `,
		`const resp = await this.baseClient.callTypedEndpoint({ endpointID: "service.Echo"`,
		`const resp = await this.baseClient.callTypedEndpointWithMeta({ endpointID: "service.Echo"`,
		`wirePath: "/_wire/service.Echo"`,
		"path: `/echo/${encodeURIComponent(String(name))}`",
		`payload: params`,
		`payloadJSON: JSON.stringify(params)`,
		`jsonBody: undefined`,
		`params: mergeCallParameters(options, { query, headers })`,
		`return await decodeTypedAPIResponse(resp) as APIResponse<EchoResponse>`,
		`public async Raw(rest: string, method: string, body?: RequestInit["body"], options?: CallParameters): Promise<globalThis.Response> {`,
		"return await this.baseClient.callAPI(method, `/raw/${encodePathWildcard(String(rest))}`, body, options)",
		`export interface EchoResponse {`,
		`message: string`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q\n%s", want, got)
		}
	}
	for _, forbidden := range []string{"protobuf", "grpc", "connect"} {
		if strings.Contains(strings.ToLower(got), forbidden) {
			t.Fatalf("generated client should not expose %q\n%s", forbidden, got)
		}
	}
}

func TestParseRuntimeDeclarationsNestedServiceAndMiddleware(t *testing.T) {
	t.Parallel()

	dir := persistentParseTestApp(t, "runtimedecls", map[string]string{
		"go.mod":       "module example.com/runtimedecls\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"runtimedecls"}`,
		"solar/projects/api.go": `package projects

import "context"

//onlava:service
type Service struct{}

type ListProjectsResponse struct {
	Items []string
}

//onlava:api public method=GET path=/tenants/:tenant_id/projects tag:foo
func (s *Service) ListProjects(ctx context.Context, tenant_id string) (*ListProjectsResponse, error) {
	return &ListProjectsResponse{}, nil
}
`,
		"solar/projects/mw/mw.go": `package mw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware target=tag:foo
func ServiceTag(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,
		"globalmw/mw.go": `package globalmw

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware global target=all
func Global(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,
		"jobs/runtime.go": `package jobs

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
var _ = temporal.NewActivity[In, Out]("orders.Unkeyed/v1", temporal.ActivityConfig{"orders.unkeyed.go", time.Minute, 0, temporal.RetryPolicy{}}, func(ctx context.Context, in In) (Out, error) {
	return Out{ID: in.ID}, nil
})
var _ = temporal.NewExternalActivity[*In, *Out]("orders.Render/v1", temporal.ActivityConfig{TaskQueue: "orders.render.ts"})
var _ = cron.NewJob("tick", cron.JobConfig{
	Every: time.Second,
	Handler: func(context.Context) error { return nil },
})
`,
	})

	app, err := parse.App(dir, "runtimedecls")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	if len(app.Runtime) != 5 {
		t.Fatalf("runtime declarations = %#v", app.Runtime)
	}
	got := make(map[string]*model.RuntimeDeclaration)
	for _, decl := range app.Runtime {
		got[decl.Name] = decl
		if decl.Package == nil || decl.File == nil || decl.CallName == "" {
			t.Fatalf("incomplete runtime declaration: %#v", decl)
		}
	}
	for _, name := range []string{"orders.Fulfill/v1", "orders.Capture/v1", "orders.Unkeyed/v1", "orders.Render/v1", "tick"} {
		if got[name] == nil {
			t.Fatalf("missing runtime declaration %q (all: %#v)", name, got)
		}
	}
	if decl := got["orders.Fulfill/v1"]; decl.Kind != model.RuntimeDeclarationTemporalWorkflow || !decl.TaskQueueResolved {
		t.Fatalf("workflow declaration = %#v", decl)
	}
	if decl := got["orders.Capture/v1"]; decl.Kind != model.RuntimeDeclarationTemporalActivity || !decl.TaskQueueExplicit || !decl.TaskQueueResolved || decl.TaskQueue != "orders.activities.go" {
		t.Fatalf("activity declaration = %#v", decl)
	}
	if decl := got["orders.Unkeyed/v1"]; decl.Kind != model.RuntimeDeclarationTemporalActivity || !decl.TaskQueueExplicit || !decl.TaskQueueResolved || decl.TaskQueue != "orders.unkeyed.go" {
		t.Fatalf("unkeyed activity declaration = %#v", decl)
	}
	if decl := got["orders.Render/v1"]; decl.Kind != model.RuntimeDeclarationTemporalExternalActivity || !decl.TaskQueueExplicit || decl.TaskQueue != "orders.render.ts" || decl.InputType != "*In" || decl.OutputType != "*Out" {
		t.Fatalf("external activity declaration = %#v", decl)
	}
	if decl := got["tick"]; decl.Kind != model.RuntimeDeclarationCronJob {
		t.Fatalf("cron declaration = %#v", decl)
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
	if len(app.Middleware) != 2 {
		t.Fatalf("expected 2 middleware declarations, got %d", len(app.Middleware))
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

func TestParseRejectsInvalidRuntimeAndEndpointDiagnostics(t *testing.T) {
	t.Parallel()

	dir := persistentParseTestApp(t, "invalidruntime", map[string]string{
		"go.mod":       "module example.com/invalidruntime\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"invalidruntime"}`,
		"svc/api.go": `package svc

import (
	"context"
	"net/http"

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

var wf = temporal.NewWorkflow[In, Out]("orders.Fulfill/v1", temporal.WorkflowConfig{}, func(ctx temporal.WorkflowContext, in In) (Out, error) {
	return Out{}, nil
})

//onlava:api public
func Ping(ctx context.Context) error {
	_, err := temporal.Start(ctx, wf, In{})
	return err
}

//onlava:api public raw
func Raw(w http.ResponseWriter, req *http.Request) {}

//onlava:api public
func CallRaw(w http.ResponseWriter, req *http.Request) {
	Raw(w, req)
}

//onlava:api public path=/hello/:name
func Hello(ctx context.Context, wrong string) error { return nil }

//onlava:service
type Service struct{}

func (s *Service) Shutdown() {}

//onlava:api public
func (s *Service) ServiceHello(ctx context.Context) error { return nil }
`,
	})

	_, err := parse.App(dir, "invalidruntime")
	if err == nil {
		t.Fatal("expected invalid runtime diagnostics")
	}
	got := err.Error()
	if count := strings.Count(got, "temporal.NewActivity requires temporal.ActivityConfig.TaskQueue"); count != 3 {
		t.Fatalf("activity task queue diagnostics = %d, want 3\n%v", count, err)
	}
	for _, want := range []string{
		"temporal.Start requires a workflow identity argument",
		"raw endpoint calls are not supported",
		"Shutdown method must have signature func(context.Context)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected diagnostic %q, got %v", want, err)
		}
	}
	if !strings.Contains(got, "path param name must match") && !strings.Contains(got, "must match function param") {
		t.Fatalf("expected path param mismatch diagnostic, got %v", err)
	}
}

func TestParseRejectsNonOnlavaDirectives(t *testing.T) {
	t.Parallel()

	dir := persistentParseTestApp(t, "otherdirective", map[string]string{
		"go.mod":       "module example.com/otherdirective\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"otherdirective"}`,
		"svc/api.go": `package svc

import "context"

//other:api public
func Hello(ctx context.Context) error { return nil }
`,
	})

	_, err := parse.App(dir, "otherdirective")
	if err == nil || !strings.Contains(err.Error(), "no onlava directives found in application") {
		t.Fatalf("expected no onlava directives error, got %v", err)
	}
}

func TestParseRejectsAppsWithoutOnlavaDirectives(t *testing.T) {
	t.Parallel()

	dir := persistentParseTestApp(t, "noonlava", map[string]string{
		"go.mod":       "module example.com/noonlava\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"noonlava"}`,
		"svc/api.go": `package svc

func Helper() {}
`,
	})

	_, err := parse.App(dir, "noonlava")
	if err == nil || !strings.Contains(err.Error(), "no onlava directives found in application") {
		t.Fatalf("expected no onlava directives error, got %v", err)
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

func persistentParseTestApp(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cacheDir, "onlava", "internal-parse-tests", name)
	fingerprint := parseTestAppFingerprint(files)
	marker := filepath.Join(root, ".onlava-test-fingerprint")
	if data, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(data)) != fingerprint {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	for _, rel := range paths {
		writeFileIfChanged(t, root, rel, files[rel])
	}
	writeFileIfChanged(t, root, ".onlava-test-fingerprint", fingerprint+"\n")
	return root
}

func parseTestAppFingerprint(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(files[rel]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeFileIfChanged(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if current, err := os.ReadFile(path); err == nil && string(current) == data {
		return
	}
	writeFile(t, root, rel, data)
}
