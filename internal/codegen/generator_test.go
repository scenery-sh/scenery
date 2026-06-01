package codegen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/codegen"
	"github.com/pbrazdil/onlava/internal/parse"
)

func TestGenerateBasicGolden(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "blankident", map[string]string{
		"go.mod":       "module example.com/blankident\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"blankident"}`,
		"svc/api.go": `package svc

import "context"

//onlava:api public
func Hello(_ context.Context) error { return nil }
`,
	})

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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "rawonly", map[string]string{
		"go.mod":       "module example.com/rawonly\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"rawonly"}`,
		"svc/api.go": `package svc

import "net/http"

//onlava:api public raw
func Hook(w http.ResponseWriter, req *http.Request) {}
`,
	})

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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "earlysecrets", map[string]string{
		"go.mod":       "module example.com/earlysecrets\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"earlysecrets"}`,
		"svc/api.go": `package svc

import "context"

var secrets struct {
	TestQueueConcurrency string
}

var maxConcurrency = secrets.TestQueueConcurrency

//onlava:api public
func Hello(ctx context.Context) error { return nil }
`,
	})

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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "middlewaregen", map[string]string{
		"go.mod":       "module example.com/middlewaregen\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"middlewaregen"}`,
		"svc/api.go": `package svc

import "context"

//onlava:api public tag:foo
func Hello(ctx context.Context) error { return nil }
`,
		"svc/mw.go": `package svc

import "github.com/pbrazdil/onlava/middleware"

//onlava:middleware target=tag:foo
func Apply(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,
	})

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

func TestGenerateRegistersServiceLifecycle(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "servicelifecycle", map[string]string{
		"go.mod":       "module example.com/servicelifecycle\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"servicelifecycle"}`,
		"svc/api.go": `package svc

import "context"

//onlava:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

func (s *Service) Shutdown(force context.Context) {}

//onlava:api public
func (s *Service) Hello(ctx context.Context) error { return nil }
`,
	})

	app, err := parse.App(dir, "servicelifecycle")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/onlava.gen.go"])
	for _, want := range []string{
		`onlavaruntime.RegisterServiceInitializer("svc", func() error {`,
		`_, err := onlavaInternalGetService()`,
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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "cronapp", map[string]string{
		"go.mod":       "module example.com/cronapp\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"cronapp"}`,
		"service/api.go": `package service

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`,
		"jobs/jobs.go": `package jobs

import (
	"example.com/cronapp/service"
	"github.com/pbrazdil/onlava/cron"
)

var _ = cron.NewJob("tick", cron.JobConfig{
	Title:    "Tick",
	Every:    60,
	Endpoint: service.Run,
})
`,
	})

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
	t.Parallel()

	dir := persistentCodegenTestApp(t, "temporalsvc", map[string]string{
		"go.mod":       "module example.com/temporalsvc\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"temporalsvc"}`,
		"svc/api.go": `package svc

//onlava:service
type Service struct{}
`,
	})

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

func TestGenerateMainConfigVariants(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "mainconfig", map[string]string{
		"go.mod":       "module example.com/mainconfig\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"mainconfig","proxy":{"api_host":"api.acme.localhost"}}`,
		"svc/api.go": `package svc

import "context"

//onlava:api public
func Run(ctx context.Context) error { return nil }
`,
	})

	app, err := parse.App(dir, "mainconfig")
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

	out, err = codegen.GenerateWithConfig(app, appcfg.Config{
		Observability: appcfg.ObservabilityConfig{
			Logs: appcfg.EndpointFilterConfig{
				ExcludeEndpoints: []string{"sync.*"},
			},
			Tracing: appcfg.EndpointFilterConfig{
				IncludeEndpoints: []string{"tenants.Config"},
			},
		},
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
				DBFilename: ".onlava/temporal/dev.db",
			},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got = string(out.Generated["onlava_internal_main/main.go"])
	for _, want := range []string{
		`Observability: onlavaruntime.ObservabilityConfig{`,
		`Logs: onlavaruntime.EndpointFilterConfig{ExcludeEndpoints: []string{"sync.*"}}`,
		`Tracing: onlavaruntime.EndpointFilterConfig{IncludeEndpoints: []string{"tenants.Config"}}`,
		`_ "github.com/pbrazdil/onlava/temporal"`,
		`Temporal: onlavaruntime.TemporalConfig{`,
		`Enabled: true`,
		`Mode: "local"`,
		`Namespace: "default"`,
		`AddressEnv: "TEMPORAL_ADDRESS"`,
		`TaskQueuePrefix: "onlava.temporalapp"`,
		`PayloadCodec: "onlava-json-v1"`,
		`APIKeyEnv: "TEMPORAL_API_KEY"`,
		`TLS: onlavaruntime.TemporalTLSConfig{Enabled: true, ServerNameEnv: "TEMPORAL_TLS_SERVER_NAME", CACertFileEnv: "TEMPORAL_TLS_CA_CERT_FILE", ClientCertFileEnv: "TEMPORAL_TLS_CERT_FILE", ClientKeyFileEnv: "TEMPORAL_TLS_KEY_FILE"}`,
		`Local: onlavaruntime.TemporalLocalConfig{AutoStart: true, DBFilename: ".onlava/temporal/dev.db"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated main to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateMainImportsTemporalDeclarationPackages(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "temporaldecl", map[string]string{
		"go.mod":       "module example.com/temporaldecl\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRoot(t) + "\n",
		".onlava.json": `{"name":"temporaldecl"}`,
		"svc/api.go": `package svc

import "context"

//onlava:api private
func Run(ctx context.Context) error { return nil }
`,
		"workers/workflows.go": `package workers

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
`,
	})

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

func persistentCodegenTestApp(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(cacheDir, "onlava", "internal-codegen-tests", name)
	fingerprint := codegenTestAppFingerprint(files)
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

func codegenTestAppFingerprint(files map[string]string) string {
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
