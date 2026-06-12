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
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	stdpgxpool "github.com/jackc/pgx/v5/pgxpool"
	"scenery.sh/errs"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/clientgen"
	"scenery.sh/internal/devmeta"
	"scenery.sh/internal/devtools"
	"scenery.sh/internal/envfile"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/redact"
	"scenery.sh/internal/stdlog"
	"scenery.sh/internal/termstyle"
	"scenery.sh/internal/wire"
	"scenery.sh/internal/wiremodel"
	scenerypgxpool "scenery.sh/pgxpool"
	"scenery.sh/rlog"
)

func TestAppDiscoverRootAcceptsLegacyID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(`{"id":"legacy-app","proxy":{"workspace":"acme","api_host":"api.acme.localhost","console_host":"console.acme.localhost","temporal_host":"temporal.acme.localhost","grafana_host":"grafana.acme.localhost","frontends":{"web":{"host":"web.acme.localhost","root":"apps/web","upstream":"127.0.0.1:5173"}}},"observability":{"logs":{"exclude_endpoints":["sync.*"]},"tracing":{"include_endpoints":["tenants.Config"]}}}`), 0o644); err != nil {
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

func TestAppDiscoverRootRejectsRemovedProxyKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	removedKey := "m" + "cp_host"
	data := `{"name":"badapp","proxy":{"` + removedKey + `":"unused.localhost"}}`
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil ||
		!strings.Contains(err.Error(), filepath.Join(dir, ".scenery.json")) ||
		!strings.Contains(err.Error(), `unknown .scenery.json field "proxy.`+removedKey+`"`) ||
		!strings.Contains(err.Error(), "proxy."+removedKey+" was removed") ||
		!strings.Contains(err.Error(), "proxy.api_host/proxy.console_host/proxy.frontends") {
		t.Fatalf("DiscoverRoot error = %v, want unknown field for %s", err, removedKey)
	}
}

func TestAppDiscoverRootAcceptsTemporalConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(`{"name":"temporalapp","temporal":{"enabled":true,"mode":"local","namespace":"default","address_env":"TEMPORAL_ADDRESS","task_queue_prefix":"scenery.temporalapp","payload_codec":"scenery-json-v1","api_key_env":"TEMPORAL_API_KEY","tls":{"enabled":true,"server_name_env":"TEMPORAL_TLS_SERVER_NAME","ca_cert_file_env":"TEMPORAL_TLS_CA_CERT_FILE","client_cert_file_env":"TEMPORAL_TLS_CERT_FILE","client_key_file_env":"TEMPORAL_TLS_KEY_FILE"},"local":{"auto_start":true,"db_filename":".scenery/temporal/dev.db"},"typescript":{"enabled":true,"runtime":"bun","auto_start":true}}}`), 0o644); err != nil {
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
	if cfg.Temporal.AddressEnv != "TEMPORAL_ADDRESS" || cfg.Temporal.TaskQueuePrefix != "scenery.temporalapp" {
		t.Fatalf("temporal env/task queue = %+v", cfg.Temporal)
	}
	if cfg.Temporal.PayloadCodec != "scenery-json-v1" || cfg.Temporal.APIKeyEnv != "TEMPORAL_API_KEY" || !cfg.Temporal.TLS.Enabled {
		t.Fatalf("temporal security = %+v", cfg.Temporal)
	}
	if !cfg.Temporal.Local.AutoStart {
		t.Fatalf("temporal booleans = %+v", cfg.Temporal)
	}
	if cfg.Temporal.Local.DBFilename != ".scenery/temporal/dev.db" {
		t.Fatalf("temporal local db = %q", cfg.Temporal.Local.DBFilename)
	}
	if !cfg.Temporal.TypeScript.Enabled || cfg.Temporal.TypeScript.Runtime != "bun" || !cfg.Temporal.TypeScript.AutoStart {
		t.Fatalf("temporal typescript = %+v", cfg.Temporal.TypeScript)
	}
}

func TestAppDiscoverRootTemporalRequiresExplicitEnabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(`{"name":"temporaloff","temporal":{"mode":"local","local":{"auto_start":true},"typescript":{"enabled":true,"runtime":"bun","auto_start":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := appcfg.DiscoverRoot(dir)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if cfg.Temporal.Enabled {
		t.Fatalf("temporal.enabled = true without explicit opt-in: %+v", cfg.Temporal)
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
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(data), 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(`{"proxy":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), ".scenery.json must define a non-empty name or id"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ".scenery.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"app","proxy":{"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), configPath+`: unknown .scenery.json field "proxy.extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRejectsUnknownTemporalFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ".scenery.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"app","temporal":{"enabled":true,"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), configPath+`: unknown .scenery.json field "temporal.extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAppDiscoverRootRejectsUnknownFieldsInConfigCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		path string
	}{
		{
			name: "frontend map value",
			json: `{"name":"app","proxy":{"frontends":{"web":{"host":"web.localhost","extra":"value"}}}}`,
			path: "proxy.frontends.web.extra",
		},
		{
			name: "client generator array value",
			json: `{"name":"app","generators":{"clients":[{"output":"apps/web/src/client.ts","extra":"value"}]}}`,
			path: "generators.clients[0].extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			configPath := filepath.Join(dir, ".scenery.json")
			if err := os.WriteFile(configPath, []byte(tt.json), 0o644); err != nil {
				t.Fatal(err)
			}

			_, _, err := appcfg.DiscoverRoot(dir)
			if err == nil {
				t.Fatal("DiscoverRoot returned nil error")
			}
			if got, want := err.Error(), configPath+`: unknown .scenery.json field "`+tt.path+`"`; got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
		})
	}
}

func TestAppDiscoverRootKeepsStringMapsOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := `{"name":"app","tasks":{"harness":{"env":{"EXTRA":"value"}}},"dev":{"services":{"electric":{"kind":"electric","env":{"ELECTRIC_INSECURE":"true"}}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".scenery.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := appcfg.DiscoverRoot(dir)
	if err != nil {
		t.Fatalf("DiscoverRoot returned error: %v", err)
	}
	if cfg.Tasks["harness"].Env["EXTRA"] != "value" {
		t.Fatalf("task env = %+v", cfg.Tasks["harness"].Env)
	}
	if cfg.Dev.Services["electric"].Env["ELECTRIC_INSECURE"] != "true" {
		t.Fatalf("service env = %+v", cfg.Dev.Services["electric"].Env)
	}
}

func TestAppDiscoverRootReportsConfigPathForInvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, ".scenery.json")
	if err := os.WriteFile(configPath, []byte(`{"name":`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := appcfg.DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if !strings.Contains(err.Error(), configPath+": decode .scenery.json:") {
		t.Fatalf("error = %q, want config path and decode prefix", err.Error())
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
	if cfg.Grafana.Version != "13.0.2" {
		t.Fatalf("grafana version = %q", cfg.Grafana.Version)
	}
	if cfg.Victoria.Metrics.Version != "v1.145.0" {
		t.Fatalf("victoria metrics version = %q", cfg.Victoria.Metrics.Version)
	}
	if cfg.Victoria.Logs.Version != "v1.50.0" {
		t.Fatalf("victoria logs version = %q", cfg.Victoria.Logs.Version)
	}
	if cfg.Victoria.Traces.Version != "v0.9.2" {
		t.Fatalf("victoria traces version = %q", cfg.Victoria.Traces.Version)
	}
}

func TestDevtoolsGrafanaPluginPreinstallSyncPinsVersions(t *testing.T) {
	t.Parallel()

	got := devtools.GrafanaPluginPreinstallSync()
	want := "victoriametrics-metrics-datasource@0.25.0,victoriametrics-logs-datasource@0.28.0"
	if got != want {
		t.Fatalf("GrafanaPluginPreinstallSync = %q, want %q", got, want)
	}
}

func TestDevtoolsPinnedVersionsRejectsMissingValues(t *testing.T) {
	t.Parallel()

	_, err := devtools.ParsePinnedVersions([]byte(`{
		"schema_version": "scenery.internal.devtools.versions.v1",
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

func TestClientgenNamedAliasesAndWireCapabilities(t *testing.T) {
	t.Parallel()

	appRoot := persistentParseTestApp(t, "clientwireapp", map[string]string{
		"go.mod":        "module example.com/clientwireapp\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + appcfg.RepoRoot() + "\n",
		".scenery.json": `{"name":"clientwireapp"}`,
		"point/point.go": `package point

type Point3 struct {
	X int ` + "`json:\"x\"`" + `
	Y int ` + "`json:\"y\"`" + `
	Z int ` + "`json:\"z\"`" + `
}
`,
		"maps/api.go": `package maps

import (
	"context"

	"example.com/clientwireapp/point"
)

type TaskStatus string

type Response struct {
	Status TaskStatus ` + "`json:\"status\"`" + `
	Point  point.Point3 ` + "`json:\"point\"`" + `
}

type UnsupportedResponse struct {
	Meta map[string]any ` + "`json:\"meta\"`" + `
}

//scenery:api public
func Get(ctx context.Context) (*Response, error) {
	return &Response{}, nil
}

//scenery:api public
func Unsupported(ctx context.Context) (*UnsupportedResponse, error) {
	return &UnsupportedResponse{}, nil
}
`,
	})
	model, err := parse.App(appRoot, "clientwireapp")
	if err != nil {
		t.Fatalf("parse.App() error = %v", err)
	}

	out, err := clientgen.GenerateTypeScript(model, clientgen.TypeScriptOptions{AppSlug: "clientwireapp"})
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

	caps := wiremodel.AppCapabilities(model)
	if got := caps.Endpoints["maps.Get"]; !got.Available {
		t.Fatalf("supported endpoint = %+v", got)
	}
	if got := caps.Endpoints["maps.Unsupported"]; got.Available || got.UnsupportedReason == "" {
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
		Token string `json:"token" scenery:"sensitive"`
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

func TestPGXPoolParseConfigInjectsSceneryTracer(t *testing.T) {
	t.Parallel()

	cfg, err := scenerypgxpool.ParseConfig("postgres://scenery:scenery@localhost/scenery?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	if cfg.ConnConfig.Tracer == nil {
		t.Fatal("expected scenery query tracer")
	}
}

func TestPGXPoolInstrumentConfigIsIdempotent(t *testing.T) {
	t.Parallel()

	cfg, err := scenerypgxpool.ParseConfig("postgres://scenery:scenery@localhost/scenery?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}
	first := cfg.ConnConfig.Tracer
	pool, err := scenerypgxpool.NewWithConfig(context.Background(), cfg)
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

	cfg, err := stdpgxpool.ParseConfig("postgres://scenery:scenery@localhost/scenery?sslmode=disable")
	if err != nil {
		t.Fatalf("standard ParseConfig returned error: %v", err)
	}
	base := &fakeQueryTracer{}
	cfg.ConnConfig.Tracer = base
	pool, err := scenerypgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	pool.Close()

	tracer := cfg.ConnConfig.Tracer
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

func TestRedactValueRedactsScenerySensitiveFields(t *testing.T) {
	t.Parallel()

	type nested struct {
		Token string `json:"token" scenery:"sensitive"`
	}
	type payload struct {
		Password string `json:"password" scenery:"sensitive"`
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

	rlog.Info("hello", "service", "scenery", "count", 3)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}
	if entry["msg"] != "hello" {
		t.Fatalf("msg = %v, want %q", entry["msg"], "hello")
	}
	if entry["service"] != "scenery" {
		t.Fatalf("service = %v, want %q", entry["service"], "scenery")
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
	return slices.Contains(env, want)
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
