package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/victoria"
)

// The process lock's exact same-process arbitration remains covered by
// TestVictoriaSubstrateLocksSerializeConcurrentStartsAndRecoveriesInProcess.
// This release journey exercises the lock and recovery through a real owner.
func runHarnessConcurrentVictoriaEnsure(parent context.Context, repoRoot, root string) (proof map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	appRoot, home := filepath.Join(root, "app"), filepath.Join(root, "agent-home")
	if err := copyHarnessBasicFixture(repoRoot, appRoot); err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		_, err := runHarnessAppCLI(cleanup, repoRoot, appRoot, home, "down", "-o", "json")
		resultErr = errors.Join(resultErr, err)
	}()
	marker := filepath.Join(root, "component-starts")
	serverSource, serverBinary := filepath.Join(root, "victoria-server.go"), filepath.Join(root, "victoria-server")
	source := `package main
import("net"; "os"; "strings"; "fmt")
func main() {
 address := ""
 for _, argument := range os.Args[1:] { if strings.HasPrefix(argument, "-httpListenAddr=") { address = strings.TrimPrefix(argument, "-httpListenAddr=") } }
 listener, err := net.Listen("tcp", address); if err != nil { os.Exit(2) }; defer listener.Close()
 marker, err := os.OpenFile(` + fmt.Sprintf("%q", marker) + `, os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600); if err != nil { os.Exit(3) }; fmt.Fprintln(marker,os.Getpid()); marker.Close()
 for { connection, err := listener.Accept(); if err != nil { return }; connection.Close() }
}`
	if err := os.WriteFile(serverSource, []byte(source), 0o600); err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", serverBinary, serverSource)
	build.Env = harnessAppEnv(home)
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build Victoria fixture: %w: %s", err, output)
	}
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return nil, err
	}
	if err := paths.Prepare(); err != nil {
		return nil, err
	}
	stackRoot := filepath.Join(paths.ControlPaths().AgentDir, "victoria")
	unlock, err := devprocess.LockSubstrate(stackRoot, localagent.SubstrateVictoria, devprocess.LockOptions{})
	if err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	env := envWithOverrides(harnessAppEnv(home), "SCENERY_DEV_VICTORIA=1", "SCENERY_VICTORIA_METRICS_BIN="+serverBinary, "SCENERY_VICTORIA_LOGS_BIN="+serverBinary, "SCENERY_VICTORIA_TRACES_BIN="+serverBinary)
	up := func() (detachedDevResult, error) {
		cmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "up", "--detach", "--wait", "ready", "--app-root", appRoot, "-o", "json")
		cmd.Dir, cmd.Env = appRoot, env
		output, err := cmd.CombinedOutput()
		var result detachedDevResult
		if err != nil {
			return result, fmt.Errorf("public Victoria up: %w: %s", err, tailString(string(output), 8192))
		}
		return result, decodeCLIJSON(output, &result)
	}
	first, err := up()
	if err != nil {
		return nil, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	health, err := client.Health(ctx)
	if err != nil {
		return nil, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		return nil, err
	}
	stale, err := client.UpsertSubstrate(ctx, localagent.UpsertSubstrateRequest{Kind: localagent.SubstrateVictoria, Status: "ready", OwnerPID: 99999991})
	if err != nil {
		return nil, err
	}
	second, err := up()
	if err != nil {
		return nil, err
	}
	if !second.AlreadyRunning || first.PID <= 0 || second.PID != first.PID {
		return nil, fmt.Errorf("duplicate public up did not reuse the one live owner")
	}
	if data, err := os.ReadFile(marker); err == nil && len(strings.Fields(string(data))) != 0 {
		return nil, fmt.Errorf("victoria started while its real substrate lock was held")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	unlock()
	locked = false
	waitReady := func(previous *localagent.Substrate) (localagent.Substrate, error) {
		var observed localagent.Substrate
		err := waitForHarnessCondition(ctx, func() bool {
			current, err := client.GetSubstrate(ctx, localagent.SubstrateVictoria)
			if err != nil || current.Status != "ready" || len(current.PIDs) != len(victoria.ComponentSpecs()) {
				return false
			}
			for name, pid := range current.PIDs {
				if pid <= 0 || current.Owners[name].PID != pid || localagent.VerifyOwner(current.Owners[name]) != nil {
					return false
				}
				if previous != nil && previous.PIDs[name] == pid {
					return false
				}
			}
			observed = current
			return true
		})
		return observed, err
	}
	before, err := waitReady(nil)
	if err != nil {
		return nil, fmt.Errorf("wait for first owned Victoria stack: %w", err)
	}
	ownerIsComponent := false
	for _, pid := range before.PIDs {
		ownerIsComponent = ownerIsComponent || pid == before.OwnerPID
	}
	if before.CreatedAt.Equal(stale.CreatedAt) || !ownerIsComponent || localagent.VerifyOwner(before.Owner) != nil {
		return nil, fmt.Errorf("stale Victoria owner was not replaced by the runtime: created=%s stale_created=%s owner=%d runtime=%d", before.CreatedAt, stale.CreatedAt, before.OwnerPID, first.PID)
	}
	data, err := os.ReadFile(marker)
	if err != nil || len(strings.Fields(string(data))) != len(victoria.ComponentSpecs()) {
		return nil, fmt.Errorf("first Victoria ensure did not start exactly one stack: %v", err)
	}
	failed := before.Owners["logs"]
	if failed.PID != before.PIDs["logs"] || localagent.VerifyOwner(failed) != nil {
		return nil, fmt.Errorf("victoria fault target lost verified ownership")
	}
	if err := killProcessIDTree(failed.PID); err != nil {
		return nil, err
	}
	after, err := waitReady(&before)
	if err != nil {
		return nil, fmt.Errorf("wait for Victoria full-stack recovery: %w", err)
	}
	substrates, err := client.ListSubstrates(ctx)
	if err != nil || len(substrates) != 1 || substrates[0].Kind != localagent.SubstrateVictoria {
		return nil, fmt.Errorf("expected one registered Victoria stack: %v", err)
	}
	if _, err := runHarnessAppCLI(ctx, repoRoot, appRoot, home, "down", "-o", "json"); err != nil {
		return nil, err
	}
	for _, pid := range after.PIDs {
		if !waitForPIDExit(ctx, pid, 3*time.Second) {
			return nil, fmt.Errorf("victoria component %d survived public down", pid)
		}
	}
	stopped, err := os.ReadFile(marker)
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}
	later, err := os.ReadFile(marker)
	if err != nil || string(stopped) != string(later) {
		return nil, fmt.Errorf("victoria restarted after public down: %v", err)
	}
	return map[string]any{"public_runtime_reused": true, "one_stack_started": true, "managed_processes": len(victoria.ComponentSpecs()), "real_substrate_lock_enforced": true, "recovered": true, "failed_pid": failed.PID, "recovered_pid": after.PIDs["logs"], "stopped_without_restart": true, "stale_owner_replaced": true}, nil
}
