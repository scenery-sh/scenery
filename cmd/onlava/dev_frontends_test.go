package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

func TestManagedFrontendCommandUsesViteLocalBin(t *testing.T) {
	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"vite"}}`)
	bin := writeFrontendBin(t, root, "vite")
	cmd, args, err := managedFrontendCommand(root, "49231")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"--host", "127.0.0.1", "--port", "49231"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendCommandUsesAstroLocalBin(t *testing.T) {
	root := t.TempDir()
	writeFrontendPackage(t, root, `{"scripts":{"dev":"astro dev"}}`)
	bin := writeFrontendBin(t, root, "astro")
	cmd, args, err := managedFrontendCommand(root, "49232")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != bin {
		t.Fatalf("command = %q, want %q", cmd, bin)
	}
	wantArgs := []string{"dev", "--host", "127.0.0.1", "--port", "49232"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

func TestManagedFrontendPackageManagerUsesWorkspaceParent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"packageManager":"bun@1.3.11"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	appRoot := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFrontendPackage(t, appRoot, `{"scripts":{"dev":"custom-dev"}}`)
	if got := managedFrontendPackageManager(appRoot); got != "bun" {
		t.Fatalf("package manager = %q, want bun", got)
	}
}

func TestFrontendDevEnvIncludesSessionRoutes(t *testing.T) {
	env := frontendDevEnv([]string{"EXISTING=1"}, "/repo/app", "127.0.0.1:49231", localagent.Session{
		SessionID: "main-abc123",
		Routes: map[string]string{
			localagent.RouteAPI: "http://api.main-abc123.onlava.localhost:9440/",
			"electric":          "http://electric.main-abc123.onlava.localhost:9440/",
		},
	})
	for _, want := range []string{
		"EXISTING=1",
		"HOST=127.0.0.1",
		"PORT=49231",
		"ONLAVA_APP_ROOT=/repo/app",
		"ONLAVA_SESSION_ID=main-abc123",
		"ONLAVA_API_BASE_URL=http://api.main-abc123.onlava.localhost:9440/",
		"VITE_API_BASE_URL=http://api.main-abc123.onlava.localhost:9440/",
		"ONLAVA_ELECTRIC_URL=http://electric.main-abc123.onlava.localhost:9440/",
		"VITE_ELECTRIC_URL=http://electric.main-abc123.onlava.localhost:9440/",
	} {
		if !containsString(env, want) {
			t.Fatalf("frontendDevEnv() missing %q in %s", want, strings.Join(env, "\n"))
		}
	}
}

func TestManagedFrontendBackendsRequiresExplicitSharedUpstream(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {
					Root:     "apps/web",
					Upstream: "127.0.0.1:5173",
				},
			},
		},
	}
	_, _, err := managedFrontendBackendsForSession(context.Background(), root, cfg, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, ".onlava", "sessions", "main-test"),
	})
	if err == nil {
		t.Fatal("expected managed frontend fallback error")
	}
	if !strings.Contains(err.Error(), "allow_shared_upstream") {
		t.Fatalf("error = %q, want allow_shared_upstream guidance", err)
	}
}

func TestManagedFrontendBackendsAllowsExplicitSharedUpstream(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {
					Root:                "apps/web",
					Upstream:            "127.0.0.1:5173",
					AllowSharedUpstream: true,
				},
			},
		},
	}
	backends, processes, err := managedFrontendBackendsForSession(context.Background(), root, cfg, nil, localagent.Session{
		SessionID: "main-test",
		StateRoot: filepath.Join(root, ".onlava", "sessions", "main-test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 0 {
		t.Fatalf("processes = %d, want 0", len(processes))
	}
	if got := backends["web"]; got.Network != "tcp" || got.Addr != "127.0.0.1:5173" {
		t.Fatalf("web backend = %+v", got)
	}
}

func writeFrontendPackage(t *testing.T, root, data string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFrontendBin(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}
