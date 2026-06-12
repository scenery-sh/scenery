package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/workers"
	sceneryruntime "scenery.sh/runtime"
)

const typeScriptWorkerStartupProbe = 750 * time.Millisecond
const typeScriptWorkerStaleGrace = 750 * time.Millisecond

const typeScriptWorkerDevRegistryFile = "dev-worker.json"

type runningTypeScriptWorker struct {
	process *devManagedProcess
	cmd     *exec.Cmd
	done    chan error
	pid     string
	output  *safeLineTail
}

type typeScriptWorkerDevRegistry struct {
	SchemaVersion string   `json:"schema_version"`
	PID           int      `json:"pid"`
	AppRoot       string   `json:"app_root"`
	OutputDir     string   `json:"output_dir"`
	WorkerPath    string   `json:"worker_path"`
	Command       []string `json:"command"`
	DevSupervisor bool     `json:"dev_supervisor"`
	StartedAt     string   `json:"started_at"`
}

func effectiveDevConfigForTypeScriptWorker(cfg app.Config, ts workers.TypeScriptWorkerModel) app.Config {
	if !typeScriptWorkerAutoStartEnabled(cfg, ts) {
		return cfg
	}
	if strings.TrimSpace(cfg.Temporal.Mode) == "" {
		cfg.Temporal.Mode = "local"
	}
	cfg.Temporal.Local.AutoStart = true
	return cfg
}

func typeScriptWorkerAutoStartEnabled(cfg app.Config, ts workers.TypeScriptWorkerModel) bool {
	return cfg.Temporal.Enabled && cfg.Temporal.TypeScript.Enabled && cfg.Temporal.TypeScript.AutoStart && len(ts.Activities) > 0
}

func (s *devSupervisor) generateTypeScriptTemporalWorker() (*workers.TypeScriptWorkerResult, error) {
	info := s.typeScriptWorkerTemporalInfo()
	result, err := workers.GenerateTypeScriptWorker(workers.TypeScriptWorkerOptions{
		AppRoot:      s.root,
		AppName:      s.cfg.Name,
		BuildID:      sceneryruntime.TemporalWorkerBuildID(info),
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
	outputDir := strings.TrimSpace(result.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(s.root, workers.TypeScriptWorkerGeneratedRelDir)
	}
	if detachedDevChildMode() {
		if err := s.reapStaleTypeScriptWorker(ctx, outputDir); err != nil {
			return nil, err
		}
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return nil, err
	}
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return nil, err
	}
	process, err := startDevManagedProcess(s.ctx, devProcessStartRequest{
		Name:    "typescript",
		Kind:    "worker",
		Role:    "temporal-activity-worker",
		Dir:     outputDir,
		Command: runtimeName,
		Args:    runtimeArgs,
		Env:     s.typeScriptWorkerEnv(baseEnv, managedEnv),
		Stdout:  s.processOutputWriter(os.Stdout),
		Stderr:  s.processOutputWriter(os.Stderr),
		OnOutput: func(pid int, stream string, data []byte) {
			source := devdash.DevSource{
				ID:     "worker:typescript",
				Kind:   "worker",
				Name:   "typescript",
				Role:   "temporal-activity-worker",
				PID:    fmt.Sprintf("%d", pid),
				Stream: stream,
				Status: "running",
			}
			s.eventSink().Output(ctx, source, data)
		},
	})
	if err != nil {
		return nil, err
	}
	worker := &runningTypeScriptWorker{
		process: process,
		cmd:     process.Cmd,
		pid:     fmt.Sprintf("%d", process.PID),
		output:  process.Tail,
	}
	go func() {
		<-process.done
		s.handleTypeScriptWorkerExit(context.Background(), worker)
	}()
	s.mu.Lock()
	s.typescript = worker
	s.mu.Unlock()
	if detachedDevChildMode() {
		if err := s.writeTypeScriptWorkerDevRegistry(ctx, worker, outputDir, runtimeName, runtimeArgs); err != nil {
			_ = worker.stop()
			s.clearTypeScriptWorker(worker)
			return nil, err
		}
	}
	if err := waitForTypeScriptWorkerStartup(ctx, worker); err != nil {
		_ = removeMatchingTypeScriptWorkerDevRegistry(outputDir, workerPIDInt(worker.pid))
		s.clearTypeScriptWorker(worker)
		return nil, err
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("typescript-worker.start", map[string]any{
			"pid":        worker.pid,
			"runtime":    filepath.Base(runtimeName),
			"output_dir": outputDir,
			"queues":     typeScriptWorkerQueues(result.Activities),
		})
	}
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "typescript-worker-start", map[string]any{
		"pid": worker.pid,
	})
	return worker, nil
}

func (s *devSupervisor) typeScriptWorkerEnv(baseEnv, managedEnv []string) []string {
	baseEnv = s.appDatabaseAuthorityEnv(baseEnv)
	info := s.typeScriptWorkerTemporalInfo()
	extra := []string{
		"SCENERY_APP_ID=" + s.activeAppID(),
		"SCENERY_APP_ROOT=" + s.root,
		"SCENERY_DEV_SUPERVISOR=1",
		fmt.Sprintf("SCENERY_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"SCENERY_DEV_REPORT_URL=" + s.devReportURL(),
		"SCENERY_DEV_REPORT_TOKEN=" + s.reportToken,
		"SCENERY_ROLE=typescript-worker",
		"TEMPORAL_ADDRESS=" + info.Address,
		"TEMPORAL_NAMESPACE=" + info.Namespace,
		"SCENERY_BUILD_ID=" + sceneryruntime.TemporalWorkerBuildID(info),
		"SCENERY_TEMPORAL_DEPLOYMENT_NAME=" + sceneryruntime.TemporalDeploymentName(info),
		"SCENERY_TEMPORAL_TASK_QUEUE_PREFIX=" + info.TaskQueuePrefix,
	}
	extra = append(extra, s.victoria.Env()...)
	extra = append(extra, s.temporal.Env()...)
	extra = append(extra, s.sessionTemporalEnv()...)
	extra = append(extra, s.sessionIdentityEnv()...)
	env := appChildEnv(envWithOverrides(baseEnv, compactEnvOverrides(extra)...), s.console != nil && s.console.palette.Enabled())
	return append(env, managedEnv...)
}

func (s *devSupervisor) typeScriptWorkerTemporalInfo() sceneryruntime.TemporalRuntimeInfo {
	info := sceneryruntime.ResolveTemporalConfig(s.cfg.Name, temporalRuntimeConfigFromApp(s.cfg.Temporal))
	if s.temporal == nil || !s.temporal.info.Enabled {
		return info
	}
	info.Address = s.temporal.info.Address
	info.AddressEnv = s.temporal.info.AddressEnv
	info.Namespace = s.temporal.info.Namespace
	return info
}

func waitForTypeScriptWorkerStartup(ctx context.Context, worker *runningTypeScriptWorker) error {
	if worker != nil && worker.process != nil {
		return worker.process.WaitReady(ctx, devProcessReadyRequest{
			Timeout: typeScriptWorkerStartupProbe,
		})
	}
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
	message := "scenery TypeScript Temporal worker exited during startup"
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
	_ = removeMatchingTypeScriptWorkerDevRegistry(workerOutputDir(worker), workerPIDInt(worker.pid))

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

func (s *devSupervisor) writeTypeScriptWorkerDevRegistry(ctx context.Context, worker *runningTypeScriptWorker, outputDir, runtimeName string, runtimeArgs []string) error {
	if worker == nil || worker.cmd == nil || worker.cmd.Process == nil {
		return nil
	}
	record := typeScriptWorkerDevRegistry{
		SchemaVersion: "scenery.dev.typescript_worker.v1",
		PID:           worker.cmd.Process.Pid,
		AppRoot:       cleanAbsPath(s.root),
		OutputDir:     cleanAbsPath(outputDir),
		WorkerPath:    cleanAbsPath(filepath.Join(outputDir, "worker.ts")),
		Command:       append([]string{runtimeName}, runtimeArgs...),
		DevSupervisor: true,
		StartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeTypeScriptWorkerDevRegistry(outputDir, record); err != nil {
		return err
	}
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "typescript-worker-register", map[string]any{
		"pid":         worker.pid,
		"worker_path": record.WorkerPath,
	})
	return nil
}

func (s *devSupervisor) reapStaleTypeScriptWorker(ctx context.Context, outputDir string) error {
	if !detachedDevChildMode() {
		return nil
	}
	record, ok, err := readTypeScriptWorkerDevRegistry(outputDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !matchesTypeScriptWorkerDevRegistry(record, s.root, outputDir) {
		return nil
	}
	if record.PID <= 0 {
		_ = os.Remove(typeScriptWorkerDevRegistryPath(outputDir))
		return nil
	}
	info, alive := inspectProcess(record.PID)
	if !alive {
		_ = os.Remove(typeScriptWorkerDevRegistryPath(outputDir))
		return nil
	}
	if !looksLikeTypeScriptWorkerProcess(info.cmd, record) {
		return nil
	}
	if err := stopStaleTypeScriptWorkerProcess(record.PID, typeScriptWorkerStaleGrace); err != nil {
		return err
	}
	_ = os.Remove(typeScriptWorkerDevRegistryPath(outputDir))
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "typescript-worker-stale-reap", map[string]any{
		"pid":         record.PID,
		"worker_path": record.WorkerPath,
		"output_dir":  record.OutputDir,
	})
	if s.console != nil && s.console.verbose {
		s.console.Event("typescript-worker.stale-reap", map[string]any{
			"pid":         record.PID,
			"worker_path": record.WorkerPath,
		})
	}
	return nil
}

func typeScriptWorkerDevRegistryPath(outputDir string) string {
	return filepath.Join(outputDir, typeScriptWorkerDevRegistryFile)
}

func writeTypeScriptWorkerDevRegistry(outputDir string, record typeScriptWorkerDevRegistry) error {
	if strings.TrimSpace(outputDir) == "" {
		return nil
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(typeScriptWorkerDevRegistryPath(outputDir), data, 0o644)
}

func readTypeScriptWorkerDevRegistry(outputDir string) (typeScriptWorkerDevRegistry, bool, error) {
	if strings.TrimSpace(outputDir) == "" {
		return typeScriptWorkerDevRegistry{}, false, nil
	}
	data, err := os.ReadFile(typeScriptWorkerDevRegistryPath(outputDir))
	if errors.Is(err, os.ErrNotExist) {
		return typeScriptWorkerDevRegistry{}, false, nil
	}
	if err != nil {
		return typeScriptWorkerDevRegistry{}, false, err
	}
	var record typeScriptWorkerDevRegistry
	if err := json.Unmarshal(data, &record); err != nil {
		return typeScriptWorkerDevRegistry{}, false, err
	}
	return record, true, nil
}

func removeMatchingTypeScriptWorkerDevRegistry(outputDir string, pid int) error {
	record, ok, err := readTypeScriptWorkerDevRegistry(outputDir)
	if err != nil || !ok {
		return err
	}
	if pid > 0 && record.PID != pid {
		return nil
	}
	return os.Remove(typeScriptWorkerDevRegistryPath(outputDir))
}

func matchesTypeScriptWorkerDevRegistry(record typeScriptWorkerDevRegistry, appRoot, outputDir string) bool {
	if !record.DevSupervisor || strings.TrimSpace(record.SchemaVersion) != "scenery.dev.typescript_worker.v1" {
		return false
	}
	if cleanAbsPath(record.AppRoot) != cleanAbsPath(appRoot) {
		return false
	}
	if cleanAbsPath(record.OutputDir) != cleanAbsPath(outputDir) {
		return false
	}
	return cleanAbsPath(record.WorkerPath) == cleanAbsPath(filepath.Join(outputDir, "worker.ts"))
}

func looksLikeTypeScriptWorkerProcess(command string, record typeScriptWorkerDevRegistry) bool {
	command = filepath.ToSlash(strings.TrimSpace(command))
	if command == "" || !strings.Contains(command, "worker.ts") {
		return false
	}
	workerPath := filepath.ToSlash(cleanAbsPath(record.WorkerPath))
	outputDir := filepath.ToSlash(cleanAbsPath(record.OutputDir))
	return strings.Contains(command, workerPath) ||
		strings.Contains(command, outputDir) ||
		strings.Contains(command, " worker.ts") ||
		strings.HasSuffix(command, "/worker.ts")
}

func stopStaleTypeScriptWorkerProcess(pid int, grace time.Duration) error {
	if err := terminateProcessIDTree(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if _, ok := inspectProcess(pid); !ok {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := killProcessIDTree(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	killDeadline := time.Now().Add(time.Second)
	for time.Now().Before(killDeadline) {
		if _, ok := inspectProcess(pid); !ok {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("stale TypeScript worker process %d did not exit after SIGKILL", pid)
}

func workerOutputDir(worker *runningTypeScriptWorker) string {
	if worker == nil {
		return ""
	}
	if worker.process != nil && worker.process.Cmd != nil {
		return worker.process.Cmd.Dir
	}
	if worker.cmd == nil {
		return ""
	}
	return worker.cmd.Dir
}

func workerPIDInt(pid string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(pid))
	if err != nil {
		return 0
	}
	return parsed
}

func cleanAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func (w *runningTypeScriptWorker) interrupt() error {
	if w != nil && w.process != nil {
		return w.process.Interrupt()
	}
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(w.cmd)
}

func (w *runningTypeScriptWorker) kill() error {
	if w != nil && w.process != nil {
		if w.process.Cmd != nil {
			return killProcessTree(w.process.Cmd)
		}
		return nil
	}
	if w == nil || w.cmd == nil || w.cmd.Process == nil {
		return nil
	}
	return killProcessTree(w.cmd)
}

func (w *runningTypeScriptWorker) waitOrKill(grace time.Duration) error {
	if w == nil {
		return nil
	}
	if w.process != nil {
		return w.process.WaitOrKill(grace)
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
	if w != nil && w.process != nil {
		return w.process.Stop(stopTimeout)
	}
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
