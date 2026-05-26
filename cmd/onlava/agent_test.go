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
	opts, err := parseAgentArgs([]string{"--socket", "/tmp/onlava.sock", "--router-listen", "127.0.0.1:0", "--json"})
	if err != nil {
		t.Fatalf("parseAgentArgs: %v", err)
	}
	if opts.SocketPath != "/tmp/onlava.sock" || opts.RouterAddr != "127.0.0.1:0" || !opts.JSON {
		t.Fatalf("opts = %+v", opts)
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
