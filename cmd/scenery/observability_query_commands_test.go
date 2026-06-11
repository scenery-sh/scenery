package main

import (
	"context"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
)

func TestParseLogsQueryArgsDefaultsAndRejectsLogQL(t *testing.T) {
	t.Parallel()

	opts, err := parseLogsQueryArgs([]string{"--query", "error", "--fields", "_time,message"})
	if err != nil {
		t.Fatalf("parseLogsQueryArgs: %v", err)
	}
	if opts.Session != "current" || opts.Since != 15*time.Minute || opts.Limit != 200 || opts.Timeout != 3*time.Second {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if strings.Join(opts.Fields, ",") != "_time,message" {
		t.Fatalf("fields = %+v", opts.Fields)
	}
	if _, err := parseLogsQueryArgs([]string{"--logql", `{app="demo"} |= "error"`}); err == nil || !strings.Contains(err.Error(), "LogsQL") {
		t.Fatalf("logql error = %v", err)
	}
	if _, err := parseLogsQueryArgs([]string{"--query", "error", "--since", "nope"}); err == nil || !strings.Contains(err.Error(), "invalid since duration") {
		t.Fatalf("since error = %v", err)
	}
}

func TestParseLogsQueryArgsClampsLimitAndTailRejectsBounds(t *testing.T) {
	t.Parallel()

	opts, err := parseLogsQueryArgs([]string{"--query", "*", "--limit", "1000000"})
	if err != nil {
		t.Fatalf("parseLogsQueryArgs: %v", err)
	}
	if opts.Limit != logsQueryLimitMax || len(opts.Warnings) != 1 || !strings.Contains(opts.Warnings[0], "clamped") {
		t.Fatalf("limit clamp = %d warnings=%+v", opts.Limit, opts.Warnings)
	}
	if _, err := parseLogsTailArgs([]string{"--query", "*", "--start", "2026-06-08T10:00:00Z"}); err == nil || !strings.Contains(err.Error(), "start_offset") {
		t.Fatalf("tail bounds error = %v", err)
	}
}

func TestParseMetricsQueryArgsDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseMetricsQueryArgs([]string{"--promql", "up", "--instant", "--limit", "7"})
	if err != nil {
		t.Fatalf("parseMetricsQueryArgs: %v", err)
	}
	if opts.Session != "current" || opts.Since != 15*time.Minute || opts.Step != 5*time.Second || opts.Timeout != 3*time.Second || !opts.Instant || opts.Limit != 7 {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if _, err := parseMetricsQueryArgs([]string{"--instant"}); err == nil || !strings.Contains(err.Error(), "missing required --promql") {
		t.Fatalf("promql error = %v", err)
	}
}

func TestParseMetricsCatalogArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseMetricsCatalogArgs([]string{"--match", "scenery_request_duration_seconds", "--since", "30m", "--timeout", "4s", "--limit", "100000"}, true)
	if err != nil {
		t.Fatalf("parseMetricsCatalogArgs: %v", err)
	}
	if opts.Session != "current" || opts.Since != 30*time.Minute || opts.Match != "scenery_request_duration_seconds" || opts.Limit != metricsCatalogLimitMax || opts.Timeout != 4*time.Second {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if len(opts.Warnings) != 1 || !strings.Contains(opts.Warnings[0], "clamped") {
		t.Fatalf("warnings = %+v", opts.Warnings)
	}
	if _, err := parseMetricsCatalogArgs(nil, true); err == nil || !strings.Contains(err.Error(), "missing required --match") {
		t.Fatalf("match error = %v", err)
	}
}

func TestResolveQueryScopeRequiresExplicitSessionToExist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"obsapp","id":"obs-id"}`)

	agentServer, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agentServer.Run(ctx) }()
	defer stopObservabilityQueryTestAgent(t, cancel, done)

	client := localagent.NewClient(agentServer.Paths().SocketPath)
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "obs-id",
		AppRoot:   root,
		SessionID: "session-a",
		Branch:    "feature/a",
	}); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := resolveQueryScopeForAppWithClient(ctx, client, root, cfg, "session-a")
	if err != nil {
		t.Fatalf("resolve existing session: %v", err)
	}
	if scope.SessionID != "session-a" || scope.Branch != "feature/a" {
		t.Fatalf("scope = %+v", scope)
	}
	if _, err := resolveQueryScopeForAppWithClient(ctx, client, root, cfg, "typo-session"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("resolve missing session error = %v", err)
	}
}

func stopObservabilityQueryTestAgent(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent server shutdown")
	}
}
