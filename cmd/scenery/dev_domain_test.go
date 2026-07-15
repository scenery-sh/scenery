package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func TestDevRoutingModeRejectsDomainWithHostMode(t *testing.T) {
	t.Parallel()

	cfg := app.Config{Dev: app.DevConfig{Routing: app.DevRoutingConfig{Mode: "host", Domain: "local.clean.tech"}}}
	if _, err := devRoutingMode(cfg); err == nil || !strings.Contains(err.Error(), "dev.routing.domain") {
		t.Fatalf("devRoutingMode error = %v", err)
	}
	cfg.Dev.Routing.Mode = "path"
	if _, err := devRoutingMode(cfg); err != nil {
		t.Fatalf("path mode with domain: %v", err)
	}
}

func TestValidateDevDomainURLWarnsWithoutEdge(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())

	session := localagent.Session{
		RouteManifest: localagent.RouteManifest{
			Mode:       localagent.RouteModePath,
			DomainHost: "pricing-local.clean.tech",
			DomainURL:  "https://pricing-local.clean.tech",
		},
	}
	url, warning := validateDevDomainURL(context.Background(), session)
	if url != "" {
		t.Fatalf("url = %q, want empty without edge", url)
	}
	if !strings.Contains(warning, "scenery system edge install") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestValidateDevDomainURLReportsConflict(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())

	session := localagent.Session{
		DomainHostConflict: &localagent.AliasLease{
			Host:    "pricing-local.clean.tech",
			AppRoot: "/tmp/other-worktree",
		},
	}
	url, warning := validateDevDomainURL(context.Background(), session)
	if url != "" {
		t.Fatalf("url = %q, want empty on conflict", url)
	}
	if !strings.Contains(warning, "/tmp/other-worktree") || !strings.Contains(warning, "pricing-local.clean.tech") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestDevExposeRouteNames(t *testing.T) {
	t.Parallel()

	cfg := app.Config{
		Frontends: map[string]app.FrontendConfig{"next": {Root: "apps/next"}},
		Dev: app.DevConfig{Routing: app.DevRoutingConfig{
			Domain: "local.clean.tech",
			Expose: []string{"api", "console", "next", "runtime", "api"},
		}},
	}
	names, err := devExposeRouteNames(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api", "dashboard", "next", "runtime"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}

	cfg.Dev.Routing.Expose = []string{"pulse"}
	if _, err := devExposeRouteNames(cfg); err == nil || !strings.Contains(err.Error(), `"pulse"`) {
		t.Fatalf("unknown frontend error = %v", err)
	}

	cfg.Dev.Routing.Expose = []string{"api"}
	cfg.Dev.Routing.Domain = ""
	if _, err := devExposeRouteNames(cfg); err == nil || !strings.Contains(err.Error(), "dev.routing.domain") {
		t.Fatalf("missing domain error = %v", err)
	}

	cfg.Dev.Routing.Expose = nil
	if names, err := devExposeRouteNames(cfg); err != nil || names != nil {
		t.Fatalf("empty expose = (%v, %v)", names, err)
	}
}

func TestRunURLDataIncludesAppURLWhenSet(t *testing.T) {
	t.Parallel()

	data := runURLData(runURLs{API: "http://localhost:4001/api/"}, false)
	if _, ok := data["app_url"]; ok {
		t.Fatalf("app_url present without a dev domain: %v", data)
	}
	data = runURLData(runURLs{App: "https://local.clean.tech", API: "http://localhost:4001/api/"}, false)
	if data["app_url"] != "https://local.clean.tech" {
		t.Fatalf("app_url = %v", data["app_url"])
	}
}

func TestWriteDetachedDevResultTextReportsDomainConflict(t *testing.T) {
	t.Setenv("SCENERY_AGENT_HOME", t.TempDir())

	result := detachedDevResult{
		Wait:          detachedDevWaitReady,
		PID:           123,
		AttachCommand: `scenery logs --follow --app-root "/tmp/app"`,
		DownCommand:   `scenery down --app-root "/tmp/app"`,
		Session: localagent.Session{
			SessionID: "app-abc",
			AppRoot:   "/tmp/app",
			Status:    "starting",
			RouteManifest: localagent.RouteManifest{
				Mode:    localagent.RouteModePath,
				BaseURL: "http://localhost:4001",
			},
			DomainHostConflict: &localagent.AliasLease{
				Host:    "pricing-local.clean.tech",
				AppRoot: "/tmp/other-worktree",
			},
		},
	}
	var buf bytes.Buffer
	if err := writeDetachedDevResult(&buf, false, result); err != nil {
		t.Fatalf("writeDetachedDevResult: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Dev domain held by another worktree:") ||
		!strings.Contains(out, "pricing-local.clean.tech owned by /tmp/other-worktree") {
		t.Fatalf("output missing domain conflict section:\n%s", out)
	}
}
