package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/workers"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

const typeScriptWorkerStartupProbe = 750 * time.Millisecond

type runningTypeScriptWorker struct {
	cmd    *exec.Cmd
	done   chan error
	pid    string
	output *safeLineTail
}

func effectiveDevConfigForTypeScriptWorker(cfg app.Config, ts workers.TypeScriptWorkerModel) app.Config {
	if !typeScriptWorkerAutoStartEnabled(cfg, ts) {
		return cfg
	}
	cfg.Temporal.Enabled = true
	if strings.TrimSpace(cfg.Temporal.Mode) == "" {
		cfg.Temporal.Mode = "local"
	}
	cfg.Temporal.Local.AutoStart = true
	return cfg
}

func typeScriptWorkerAutoStartEnabled(cfg app.Config, ts workers.TypeScriptWorkerModel) bool {
	return cfg.Temporal.TypeScript.Enabled && cfg.Temporal.TypeScript.AutoStart && len(ts.Activities) > 0
}

func (s *devSupervisor) generateTypeScriptTemporalWorker() (*workers.TypeScriptWorkerResult, error) {
	info := s.typeScriptWorkerTemporalInfo()
	result, err := workers.GenerateTypeScriptWorker(workers.TypeScriptWorkerOptions{
		AppRoot:      s.root,
		AppName:      s.cfg.Name,
		BuildID:      onlavaruntime.TemporalWorkerBuildID(info),
		Namespace:    info.Namespace,
		PayloadCodec: info.PayloadCodec,
	})
	if err != nil {
		return nil, err
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("typescript-worker.generate", map[string]any{
			"output_dir": result.OutputDir,
			"activities": len(result.Activities),
		})
	}
	return &result, nil
}

func (s *devSupervisor) startTypeScriptWorker(ctx context.Context, result workers.TypeScriptWorkerResult) (*runningTypeScriptWorker, error) {
	runtimeName, runtimeArgs, err := typeScriptWorkerCommand(s.cfg.Temporal.TypeScript.Runtime)
	if err != nil {
		return nil, err
	}
	cmd := commandTreeContext(s.ctx, runtimeName, runtimeArgs...)
	cmd.Dir = result.OutputDir
	if strings.TrimSpace(cmd.Dir) == "" {
		cmd.Dir = filepath.Join(s.root, workers.TypeScriptWorkerGeneratedRelDir)
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return nil, err
	}
	cmd.Env = s.typeScriptWorkerEnv(baseEnv)
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	worker := &runningTypeScriptWorker{
		cmd:    cmd,
		done:   make(chan error, 1),
		pid:    fmt.Sprintf("%d", cmd.Process.Pid),
		output: &safeLineTail{limit: 80},
	}
	go s.captureTypeScriptWorkerOutput(ctx, worker, "stdout", stdout, os.Stdout)
	go s.captureTypeScriptWorkerOutput(ctx, worker, "stderr", stderr, os.Stderr)
	go func() {
		worker.done <- cmd.Wait()
		close(worker.done)
		s.handleTypeScriptWorkerExit(context.Background(), worker)
	}()
	s.mu.Lock()
	s.typescript = worker
	s.mu.Unlock()
	if err := waitForTypeScriptWorkerStartup(ctx, worker); err != nil {
		s.clearTypeScriptWorker(worker)
		return nil, err
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("typescript-worker.start", map[string]any{
			"pid":        worker.pid,
			"runtime":    filepath.Base(runtimeName),
			"output_dir": cmd.Dir,
			"queues":     typeScriptWorkerQueues(result.Activities),
		})
	}
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "typescript-worker-start", map[string]any{
		"pid": worker.pid,
	})
	return worker, nil
}

func (s *devSupervisor) typeScriptWorkerEnv(baseEnv []string) []string {
	info := s.typeScriptWorkerTemporalInfo()
	extra := []string{
		"ONLAVA_APP_ID=" + s.activeAppID(),
		"ONLAVA_APP_ROOT=" + s.root,
		"ONLAVA_DEV_SUPERVISOR=1",
		fmt.Sprintf("ONLAVA_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"ONLAVA_DEV_REPORT_URL=" + s.devReportURL(),
		"ONLAVA_DEV_REPORT_TOKEN=" + s.reportToken,
		"ONLAVA_ROLE=typescript-worker",
		"TEMPORAL_ADDRESS=" + info.Address,
		"TEMPORAL_NAMESPACE=" + info.Namespace,
		"ONLAVA_BUILD_ID=" + onlavaruntime.TemporalWorkerBuildID(info),
		"ONLAVA_TEMPORAL_DEPLOYMENT_NAME=" + onlavaruntime.TemporalDeploymentName(info),
		"ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX=" + info.TaskQueuePrefix,
	}
	extra = append(extra, s.victoria.Env()...)
	extra = append(extra, s.temporal.Env()...)
	extra = append(extra, s.sessionTemporalEnv()...)
	extra = append(extra, s.sessionIdentityEnv()...)
	return appChildEnv(envWithOverrides(baseEnv, compactEnvOverrides(extra)...), s.console != nil && s.console.palette.Enabled())
}

func (s *devSupervisor) typeScriptWorkerTemporalInfo() onlavaruntime.TemporalRuntimeInfo {
	info := onlavaruntime.ResolveTemporalConfig(s.cfg.Name, temporalRuntimeConfigFromApp(s.cfg.Temporal))
	if s.temporal == nil || !s.temporal.info.Enabled {
		return info
	}
	info.Address = s.temporal.info.Address
	info.AddressEnv = s.temporal.info.AddressEnv
	info.Namespace = s.temporal.info.Namespace
	return info
}

func (s *devSupervisor) captureTypeScriptWorkerOutput(ctx context.Context, worker *runningTypeScriptWorker, stream string, src io.Reader, dst io.Writer) {
	pid := ""
	var tail *safeLineTail
	if worker != nil {
		pid = worker.pid
		tail = worker.output
	}
	s.captureProcessOutput(ctx, pid, stream, tail, bufio.NewReader(src), dst)
}

func waitForTypeScriptWorkerStartup(ctx context.Context, worker *runningTypeScriptWorker) error {
	timer := time.NewTimer(typeScriptWorkerStartupProbe)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = worker.stop()
		return ctx.Err()
	case err, ok := <-worker.done:
		if !ok {
			return typeScriptWorkerStartupExitError(worker, nil)
		}
		return typeScriptWorkerStartupExitError(worker, err)
	case <-timer.C:
		return nil
	}
}

func typeScriptWorkerStartupExitError(worker *runningTypeScriptWorker, err error) error {
	message := "onlava TypeScript Temporal worker exited during startup"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += ": process exited without an error"
	}
	if worker != nil && worker.output != nil {
		if output := strings.TrimSpace(worker.output.String()); output != "" {
			message += "\n" + output
		}
	}
	return errors.New(message)
}

func (s *devSupervisor) handleTypeScriptWorkerExit(ctx context.Context, worker *runningTypeScriptWorker) {
	if worker == nil {
		return
	}
	if !s.clearTypeScriptWorker(worker) {
		return
	}

	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "typescript-worker-stop", map[string]any{
		"pid": worker.pid,
	})
	if s.console != nil {
		s.console.Event("typescript-worker.stop", map[string]any{
			"pid": worker.pid,
		})
	}
}

func (s *devSupervisor) clearTypeScriptWorker(worker *runningTypeScriptWorker) bool {
	if worker == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.typescript == nil || s.typescript.pid != worker.pid {
		return false
	}
	s.typescript = nil
	return true
}

func (w *runningTypeScriptWorker) interrupt() error {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(w.cmd)
}

func (w *runningTypeScriptWorker) kill() error {
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return nil
	}
	return killProcessTree(w.cmd)
}

func (w *runningTypeScriptWorker) waitOrKill(grace time.Duration) error {
	if w == nil {
		return nil
	}
	select {
	case err := <-w.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		_ = w.kill()
		select {
		case err := <-w.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("TypeScript worker did not exit after SIGKILL")
		}
	}
}

func (w *runningTypeScriptWorker) stop() error {
	if err := w.interrupt(); err != nil {
		return err
	}
	return w.waitOrKill(stopTimeout)
}

func typeScriptWorkerQueues(activities []workers.TypeScriptActivity) []string {
	seen := map[string]struct{}{}
	for _, activity := range activities {
		queue := strings.TrimSpace(activity.TaskQueue)
		if queue == "" {
			continue
		}
		seen[queue] = struct{}{}
	}
	queues := make([]string, 0, len(seen))
	for queue := range seen {
		queues = append(queues, queue)
	}
	slices.Sort(queues)
	return queues
}

func compactEnvOverrides(overrides []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(overrides))
	for i := len(overrides) - 1; i >= 0; i-- {
		key, _, ok := strings.Cut(overrides[i], "=")
		if !ok {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, overrides[i])
	}
	slices.Reverse(out)
	return out
}
