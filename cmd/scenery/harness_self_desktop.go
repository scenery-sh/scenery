package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/desktop"
	"scenery.sh/internal/envpolicy"
)

const harnessDesktopProcessProbeName = "desktop shell process probe"

type harnessDesktopProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDesktopProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDesktopProcessProbeStepWithCheck(ctx, repoRoot, runHarnessDesktopProcessProbeCheck)
}

func runHarnessDesktopProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDesktopProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessDesktopProcessProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the desktop shell process/agent boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDesktopProcessProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-desktop-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	desktopRunnerProof, err := runHarnessDesktopRunnerBoundary(ctx, root)
	if err != nil {
		return nil, nil, err
	}

	agentCtx, stopAgent := context.WithCancel(ctx)
	server, err := localagent.NewServer(localagent.RunOptions{
		Home:       filepath.Join(root, "agent-home"),
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
	})
	if err != nil {
		stopAgent()
		return nil, nil, err
	}
	agentDone := make(chan error, 1)
	go func() { agentDone <- server.Run(agentCtx) }()
	defer func() {
		stopAgent()
		select {
		case <-agentDone:
		case <-time.After(2 * time.Second):
		}
	}()
	client := localagent.NewClient(server.Paths().SocketPath)
	if err := waitForHarnessAgentHealth(ctx, client); err != nil {
		return nil, nil, err
	}

	appRoot := filepath.Join(root, "app")
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`, 0o644); err != nil {
		return nil, nil, err
	}
	marker := filepath.Join(appRoot, "tauri-invocation.json")
	script := `#!/bin/sh
set -eu
printf '%s\n%s\n%s\n%s\n%s\n%s\n' "$PWD" "$1" "$2" "$3" "$SCENERY_ENV" "$VITE_API_BASE_URL" > "` + marker + `"
trap 'exit 0' INT TERM
while :; do sleep 1; done
`
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), script, 0o755); err != nil {
		return nil, nil, err
	}
	cfg := app.Config{
		Name: "desktop-demo",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		return nil, nil, err
	}
	cfg.Frontends = env.Frontends
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   cfg.AppID(),
		Environment: env.Name,
		AppRoot:     appRoot,
		Status:      "starting",
		OwnerPID:    os.Getpid(),
		Backends: map[string]localagent.Backend{
			"web": {Network: "tcp", Addr: "127.0.0.1:5173"},
			"api": {Network: "tcp", Addr: "127.0.0.1:4000"},
		},
		RouteManifest: localagent.RouteManifest{Routes: map[string]localagent.RouteRecord{
			"api": {URL: "https://desktop-demo.local.dev/api/"},
		}},
		ClaimOwner: true,
	})
	if err != nil {
		return nil, nil, err
	}
	supervisor, err := newDevSupervisor(ctx, appRoot, cfg, env, devBackend{Network: "tcp", Addr: "127.0.0.1:4000"}, nil, client, &session)
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = supervisor.Close()
		}
	}()
	if err := supervisor.startDesktopShells(ctx); err != nil {
		return nil, nil, err
	}

	var invocation []string
	if err := waitForHarnessCondition(ctx, func() bool {
		data, readErr := os.ReadFile(marker)
		if readErr != nil {
			return false
		}
		invocation = strings.Split(strings.TrimSpace(string(data)), "\n")
		sessions, listErr := client.List(ctx, appRoot)
		return len(invocation) == 6 && listErr == nil && len(sessions) == 1 && sessions[0].Processes["desktop-web"].PID > 0
	}); err != nil {
		return nil, nil, err
	}
	resolvedFrontendRoot, err := filepath.EvalSymlinks(frontendRoot)
	if err != nil {
		return nil, nil, err
	}
	if invocation[0] != resolvedFrontendRoot || invocation[1] != "dev" || invocation[2] != "--config" || invocation[4] != "local" || invocation[5] != "https://desktop-demo.local.dev/api/" {
		return nil, nil, fmt.Errorf("desktop invocation = %#v", invocation)
	}
	var overlay struct {
		Build struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(invocation[3]), &overlay); err != nil {
		return nil, nil, err
	}
	if overlay.Build.DevURL != "http://127.0.0.1:5173" || overlay.Build.BeforeDevCommand != "" {
		return nil, nil, fmt.Errorf("desktop overlay = %+v", overlay)
	}
	supervisor.mu.RLock()
	desktopProcess := supervisor.desktops["web"]
	supervisor.mu.RUnlock()
	if desktopProcess == nil || desktopProcess.Process == nil {
		return nil, nil, fmt.Errorf("desktop process is not supervised")
	}
	pid := desktopProcess.Process.PID
	if err := supervisor.Close(); err != nil {
		return nil, nil, err
	}
	closed = true
	select {
	case <-desktopProcess.Process.done:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	exitStarts, err := runHarnessDesktopExitNoRestart(ctx, filepath.Join(root, "exit-app"), client)
	if err != nil {
		return nil, nil, err
	}
	frontendRestart, err := runHarnessManagedFrontendRestart(ctx, filepath.Join(root, "frontend-app"), client, root)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":                      "desktop_shell_and_managed_frontend_process_boundaries_verified",
		"frontend":                   "web",
		"pid":                        pid,
		"dev_url":                    overlay.Build.DevURL,
		"api_base_url":               invocation[5],
		"normal_exit_process_starts": exitStarts,
		"managed_frontend_restart":   frontendRestart,
		"desktop_runner":             desktopRunnerProof,
	}, nil, nil
}

func runHarnessDesktopRunnerBoundary(ctx context.Context, root string) (map[string]any, error) {
	commandPath := filepath.Join(root, "desktop-command")
	if err := writeHarnessDesktopFile(commandPath, `#!/bin/sh
printf 'stdout:%s:%s:%s\n' "$PWD" "$1" "$DESKTOP_TEST_VALUE"
printf 'stderr:desktop failed\n' >&2
exit 7
`, 0o755); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	runErr := desktop.Run(ctx, desktop.Command{Path: commandPath, Args: []string{"build"}, Dir: root}, envWithOverrides(envpolicy.Environ(), "DESKTOP_TEST_VALUE=runner-env"), &output)
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 7 {
		return nil, fmt.Errorf("desktop runner error = %v, want child exit 7", runErr)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	for _, want := range []string{
		"stdout:" + resolvedRoot + ":build:runner-env",
		"stderr:desktop failed",
	} {
		if !strings.Contains(output.String(), want) || !strings.Contains(runErr.Error(), want) {
			return nil, fmt.Errorf("desktop runner output or error missing %q: output=%q error=%v", want, output.String(), runErr)
		}
	}
	return map[string]any{"exit_code": exitErr.ExitCode(), "combined_streams": true, "error_tail": true}, nil
}

func runHarnessManagedFrontendRestart(ctx context.Context, appRoot string, client *localagent.Client, root string) (map[string]any, error) {
	serverSource := filepath.Join(root, "frontend-server.go")
	serverBinary := filepath.Join(root, "frontend-server")
	marker := filepath.Join(root, "frontend-starts.log")
	readyFile := filepath.Join(root, "frontend.ready")
	if err := writeHarnessDesktopFile(serverSource, fmt.Sprintf(`package main

import (
	"net"
	"os"
	"time"
)

func main() {
	for {
		if _, err := os.Stat(%q); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Args[1])
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		_ = connection.Close()
	}
}
`, readyFile), 0o644); err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", serverBinary, serverSource)
	build.Env = append(envpolicy.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build managed frontend probe server: %w: %s", err, strings.TrimSpace(string(output)))
	}

	frontendRoot := filepath.Join(appRoot, "apps", "web")
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "package.json"), `{"scripts":{"dev":"vite"}}`, 0o644); err != nil {
		return nil, err
	}
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "node_modules", ".bin", "vite"), `#!/bin/sh
set -eu
port=""
previous=""
for argument in "$@"; do
	if [ "$previous" = "--port" ]; then
		port="$argument"
		break
	fi
	previous="$argument"
done
printf '%s %s\n' "$$" "$port" >> `+shellQuote(marker)+`
exec `+shellQuote(serverBinary)+` "$port"
`, 0o755); err != nil {
		return nil, err
	}
	cfg := app.Config{
		Name: "frontend-restart",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web"},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		return nil, err
	}
	cfg.Frontends = env.Frontends
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: cfg.AppID(), Environment: env.Name, AppRoot: appRoot,
		Status: "starting", OwnerPID: os.Getpid(), ClaimOwner: true,
	})
	if err != nil {
		return nil, err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot, env.DotEnvFiles()...)
	if err != nil {
		return nil, err
	}
	backends, processes, wait, err := beginManagedFrontendBackendsForSession(ctx, appRoot, cfg, baseEnv, session)
	if err != nil {
		return nil, err
	}
	if wait == nil {
		stopManagedFrontendProcesses(processes)
		return nil, fmt.Errorf("managed frontend readiness join is nil")
	}
	if len(processes) != 1 || processes[0] == nil || processes[0].Process == nil {
		stopManagedFrontendProcesses(processes)
		return nil, fmt.Errorf("managed frontend processes = %d, want one live process", len(processes))
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- wait(ctx) }()
	select {
	case waitErr := <-waitDone:
		stopManagedFrontendProcesses(processes)
		return nil, fmt.Errorf("managed frontend readiness finished before release: %v", waitErr)
	case <-time.After(75 * time.Millisecond):
	}
	if err := os.WriteFile(readyFile, []byte("ready"), 0o644); err != nil {
		stopManagedFrontendProcesses(processes)
		return nil, err
	}
	select {
	case waitErr := <-waitDone:
		if waitErr != nil {
			return nil, waitErr
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	registered, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: cfg.AppID(), Environment: env.Name, AppRoot: appRoot,
		SessionID: session.SessionID, Branch: session.Branch, Status: "running",
		OwnerPID: os.Getpid(), Backends: backends, Processes: frontendSessionProcesses(processes), ClaimOwner: true,
	})
	if err != nil {
		stopManagedFrontendProcesses(processes)
		return nil, err
	}
	supervisor, err := newDevSupervisor(ctx, appRoot, cfg, env, devBackend{}, nil, client, &registered)
	if err != nil {
		stopManagedFrontendProcesses(processes)
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = supervisor.Close()
		}
	}()
	oldDelay := managedFrontendRestartDelay
	managedFrontendRestartDelay = 10 * time.Millisecond
	defer func() { managedFrontendRestartDelay = oldDelay }()
	supervisor.adoptManagedFrontends(processes)

	first := processes[0]
	oldAddr := first.Addr
	oldPID := first.Process.PID
	if err := killProcessTree(first.Process.Cmd); err != nil {
		return nil, err
	}
	var newAddr string
	newPID := 0
	if err := waitForHarnessCondition(ctx, func() bool {
		sessions, listErr := client.List(ctx, appRoot)
		if listErr != nil || len(sessions) != 1 {
			return false
		}
		backend := sessions[0].Backends["web"]
		process := sessions[0].Processes["frontend-web"]
		if backend.Addr == "" || backend.Addr == oldAddr || process.PID <= 0 || process.PID == oldPID || sessions[0].Environment != "local" {
			return false
		}
		if !tcpAddrAcceptsConnections(backend.Addr) {
			return false
		}
		newAddr = backend.Addr
		newPID = process.PID
		return true
	}); err != nil {
		return nil, fmt.Errorf("wait for managed frontend restart: %w", err)
	}
	if err := supervisor.Close(); err != nil {
		return nil, err
	}
	closed = true
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil, err
	}
	starts := len(strings.Fields(strings.TrimSpace(string(data)))) / 2
	if starts != 2 {
		return nil, fmt.Errorf("managed frontend starts = %d, want exactly two", starts)
	}
	return map[string]any{
		"old_address":        oldAddr,
		"old_pid":            oldPID,
		"new_address":        newAddr,
		"new_pid":            newPID,
		"starts":             starts,
		"readiness_deferred": true,
	}, nil
}

func runHarnessDesktopExitNoRestart(ctx context.Context, appRoot string, client *localagent.Client) (int, error) {
	frontendRoot := filepath.Join(appRoot, "apps", "web")
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`, 0o644); err != nil {
		return 0, err
	}
	marker := filepath.Join(appRoot, "desktop-starts.log")
	if err := writeHarnessDesktopFile(filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), "#!/bin/sh\nset -eu\necho start >> \""+marker+"\"\n", 0o755); err != nil {
		return 0, err
	}
	cfg := app.Config{
		Name: "desktop-exit",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
		Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	env, err := cfg.ResolveEnv("")
	if err != nil {
		return 0, err
	}
	cfg.Frontends = env.Frontends
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: cfg.AppID(), Environment: env.Name, AppRoot: appRoot,
		Status: "running", OwnerPID: os.Getpid(), ClaimOwner: true,
		Backends: map[string]localagent.Backend{"web": {Network: "tcp", Addr: "127.0.0.1:5173"}},
	})
	if err != nil {
		return 0, err
	}
	supervisor, err := newDevSupervisor(ctx, appRoot, cfg, env, devBackend{Network: "tcp", Addr: "127.0.0.1:4000"}, nil, client, &session)
	if err != nil {
		return 0, err
	}
	defer supervisor.Close()
	if err := supervisor.startDesktopShells(ctx); err != nil {
		return 0, err
	}
	if err := waitForHarnessCondition(ctx, func() bool {
		sessions, listErr := client.List(ctx, appRoot)
		if listErr != nil || len(sessions) != 1 {
			return false
		}
		_, registered := sessions[0].Processes["desktop-web"]
		return !registered
	}); err != nil {
		return 0, err
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return 0, err
	}
	if len(sessions) != 1 || sessions[0].Status != "running" || sessions[0].Backends["web"].Addr != "127.0.0.1:5173" {
		return 0, fmt.Errorf("session changed after desktop exit: %+v", sessions)
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return 0, err
	}
	starts := strings.Count(string(data), "start")
	if starts != 1 {
		return starts, fmt.Errorf("desktop starts = %d, want exactly one", starts)
	}
	return starts, nil
}

func waitForHarnessAgentHealth(ctx context.Context, client *localagent.Client) error {
	return waitForHarnessCondition(ctx, func() bool {
		_, err := client.Health(ctx)
		return err == nil
	})
}

func waitForHarnessCondition(ctx context.Context, condition func() bool) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeHarnessDesktopFile(path, body string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), mode)
}
