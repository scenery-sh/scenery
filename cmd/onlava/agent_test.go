package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
)

func TestParseAgentArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseAgentArgs([]string{"--socket", "/tmp/onlava.sock", "--router-listen", "127.0.0.1:0", "--router-tls", "--trust", "--json"})
	if err != nil {
		t.Fatalf("parseAgentArgs: %v", err)
	}
	if opts.SocketPath != "/tmp/onlava.sock" || opts.RouterAddr != "127.0.0.1:0" || !opts.RouterTLS || !opts.Trust || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestAgentRouterTLSDefaultsOn(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_ROUTER_TLS", "")
	opts, err := parseAgentArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.effectiveRouterTLS() {
		t.Fatalf("effectiveRouterTLS() = false, want true")
	}
	opts, err = parseAgentArgs([]string{"--router-http"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.effectiveRouterTLS() {
		t.Fatalf("effectiveRouterTLS() with --router-http = true, want false")
	}
	t.Setenv("ONLAVA_AGENT_ROUTER_TLS", "0")
	opts, err = parseAgentArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.effectiveRouterTLS() {
		t.Fatalf("effectiveRouterTLS() with ONLAVA_AGENT_ROUTER_TLS=0 = true, want false")
	}
}

func TestStatusAndDownCommandsUseAgent(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".onlava.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Branch:    "feature/status",
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() error {
		return statusCommand([]string{"--json", "--app-root", appRoot})
	})
	var status struct {
		Sessions []localagent.Session `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("status json: %v\n%s", err, output)
	}
	if len(status.Sessions) != 1 || status.Sessions[0].SessionID != session.SessionID {
		t.Fatalf("status sessions = %+v, want %s", status.Sessions, session.SessionID)
	}

	output = captureStdout(t, func() error {
		return downCommand([]string{"--session", session.SessionID})
	})
	if !strings.Contains(output, session.SessionID) {
		t.Fatalf("down output %q missing session id", output)
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after down = %+v, want none", sessions)
	}
}

func TestParseDownArgsCleanupFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseDownArgs([]string{"--app-root", "/tmp/app", "--session", "session-a", "--db", "--state", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.AppRoot != "/tmp/app" || opts.SessionID != "session-a" || !opts.DB || !opts.State || !opts.All {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParsePruneArgs(t *testing.T) {
	opts, err := parsePruneArgs([]string{"--older-than", "14d", "--app-root", "/tmp/app", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.OlderThan != 14*24*time.Hour || opts.AppRoot != "/tmp/app" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parsePruneArgs([]string{}); err == nil {
		t.Fatal("expected missing --older-than to fail")
	}
}

func TestDownCommandRemovesSessionState(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".onlava.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Branch:    "feature/state-cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(session.StateRoot, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() error {
		return downCommand([]string{"--session", session.SessionID, "--state"})
	})
	if !strings.Contains(output, "removed onlava session state") {
		t.Fatalf("down output = %q", output)
	}
	if _, err := os.Stat(session.StateRoot); !os.IsNotExist(err) {
		t.Fatalf("session state still exists or stat failed: %v", err)
	}
}

func TestPruneCommandPrunesOldSessionState(t *testing.T) {
	t.Setenv("ONLAVA_AGENT_HOME", t.TempDir())
	server, err := localagent.NewServer(localagent.RunOptions{RouterAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("agent shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for agent shutdown")
		}
	}()

	client, err := localagent.DefaultClient()
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".onlava.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		SessionID: "old-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(session.StateRoot, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	output := captureStdout(t, func() error {
		return pruneCommand([]string{"--app-root", appRoot, "--older-than", "1ms"})
	})
	if !strings.Contains(output, "pruned onlava session old-session") {
		t.Fatalf("prune output = %q", output)
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after prune = %+v, want none", sessions)
	}
	if _, err := os.Stat(session.StateRoot); !os.IsNotExist(err) {
		t.Fatalf("session state still exists or stat failed: %v", err)
	}
}

func waitForAgentCommandPing(ctx context.Context, client *localagent.Client) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := client.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return lastErr
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	callErr := fn()
	_ = w.Close()
	os.Stdout = old
	data, readErr := io.ReadAll(r)
	_ = r.Close()
	if callErr != nil {
		t.Fatalf("command returned error: %v", callErr)
	}
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	return string(data)
}
