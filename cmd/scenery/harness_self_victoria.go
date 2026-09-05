package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/victoria"
)

const harnessVictoriaProcessProbeName = "Victoria process probe"

type harnessVictoriaProcessCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessVictoriaProcessProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessVictoriaProcessProbeStepWithCheck(ctx, repoRoot, runHarnessVictoriaProcessProbeCheck)
}

func runHarnessVictoriaProcessProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessVictoriaProcessCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessVictoriaProcessProbeName,
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
				SuggestedAction: "Fix the Victoria process-start attribution boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessVictoriaProcessProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	root, err := os.MkdirTemp("", "scenery-victoria-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = occupied.Close() }()
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	logsPort := available.Addr().(*net.TCPAddr).Port
	if err := available.Close(); err != nil {
		return nil, nil, err
	}
	failingBinary := filepath.Join(root, "victoria-logs-prod")
	if err := os.WriteFile(failingBinary, []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		return nil, nil, err
	}

	console := &harnessVictoriaConsole{}
	stack := victoria.StartAtRootWithConfig(ctx, root, console, victoria.StartConfig{
		Components: []victoria.ComponentSpec{
			{
				Name:         "metrics",
				DisplayName:  "VictoriaMetrics",
				DefaultPort:  occupied.Addr().(*net.TCPAddr).Port,
				EndpointPath: "/opentelemetry/v1/metrics",
				StorageDir:   "metrics-data",
			},
			{
				Name:         "logs",
				DisplayName:  "VictoriaLogs",
				DefaultPort:  logsPort,
				EndpointPath: "/insert/opentelemetry/v1/logs",
				StorageDir:   "logs-data",
			},
		},
		BinaryPaths: map[string]string{"logs": failingBinary},
	})
	if stack == nil || len(stack.Components()) != 1 || stack.Components()[0].Name() != "metrics" || !stack.Components()[0].External() {
		return nil, nil, fmt.Errorf("victoria stack = %+v, want one reused external metrics component", stack)
	}
	warning := console.messageContaining("VictoriaLogs unavailable: VictoriaLogs exited before accepting TCP connections")
	if warning == "" {
		return nil, nil, fmt.Errorf("VictoriaLogs start attribution missing from events: %v", console.messages)
	}
	concurrentEnsure, err := runHarnessConcurrentVictoriaEnsure(ctx, filepath.Join(root, "concurrent"))
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{
		"proof":              "process_start_attribution_and_concurrent_shared_stack_ensure_verified",
		"reused_component":   "metrics",
		"failed_component":   "logs",
		"attributed_warning": warning,
		"concurrent_ensure":  concurrentEnsure,
	}, nil, nil
}

func runHarnessConcurrentVictoriaEnsure(parent context.Context, root string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	serverSource := filepath.Join(root, "victoria-server.go")
	serverBinary := filepath.Join(root, "victoria-server")
	if err := os.WriteFile(serverSource, []byte(`package main

import (
	"net"
	"os"
	"strings"
)

func main() {
	address := ""
	for _, argument := range os.Args[1:] {
		if strings.HasPrefix(argument, "-httpListenAddr=") {
			address = strings.TrimPrefix(argument, "-httpListenAddr=")
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		os.Exit(2)
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
`), 0o644); err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", serverBinary, serverSource)
	build.Env = append(envpolicy.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build Victoria probe server: %w: %s", err, strings.TrimSpace(string(output)))
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
		return nil, err
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
		return nil, err
	}

	specs := victoria.ComponentSpecs()
	binaryPaths := make(map[string]string, len(specs))
	listeners := make([]net.Listener, 0, len(specs))
	for i := range specs {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, listener)
		specs[i].DefaultPort = listener.Addr().(*net.TCPAddr).Port
		binaryPaths[specs[i].Name] = serverBinary
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			return nil, err
		}
	}
	config := victoria.StartConfig{Components: specs, BinaryPaths: binaryPaths}
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	supervisor := &devSupervisor{ctx: runCtx, cancel: stopRun, agent: client}
	supervisor.victoriaProcesses = victoriaProcessConfig{
		start: func(ctx context.Context, root string, console victoria.Console) *victoria.Stack {
			return victoria.StartAtRootWithConfig(ctx, root, console, config)
		},
		portsAvailable: func() bool {
			for _, spec := range specs {
				listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spec.DefaultPort))
				if err != nil {
					return false
				}
				_ = listener.Close()
			}
			return true
		},
	}

	stackRoot := filepath.Join(root, "stack")
	stale, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{
		Kind:     localagent.SubstrateVictoria,
		Status:   "ready",
		OwnerPID: 99999991,
	})
	if err != nil {
		return nil, err
	}
	processUnlock := lockVictoriaSubstrateProcess(stackRoot)
	locked := true
	defer func() {
		if locked {
			processUnlock()
		}
	}()
	type ensureResult struct {
		stack  *victoria.Stack
		reused bool
		err    error
	}
	attempted := make(chan struct{}, 2)
	results := make(chan ensureResult, 2)
	for range 2 {
		go func() {
			attempted <- struct{}{}
			stack, reused, err := supervisor.ensureSharedVictoriaStack(runCtx, stackRoot)
			results <- ensureResult{stack: stack, reused: reused, err: err}
		}()
	}
	for range 2 {
		select {
		case <-attempted:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case result := <-results:
		return nil, fmt.Errorf("victoria ensure returned while process lock was held: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	processUnlock()
	locked = false

	collected := make([]ensureResult, 0, 2)
	for range 2 {
		select {
		case result := <-results:
			collected = append(collected, result)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pids := map[int]bool{}
	defer func() {
		for pid := range pids {
			if process, findErr := os.FindProcess(pid); findErr == nil {
				_ = process.Kill()
			}
		}
	}()
	started, reused := 0, 0
	var startedStack *victoria.Stack
	for _, result := range collected {
		if result.err != nil || result.stack == nil {
			return nil, fmt.Errorf("concurrent Victoria ensure stack=%T reused=%v err=%v", result.stack, result.reused, result.err)
		}
		for _, pid := range result.stack.SubstrateRequest(0).PIDs {
			if pid > 0 {
				pids[pid] = true
			}
		}
		if result.reused {
			reused++
		} else {
			started++
			startedStack = result.stack
		}
	}
	if started != 1 || reused != 1 || len(pids) != len(specs) || startedStack == nil {
		return nil, fmt.Errorf("concurrent Victoria results started=%d reused=%d pids=%d, want 1, 1, %d", started, reused, len(pids), len(specs))
	}
	fresh, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return nil, err
	}
	if fresh.CreatedAt.Equal(stale.CreatedAt) || fresh.OwnerPID <= 0 {
		return nil, fmt.Errorf("stale Victoria owner was not replaced: stale=%+v fresh=%+v", stale, fresh)
	}

	supervisor.victoria = startedStack
	initialMonitor := monitorVictoriaSubstrate(stackRoot, client, nil, startedStack)
	recoveryDone := supervisor.monitorVictoriaRecovery(stackRoot, 10*time.Millisecond, 50*time.Millisecond)
	before, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return nil, err
	}
	failedPID := before.PIDs["logs"]
	if failedPID <= 0 {
		return nil, fmt.Errorf("initial Victoria logs PID = %d", failedPID)
	}
	failedProcess, err := os.FindProcess(failedPID)
	if err != nil {
		return nil, err
	}
	if err := failedProcess.Kill(); err != nil {
		return nil, err
	}
	var after localagent.Substrate
	if err := waitForHarnessCondition(ctx, func() bool {
		current, getErr := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
		if getErr != nil || current.Status != "ready" || len(current.PIDs) != len(specs) || current.PIDs["logs"] <= 0 || current.PIDs["logs"] == failedPID {
			return false
		}
		for name, pid := range before.PIDs {
			if current.PIDs[name] == pid {
				return false
			}
		}
		after = current
		return true
	}); err != nil {
		return nil, fmt.Errorf("wait for Victoria recovery: %w", err)
	}
	for _, pid := range after.PIDs {
		if pid > 0 {
			pids[pid] = true
		}
	}
	substrates, err := client.ListSubstrates(ctx)
	if err != nil {
		return nil, err
	}
	if len(substrates) != 1 || substrates[0].Kind != localagent.SubstrateVictoria {
		return nil, fmt.Errorf("registered substrates = %+v, want one Victoria stack", substrates)
	}
	stopRun()
	for label, done := range map[string]<-chan struct{}{"initial substrate monitor": initialMonitor, "recovery monitor": recoveryDone} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("%s did not stop", label)
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(75 * time.Millisecond):
	}
	stopped, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return nil, err
	}
	for name, pid := range after.PIDs {
		if stopped.PIDs[name] != pid {
			return nil, fmt.Errorf("victoria restarted after supervisor shutdown: before=%v after=%v", after.PIDs, stopped.PIDs)
		}
	}
	return map[string]any{
		"started":                 started,
		"reused":                  reused,
		"managed_processes":       len(specs),
		"recovered":               true,
		"failed_pid":              failedPID,
		"recovered_pid":           after.PIDs["logs"],
		"stopped_without_restart": true,
		"stale_owner_replaced":    true,
	}, nil
}

type harnessVictoriaConsole struct {
	messages []string
}

func (*harnessVictoriaConsole) Verbose() bool { return true }
func (*harnessVictoriaConsole) JSON() bool    { return true }

func (console *harnessVictoriaConsole) Event(_ string, fields map[string]any) {
	if message, ok := fields["message"].(string); ok {
		console.messages = append(console.messages, message)
	}
}

func (console *harnessVictoriaConsole) messageContaining(fragment string) string {
	for _, message := range console.messages {
		if strings.Contains(message, fragment) {
			return message
		}
	}
	return ""
}
