package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRootAcceptsLegacyID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".onlava.json"), []byte(`{"id":"legacy-app","proxy":{"workspace":"acme","api_host":"api.acme.localhost","console_host":"console.acme.localhost","mcp_host":"mcp.acme.localhost","frontends":{"pulse":{"host":"pulse.acme.localhost","root":"apps/pulse","upstream":"127.0.0.1:5173"}}},"observability":{"logs":{"exclude_endpoints":["sync.*"]},"tracing":{"include_endpoints":["tenants.Config"]}}}`), 0o644); err != nil {
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
	if cfg.Proxy.Frontends["pulse"].Host != "pulse.acme.localhost" {
		t.Fatalf("cfg.Proxy.Frontends[pulse].Host = %q, want %q", cfg.Proxy.Frontends["pulse"].Host, "pulse.acme.localhost")
	}
	if len(cfg.Observability.Logs.ExcludeEndpoints) != 1 || cfg.Observability.Logs.ExcludeEndpoints[0] != "sync.*" {
		t.Fatalf("cfg.Observability.Logs.ExcludeEndpoints = %v", cfg.Observability.Logs.ExcludeEndpoints)
	}
	if len(cfg.Observability.Tracing.IncludeEndpoints) != 1 || cfg.Observability.Tracing.IncludeEndpoints[0] != "tenants.Config" {
		t.Fatalf("cfg.Observability.Tracing.IncludeEndpoints = %v", cfg.Observability.Tracing.IncludeEndpoints)
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
