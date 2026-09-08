package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"path/filepath"
	"strings"
	"time"

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
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary"},
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
				SuggestedAction: "Fix the Victoria process-start attribution boundary, then rerun `go run ./scripts/verify --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessVictoriaProcessProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
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
	concurrentEnsure, err := runHarnessConcurrentVictoriaEnsure(ctx, repoRoot, filepath.Join(root, "concurrent"))
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
