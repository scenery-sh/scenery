package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/codegen"
	"github.com/pbrazdil/onlava/internal/parse"
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

	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_service_onlava.gen.go"), out.Generated["service/onlava.gen.go"])
	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_main.go"), out.Generated["onlava_internal_main/main.go"])
}

func TestGenerateSanitizesBlankIdentifiers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/blankident\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"blankident"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
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

	got := string(out.Generated["svc/onlava.gen.go"])
	if !strings.Contains(got, "onlavaArg0 context.Context") {
		t.Fatalf("expected sanitized context param, got:\n%s", got)
	}
	if strings.Contains(got, "CallEndpoint(_,") {
		t.Fatalf("expected blank identifier to be sanitized, got:\n%s", got)
	}
	if !strings.Contains(got, "onlavaInternalImplHello(ctx)") {
		t.Fatalf("expected invoke closure to use ctx, got:\n%s", got)
	}
}

func TestGenerateRawOnlyPackageOmitsContextImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/rawonly\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"rawonly"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "net/http"

//onlava:api public raw
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

	got := string(out.Generated["svc/onlava.gen.go"])
	if strings.Contains(got, "\"context\"") {
		t.Fatalf("expected raw-only package to omit context import, got:\n%s", got)
	}
}

func TestGeneratePopulatesSecretsBeforePackageVarInitializers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/earlysecrets\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"earlysecrets"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

var secrets struct {
	TestQueueConcurrency string
}

var maxConcurrency = secrets.TestQueueConcurrency

//onlava:api public
func Hello(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "earlysecrets")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/00_onlava_config.gen.go"])
	for _, want := range []string{
		`var onlavaInternalDotEnvInitialized = onlavaruntime.MustLoadDotEnvIntoEnv()`,
		`var onlavaInternalSecretsInitialized = func() bool {`,
		`onlavaruntime.MustPopulateSecrets(&secrets)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected early secrets file to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateRegistersMiddlewareAndEndpointLinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/middlewaregen\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"middlewaregen"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public tag:foo
func Hello(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "svc/mw.go", `package svc

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware target=tag:foo
func Apply(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`)

	app, err := parse.App(dir, "middlewaregen")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/onlava.gen.go"])
	if !strings.Contains(got, "RegisterMiddleware(&onlavaruntime.Middleware") {
		t.Fatalf("expected middleware registration, got:\n%s", got)
	}
	if !strings.Contains(got, `MiddlewareIDs:`) || !strings.Contains(got, `[]string{"example.com/middlewaregen/svc.Apply"}`) {
		t.Fatalf("expected endpoint middleware ids, got:\n%s", got)
	}
}

func TestGenerateRegistersServiceInitializer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/serviceinit\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"serviceinit"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

//onlava:api public
func (s *Service) Hello(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "serviceinit")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/onlava.gen.go"])
	if !strings.Contains(got, `onlavaruntime.RegisterServiceInitializer("svc", func() error {`) {
		t.Fatalf("expected service initializer registration, got:\n%s", got)
	}
	if !strings.Contains(got, "_, err := onlavaInternalGetService()") {
		t.Fatalf("expected service initializer to call generated getter, got:\n%s", got)
	}
}

func TestGenerateRegistersServiceShutdownAndMockLookup(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/serviceshutdown\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"serviceshutdown"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

func (s *Service) Shutdown(force context.Context) {}

//onlava:api public
func (s *Service) Hello(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "serviceshutdown")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/onlava.gen.go"])
	for _, want := range []string{
		`onlavaruntime.LookupServiceMock(onlavaruntime.TypeOf[*Service]())`,
		`onlavaruntime.MarkServiceInitialized("svc", func(force context.Context) { onlavaInternalServiceService.svc.Shutdown(force) })`,
		`onlavaruntime.RegisterEndpointFunc(Hello, "svc", "Hello")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated file to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateMainImportsCronOnlyPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/cronapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"cronapp"}`)
	writeFile(t, dir, "service/api.go", `package service

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "jobs/jobs.go", `package jobs

import (
	"example.com/cronapp/service"
	"github.com/pbrazdil/onlava/cron"
)

var _ = cron.NewJob("tick", cron.JobConfig{
	Title:    "Tick",
	Every:    60,
	Endpoint: service.Run,
})
`)

	app, err := parse.App(dir, "cronapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["onlava_internal_main/main.go"])
	if !strings.Contains(got, `_ "example.com/cronapp/jobs"`) {
		t.Fatalf("expected generated main to import cron package, got:\n%s", got)
	}
	if !strings.Contains(got, `Temporal: onlavaruntime.TemporalConfig{Enabled: true}`) {
		t.Fatalf("expected generated main to enable temporal runtime for cron, got:\n%s", got)
	}
}

func TestGenerateRegistersTemporalServiceAccessor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/temporalsvc\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"temporalsvc"}`)
	writeFile(t, dir, "svc/api.go", `package svc

//onlava:service
type Service struct{}
`)

	app, err := parse.App(dir, "temporalsvc")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/onlava.gen.go"])
	if !strings.Contains(got, `onlavatemporal.RegisterServiceAccessorFor[*Service](func() (any, error) {`) {
		t.Fatalf("expected generated service accessor registration, got:\n%s", got)
	}
}

func TestGenerateMainEnablesDBStudioWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/dbstudioapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"dbstudioapp"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "dbstudioapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.GenerateWithConfig(app, appcfg.Config{EnableDBStudio: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["onlava_internal_main/main.go"])
	if !strings.Contains(got, `_ "github.com/pbrazdil/onlava/runtimeapp"`) {
		t.Fatalf("expected generated main to import runtimeapp for db studio, got:\n%s", got)
	}
	if !strings.Contains(got, "EnableDBStudio: true") {
		t.Fatalf("expected generated main to enable db studio, got:\n%s", got)
	}
}

func TestGenerateMainOmitsRuntimeAppByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/headlessapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"headlessapp","proxy":{"api_host":"api.acme.localhost"}}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
func Run(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "headlessapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["onlava_internal_main/main.go"])
	if strings.Contains(got, `github.com/pbrazdil/onlava/runtimeapp`) {
		t.Fatalf("generated main imported runtimeapp by default:\n%s", got)
	}
}

func TestGenerateMainIncludesObservabilityFiltersWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/obsapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"obsapp"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
func Run(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "obsapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.GenerateWithConfig(app, appcfg.Config{
		Observability: appcfg.ObservabilityConfig{
			Logs: appcfg.EndpointFilterConfig{
				ExcludeEndpoints: []string{"sync.*"},
			},
			Tracing: appcfg.EndpointFilterConfig{
				IncludeEndpoints: []string{"tenants.Config"},
			},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["onlava_internal_main/main.go"])
	for _, want := range []string{
		`Observability: onlavaruntime.ObservabilityConfig{`,
		`Logs: onlavaruntime.EndpointFilterConfig{ExcludeEndpoints: []string{"sync.*"}}`,
		`Tracing: onlavaruntime.EndpointFilterConfig{IncludeEndpoints: []string{"tenants.Config"}}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated main to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateMainIncludesTemporalConfigWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/temporalapp\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"temporalapp"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api public
func Run(ctx context.Context) error { return nil }
`)

	app, err := parse.App(dir, "temporalapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.GenerateWithConfig(app, appcfg.Config{
		Temporal: appcfg.TemporalConfig{
			Enabled:         true,
			Mode:            "local",
			Namespace:       "default",
			AddressEnv:      "TEMPORAL_ADDRESS",
			TaskQueuePrefix: "onlava.temporalapp",
			PayloadCodec:    "onlava-json-v1",
			APIKeyEnv:       "TEMPORAL_API_KEY",
			TLS: appcfg.TemporalTLSConfig{
				Enabled:           true,
				ServerNameEnv:     "TEMPORAL_TLS_SERVER_NAME",
				CACertFileEnv:     "TEMPORAL_TLS_CA_CERT_FILE",
				ClientCertFileEnv: "TEMPORAL_TLS_CERT_FILE",
				ClientKeyFileEnv:  "TEMPORAL_TLS_KEY_FILE",
			},
			Local: appcfg.TemporalLocalConfig{
				AutoStart:  true,
				DBFilename: ".onlava/temporal/dev.sqlite",
			},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["onlava_internal_main/main.go"])
	for _, want := range []string{
		`Temporal: onlavaruntime.TemporalConfig{`,
		`Enabled: true`,
		`Mode: "local"`,
		`Namespace: "default"`,
		`AddressEnv: "TEMPORAL_ADDRESS"`,
		`TaskQueuePrefix: "onlava.temporalapp"`,
		`PayloadCodec: "onlava-json-v1"`,
		`APIKeyEnv: "TEMPORAL_API_KEY"`,
		`TLS: onlavaruntime.TemporalTLSConfig{Enabled: true, ServerNameEnv: "TEMPORAL_TLS_SERVER_NAME", CACertFileEnv: "TEMPORAL_TLS_CA_CERT_FILE", ClientCertFileEnv: "TEMPORAL_TLS_CERT_FILE", ClientKeyFileEnv: "TEMPORAL_TLS_KEY_FILE"}`,
		`Local: onlavaruntime.TemporalLocalConfig{AutoStart: true, DBFilename: ".onlava/temporal/dev.sqlite"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated main to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateMainImportsTemporalDeclarationPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/temporaldecl\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+repoRoot(t)+"\n")
	writeFile(t, dir, ".onlava.json", `{"name":"temporaldecl"}`)
	writeFile(t, dir, "svc/api.go", `package svc

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`)
	writeFile(t, dir, "workers/workflows.go", `package workers

import "github.com/pbrazdil/onlava/temporal"

type Input struct {
	ID string
}

type Output struct {
	ID string
}

var Fulfill = temporal.NewWorkflow[Input, Output]("orders.Fulfill/v1", temporal.WorkflowConfig{}, func(ctx temporal.WorkflowContext, in Input) (Output, error) {
	return Output{ID: in.ID}, nil
})
`)

	app, err := parse.App(dir, "temporaldecl")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(out.Generated["onlava_internal_main/main.go"])
	if !strings.Contains(got, `_ "example.com/temporaldecl/workers"`) {
		t.Fatalf("expected generated main to import temporal declaration package, got:\n%s", got)
	}
	if !strings.Contains(got, `Temporal: onlavaruntime.TemporalConfig{Enabled: true}`) {
		t.Fatalf("expected generated main to enable temporal runtime, got:\n%s", got)
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
