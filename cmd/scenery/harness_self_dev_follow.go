package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

const harnessDevFollowProbeName = "dev runtime follower process probe"

type harnessDevFollowCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessDevFollowProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessDevFollowProbeStepWithCheck(ctx, repoRoot, runHarnessDevFollowProbeCheck)
}

func runHarnessDevFollowProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessDevFollowCheck) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessDevFollowProbeName, Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage: step.Name, Severity: "error", Message: step.Error,
				SuggestedAction: "Fix dev-runtime owner following, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessDevFollowProbeCheck(parent context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{"proof": "not_applicable_on_windows", "reason": "local agent control uses a Unix socket"}, nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-dev-follow-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(root)
	agentHome := filepath.Join(root, "agent-home")
	restoreEnv := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(agentHome)})
	defer restoreEnv()

	server, err := localagent.NewServer(localagent.RunOptions{
		Home:       agentHome,
		RouterAddr: "127.0.0.1:0",
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    "127.0.0.1:9",
		},
		Identity: cliBuildIdentity(),
	})
	if err != nil {
		return nil, nil, err
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(serverCtx) }()
	defer func() {
		stopServer()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
		}
	}()
	client := localagent.NewClient(server.Paths().SocketPath)
	if err := waitForHarnessAgent(ctx, client); err != nil {
		return nil, nil, err
	}

	appRoot := filepath.Join(root, "app")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return nil, nil, err
	}
	owner := exec.Command("/bin/sleep", "30")
	if err := owner.Start(); err != nil {
		return nil, nil, fmt.Errorf("start owner fixture: %w", err)
	}
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- owner.Wait() }()
	defer func() {
		_ = owner.Process.Kill()
		select {
		case <-ownerDone:
		case <-time.After(2 * time.Second):
		}
	}()
	if _, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID: "harness-dev-follow",
		AppRoot:   appRoot,
		SessionID: localagent.SessionID(appRoot, ""),
		Status:    "running",
		OwnerPID:  owner.Process.Pid,
		Owner:     localagent.CaptureOwner(owner.Process.Pid, "harness dev owner"),
	}); err != nil {
		return nil, nil, fmt.Errorf("register live owner session: %w", err)
	}

	go func() {
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			_ = owner.Process.Kill()
		}
	}()
	var output bytes.Buffer
	console := newRunConsole(&output, io.Discard, false, false, "harness-dev-follow", appRoot)
	err = followAlreadyRunningDevSessionWith(
		ctx,
		console,
		appRoot,
		func(logCtx context.Context, _ io.Writer, _ []string) error {
			<-logCtx.Done()
			return logCtx.Err()
		},
		func(watchCtx context.Context, watchRoot string) bool {
			return devSessionOwnerGoneWithInterval(watchCtx, watchRoot, 10*time.Millisecond)
		},
	)
	if err != nil {
		return nil, nil, err
	}
	if !strings.Contains(output.String(), "The running dev runtime stopped") {
		return nil, nil, fmt.Errorf("follower output did not report owner exit: %s", strings.TrimSpace(output.String()))
	}
	return map[string]any{
		"proof":      "real_agent_session_owner_process_exit_detached_follower",
		"owner_pid":  owner.Process.Pid,
		"app_root":   appRoot,
		"agent_home": agentHome,
	}, nil, nil
}
