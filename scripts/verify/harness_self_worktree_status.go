package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
)

func harnessStatusFixtureInventory(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fmt.Sprintf("%o/%d/%d/%x", info.Mode().Perm(), info.Size(), info.ModTime().UnixNano(), sha256.Sum256(data))
		return nil
	})
	return out, err
}

func harnessStatusFixtureEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// checkHarnessWorktreeStatusSchema exercises public status with owned current
// control endpoints and retained locators; it never starts an application.
func checkHarnessWorktreeStatusSchema(parent context.Context, repo string) (report map[string]any, resultErr error) {
	binary := harnessLocalSceneryBinaryPath(repo)
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	original, err := os.MkdirTemp("", "scn-status-schema-")
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(original)
	if err != nil {
		return nil, err
	}
	home, app := filepath.Join(root, "home"), filepath.Join(root, "app")
	if err := os.MkdirAll(app, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(app, "app.scn"), []byte("app StatusProbe\n"), 0o600); err != nil {
		return nil, err
	}
	paths, err := localagent.PathsForWorktree(home, app)
	if err != nil {
		return nil, err
	}
	var live *localagent.ProcessLock
	var server *localagent.Server
	var serverCancel context.CancelFunc
	var serverDone chan error
	var control *http.Server
	var controlDone chan error
	report = map[string]any{"fixture_original": original, "fixture_root": root, "app_root": app, "home": home, "socket_dir": paths.SocketDir, "controller_pid": os.Getpid(), "binary": binary}
	defer func() {
		var cleanup error
		if control != nil {
			cleanup = errors.Join(cleanup, control.Close())
			cleanup = errors.Join(cleanup, <-controlDone)
		}
		if server != nil {
			serverCancel()
			cleanup = errors.Join(cleanup, server.Close(), <-serverDone)
		}
		if live != nil {
			cleanup = errors.Join(cleanup, live.Release())
		}
		if !strings.HasPrefix(paths.SocketDir, root+string(os.PathSeparator)) {
			if err := os.Remove(paths.SocketDir); err != nil && !os.IsNotExist(err) {
				cleanup = errors.Join(cleanup, err)
			}
		}
		cleanup = errors.Join(cleanup, os.RemoveAll(root))
		absent := true
		for _, path := range []string{original, root, paths.SocketDir} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				absent = false
				cleanup = errors.Join(cleanup, fmt.Errorf("owned path remains %s: %v", path, err))
			}
		}
		report["owned_cleanup_verified"] = cleanup == nil && absent
		if cleanup != nil {
			report["cleanup_error"] = cleanup.Error()
		}
		resultErr = errors.Join(resultErr, cleanup)
	}()
	operation, err := paths.BeginOperation()
	if err != nil {
		return report, err
	}
	record := localagent.NewWorktreeRecord(paths, "status-probe")
	err = errors.Join(operation.SaveRecord(record), operation.Close())
	if err != nil {
		return report, err
	}
	live, err = paths.AcquireLiveLock()
	if err != nil {
		return report, err
	}
	router, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return report, err
	}
	server, err = localagent.NewWorktreeServer(paths, record, localagent.Identity{}, localagent.Backend{}, "owned-status-probe", router)
	if err != nil {
		_ = router.Close()
		return report, err
	}
	serverCtx, stop := context.WithCancel(ctx)
	serverCancel = stop
	serverDone = make(chan error, 1)
	go func() { serverDone <- server.Run(serverCtx) }()
	client := localagent.NewClient(paths.Socket)
	health, err := client.Health(ctx)
	client.CloseIdleConnections()
	if err != nil {
		return report, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		return report, err
	}
	serverCancel()
	if err := errors.Join(server.Close(), <-serverDone); err != nil {
		server = nil
		return report, err
	}
	server = nil
	listener, err := net.Listen("unix", paths.Socket)
	if err != nil {
		return report, err
	}
	var scenario atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(health) })
	mux.HandleFunc("/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		switch scenario.Load() {
		case 0:
			_, _ = w.Write([]byte(`{"sessions":[]}`))
		case 1:
			_, _ = w.Write([]byte(`{"sessions":null}`))
		case 2:
			http.Error(w, "owned list refusal", http.StatusServiceUnavailable)
		case 3:
			_ = json.NewEncoder(w).Encode(localagent.StatusResponse{Sessions: []localagent.Session{{SessionID: "status-control", AppRoot: app, BaseAppID: "status-probe", Status: "stopped"}}})
		}
	})
	control = &http.Server{Handler: mux}
	controlDone = make(chan error, 1)
	go func() {
		err := control.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		controlDone <- err
	}()
	observations := []map[string]any{}
	runPS := func(label string, args ...string) error {
		before, err := harnessStatusFixtureInventory(filepath.Join(home, "worktrees"))
		if err != nil {
			return err
		}
		command := commandTreeContext(ctx, binary, append([]string{"ps"}, args...)...)
		command.Dir = root
		for _, value := range envpolicy.Environ() {
			key, _, _ := strings.Cut(value, "=")
			if key != "SCENERY_AGENT_HOME" && key != "SCENERY_APP_ROOT" && key != "SCENERY_AGENT_SOCKET" && key != "SCENERY_AGENT_ROUTER_ADDR" && key != "DATABASE_URL" {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "SCENERY_AGENT_HOME="+home)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Start(); err != nil {
			return err
		}
		pid := command.Process.Pid
		err = command.Wait()
		exit := 0
		if err != nil {
			var status *exec.ExitError
			if !errors.As(err, &status) {
				return err
			}
			exit = status.ExitCode()
		}
		after, inventoryErr := harnessStatusFixtureInventory(filepath.Join(home, "worktrees"))
		if inventoryErr != nil {
			return inventoryErr
		}
		var envelope struct {
			OK       bool            `json:"ok"`
			Data     json.RawMessage `json:"data"`
			Producer any             `json:"producer"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			return fmt.Errorf("%s output: %w %s", label, err, stderr.String())
		}
		var payload any
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.agent.status.schema.json"), payload)
		observation := map[string]any{"label": label, "argv": command.Args, "pid": pid, "joined": true, "exit": exit, "stdout": stdout.String(), "stderr": stderr.String(), "schema_valid": len(diagnostics) == 0, "schema_diagnostics": diagnostics, "authority_inventory_unchanged": harnessStatusFixtureEqual(before, after), "producer": envelope.Producer}
		observations = append(observations, observation)
		report["observations"] = observations
		if len(diagnostics) != 0 {
			return fmt.Errorf("%s public status violates schema: %v", label, diagnostics)
		}
		items := payload.(map[string]any)["worktrees"].([]any)
		if label == "global-locator-case" {
			if len(items) != 2 {
				return fmt.Errorf("global status returned %d entries, want retained root and canonical invalid locator", len(items))
			}
			for _, value := range items {
				item := value.(map[string]any)
				if item["key"] != paths.Key && (item["key"] != strings.Repeat("b", 64) || item["status"] != "incompatible-or-invalid") {
					return fmt.Errorf("global status admitted an unexpected locator: %v", item["key"])
				}
			}
		} else {
			if len(items) != 1 {
				return fmt.Errorf("%s did not select one root", label)
			}
			item := items[0].(map[string]any)
			wantStatus, wantOwner, wantCount := "running", "unknown", 0
			if label == "failed-list-after-health" {
				wantStatus, wantOwner = "unavailable", "unavailable"
			}
			if label == "stopped-control" {
				wantStatus, wantOwner = "stopped", "stopped"
			}
			if label == "nonempty-control" {
				wantCount = 1
			}
			if item["app_root"] != app || item["key"] != paths.Key || item["status"] != wantStatus || item["owner_status"] != wantOwner || len(item["sessions"].([]any)) != wantCount {
				return fmt.Errorf("%s root/status/session control changed", label)
			}
		}
		if exit != 0 || !envelope.OK || !harnessStatusFixtureEqual(before, after) {
			return fmt.Errorf("%s exit/admission/authority control failed", label)
		}
		return nil
	}
	for i, label := range []string{"empty-list", "nil-list", "failed-list-after-health", "nonempty-control"} {
		scenario.Store(int32(i))
		if err := runPS(label, "--app-root", app, "-o", "json"); err != nil {
			return report, err
		}
	}
	if err := control.Close(); err != nil {
		return report, err
	}
	if err := <-controlDone; err != nil {
		control = nil
		return report, err
	}
	control = nil
	if err := live.Release(); err != nil {
		live = nil
		return report, err
	}
	live = nil
	if err := runPS("stopped-control", "--app-root", app, "-o", "json"); err != nil {
		return report, err
	}
	for _, name := range []string{strings.Repeat("b", 64), strings.Repeat("A", 64), strings.Repeat("C", 63) + "d", "not-a-key"} {
		if err := os.WriteFile(filepath.Join(home, "worktrees", name), []byte("owned invalid locator\n"), 0o600); err != nil {
			return report, err
		}
	}
	if err := runPS("global-locator-case", "-o", "json"); err != nil {
		return report, err
	}
	return report, nil
}
