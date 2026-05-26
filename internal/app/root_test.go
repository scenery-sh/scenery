package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRootAcceptsLegacyID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"id":"legacy-app","proxy":{"workspace":"acme","api_host":"api.acme.localhost","console_host":"console.acme.localhost","mcp_host":"mcp.acme.localhost","temporal_host":"temporal.acme.localhost","grafana_host":"grafana.acme.localhost","frontends":{"web":{"host":"web.acme.localhost","root":"apps/web","upstream":"127.0.0.1:5173"}}},"observability":{"logs":{"exclude_endpoints":["sync.*"]},"tracing":{"include_endpoints":["tenants.Config"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	root, cfg, err := DiscoverRoot(dir)
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

func TestDiscoverRootAcceptsTemporalConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"temporalapp","temporal":{"enabled":true,"mode":"local","namespace":"default","address_env":"TEMPORAL_ADDRESS","task_queue_prefix":"onlava.temporalapp","payload_codec":"onlava-json-v1","api_key_env":"TEMPORAL_API_KEY","tls":{"enabled":true,"server_name_env":"TEMPORAL_TLS_SERVER_NAME","ca_cert_file_env":"TEMPORAL_TLS_CA_CERT_FILE","client_cert_file_env":"TEMPORAL_TLS_CERT_FILE","client_key_file_env":"TEMPORAL_TLS_KEY_FILE"},"local":{"auto_start":true,"db_filename":".onlava/temporal/dev.sqlite"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := DiscoverRoot(dir)
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
}

func TestConfigAppIDPrefersExplicitID(t *testing.T) {
	cfg := Config{Name: "display-name", ID: "stable-id"}
	if got, want := cfg.AppID(), "stable-id"; got != want {
		t.Fatalf("AppID() = %q, want %q", got, want)
	}
	cfg.ID = ""
	if got, want := cfg.AppID(), "display-name"; got != want {
		t.Fatalf("AppID() fallback = %q, want %q", got, want)
	}
}

func TestDiscoverRootRequiresNameOrID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"proxy":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), ".onlava.json must define a non-empty name or id"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestDiscoverRootRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"app","proxy":{"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), `json: unknown field "extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestDiscoverRootRejectsUnknownTemporalFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"name":"app","temporal":{"enabled":true,"extra":"value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := DiscoverRoot(dir)
	if err == nil {
		t.Fatal("DiscoverRoot returned nil error")
	}
	if got, want := err.Error(), `json: unknown field "extra"`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
