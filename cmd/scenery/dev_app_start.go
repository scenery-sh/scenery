package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
	"scenery.sh/runtime"
)

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot *fileSnapshot) (returnErr error) {
	if snapshot == nil {
		return errors.New("application rebuild requires a captured source snapshot")
	}
	if err := refreshBuildCompilerMembership(s.root, snapshot); err != nil {
		return err
	}
	captured := *snapshot
	operationID := newDevBuildOperationID()
	ctx = build.WithTraceOperation(ctx, operationID, s.emitBuildStep)
	requestStarted := time.Now()
	recordDevScheduling(ctx, clearInheritedBackgroundPolicy)
	defer func() {
		reason := "source_rebuild"
		if initial {
			reason = "initial_build"
		}
		build.RecordStep(ctx, build.Step{
			Name: "build.request", StartedAt: requestStarted, Duration: time.Since(requestStarted),
			Cache: "not_applicable", Reason: reason, OK: returnErr == nil, SnapshotDigest: snapshotFingerprint(captured),
		})
	}()
	if !captured.scanStartedAt.IsZero() && !captured.capturedAt.IsZero() {
		build.RecordStep(ctx, build.Step{
			Name: "watch.scan", StartedAt: captured.scanStartedAt, Duration: captured.capturedAt.Sub(captured.scanStartedAt),
			Cache: "directory_listing", Reason: "captured_snapshot", OK: true, Actions: len(captured.files), SnapshotDigest: snapshotFingerprint(captured),
			CacheHits: captured.scanStats.dirsReused, CacheMisses: captured.scanStats.dirsRead,
			FilesHashed: captured.scanStats.filesHashed, BytesHashed: captured.scanStats.bytesHashed,
		})
	}
	if !captured.capturedAt.IsZero() {
		queue := time.Since(captured.capturedAt)
		if queue < 0 {
			queue = 0
		}
		build.RecordStep(ctx, build.Step{
			Name: "build.queue", StartedAt: captured.capturedAt, Duration: queue, QueueDuration: queue,
			Cache: "not_applicable", Reason: "captured_snapshot_to_build_start", OK: true, SnapshotDigest: snapshotFingerprint(captured),
		})
	}
	previousConfig, previousEnvironment := s.cfg, s.env
	activated := false
	defer func() {
		if !activated {
			s.cfg, s.env = previousConfig, previousEnvironment
			s.setAppIdentity(previousConfig)
		}
	}()
	configStarted := time.Now()
	cfg, err := s.reloadConfig(captured)
	build.RecordStep(ctx, build.Step{Name: "supervisor.config_reload", StartedAt: configStarted, Duration: time.Since(configStarted), Cache: "not_applicable", Reason: "captured_snapshot_config", OK: err == nil})
	if err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	s.cfg = cfg
	s.setAppIdentity(cfg)
	s.setCompiling(true, "")
	statusStarted := time.Now()
	err = s.persistStatus(ctx)
	build.RecordStep(ctx, build.Step{Name: "supervisor.status_persist", StartedAt: statusStarted, Duration: time.Since(statusStarted), Cache: "not_applicable", Reason: "compile_start", OK: err == nil})
	if err != nil {
		return err
	}
	notifyStarted := time.Now()
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "running"}, "info", "build started", map[string]any{
		"initial": initial,
	})
	s.writeProcessEvent(ctx, "compile-start", s.compactAppStatus())
	if s.console != nil {
		s.console.Event("process.compile-start", map[string]any{
			"initial": initial,
		})
	}
	build.RecordStep(ctx, build.Step{Name: "supervisor.compile_start_notify", StartedAt: notifyStarted, Duration: time.Since(notifyStarted), Cache: "not_applicable", Reason: "events", OK: true})

	var earlyAssistants *assistantStageAttempt
	if initial && s.assistants != nil && captured.contract.Valid() {
		s.assistants.lifecycle.Lock()
		defer s.assistants.lifecycle.Unlock()
		earlyAssistants = s.assistants.beginStage(ctx, captured.contract)
		defer earlyAssistants.release()
	}
	plan, err := s.prepareDevRuntimePlan(ctx, initial, captured)
	if err != nil {
		metadata, apiEncoding := devBuildErrorPayload(err)
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if initial {
		if err := s.waitForStartupReady(ctx); err != nil {
			return err
		}
	}
	if err := build.VerifyOwnedGoModuleSourcesContext(ctx, plan.Result.OwnedGoModuleSources); err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}
	defer plan.Prepared.release(s)
	snapshotStarted := time.Now()
	err = s.requireCurrentBuildSnapshot(captured)
	build.RecordStep(ctx, build.Step{Name: "supervisor.snapshot_verify", StartedAt: snapshotStarted, Duration: time.Since(snapshotStarted), Cache: "not_applicable", Reason: "source_unchanged_since_capture", OK: err == nil})
	if err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}
	activationStarted := time.Now()
	current, reload, err := s.activateDevProcesses(ctx, plan, earlyAssistants)
	build.RecordStep(ctx, build.Step{
		Name: "runtime.activation", StartedAt: activationStarted, Duration: time.Since(activationStarted),
		Cache: "not_applicable", Reason: "publish_process_generation", OK: err == nil, PackagesRebuilt: plan.Processes.Rebuilt,
		ContractRevision: plan.Result.Contract.Manifest.ContractRevision,
	})
	if err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}
	activated = true
	s.mu.Lock()
	s.buildFailed = false
	s.mu.Unlock()
	publishStarted := time.Now()
	err = s.publishActivatedApp(ctx, initial, snapshot, plan, current, reload)
	build.RecordStep(ctx, build.Step{Name: "supervisor.publish", StartedAt: publishStarted, Duration: time.Since(publishStarted), Cache: "not_applicable", Reason: "activated_generation_status", OK: err == nil})
	return err
}

// publishActivatedApp reports a successfully activated application generation
// to status, the dashboard, the console and the local agent.
func (s *devSupervisor) publishActivatedApp(ctx context.Context, initial bool, snapshot *fileSnapshot, plan *devRuntimePlan, current *runningApp, reload bool) error {
	s.setCompiling(false, "")
	s.setRunning(current.pid, plan.Metadata, plan.APIEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "ready"}, "info", "build succeeded", map[string]any{
		"initial": initial,
		"pid":     current.pid,
	})

	method := "process/start"
	if reload {
		method = "process/reload"
	}
	s.writeProcessEvent(ctx, method, s.compactAppStatus())
	if s.console != nil {
		s.console.Event(method, map[string]any{
			"pid":         current.pid,
			"listen_addr": s.addr,
			"initial":     initial,
		})
	}
	if err := s.console.Phase("Publishing application process identity", func() error {
		s.updateAgentSession(ctx, "running", current.pid)
		return nil
	}); err != nil {
		return err
	}
	if initial {
		s.console.Banner(s.runURLs())
	}
	refreshSnapshotContract(s.root, snapshot, plan.Result.Contract)
	return nil
}

// requireCurrentBuildSnapshot compares the captured snapshot with a fresh scan
// that reads every directory, so an activation never rests on the same reused
// directory listings its capture observed.
func (s *devSupervisor) requireCurrentBuildSnapshot(snapshot fileSnapshot) error {
	current, err := scanWatchedFilesFresh(s.root, snapshot)
	if err != nil {
		return fmt.Errorf("verify current build inputs: %w", err)
	}
	if !buildInputSnapshotsEqual(snapshot, current) {
		return fmt.Errorf("source changed during candidate preparation; discard the superseded generation and retry")
	}
	return nil
}

func (s *devSupervisor) reloadConfig(snapshot fileSnapshot) (app.Config, error) {
	stamp, ok := snapshot.files[app.PrimaryConfigFilename]
	if !ok || stamp.data == nil {
		return app.Config{}, fmt.Errorf("scenery app config moved from %s", s.root)
	}
	cfg, err := app.ParseConfig(s.root, stamp.data)
	if err != nil {
		return app.Config{}, err
	}
	resolved, err := cfg.ResolveEnv(s.env.Name)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Frontends = resolved.Frontends
	s.env = resolved
	return cfg, nil
}

// appChildEnvironment is the complete environment of an application runtime
// process listening on the supervisor's API backend.
func (s *devSupervisor) appChildEnvironment(result *build.Result, environment *devRuntimeEnvironment) []string {
	agentSession := s.currentAgentSession()
	appBaseEnv := s.appDatabaseAuthorityEnv(environment.base, result.Contract.SQLRequirements)
	env := appChildEnv(
		appBaseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_LISTEN_NETWORK="+s.backend.Network,
		"SCENERY_LISTEN_ADDR="+s.addr,
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_ENV="+s.env.Name,
		"SCENERY_RUNTIME_ENV="+s.env.Name,
		"SCENERY_DEV_SUPERVISOR=1",
		"SCENERY_DEV_ENDPOINTS=1",
		fmt.Sprintf("SCENERY_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"SCENERY_DEV_REPORT_URL="+s.devReportURL(),
		"SCENERY_DEV_REPORT_TOKEN="+s.reportToken,
	)
	env = append(env, s.observabilityEnvironment()...)
	env = append(env, s.sessionIdentityEnv()...)
	env = append(env, environment.managed...)
	env = append(env, environment.storage...)
	if agentSession != nil && agentSession.RouteManifest.Routes[localagent.RouteAPI].URL != "" {
		env = append(env, "SCENERY_PUBLIC_BASE_URL="+agentSession.RouteManifest.Routes[localagent.RouteAPI].URL)
	}
	env = append(env, s.sessionAuthEnv()...)
	// Framework-owned assistant handoff values are appended last so a dotenv
	// file or app-managed env map cannot override the private descriptor/key
	// path with ambient user input.
	if path := s.assistantRuntimeConfigPath(); path != "" {
		env = append(env, runtime.AssistantRuntimeConfigEnv+"="+path)
	}
	if path := strings.TrimSpace(s.assistantTokenKeyPath); path != "" {
		env = append(env, runtime.AssistantTokenKeyFileEnv+"="+path)
	}
	return env
}

func (s *devSupervisor) appProcessStartRequest(ctx context.Context, name, role, binary string, env []string) devProcessStartRequest {
	return devProcessStartRequest{
		Name:    name,
		Kind:    "app",
		Role:    role,
		Dir:     s.root,
		Command: binary,
		Env:     env,
		Stdout:  s.processOutputWriter(os.Stdout),
		Stderr:  s.processOutputWriter(os.Stderr),
		Filter:  s.processOutputFilter,
		OnOutput: func(pid int, stream string, data []byte) {
			source := devdash.DevSource{
				ID:     name,
				Kind:   "app",
				Name:   name,
				Role:   role,
				PID:    fmt.Sprintf("%d", pid),
				Stream: stream,
				Status: "running",
			}
			s.eventSink().Output(ctx, source, data)
		},
	}
}
