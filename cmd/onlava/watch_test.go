package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

func TestScanWatchedFilesIncludesWatchedSourceFiles(t *testing.T) {
	root := t.TempDir()

	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "go.sum", "example.com/mod v1.0.0 h1:abc\n")
	writeWatchFile(t, root, ".env", "DatabaseURL=postgres://localhost/db\n")
	writeWatchFile(t, root, ".env.local", "DatabaseURL=postgres://localhost/local\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")
	writeWatchFile(t, root, "svc/native.cpp", "int main() { return 0; }\n")
	writeWatchFile(t, root, "svc/native.h", "#pragma once\n")
	writeWatchFile(t, root, "svc/native.s", "TEXT noop(SB),$0\n")
	writeWatchFile(t, root, "README.md", "# ignored\n")
	writeWatchFile(t, root, ".git/config", "[core]\n")
	writeWatchFile(t, root, "node_modules/pkg/index.js", "console.log('ignored')\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}

	for _, want := range []string{".onlava.json", "go.mod", "go.sum", ".env", ".env.local", "svc/api.go", "svc/native.cpp", "svc/native.h", "svc/native.s"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"README.md", ".git/config", "node_modules/pkg/index.js"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestScanWatchedFilesIncludesEmbeddedFiles(t *testing.T) {
	root := t.TempDir()

	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "svc/embed.go", `package svc

import _ "embed"

//go:embed data/config.json "data/with space.txt" assets/*.txt static
var embedded []byte
`)
	writeWatchFile(t, root, "svc/data/config.json", `{"ok":true}`)
	writeWatchFile(t, root, "svc/data/with space.txt", "hello\n")
	writeWatchFile(t, root, "svc/assets/a.txt", "a\n")
	writeWatchFile(t, root, "svc/assets/ignored.md", "ignored\n")
	writeWatchFile(t, root, "svc/static/index.html", "<h1>hi</h1>\n")
	writeWatchFile(t, root, "svc/static/.hidden", "hidden\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	for _, want := range []string{"svc/data/config.json", "svc/data/with space.txt", "svc/assets/a.txt", "svc/static/index.html"} {
		if _, ok := snapshot[want]; !ok {
			t.Fatalf("snapshot missing embedded file %q: %+v", want, snapshot)
		}
	}
	for _, ignored := range []string{"svc/assets/ignored.md", "svc/static/.hidden"} {
		if _, ok := snapshot[ignored]; ok {
			t.Fatalf("snapshot unexpectedly included %q: %+v", ignored, snapshot)
		}
	}
}

func TestShouldIgnoreWatchPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "svc/api.go", want: false},
		{path: "svc/native.cpp", want: false},
		{path: ".env", want: false},
		{path: ".env.local", want: false},
		{path: ".git/config", want: true},
		{path: "node_modules/pkg/index.js", want: true},
		{path: "onlava_internal_main/main.go", want: true},
		{path: "svc/.cache/tmp.go", want: true},
	}
	for _, tt := range tests {
		if got := shouldIgnoreWatchPath(tt.path); got != tt.want {
			t.Fatalf("shouldIgnoreWatchPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestWaitForStableChangeEventsPollsWhenEventsAreMissed(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, ".onlava.json", `{"name":"watchapp"}`)
	writeWatchFile(t, root, "go.mod", "module example.com/watchapp\n\ngo 1.26.0\n")
	writeWatchFile(t, root, "svc/api.go", "package svc\n")

	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedFiles returned error: %v", err)
	}
	writeWatchFile(t, root, "svc/api.go", "package svc\n\nconst changed = true\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan struct{})
	next, err := waitForStableChangeEvents(ctx, root, snapshot, events)
	if err != nil {
		t.Fatalf("waitForStableChangeEvents returned error: %v", err)
	}
	if snapshotsEqual(snapshot, next) {
		t.Fatal("snapshot did not change")
	}
}

func TestSnapshotsEqual(t *testing.T) {
	a := fileSnapshot{
		"a.go": {size: 1},
		"b.go": {size: 2},
	}
	b := fileSnapshot{
		"b.go": {size: 2},
		"a.go": {size: 1},
	}
	c := fileSnapshot{
		"a.go": {size: 3},
		"b.go": {size: 2},
	}

	if !snapshotsEqual(a, b) {
		t.Fatal("snapshotsEqual returned false for equal snapshots")
	}
	if snapshotsEqual(a, c) {
		t.Fatal("snapshotsEqual returned true for different snapshots")
	}
}

func TestChangedPaths(t *testing.T) {
	before := fileSnapshot{
		"svc/added.go":   {size: 1},
		"svc/deleted.go": {size: 2},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 4},
	}
	after := fileSnapshot{
		"svc/added.go":   {size: 9},
		"svc/new.go":     {size: 5},
		"svc/same.go":    {size: 3},
		"svc/updated.go": {size: 7},
	}

	got := changedPaths(before, after)
	want := []string{
		"svc/added.go",
		"svc/deleted.go",
		"svc/new.go",
		"svc/updated.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changedPaths mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestPrepareDevAgentSessionDefaultsToUnixBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	t.Setenv(devElectricUpstreamEnv, "http://127.0.0.1:3001")
	client, session, backend, restore, err := prepareDevAgentSession(ctx, root, app.Config{
		Name: "demo",
		Dev: app.DevConfig{
			Services: map[string]app.DevServiceConfig{
				"electric": {Kind: "electric", Route: "electric"},
			},
		},
		Proxy: app.ProxyConfig{
			Frontends: map[string]app.FrontendConfig{
				"web": {Host: "web.demo.localhost", Upstream: "127.0.0.1:5173", AllowSharedUpstream: true},
			},
		},
	}, devListenRequest{})
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if client == nil || session == nil {
		t.Fatalf("agent client/session = %v/%v, want both", client, session)
	}
	agentPaths, err := localagent.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := os.Getenv("ONLAVA_DEV_CACHE_DIR"), filepath.Join(agentPaths.AgentDir, "dashboard"); got != want {
		t.Fatalf("ONLAVA_DEV_CACHE_DIR = %q, want %q", got, want)
	}
	if got, want := os.Getenv("ONLAVA_AGENT_HOME"), agentPaths.Home; got != want {
		t.Fatalf("ONLAVA_AGENT_HOME = %q, want %q", got, want)
	}
	if backend.Network != "unix" {
		t.Fatalf("backend network = %q, want unix", backend.Network)
	}
	wantPrefix := filepath.Join(root, ".onlava", "sessions", session.SessionID, "run")
	if !strings.HasPrefix(backend.Addr, wantPrefix) || filepath.Base(backend.Addr) != "api.sock" {
		t.Fatalf("backend addr = %q, want under %q", backend.Addr, wantPrefix)
	}
	api := session.Backends[localagent.RouteAPI]
	if api.Network != "unix" || api.Addr != backend.Addr {
		t.Fatalf("session API backend = %+v, want unix %q", api, backend.Addr)
	}
	if _, ok := session.Backends[localagent.RouteDashboard]; ok {
		t.Fatalf("session dashboard backend should not be visible when the agent dashboard is active: %+v", session.Backends)
	}
	if _, ok := session.Backends[localagent.RouteMCP]; ok {
		t.Fatalf("session MCP backend should not be visible when the agent dashboard is active: %+v", session.Backends)
	}
	if route := session.Routes[localagent.RouteDashboard]; !strings.Contains(route, "console.onlava.localhost") || !strings.Contains(route, "/s/"+session.SessionID) {
		t.Fatalf("session dashboard route = %q", route)
	}
	if route := session.Routes[localagent.RouteMCP]; !strings.Contains(route, "mcp."+session.SessionID+".onlava.localhost") {
		t.Fatalf("session MCP route = %q", route)
	}
	if _, err := os.Stat(filepath.Join(root, ".onlava", "sessions", session.SessionID, "manifest.json")); err != nil {
		t.Fatalf("session manifest missing: %v", err)
	}
	web := session.Backends["web"]
	if web.Network != "tcp" || web.Addr != "127.0.0.1:5173" {
		t.Fatalf("session frontend backend = %+v", web)
	}
	if route := session.Routes["web"]; !strings.Contains(route, "web."+session.SessionID+".onlava.localhost") {
		t.Fatalf("session frontend route = %q", route)
	}
	electric := session.Backends["electric"]
	if electric.Network != "tcp" || electric.Addr != "127.0.0.1:3001" {
		t.Fatalf("session electric backend = %+v", electric)
	}
	if route := session.Routes["electric"]; !strings.Contains(route, "electric."+session.SessionID+".onlava.localhost") {
		t.Fatalf("session electric route = %q", route)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionPrefersTCPWhenRequested(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	_, session, backend, restore, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{PreferTCP: true})
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if backend.Network != "tcp" || !strings.HasPrefix(backend.Addr, "127.0.0.1:") {
		t.Fatalf("backend = %+v, want hidden loopback TCP", backend)
	}
	api := session.Backends[localagent.RouteAPI]
	if api.Network != "tcp" || api.Addr != backend.Addr {
		t.Fatalf("session API backend = %+v, want tcp %q", api, backend.Addr)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionUsesExplicitSessionID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	_, session, _, restore, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{SessionID: "review-a"})
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if session.SessionID != "review-a" {
		t.Fatalf("session id = %q, want review-a", session.SessionID)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionNewSessionAddsSuffix(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agentDone := startTestAgentServer(t, ctx)

	root := t.TempDir()
	_, first, _, restoreFirst, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{NewSession: true})
	defer restoreFirst()
	if err != nil {
		t.Fatalf("prepareDevAgentSession first: %v", err)
	}
	_, second, _, restoreSecond, err := prepareDevAgentSession(ctx, root, app.Config{Name: "demo"}, devListenRequest{NewSession: true})
	defer restoreSecond()
	if err != nil {
		t.Fatalf("prepareDevAgentSession second: %v", err)
	}
	if first.SessionID == second.SessionID {
		t.Fatalf("new sessions reused id %q", first.SessionID)
	}
	wantPrefix := localagent.SessionID(root, "")
	if !strings.HasPrefix(first.SessionID, wantPrefix+"-") || !strings.HasPrefix(second.SessionID, wantPrefix+"-") {
		t.Fatalf("new session ids = %q and %q, want %q-*", first.SessionID, second.SessionID, wantPrefix)
	}

	cancel()
	waitForTestAgentServer(t, agentDone)
}

func TestPrepareDevAgentSessionFallsBackWhenAgentDisabled(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_DISABLE", "1")
	_, session, backend, restore, err := prepareDevAgentSession(context.Background(), t.TempDir(), app.Config{Name: "demo"}, devListenRequest{})
	defer restore()
	if err != nil {
		t.Fatalf("prepareDevAgentSession: %v", err)
	}
	if session != nil {
		t.Fatalf("session = %+v, want nil", session)
	}
	if backend.Network != "tcp" || backend.Addr != "127.0.0.1:4000" {
		t.Fatalf("backend = %+v, want default TCP fallback", backend)
	}
}

func startTestAgentServer(t *testing.T, ctx context.Context) <-chan error {
	t.Helper()
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	return done
}

func waitForTestAgentServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent shutdown")
	}
}

func writeWatchFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
