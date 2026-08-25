package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const harnessAgentRestartProbeName = "local agent restart probe"

type harnessAgentRestartCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessAgentRestartProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessAgentRestartProbeStepWithCheck(ctx, repoRoot, runHarnessAgentRestartProbeCheck)
}

func runHarnessAgentRestartProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessAgentRestartCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessAgentRestartProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix local-agent restart persistence or route recovery, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessAgentRestartProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "local agent control uses a Unix socket"}, nil, nil
	}
	home, err := os.MkdirTemp("", "scenery-agent-restart-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(home)
	paths := localagent.PathsForHome(home)
	if err := localagent.EnsureDirs(paths); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(paths.EdgeTokenPath, []byte("test-token"), 0o600); err != nil {
		return nil, nil, err
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "route ok")
	}))
	defer backend.Close()

	type substrateProcess struct {
		command *exec.Cmd
		owner   localagent.Owner
	}
	processes := map[string]substrateProcess{}
	for _, kind := range []string{localagent.SubstratePostgres, localagent.SubstrateVictoria} {
		command := exec.CommandContext(ctx, "/bin/sleep", "30")
		if err := command.Start(); err != nil {
			return nil, nil, err
		}
		owner := localagent.CaptureOwner(command.Process.Pid, "harness "+kind)
		if err := localagent.VerifyOwner(owner); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, nil, err
		}
		processes[kind] = substrateProcess{command: command, owner: owner}
	}
	defer func() {
		for _, process := range processes {
			_ = process.command.Process.Kill()
			_ = process.command.Wait()
		}
	}()

	server, err := localagent.NewServer(localagent.RunOptions{Home: home, RouterAddr: "127.0.0.1:0"})
	if err != nil {
		return nil, nil, err
	}
	routerAddr := server.RouterAddr()
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(serverCtx) }()
	serverStopped := false
	defer func() {
		if !serverStopped {
			stopServer()
			select {
			case <-serverDone:
			case <-time.After(2 * time.Second):
			}
		}
	}()
	client := localagent.NewClient(server.Paths().SocketPath)
	if err := waitForHarnessAgentHealth(ctx, client); err != nil {
		return nil, nil, err
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "restart-probe", AppRoot: filepath.Join(home, "app"), SessionID: "main", OwnerPID: os.Getpid(),
		Backends:      map[string]localagent.Backend{localagent.RouteAPI: {Network: "tcp", Addr: strings.TrimPrefix(backend.URL, "http://")}},
		RouteManifest: localagent.RouteManifest{Mode: localagent.RouteModePath, BaseURL: "http://localhost:4001"},
	})
	if err != nil {
		return nil, nil, err
	}
	for kind, process := range processes {
		component := "server"
		if kind == localagent.SubstrateVictoria {
			component = "metrics"
		}
		if _, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
			Kind: kind, Status: "ready", OwnerPID: process.owner.PID, Owner: process.owner,
			PIDs: map[string]int{component: process.owner.PID}, Owners: map[string]localagent.Owner{component: process.owner},
		}); err != nil {
			return nil, nil, err
		}
	}
	if err := assertHarnessAgentRoute(ctx, routerAddr, session.SessionID); err != nil {
		return nil, nil, err
	}
	client.CloseIdleConnections()
	stopServer()
	select {
	case err := <-serverDone:
		if err != nil {
			return nil, nil, err
		}
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	serverStopped = true
	for kind, process := range processes {
		if err := localagent.VerifyOwner(process.owner); err != nil {
			return nil, nil, fmt.Errorf("%s process did not survive agent shutdown: %w", kind, err)
		}
	}

	restarted, err := localagent.NewServer(localagent.RunOptions{Home: home, RouterAddr: routerAddr})
	if err != nil {
		return nil, nil, err
	}
	restartCtx, stopRestart := context.WithCancel(ctx)
	restartDone := make(chan error, 1)
	go func() { restartDone <- restarted.Run(restartCtx) }()
	defer func() {
		stopRestart()
		select {
		case <-restartDone:
		case <-time.After(2 * time.Second):
		}
	}()
	restartClient := localagent.NewClient(restarted.Paths().SocketPath)
	if err := waitForHarnessAgentHealth(ctx, restartClient); err != nil {
		return nil, nil, err
	}
	for kind, process := range processes {
		substrate, err := restartClient.GetSubstrate(ctx, kind)
		if err != nil || substrate.OwnerPID != process.owner.PID {
			return nil, nil, fmt.Errorf("%s substrate after restart = %+v, err=%v", kind, substrate, err)
		}
	}
	if err := assertHarnessAgentRoute(ctx, routerAddr, session.SessionID); err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":                "real_agent_restart_preserved_live_substrates_and_registered_route",
		"substrates_preserved": []string{localagent.SubstratePostgres, localagent.SubstrateVictoria},
		"route_preserved":      true,
	}, nil, nil
}

func assertHarnessAgentRoute(ctx context.Context, routerAddr, sessionID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+routerAddr+"/api/health", nil)
	if err != nil {
		return err
	}
	request.Host = "localhost:4001"
	request.Header.Set("X-Scenery-Session", sessionID)
	request.Header.Set("X-Scenery-Local-Route-Mode", string(localagent.RouteModePath))
	request.Header.Set("X-Scenery-Edge-Token", "test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "route ok" {
		return fmt.Errorf("agent route status=%d body=%q", response.StatusCode, body)
	}
	return nil
}
