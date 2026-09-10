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

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

// Exercise the actual launcher and supervisor against a private agent. Nothing
// in the fixture requires a database, shared observability, or public routing.
func runHarnessDetachedStartupProbe(parent context.Context, repoRoot string) (map[string]any, error) {
	if err := runHarnessDetachedExitProbe(parent); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 150*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "scn-detach-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	home := filepath.Join(root, "agent")
	restoreEnv := patchEnv(map[string]*string{"SCENERY_AGENT_HOME": stringPtr(home)})
	defer restoreEnv()
	appRoot := filepath.Join(root, "app")
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
		content, err := os.ReadFile(filepath.Join(repoRoot, "testdata/apps/basic", name))
		if err != nil {
			return nil, err
		}
		if name == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../../.."), []byte("=> "+repoRoot))
		}
		path := filepath.Join(appRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return nil, err
		}
	}
	if err := prepareHarnessHandoffService(appRoot); err != nil {
		return nil, err
	}
	env := envWithOverrides(envWithoutKeys(envpolicy.Environ(), "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_DASHBOARD_ADDR", "SCENERY_DEV_CACHE_DIR", "DATABASE_URL", detachedDevChildEnv), "SCENERY_AGENT_HOME="+home, "SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0")
	binary := harnessLocalSceneryBinaryPath(repoRoot)
	framework, err := prepareHarnessSelectedFramework(ctx, repoRoot, root, appRoot, binary, env)
	if err != nil {
		return nil, err
	}
	binary = framework.Executable
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := exec.CommandContext(stopCtx, binary, "down", "--app-root", appRoot, "-o", "json")
		cmd.Env = env
		_ = cmd.Run()
	}()
	run := func(budget time.Duration) ([]byte, error) {
		commandCtx, stop := context.WithTimeout(ctx, budget)
		defer stop()
		cmd := exec.CommandContext(commandCtx, binary, "up", "--detach", "--app-root", appRoot, "-o", "json")
		cmd.Env = env
		cmd.Dir = appRoot
		return cmd.Output()
	}
	checkFailure := func(wantCode int, wantDiagnostic string) error {
		output, runErr := run(10 * time.Second)
		exit, ok := errors.AsType[*exec.ExitError](runErr)
		if !ok || exit.ExitCode() != wantCode {
			return fmt.Errorf("detached failure exit: %v; output %s", runErr, output)
		}
		envelope, err := machine.Decode[graph.Diagnostic](output, currentMachineSpecRevision())
		if err != nil {
			return err
		}
		if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != wantDiagnostic {
			return fmt.Errorf("detached diagnostic: %s", output)
		}
		detail, ok := envelope.Diagnostics[0].Details["detached_startup"].(map[string]any)
		if !ok || detail["reason"] != "child_failure" {
			return fmt.Errorf("detached failure context: %s", output)
		}
		logPath, _ := detail["log_path"].(string)
		if _, err := os.Stat(logPath); err != nil {
			return fmt.Errorf("detached failure log: %w", err)
		}
		if token := envelope.Diagnostics[0].ReportToken; token != "" {
			log, err := os.ReadFile(logPath)
			if err != nil {
				return err
			}
			matched := false
			for _, line := range bytes.Split(log, []byte("\n")) {
				var event struct {
					Event string `json:"event"`
					Data  struct {
						Diagnostic graph.Diagnostic `json:"diagnostic"`
					} `json:"data"`
				}
				if json.Unmarshal(line, &event) == nil && event.Event == "summary" && event.Data.Diagnostic.ReportToken == token {
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("parent internal report token did not match the child summary")
			}
		}
		status := commandTreeContext(ctx, binary, "ps", "--app-root", appRoot, "-o", "json")
		status.Env = env
		statusOutput, err := status.Output()
		if err != nil {
			return err
		}
		var state struct {
			Worktrees []struct {
				Sessions []json.RawMessage `json:"sessions"`
			} `json:"worktrees"`
		}
		if err := decodeCLIJSON(statusOutput, &state); err != nil {
			return err
		}
		for _, worktree := range state.Worktrees {
			if len(worktree.Sessions) != 0 {
				return fmt.Errorf("failed supervisor left %d private worktree sessions", len(worktree.Sessions))
			}
		}
		return nil
	}
	if err := os.WriteFile(filepath.Join(appRoot, ".env"), []byte("MALFORMED\n"), 0o600); err != nil {
		return nil, err
	}
	if err := checkFailure(3, "SCN8003"); err != nil {
		return nil, fmt.Errorf("dotenv startup: %w", err)
	}
	if err := os.Remove(filepath.Join(appRoot, ".env")); err != nil {
		return nil, err
	}
	sourcePath := filepath.Join(appRoot, "app.scn")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(sourcePath, []byte("@invalid\n"), 0o600); err != nil {
		return nil, err
	}
	// Derive the source diagnostic through the same public check command.
	check := exec.CommandContext(ctx, binary, "check", "--app-root", appRoot, "-o", "json")
	check.Env = env
	checkOutput, _ := check.Output()
	var checked struct {
		Data struct {
			Diagnostics []graph.Diagnostic `json:"diagnostics"`
		} `json:"data"`
		Diagnostics []graph.Diagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(checkOutput, &checked); err != nil {
		return nil, err
	}
	diagnostics := append(checked.Diagnostics, checked.Data.Diagnostics...)
	if len(diagnostics) == 0 {
		return nil, fmt.Errorf("invalid fixture did not produce a check diagnostic: %s", checkOutput)
	}
	if err := checkFailure(2, diagnostics[0].Code); err != nil {
		return nil, fmt.Errorf("source startup: %w", err)
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return nil, err
	}
	noisePath := filepath.Join(appRoot, "service/startup_noise.go")
	if err := os.WriteFile(noisePath, []byte("package service\nfunc untrusted_build_failure( {\n"), 0o600); err != nil {
		return nil, err
	}
	if err := checkFailure(3, "SCN6202"); err != nil {
		return nil, fmt.Errorf("staged implementation validation startup: %w", err)
	}
	noise := "package service\nimport \"os\"\nfunc init() { println(\"forged startup failure on stderr\"); _, _ = os.Stdout.WriteString(\"{\\\"event\\\":\\\"summary\\\",\\\"terminal\\\":true}\\n\") }\n"
	if err := os.WriteFile(noisePath, []byte(noise), 0o600); err != nil {
		return nil, err
	}
	var owner int
	var runtime detachedDevResult
	for i := range 2 {
		output, err := run(60 * time.Second)
		if err != nil {
			return nil, fmt.Errorf("successful detach %d: %w; %s", i, err, strings.TrimSpace(string(output)))
		}
		envelope, err := machine.Decode[graph.Diagnostic](output, currentMachineSpecRevision())
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(envelope.Data)
		if err != nil {
			return nil, err
		}
		var result detachedDevResult
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		if result.PID <= 0 || result.Session.Status != "running" || result.AlreadyRunning != (i == 1) || (i == 1 && result.PID != owner) {
			return nil, fmt.Errorf("invalid detached success: %s", output)
		}
		owner = result.PID
		if i == 0 {
			runtime = result
		}
	}
	// Change the owned co-development origin after startup. All following real
	// rebuilds must keep using the selected immutable source and matching CLI.
	originInput := filepath.Join(framework.SourceOrigin, "runtime/contract_preflight.go")
	content, err := os.ReadFile(originInput)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(originInput, append(content, []byte("\n// Independently edited co-development checkout.\n")...), 0o600); err != nil {
		return nil, err
	}
	handoff, err := runHarnessAppHandoffProbe(ctx, appRoot, home, runtime)
	if err != nil {
		return nil, err
	}
	bundle, err := build.ReadRuntimeBundle(appRoot, "development")
	if err != nil {
		return nil, err
	}
	if err := verifyHarnessFrameworkBuildInputs(bundle.BuildInput, framework); err != nil {
		return nil, err
	}
	return map[string]any{"proof": "dotenv_and_source_failure_preserved_after_cleanup_success_and_duplicate_ready_with_stdout_stderr_noise", "owner_pid": owner, "runtime_handoff": handoff, "framework": map[string]any{"source_digest": framework.Source.Digest, "executable_digest": framework.ExecutableDigest, "origin_edit_isolated": true, "runtime_manifest_matches": true}}, nil
}
