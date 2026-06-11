package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func TestParseAgentArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseAgentArgs([]string{"--socket", "/tmp/scenery.sock", "--router-listen", "127.0.0.1:0", "--router-tls", "--trust", "--json"})
	if err != nil {
		t.Fatalf("parseAgentArgs: %v", err)
	}
	if opts.SocketPath != "/tmp/scenery.sock" || opts.RouterAddr != "127.0.0.1:0" || !opts.RouterTLS || !opts.Trust || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestAgentRouterTLSDefaultsOn(t *testing.T) {
	t.Setenv("SCENERY_AGENT_ROUTER_TLS", "")
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
	t.Setenv("SCENERY_AGENT_ROUTER_TLS", "0")
	opts, err = parseAgentArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.effectiveRouterTLS() {
		t.Fatalf("effectiveRouterTLS() with SCENERY_AGENT_ROUTER_TLS=0 = true, want false")
	}
}

func TestWaitForAgentStartSurfacesRouterBindFailureFromLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("old log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := fileSize(logPath)
	failure := "listen scenery agent router at 127.0.0.1:443 failed; choose a different --router-listen address or free that port: listen tcp 127.0.0.1:443: bind: permission denied\n"
	if err := os.WriteFile(logPath, []byte("old log line\n"+failure), 0o644); err != nil {
		t.Fatal(err)
	}
	client := localagent.NewClient(filepath.Join(dir, "missing.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := waitForAgentStart(ctx, client, 0, logPath, offset)
	if err == nil {
		t.Fatal("expected waitForAgentStart to fail")
	}
	if !strings.Contains(err.Error(), "restarted scenery agent failed to start") || !strings.Contains(err.Error(), "bind: permission denied") {
		t.Fatalf("waitForAgentStart error = %v", err)
	}
	if strings.Contains(err.Error(), "old log line") {
		t.Fatalf("waitForAgentStart included pre-existing log line: %v", err)
	}
}

func TestWaitForAgentStartSurfacesPermissionFailureFromLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(logPath, []byte("old log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	offset := fileSize(logPath)
	failure := "open /Users/petrbrazdil/.scenery/agent/dashboard/devdash.json: permission denied\n"
	if err := os.WriteFile(logPath, []byte("old log line\n"+failure), 0o644); err != nil {
		t.Fatal(err)
	}
	client := localagent.NewClient(filepath.Join(dir, "missing.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := waitForAgentStart(ctx, client, 0, logPath, offset)
	if err == nil {
		t.Fatal("expected waitForAgentStart to fail")
	}
	if !strings.Contains(err.Error(), "restarted scenery agent failed to start") || !strings.Contains(err.Error(), "devdash.json: permission denied") {
		t.Fatalf("waitForAgentStart error = %v", err)
	}
	if strings.Contains(err.Error(), "old log line") {
		t.Fatalf("waitForAgentStart included pre-existing log line: %v", err)
	}
}

func TestStatusAndDownCommandsUseAgent(t *testing.T) {
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Branch:    "feature/status",
		RouteNamespace: localagent.RouteNamespace{
			Hosts: map[string]string{
				localagent.RouteAPI: "api.demo.localhost",
			},
		},
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliasURL := session.Aliases[localagent.RouteAPI]
	if aliasURL == "" {
		t.Fatalf("session did not claim api alias: %+v", session.Aliases)
	}
	exit := localagent.SubstrateExit{
		Component:     "server",
		PID:           123,
		ExitedAt:      time.Now().UTC(),
		ExitCode:      2,
		LogPath:       "/tmp/temporal.stderr.log",
		StdoutLogPath: "/tmp/temporal.stdout.log",
		StderrLogPath: "/tmp/temporal.stderr.log",
	}
	if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateTemporal,
		Status:   "exited",
		LastExit: &exit,
		ComponentExits: map[string]localagent.SubstrateExit{
			"server": exit,
		},
	}); err != nil {
		t.Fatal(err)
	}

	output := commandOutput(t, func(stdout io.Writer) error {
		return statusCommandWithClient(client, stdout, []string{"--json", "--app-root", appRoot})
	})
	var status struct {
		Sessions   []localagent.Session   `json:"sessions"`
		Substrates []localagent.Substrate `json:"substrates"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("status json: %v\n%s", err, output)
	}
	if len(status.Sessions) != 1 || status.Sessions[0].SessionID != session.SessionID {
		t.Fatalf("status sessions = %+v, want %s", status.Sessions, session.SessionID)
	}
	if len(status.Substrates) != 1 || status.Substrates[0].Status != "exited" || status.Substrates[0].LastExit == nil {
		t.Fatalf("status substrates = %+v", status.Substrates)
	}

	output = commandOutput(t, func(stdout io.Writer) error {
		return statusCommandWithClient(client, stdout, []string{"--app-root", appRoot})
	})
	if !strings.Contains(output, "APP ROOT") || !strings.Contains(output, "STATUS") || !strings.Contains(output, "API") {
		t.Fatalf("human status output missing table header:\n%s", output)
	}
	if strings.Contains(output, "SESSION") || !strings.Contains(output, appRoot) || !strings.Contains(output, "http://api.") {
		t.Fatalf("human status output should show app root entries and API route:\n%s", output)
	}

	output = commandOutput(t, func(stdout io.Writer) error {
		return downCommandWithClient(client, stdout, []string{"--app-root", appRoot, "--json"})
	})
	var down downResponse
	if err := json.Unmarshal([]byte(output), &down); err != nil {
		t.Fatalf("down json: %v\n%s", err, output)
	}
	if down.SchemaVersion != "scenery.down.v1" || down.SessionID != session.SessionID || !down.Deleted || down.RecordPreserved {
		t.Fatalf("down json = %+v", down)
	}
	if len(down.Messages) == 0 || !strings.Contains(down.Messages[len(down.Messages)-1], appRoot) {
		t.Fatalf("down json missing stop message: %+v", down)
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions after down = %+v, want none", sessions)
	}
	replacement, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   t.TempDir(),
		SessionID: "replacement",
		RouteNamespace: localagent.RouteNamespace{
			Hosts: map[string]string{
				localagent.RouteAPI: "api.demo.localhost",
			},
		},
		Backends: map[string]localagent.Backend{
			localagent.RouteAPI: {Network: "tcp", Addr: "127.0.0.1:4001"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Aliases[localagent.RouteAPI] != aliasURL {
		t.Fatalf("down did not free api alias: replacement=%+v want %q", replacement.Aliases, aliasURL)
	}
}

func TestDeleteStoppedSessionRecordToleratesAlreadyDeletedSession(t *testing.T) {
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Branch:    "feature/self-delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Delete(ctx, session.SessionID, false); err != nil {
		t.Fatal(err)
	}
	deleted, ok, err := deleteStoppedSessionRecord(ctx, client, session)
	if err != nil {
		t.Fatalf("deleteStoppedSessionRecord: %v", err)
	}
	if !ok {
		t.Fatal("already deleted session should be treated as deleted")
	}
	if deleted.SessionID != session.SessionID || deleted.StateRoot != session.StateRoot {
		t.Fatalf("deleted fallback session = %+v, want %+v", deleted, session)
	}
}

func TestDeleteStoppedSessionRecordPreservesChangedOwner(t *testing.T) {
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    appRoot,
		Branch:     "feature/owner-change",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := session
	stale.OwnerPID = 99999992
	stale.Owner = localagent.Owner{PID: stale.OwnerPID}
	if _, deleted, err := deleteStoppedSessionRecord(ctx, client, stale); err != nil {
		t.Fatalf("deleteStoppedSessionRecord: %v", err)
	} else if deleted {
		t.Fatal("stale owner delete should not delete current session record")
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].OwnerPID != os.Getpid() {
		t.Fatalf("sessions after stale owner delete = %+v", sessions)
	}
}

func TestDeleteStoppedSessionRecordPreservesSamePIDFingerprintMismatch(t *testing.T) {
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    appRoot,
		Branch:     "feature/same-pid-fingerprint",
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := session
	stale.Owner.CmdlineHash = "sha256:older-owner"
	if _, deleted, err := deleteStoppedSessionRecord(ctx, client, stale); err != nil {
		t.Fatalf("deleteStoppedSessionRecord: %v", err)
	} else if deleted {
		t.Fatal("stale owner fingerprint delete should not delete current session record")
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Owner.CmdlineHash != session.Owner.CmdlineHash {
		t.Fatalf("sessions after stale fingerprint delete = %+v", sessions)
	}
}

func TestDeleteStoppedSessionRecordPreservesOwnerClaimedFromOwnerlessSession(t *testing.T) {
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	ownerless, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "demo",
		AppRoot:   appRoot,
		Branch:    "feature/ownerless-claim",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:  "demo",
		AppRoot:    appRoot,
		SessionID:  ownerless.SessionID,
		OwnerPID:   os.Getpid(),
		ClaimOwner: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, deleted, err := deleteStoppedSessionRecord(ctx, client, ownerless); err != nil {
		t.Fatalf("deleteStoppedSessionRecord: %v", err)
	} else if deleted {
		t.Fatal("ownerless stale delete should not delete newly claimed session record")
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].OwnerPID != os.Getpid() {
		t.Fatalf("sessions after ownerless stale delete = %+v", sessions)
	}
}

func TestParseDownArgsCleanupFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseDownArgs([]string{"--app-root", "/tmp/app", "--db", "--state", "--all", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.AppRoot != "/tmp/app" || !opts.DB || !opts.State || !opts.All || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseDownArgs([]string{"--session", "session-a"}); err == nil || !strings.Contains(err.Error(), "use --app-root") {
		t.Fatalf("parseDownArgs --session error = %v", err)
	}
}

func TestParsePruneArgs(t *testing.T) {
	t.Parallel()

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
	t.Parallel()
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
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
	output := commandOutput(t, func(stdout io.Writer) error {
		return downCommandWithClient(client, stdout, []string{"--app-root", appRoot, "--state"})
	})
	if !strings.Contains(output, "removed scenery dev runtime state") {
		t.Fatalf("down output = %q", output)
	}
	if _, err := os.Stat(session.StateRoot); !os.IsNotExist(err) {
		t.Fatalf("session state still exists or stat failed: %v", err)
	}
}

func TestPruneCommandPrunesOldSessionState(t *testing.T) {
	t.Parallel()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	server, err := localagent.NewServer(localagent.RunOptions{Home: t.TempDir(), RouterAddr: "127.0.0.1:0"})
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

	client := localagent.NewClient(server.Paths().SocketPath)
	defer client.CloseIdleConnections()
	if err := waitForAgentCommandPing(ctx, client); err != nil {
		t.Fatal(err)
	}
	appRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
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
	store, err := devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	event := devdash.NewDevEvent("demo", session.SessionID, devdash.DevSource{ID: "api", Kind: "app"}, "info", "old event", nil, time.Now().UTC())
	if err := store.WriteDevEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)

	output := commandOutput(t, func(stdout io.Writer) error {
		return pruneCommandWithDeps(client, stdout, func() (*devdash.Store, error) {
			return devdash.OpenStore(cacheRoot)
		}, []string{"--app-root", appRoot, "--older-than", "1ms"})
	})
	if !strings.Contains(output, "pruned scenery session old-session") {
		t.Fatalf("prune output = %q", output)
	}
	if !strings.Contains(output, "dev_events=1 dev_sources=1") {
		t.Fatalf("prune output missing dev-event prune counts: %q", output)
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
	store, err = devdash.OpenStore(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events, err := store.ListDevEvents(context.Background(), devdash.DevEventQuery{AppID: "demo", SessionID: session.SessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("dev events after prune = %+v, want none", events)
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

func commandOutput(t *testing.T, fn func(stdout io.Writer) error) string {
	t.Helper()
	var out bytes.Buffer
	if err := fn(&out); err != nil {
		t.Fatalf("command returned error: %v", err)
	}
	return out.String()
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
