package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestValidateDevDomainURLWarnsWithoutEdge(t *testing.T) {
	_ = isolateCommandAgentHome(t)

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
