package codegen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/parse"
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

	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_service_scenery.gen.go"), out.Generated["service/scenery.gen.go"])
	assertGolden(t, filepath.Join(repoRoot(t), "testdata", "golden", "basic_main.go"), out.Generated["scenery_internal_main/main.go"])
}

func TestGenerateEndpointMiddlewareAndRawEdges(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "endpointedges", map[string]string{
		"go.mod":        "module example.com/endpointedges\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRoot(t) + "\n",
		".scenery.json": `{"name":"endpointedges"}`,
		"svc/api.go": `package svc

import "context"

//scenery:api public tag:foo
func Hello(_ context.Context) error { return nil }
`,
		"svc/mw.go": `package svc

import "scenery.sh/middleware"

//scenery:middleware target=tag:foo
func Apply(req middleware.Request, next middleware.Next) middleware.Response {
	return next(req)
}
`,
		"raw/api.go": `package raw

import "net/http"

//scenery:api public raw
func Hook(w http.ResponseWriter, req *http.Request) {}
`,
	})

	app, err := parse.App(dir, "endpointedges")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["svc/scenery.gen.go"])
	if !strings.Contains(got, "sceneryArg0 context.Context") {
		t.Fatalf("expected sanitized context param, got:\n%s", got)
	}
	if strings.Contains(got, "CallEndpoint(_,") {
		t.Fatalf("expected blank identifier to be sanitized, got:\n%s", got)
	}
	if !strings.Contains(got, "sceneryInternalImplHello(ctx)") {
		t.Fatalf("expected invoke closure to use ctx, got:\n%s", got)
	}
	if !strings.Contains(got, "RegisterMiddleware(&sceneryruntime.Middleware") {
		t.Fatalf("expected middleware registration, got:\n%s", got)
	}
	if !strings.Contains(got, `MiddlewareIDs:`) || !strings.Contains(got, `[]string{"example.com/endpointedges/svc.Apply"}`) {
		t.Fatalf("expected endpoint middleware ids, got:\n%s", got)
	}

	rawGot := string(out.Generated["raw/scenery.gen.go"])
	if strings.Contains(rawGot, "\"context\"") {
		t.Fatalf("expected raw-only package to omit context import, got:\n%s", rawGot)
	}
}

func TestGenerateModelCRUDBackend(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot(t), "testdata", "apps", "model-dsl")
	app, err := parse.App(root, "modeldsl")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(out.Generated["tasks/scenery.gen.go"])
	for _, want := range []string{
		"type TaskCreate struct",
		"type TaskPatch struct",
		"sceneryModelTaskStore",
		`"CreateTask"`,
		`"/tasks"`,
		"sceneryModelUpdateTask(ctx, pathArgs[0], payload.(TaskPatch))",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated CRUD backend missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `Name: "DeleteTask"`) {
		t.Fatalf("disabled delete endpoint generated:\n%s", got)
	}
	mainGot := string(out.Generated["scenery_internal_main/main.go"])
	if !strings.Contains(mainGot, `_ "example.com/modeldsl/tasks"`) {
		t.Fatalf("main did not import generated model package:\n%s", mainGot)
	}
}

func TestGenerateServiceLifecycleAndEarlySecrets(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "servicelifecycle", map[string]string{
		"go.mod":        "module example.com/servicelifecycle\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRoot(t) + "\n",
		".scenery.json": `{"name":"servicelifecycle"}`,
		"svc/api.go": `package svc

import "context"

var secrets struct {
	TestQueueConcurrency string
}

var maxConcurrency = secrets.TestQueueConcurrency

//scenery:service
type Service struct{}

func initService() (*Service, error) { return &Service{}, nil }

func (s *Service) Shutdown(force context.Context) {}

//scenery:api public
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

	configGot := string(out.Generated["svc/00_scenery_config.gen.go"])
	for _, want := range []string{
		`var sceneryInternalDotEnvInitialized = sceneryruntime.MustLoadDotEnvIntoEnv()`,
		`var sceneryInternalSecretsInitialized = func() bool {`,
		`sceneryruntime.MustPopulateSecrets(&secrets)`,
	} {
		if !strings.Contains(configGot, want) {
			t.Fatalf("expected early secrets file to contain %q, got:\n%s", want, configGot)
		}
	}

	got := string(out.Generated["svc/scenery.gen.go"])
	for _, want := range []string{
		`sceneryruntime.RegisterServiceInitializer("svc", func() error {`,
		`_, err := sceneryInternalGetService()`,
		`sceneryruntime.LookupServiceMock(sceneryruntime.TypeOf[*Service]())`,
		`sceneryruntime.MarkServiceInitialized("svc", func(force context.Context) { sceneryInternalServiceService.svc.Shutdown(force) })`,
		`sceneryruntime.RegisterEndpointFunc(Hello, "svc", "Hello")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated file to contain %q, got:\n%s", want, got)
		}
	}
}

func TestGenerateMainImportsRuntimeDeclarationPackages(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "runtimeimports", map[string]string{
		"go.mod":        "module example.com/runtimeimports\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRoot(t) + "\n",
		".scenery.json": `{"name":"runtimeimports"}`,
		"service/api.go": `package service

import "context"

//scenery:api private
func Run(ctx context.Context) error { return nil }
`,
		"jobs/jobs.go": `package jobs

import (
	"example.com/runtimeimports/service"
	"scenery.sh/cron"
)

var _ = cron.NewJob("tick", cron.JobConfig{
	Title:    "Tick",
	Every:    60,
	Endpoint: service.Run,
})
`,
		"workers/workflows.go": `package workers

import "scenery.sh/temporal"

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

	app, err := parse.App(dir, "runtimeimports")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	out, err := codegen.Generate(app)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got := string(out.Generated["scenery_internal_main/main.go"])
	if !strings.Contains(got, `_ "example.com/runtimeimports/jobs"`) {
		t.Fatalf("expected generated main to import cron package, got:\n%s", got)
	}
	if !strings.Contains(got, `_ "example.com/runtimeimports/workers"`) {
		t.Fatalf("expected generated main to import temporal declaration package, got:\n%s", got)
	}
	if strings.Contains(got, `Temporal: sceneryruntime.TemporalConfig{Enabled: true}`) {
		t.Fatalf("expected generated main to leave temporal disabled without explicit config, got:\n%s", got)
	}
}

func TestGenerateRegistersTemporalServiceAccessor(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "temporalsvc", map[string]string{
		"go.mod":        "module example.com/temporalsvc\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRoot(t) + "\n",
		".scenery.json": `{"name":"temporalsvc"}`,
		"svc/api.go": `package svc

//scenery:service
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

	got := string(out.Generated["svc/scenery.gen.go"])
	if !strings.Contains(got, `scenerytemporal.RegisterServiceAccessorFor[*Service](func() (any, error) {`) {
		t.Fatalf("expected generated service accessor registration, got:\n%s", got)
	}
}

func TestGenerateMainConfigVariants(t *testing.T) {
	t.Parallel()

	dir := persistentCodegenTestApp(t, "mainconfig", map[string]string{
		"go.mod":        "module example.com/mainconfig\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + repoRoot(t) + "\n",
		".scenery.json": `{"name":"mainconfig","proxy":{"api_host":"api.acme.localhost"}}`,
		"svc/api.go": `package svc

import "context"

//scenery:api public
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

	got := string(out.Generated["scenery_internal_main/main.go"])
	if strings.Contains(got, `scenery.sh/runtimeapp`) {
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
			TaskQueuePrefix: "scenery.temporalapp",
			PayloadCodec:    "scenery-json-v1",
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
				DBFilename: ".scenery/temporal/dev.db",
			},
		},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	got = string(out.Generated["scenery_internal_main/main.go"])
	for _, want := range []string{
		`Observability: sceneryruntime.ObservabilityConfig{`,
		`Logs: sceneryruntime.EndpointFilterConfig{ExcludeEndpoints: []string{"sync.*"}}`,
		`Tracing: sceneryruntime.EndpointFilterConfig{IncludeEndpoints: []string{"tenants.Config"}}`,
		`_ "scenery.sh/temporal"`,
		`Temporal: sceneryruntime.TemporalConfig{`,
		`Enabled: true`,
		`Mode: "local"`,
		`Namespace: "default"`,
		`AddressEnv: "TEMPORAL_ADDRESS"`,
		`TaskQueuePrefix: "scenery.temporalapp"`,
		`PayloadCodec: "scenery-json-v1"`,
		`APIKeyEnv: "TEMPORAL_API_KEY"`,
		`TLS: sceneryruntime.TemporalTLSConfig{Enabled: true, ServerNameEnv: "TEMPORAL_TLS_SERVER_NAME", CACertFileEnv: "TEMPORAL_TLS_CA_CERT_FILE", ClientCertFileEnv: "TEMPORAL_TLS_CERT_FILE", ClientKeyFileEnv: "TEMPORAL_TLS_KEY_FILE"}`,
		`Local: sceneryruntime.TemporalLocalConfig{AutoStart: true, DBFilename: ".scenery/temporal/dev.db"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated main to contain %q, got:\n%s", want, got)
		}
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
	root := filepath.Join(cacheDir, "scenery", "internal-codegen-tests", name)
	fingerprint := codegenTestAppFingerprint(files)
	marker := filepath.Join(root, ".scenery-test-fingerprint")
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
	writeFileIfChanged(t, root, ".scenery-test-fingerprint", fingerprint+"\n")
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
