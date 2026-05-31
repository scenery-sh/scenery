package parse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	stdpgxpool "github.com/jackc/pgx/v5/pgxpool"
	"github.com/pbrazdil/onlava/errs"
	appcfg "github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/clientgen"
	"github.com/pbrazdil/onlava/internal/devmeta"
	"github.com/pbrazdil/onlava/internal/devtools"
	"github.com/pbrazdil/onlava/internal/envfile"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
	"github.com/pbrazdil/onlava/internal/redact"
	"github.com/pbrazdil/onlava/internal/stdlog"
	"github.com/pbrazdil/onlava/internal/termstyle"
	"github.com/pbrazdil/onlava/internal/wire"
	"github.com/pbrazdil/onlava/internal/wiremodel"
	onlavapgxpool "github.com/pbrazdil/onlava/pgxpool"
	"github.com/pbrazdil/onlava/rlog"
)

func TestAppDiscoverRootAcceptsLegacyID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"id":"legacy-app","proxy":{"workspace":"acme","api_host":"api.acme.localhost","console_host":"console.acme.localhost","mcp_host":"mcp.acme.localhost","temporal_host":"temporal.acme.localhost","grafana_host":"grafana.acme.localhost","frontends":{"web":{"host":"web.acme.localhost","root":"apps/web","upstream":"127.0.0.1:5173"}}},"observability":{"logs":{"exclude_endpoints":["sync.*"]},"tracing":{"include_endpoints":["tenants.Config"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	root, cfg, err := appcfg.DiscoverRoot(dir)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if root != dir {
		t.Fatalf("root = %q, want %q", root, dir)
	}
	if cfg.Name != "legacy-app" {
		t.Fatalf("cfg.Name = %q, want %q", cfg.Name, "legacy-app")
	}
	if cfg.Proxy.Workspace != "acme" {
		t.Fatalf("cfg.Proxy.Workspace = %q, want %q", cfg.Proxy.Workspace, "acme")
	}
	if cfg.Proxy.APIHost != "api.acme.localhost" {
		t.Fatalf("cfg.Proxy.APIHost = %q, want %q", cfg.Proxy.APIHost, "api.acme.localhost")
	}
	if cfg.Proxy.TemporalHost != "temporal.acme.localhost" {
		t.Fatalf("cfg.Proxy.TemporalHost = %q, want %q", cfg.Proxy.TemporalHost, "temporal.acme.localhost")
	}
	if cfg.Proxy.GrafanaHost != "grafana.acme.localhost" {
		t.Fatalf("cfg.Proxy.GrafanaHost = %q, want %q", cfg.Proxy.GrafanaHost, "grafana.acme.localhost")
	}
	if cfg.Proxy.Frontends["web"].Host != "web.acme.localhost" {
		t.Fatalf("cfg.Proxy.Frontends[web].Host = %q, want %q", cfg.Proxy.Frontends["web"].Host, "web.acme.localhost")
	}
	if len(cfg.Observability.Logs.ExcludeEndpoints) != 1 || cfg.Observability.Logs.ExcludeEndpoints[0] != "sync.*" {
		t.Fatalf("cfg.Observability.Logs.ExcludeEndpoints = %v", cfg.Observability.Logs.ExcludeEndpoints)
	}
	if len(cfg.Observability.Tracing.IncludeEndpoints) != 1 || cfg.Observability.Tracing.IncludeEndpoints[0] != "tenants.Config" {
		t.Fatalf("cfg.Observability.Tracing.IncludeEndpoints = %v", cfg.Observability.Tracing.IncludeEndpoints)
	}
}

func TestAppDiscoverRootAcceptsTemporalConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"temporalapp","temporal":{"enabled":true,"mode":"local","namespace":"default","address_env":"TEMPORAL_ADDRESS","task_queue_prefix":"onlava.temporalapp","payload_codec":"onlava-json-v1","api_key_env":"TEMPORAL_API_KEY","tls":{"enabled":true,"server_name_env":"TEMPORAL_TLS_SERVER_NAME","ca_cert_file_env":"TEMPORAL_TLS_CA_CERT_FILE","client_cert_file_env":"TEMPORAL_TLS_CERT_FILE","client_key_file_env":"TEMPORAL_TLS_KEY_FILE"},"local":{"auto_start":true,"db_filename":".onlava/temporal/dev.sqlite"},"typescript":{"enabled":true,"runtime":"bun","auto_start":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := appcfg.DiscoverRoot(dir)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if !cfg.Temporal.Enabled {
		t.Fatal("expected temporal.enabled")
	}
	if cfg.Temporal.Mode != "local" || cfg.Temporal.Namespace != "default" {
		t.Fatalf("temporal mode/namespace = %+v", cfg.Temporal)
	}
	if cfg.Temporal.AddressEnv != "TEMPORAL_ADDRESS" || cfg.Temporal.TaskQueuePrefix != "onlava.temporalapp" {
		t.Fatalf("temporal env/task queue = %+v", cfg.Temporal)
	}
	if cfg.Temporal.PayloadCodec != "onlava-json-v1" || cfg.Temporal.APIKeyEnv != "TEMPORAL_API_KEY" || !cfg.Temporal.TLS.Enabled {
		t.Fatalf("temporal security = %+v", cfg.Temporal)
	}
	if !cfg.Temporal.Local.AutoStart {
		t.Fatalf("temporal booleans = %+v", cfg.Temporal)
	}
	if cfg.Temporal.Local.DBFilename != ".onlava/temporal/dev.sqlite" {
		t.Fatalf("temporal local db = %q", cfg.Temporal.Local.DBFilename)
	}
	if !cfg.Temporal.TypeScript.Enabled || cfg.Temporal.TypeScript.Runtime != "bun" || !cfg.Temporal.TypeScript.AutoStart {
		t.Fatalf("temporal typescript = %+v", cfg.Temporal.TypeScript)
	}
}

func TestAppDiscoverRootAcceptsDevServicesConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := `{
  "name": "devservices",
  "dev": {
    "setup": ["./scripts/db-safe-apply.sh"],
    "services": {
      "postgres": {
        "kind": "postgres",
        "version": "18",
        "isolation": "database"
      },
      "electric": {
        "kind": "electric",
        "image": "electricsql/electric:canary",
        "database": "postgres",
        "route": "electric",
        "env": {
          "ELECTRIC_INSECURE": "true"
        }
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := appcfg.DiscoverRoot(dir)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	postgres := cfg.Dev.Services["postgres"]
	if len(cfg.Dev.Setup) != 1 || cfg.Dev.Setup[0] != "./scripts/db-safe-apply.sh" {
		t.Fatalf("dev setup = %+v", cfg.Dev.Setup)
	}
	if postgres.Kind != "postgres" || postgres.Version != "18" || postgres.Isolation != "database" {
		t.Fatalf("postgres service = %+v", postgres)
	}
	electric := cfg.Dev.Services["electric"]
	if electric.Kind != "electric" || electric.Database != "postgres" || electric.Route != "electric" {
		t.Fatalf("electric service = %+v", electric)
	}
	if electric.Env["ELECTRIC_INSECURE"] != "true" {
		t.Fatalf("electric env = %+v", electric.Env)
	}
}

func TestAppConfigAppIDPrefersExplicitID(t *testing.T) {
	t.Parallel()

	cfg := appcfg.Config{Name: "display-name", ID: "stable-id"}
	if got, want := cfg.AppID(), "stable-id"; got != want {
		t.Fatalf("AppID() = %q, want %q", got, want)
	}
	cfg.ID = ""
	if got, want := cfg.AppID(), "display-name"; got != want {
		t.Fatalf("AppID() fallback = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRequiresNameOrID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"proxy":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), ".onlava.json must define a non-empty name or id"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"app","proxy":{"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), `json: unknown field "extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRejectsUnknownTemporalFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"app","temporal":{"enabled":true,"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), `json: unknown field "extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestDevmetaBuildMetadataSnapshotIncludesPlatformStats(t *testing.T) {
	t.Parallel()

	metaJSON, err := devmeta.BuildMetadataSnapshot(&model.App{})
	if err != nil {
		t.Fatalf("BuildMetadataSnapshot() error = %v", err)
	}

	var payload struct {
		Services []struct {
			Name string `json:"name"`
			RPCs []struct {
				Name        string   `json:"name"`
				AccessType  string   `json:"access_type"`
				HTTPMethods []string `json:"http_methods"`
			} `json:"rpcs"`
		} `json:"svcs"`
	}
	if err := json.Unmarshal(metaJSON, &payload); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	for _, svc := range payload.Services {
		if svc.Name != "platform" {
			continue
		}
		for _, rpc := range svc.RPCs {
			if rpc.Name != "Stats" {
				continue
			}
			if rpc.AccessType != "public" {
				t.Fatalf("access_type = %q, want public", rpc.AccessType)
			}
			if len(rpc.HTTPMethods) != 1 || rpc.HTTPMethods[0] != "GET" {
				t.Fatalf("http_methods = %v, want [GET]", rpc.HTTPMethods)
			}
			return
		}
	}
	t.Fatal("platform.Stats metadata missing")
}

func TestDevmetaBuildAPIEncodingIncludesPlatformStats(t *testing.T) {
	t.Parallel()

	apiJSON, err := devmeta.BuildAPIEncoding(&model.App{})
	if err != nil {
		t.Fatalf("BuildAPIEncoding() error = %v", err)
	}

	var payload struct {
		Services []struct {
			Name string `json:"name"`
			RPCs []struct {
				Name    string   `json:"name"`
				Path    string   `json:"path"`
				Methods []string `json:"methods"`
			} `json:"rpcs"`
		} `json:"services"`
	}
	if err := json.Unmarshal(apiJSON, &payload); err != nil {
		t.Fatalf("decode api encoding: %v", err)
	}

	for _, svc := range payload.Services {
		if svc.Name != "platform" {
			continue
		}
		for _, rpc := range svc.RPCs {
			if rpc.Name != "Stats" {
				continue
			}
			if rpc.Path != "/platform.Stats" {
				t.Fatalf("path = %q, want /platform.Stats", rpc.Path)
			}
			if len(rpc.Methods) != 1 || rpc.Methods[0] != "GET" {
				t.Fatalf("methods = %v, want [GET]", rpc.Methods)
			}
			return
		}
	}
	t.Fatal("platform.Stats API encoding missing")
}

func TestDevtoolsPinnedVersionsConfig(t *testing.T) {
	t.Parallel()

	cfg := devtools.PinnedVersions()
	if cfg.Grafana.Version != "13.0.1+security-01" {
		t.Fatalf("grafana version = %q", cfg.Grafana.Version)
	}
	if cfg.Victoria.Metrics.Version != "v1.141.0" {
		t.Fatalf("victoria metrics version = %q", cfg.Victoria.Metrics.Version)
	}
	if cfg.Victoria.Logs.Version != "v1.50.0" {
		t.Fatalf("victoria logs version = %q", cfg.Victoria.Logs.Version)
	}
	if cfg.Victoria.Traces.Version != "v0.8.1" {
		t.Fatalf("victoria traces version = %q", cfg.Victoria.Traces.Version)
	}
}

func TestDevtoolsGrafanaPluginPreinstallSyncPinsVersions(t *testing.T) {
	t.Parallel()

	got := devtools.GrafanaPluginPreinstallSync()
	want := "victoriametrics-metrics-datasource@0.24.0,victoriametrics-logs-datasource@0.27.1"
	if got != want {
		t.Fatalf("GrafanaPluginPreinstallSync = %q, want %q", got, want)
	}
}

func TestDevtoolsPinnedVersionsRejectsMissingValues(t *testing.T) {
	t.Parallel()

	_, err := devtools.ParsePinnedVersions([]byte(`{
		"schema_version": "onlava.internal.devtools.versions.v1",
		"grafana": {
			"version": "",
			"plugins": []
		},
		"victoria": {
			"metrics": {"version": "v1"},
			"logs": {"version": "v2"},
			"traces": {"version": "v3"}
		}
	}`))
	if err == nil {
		t.Fatal("expected missing grafana version error")
	}
}

func TestClientgenGenerateTypeScriptIncludesStructuredRequestHandling(t *testing.T) {
	t.Parallel()

	appRoot := filepath.Join(appcfg.RepoRoot(), "testdata", "apps", "basic")
	model, err := parse.App(appRoot, "basicapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	out, err := clientgen.GenerateTypeScript(model, clientgen.TypeScriptOptions{AppSlug: "basicapp"})
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

func TestClientgenGenerateTypeScriptIncludesNamedAliases(t *testing.T) {
	t.Parallel()

	appRoot := t.TempDir()
	writeRelocatedUnitTestFile(t, appRoot, "go.mod", "module example.com/clientapp\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+appcfg.RepoRoot()+"\n")
	writeRelocatedUnitTestFile(t, appRoot, ".onlava.json", `{"name":"clientapp"}`)
	writeRelocatedUnitTestFile(t, appRoot, "point/point.go", `package point

type Point3 struct {
	X int `+"`json:\"x\"`"+`
	Y int `+"`json:\"y\"`"+`
	Z int `+"`json:\"z\"`"+`
}
`)
	writeRelocatedUnitTestFile(t, appRoot, "maps/api.go", `package maps

import (
	"context"

	"example.com/clientapp/point"
)

type TaskStatus string

type Response struct {
	Status TaskStatus `+"`json:\"status\"`"+`
	Point  point.Point3 `+"`json:\"point\"`"+`
}

//onlava:api public
func Get(ctx context.Context) (*Response, error) {
	return &Response{}, nil
}
`)

	model, err := parse.App(appRoot, "clientapp")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	out, err := clientgen.GenerateTypeScript(model, clientgen.TypeScriptOptions{AppSlug: "clientapp"})
	if err != nil {
		t.Fatalf("GenerateTypeScript() error = %v", err)
	}
	got := string(out)

	for _, want := range []string{
		`export type TaskStatus = string`,
		`status: TaskStatus`,
		`export namespace point {`,
		`export interface Point3 {`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated client missing %q", want)
		}
	}
}

func TestWiremodelAppCapabilitiesMarksUnsupportedEndpointJSONOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeRelocatedUnitTestFile(t, root, "go.mod", "module example.com/wiretest\n\ngo 1.26.3\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => "+appcfg.RepoRoot()+"\n")
	writeRelocatedUnitTestFile(t, root, ".onlava.json", `{"name":"wiretest"}`)
	writeRelocatedUnitTestFile(t, root, "svc/api.go", `package svc

import "context"

type SupportedRequest struct {
	Name string `+"`json:\"name\"`"+`
}

type UnsupportedResponse struct {
	Meta map[string]any `+"`json:\"meta\"`"+`
}

//onlava:api public
func Supported(ctx context.Context, req *SupportedRequest) (*SupportedRequest, error) {
	return req, nil
}

//onlava:api public
func Unsupported(ctx context.Context) (*UnsupportedResponse, error) {
	return &UnsupportedResponse{}, nil
}
`)
	model, err := parse.App(root, "wiretest")
	if err != nil {
		t.Fatalf("parse.App() error = %v", err)
	}
	caps := wiremodel.AppCapabilities(model)
	if got := caps.Endpoints["svc.Supported"]; !got.Available {
		t.Fatalf("supported endpoint = %+v", got)
	}
	if got := caps.Endpoints["svc.Unsupported"]; got.Available || got.UnsupportedReason == "" {
		t.Fatalf("unsupported endpoint = %+v", got)
	}
	if caps.SchemaHash == "" {
		t.Fatal("schema hash is empty")
	}
}

func TestEnvfileParseFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".env")
	if err := os.WriteFile(path, []byte("\ufeff# comment\nexport A=one\nB=\"two\\nlines\"\nC='three'\nD=four\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := envfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := map[string]string{
		"A": "one",
		"B": "two\nlines",
		"C": "three",
		"D": "four",
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("ParseFile()[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestEnvfileMergeFilesAndAppendMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("A=from-env\nB=from-env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("B=from-local\nC=from-local\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	values, err := envfile.MergeFiles(root, ".env", ".env.local")
	if err != nil {
		t.Fatalf("MergeFiles: %v", err)
	}
	env := envfile.AppendMissing([]string{"A=from-process"}, values)

	if !containsEnv(env, "A=from-process") {
		t.Fatalf("AppendMissing missing process value: %v", env)
	}
	if containsEnv(env, "A=from-env") {
		t.Fatalf("AppendMissing overwrote process value: %v", env)
	}
	if !containsEnv(env, "B=from-local") {
		t.Fatalf("AppendMissing missing .env.local override: %v", env)
	}
	if !containsEnv(env, "C=from-local") {
		t.Fatalf("AppendMissing missing .env.local value: %v", env)
	}
}

func TestEnvfileParseFileMissingReturnsEmpty(t *testing.T) {
	t.Parallel()

	got, err := envfile.ParseFile(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("ParseFile missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ParseFile missing returned %#v, want empty map", got)
	}
}

func TestErrsHTTPErrorRedactsSensitiveMeta(t *testing.T) {
	t.Parallel()

	type credentials struct {
		Token string `json:"token" onlava:"sensitive"`
		Name  string `json:"name"`
	}

	rec := httptest.NewRecorder()
	errs.HTTPError(rec, errs.B().Code(errs.InvalidArgument).Msg("bad input").Meta("credentials", credentials{
		Token: "secret",
		Name:  "visible",
	}).Err())

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	meta := got["meta"].(map[string]any)
	creds := meta["credentials"].(map[string]any)
	if creds["token"] != "[redacted]" {
		t.Fatalf("token = %#v, want %q", creds["token"], "[redacted]")
	}
	if creds["name"] != "visible" {
		t.Fatalf("name = %#v, want %q", creds["name"], "visible")
	}
}

func TestPGXPoolParseConfigInjectsOnlavaTracer(t *testing.T) {
	t.Parallel()

	cfg, err := onlavapgxpool.ParseConfig("postgres://onlava:onlava@localhost/onlava?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if cfg.ConnConfig.Tracer == nil {
		t.Fatal("expected onlava query tracer")
	}
}

func TestPGXPoolInstrumentConfigIsIdempotent(t *testing.T) {
	t.Parallel()

	cfg, err := onlavapgxpool.ParseConfig("postgres://onlava:onlava@localhost/onlava?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	first := cfg.ConnConfig.Tracer
	pool, err := onlavapgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	pool.Close()
	if cfg.ConnConfig.Tracer != first {
		t.Fatalf("NewWithConfig wrapped tracer twice: first=%T second=%T", first, cfg.ConnConfig.Tracer)
	}
}

func TestPGXPoolQueryTracerDelegatesToBaseTracer(t *testing.T) {
	t.Parallel()

	cfg, err := stdpgxpool.ParseConfig("postgres://onlava:onlava@localhost/onlava?sslmode=disable")
	if err != nil {
		t.Fatalf("standard ParseConfig returned error: %v", err)
	}
	base := &fakeQueryTracer{}
	cfg.ConnConfig.Tracer = base
	pool, err := onlavapgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	pool.Close()

	tracer, ok := cfg.ConnConfig.Tracer.(pgx.QueryTracer)
	if !ok {
		t.Fatalf("instrumented tracer = %T, want pgx.QueryTracer", cfg.ConnConfig.Tracer)
	}
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{
		SQL:  "SELECT 1",
		Args: []any{1},
	})
	if got := ctx.Value(fakeTracerKey("start")); got != "ok" {
		t.Fatalf("start context value = %#v, want %q", got, "ok")
	}

	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	if base.starts != 1 {
		t.Fatalf("base starts = %d, want 1", base.starts)
	}
	if base.ends != 1 {
		t.Fatalf("base ends = %d, want 1", base.ends)
	}
}

func TestRedactValueRedactsOnlavaSensitiveFields(t *testing.T) {
	t.Parallel()

	type nested struct {
		Token string `json:"token" onlava:"sensitive"`
	}
	type payload struct {
		Password string `json:"password" onlava:"sensitive"`
		Nested   nested `json:"nested"`
		Name     string `json:"name"`
	}

	got := redact.Value(payload{
		Password: "secret",
		Nested:   nested{Token: "abc"},
		Name:     "visible",
	}).(map[string]any)

	if got["password"] != redact.Placeholder {
		t.Fatalf("password = %#v, want %q", got["password"], redact.Placeholder)
	}
	nestedValue := got["nested"].(map[string]any)
	if nestedValue["token"] != redact.Placeholder {
		t.Fatalf("token = %#v, want %q", nestedValue["token"], redact.Placeholder)
	}
	if got["name"] != "visible" {
		t.Fatalf("name = %#v, want %q", got["name"], "visible")
	}
}

func TestRedactHeadersRedactsSensitiveKeys(t *testing.T) {
	t.Parallel()

	headers := http.Header{
		"Authorization": []string{"Bearer secret"},
		"X-Test":        []string{"ok"},
	}
	got := redact.Headers(headers)
	if got["Authorization"] != redact.Placeholder {
		t.Fatalf("Authorization = %q, want %q", got["Authorization"], redact.Placeholder)
	}
	if got["X-Test"] != "ok" {
		t.Fatalf("X-Test = %q, want %q", got["X-Test"], "ok")
	}
}

func TestRedactStringRedactsSensitiveAssignments(t *testing.T) {
	t.Parallel()

	got := redact.String("token=abc password:secret Authorization=Bearer123 ok=value")
	for _, wantGone := range []string{"abc", "secret", "Bearer123"} {
		if got == "" || got == wantGone || bytes.Contains([]byte(got), []byte(wantGone)) {
			t.Fatalf("String(...) leaked %q in %q", wantGone, got)
		}
	}
	if !bytes.Contains([]byte(got), []byte(redact.Placeholder)) {
		t.Fatalf("String(...) = %q, want placeholder", got)
	}
}

func TestRedactURLRedactsSensitiveQueryAndUserinfo(t *testing.T) {
	t.Parallel()

	got, ok := redact.URL("https://user:pass@example.com/path?token=abc&x=1")
	if !ok {
		t.Fatal("URL(...) should parse")
	}
	if bytes.Contains([]byte(got), []byte("pass")) || bytes.Contains([]byte(got), []byte("abc")) {
		t.Fatalf("URL(...) leaked secret in %q", got)
	}
	if got != "https://user:%5Bredacted%5D@example.com/path?token=%5Bredacted%5D&x=1" {
		t.Fatalf("URL(...) = %q", got)
	}
}

func TestRlogInfoLogsWithKeyValues(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	rlog.Info("hello", "service", "onlava", "count", 3)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}
	if entry["msg"] != "hello" {
		t.Fatalf("msg = %v, want %q", entry["msg"], "hello")
	}
	if entry["service"] != "onlava" {
		t.Fatalf("service = %v, want %q", entry["service"], "onlava")
	}
	if entry["count"] != float64(3) {
		t.Fatalf("count = %v, want %v", entry["count"], 3)
	}
}

func TestRlogWithCarriesContext(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := rlog.With("component", "health").With("request_id", "abc")
	ctx.Warn("degraded", "status", 503)

	line := buf.String()
	for _, part := range []string{"component=health", "request_id=abc", "status=503", "level=WARN", "msg=degraded"} {
		if !strings.Contains(line, part) {
			t.Fatalf("log output missing %q: %s", part, line)
		}
	}
}

func TestRlogHandlesOddAndAttrInput(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	rlog.Info("hello", "foo", "bar", slog.String("a", "b"), "lonely")

	line := buf.String()
	for _, part := range []string{"foo=bar", "a=b", "lonely=<nil>"} {
		if !strings.Contains(line, part) {
			t.Fatalf("log output missing %q: %s", part, line)
		}
	}
}

func TestStdlogInstallSuppressesIdleHTTPChannelNoise(t *testing.T) {
	var out bytes.Buffer
	prev := log.Writer()
	stdlog.Install(&out)
	t.Cleanup(func() { log.SetOutput(prev) })

	n, err := log.Writer().Write([]byte(`2026/04/20 13:26:37 INFO Unsolicited response received on idle HTTP channel starting with "HTTP/1.1 400 Bad Request\r\n\r\n"; err=<nil>`))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n == 0 {
		t.Fatal("Write() = 0, want consumed bytes")
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q, want empty", out.String())
	}
}

func TestStdlogInstallPassesOtherLogsThrough(t *testing.T) {
	var out bytes.Buffer
	prev := log.Writer()
	stdlog.Install(&out)
	t.Cleanup(func() { log.SetOutput(prev) })

	input := []byte("2026/04/20 13:26:37 some other log\n")
	n, err := log.Writer().Write(input)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(input) {
		t.Fatalf("Write() = %d, want %d", n, len(input))
	}
	if out.String() != string(input) {
		t.Fatalf("output = %q, want %q", out.String(), string(input))
	}
}

func TestTermstyleCLICOLORFORCEOverridesNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")

	palette := termstyle.New(&bytes.Buffer{})
	if !palette.Enabled() {
		t.Fatal("palette should enable color when CLICOLOR_FORCE=1")
	}
}

func TestWireRequestFrameRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"message":"hello"}`)
	pathParams := []byte(`{"id":"42"}`)

	data := wire.EncodeRequestFrame("schema-123", pathParams, payload)
	got, ok, err := wire.DecodeRequestFrame(data)
	if err != nil {
		t.Fatalf("DecodeRequestFrame() error = %v", err)
	}
	if !ok {
		t.Fatalf("DecodeRequestFrame() ok = false")
	}
	if got.SchemaHash != "schema-123" {
		t.Fatalf("SchemaHash = %q", got.SchemaHash)
	}
	if !bytes.Equal(got.PathParamsJSON, pathParams) {
		t.Fatalf("PathParamsJSON = %q", got.PathParamsJSON)
	}
	if !bytes.Equal(got.PayloadJSON, payload) {
		t.Fatalf("PayloadJSON = %q", got.PayloadJSON)
	}
}

func TestWireResponseFrameRoundTrip(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"code":"invalid_argument","message":"bad"}`)

	data := wire.EncodeResponseFrame(400, true, payload)
	got, ok, err := wire.DecodeResponseFrame(data)
	if err != nil {
		t.Fatalf("DecodeResponseFrame() error = %v", err)
	}
	if !ok {
		t.Fatalf("DecodeResponseFrame() ok = false")
	}
	if got.Status != 400 {
		t.Fatalf("Status = %d", got.Status)
	}
	if !got.Error {
		t.Fatalf("Error = false")
	}
	if !bytes.Equal(got.PayloadJSON, payload) {
		t.Fatalf("PayloadJSON = %q", got.PayloadJSON)
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}

func writeRelocatedUnitTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeTracerKey string

type fakeQueryTracer struct {
	starts int
	ends   int
}

func (f *fakeQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	f.starts++
	return context.WithValue(ctx, fakeTracerKey("start"), "ok")
}

func (f *fakeQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	f.ends++
}
