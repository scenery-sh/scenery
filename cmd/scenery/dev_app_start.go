package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
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
	if cfg, err := s.reloadConfig(captured); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	} else {
		s.cfg = cfg
		s.setAppIdentity(cfg)
	}
	s.setCompiling(true, "")
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "running"}, "info", "build started", map[string]any{
		"initial": initial,
	})
	s.dashboard.notify(&devdash.Notification{
		Method: "process/compile-start",
		Params: s.appStatus(),
	})
	s.writeProcessEvent(ctx, "compile-start", s.compactAppStatus())
	if s.console != nil {
		s.console.Event("process.compile-start", map[string]any{
			"initial": initial,
		})
	}

	var earlyAssistants *assistantStageAttempt
	if initial && s.assistants != nil && captured.contract.Valid() {
		s.assistants.lifecycle.Lock()
		defer s.assistants.lifecycle.Unlock()
		earlyAssistants = s.assistants.beginStage(ctx, captured.contract)
		defer earlyAssistants.release()
	}
	if err := s.requireCurrentBuildSnapshot(captured); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
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
	if err := s.requireCurrentBuildSnapshot(captured); err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}
	var candidate *appStartPlan
	candidateStarted := time.Now()
	err = s.console.Phase("Preparing candidate process", func() error {
		candidate, err = s.prepareAppStart(ctx, plan.Result, plan.Metadata, plan.APIEncoding, plan.Environment)
		return err
	})
	build.RecordStep(ctx, build.Step{Name: "candidate.prepare", StartedAt: candidateStarted, Duration: time.Since(candidateStarted), Cache: "not_applicable", Reason: "retained_executable_and_environment", OK: err == nil})
	defer s.releaseUnusedAppBinary(candidate)
	if err == nil {
		preflightStarted := time.Now()
		err = s.console.Phase("Verifying candidate preflight", func() error { return preflightAppStart(ctx, candidate) })
		build.RecordStep(ctx, build.Step{Name: "candidate.preflight", StartedAt: preflightStarted, Duration: time.Since(preflightStarted), Cache: "not_applicable", Reason: "exact_executable_attestation", OK: err == nil})
	}
	if err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if s.assistants != nil {
		if earlyAssistants == nil {
			s.assistants.lifecycle.Lock()
			defer s.assistants.lifecycle.Unlock()
		}
		previousStage := s.assistants.captureStage()
		defer s.assistants.releaseStage(previousStage)
		s.mu.Lock()
		previous := s.current
		if previous != nil && previous.launch != nil {
			previous.launch.assistants = previousStage
		}
		s.mu.Unlock()
		err = s.console.Phase("Staging assistant runtimes", func() error {
			var stageErr error
			if earlyAssistants != nil {
				if earlyAssistants.matches(plan.Result.Contract) {
					candidate.assistants, stageErr = earlyAssistants.wait()
					return stageErr
				}
				// A source change during startup may have forced a fresh graph.
				// Join and retire the old private stage before preparing that graph.
				earlyAssistants.release()
			}
			candidate.assistants, stageErr = s.assistants.stage(ctx, plan.Result.Contract)
			return stageErr
		})
		defer s.assistants.releaseStage(candidate.assistants)
		if err != nil && previous != nil {
			return s.handleCompileError(ctx, nil, nil, err)
		}
		if earlyAssistants != nil {
			unchanged, checkErr := compiler.SnapshotUnchanged(plan.Result.Contract)
			if checkErr != nil {
				return s.handleCompileError(ctx, nil, nil, checkErr)
			}
			if !unchanged {
				return s.handleCompileError(ctx, nil, nil, errors.New("source changed during startup preparation; retry from a stable snapshot"))
			}
		}
	}

	// Detach before stopping so the exit watchers treat this as an intentional
	// restart rather than a crash; otherwise handleExit races the restart and
	// can register the session as "stopped" after the new app is running.
	if err := s.requireCurrentBuildSnapshot(captured); err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}
	activationStarted := time.Now()
	previous := s.detachCurrentApp()
	var current *runningApp
	var recovered bool
	err = s.console.Phase("Starting scenery application", func() error {
		current, recovered, err = replaceAppGeneration(ctx, previous, candidate, func(app *runningApp) error {
			return s.console.Phase("Stopping previous application process", app.stop)
		}, s.startPreparedApp)
		return err
	})
	activationStep := build.Step{
		Name: "runtime.activation", StartedAt: activationStarted, Duration: time.Since(activationStarted),
		Cache: "not_applicable", Reason: "retire_launch_listener", OK: err == nil,
	}
	if candidate != nil && candidate.result != nil {
		activationStep.FrameworkSourceDigest = candidate.result.FrameworkSourceDigest
		if candidate.result.Target != nil {
			activationStep.GoTarget = candidate.result.Target.Name
			activationStep.ImplementationRevision = candidate.result.ImplementationRevisions[candidate.result.Target.Name]
		}
		if candidate.result.Contract != nil && candidate.result.Contract.Manifest != nil {
			activationStep.ContractRevision = candidate.result.Contract.Manifest.ContractRevision
		}
		if candidate.result.BuildInput != nil {
			activationStep.BuildInputDigest = candidate.result.BuildInput.Digest
		}
	}
	build.RecordStep(ctx, activationStep)
	if err != nil {
		s.mu.Lock()
		s.current = current
		s.mu.Unlock()
		if recovered {
			s.setRunning(current.pid, current.launch.metadata, current.launch.apiEncoding)
			s.writeProcessEvent(ctx, "process/rollback", map[string]any{"pid": current.pid, "reason": err.Error()})
		}
		return s.handleCompileError(ctx, nil, nil, err)
	}
	activated = true
	s.mu.Lock()
	s.current = current
	s.buildFailed = false
	s.mu.Unlock()
	if previous != nil {
		s.releaseUnusedAppBinary(previous.launch)
	}

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
	if previous != nil {
		method = "process/reload"
	}
	s.dashboard.notify(&devdash.Notification{
		Method: method,
		Params: s.appStatus(),
	})
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

func (s *devSupervisor) requireCurrentBuildSnapshot(snapshot fileSnapshot) error {
	current, err := scanWatchedFilesReusing(s.root, snapshot)
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

func (s *devSupervisor) prepareAppStart(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage, environment *devRuntimeEnvironment) (*appStartPlan, error) {
	if result == nil || result.Contract == nil || !result.Contract.Valid() {
		return nil, fmt.Errorf("application startup requires a valid compiled contract")
	}
	agentSession := s.currentAgentSession()
	binary := result.Binary
	sessionBinary, environment, err := prepareAppStartInputs(func() (string, error) {
		return prepareSessionAppBinary(agentSession, result.Binary)
	}, func() (*devRuntimeEnvironment, error) {
		if environment != nil {
			return environment, nil
		}
		return s.prepareRuntimeEnvironment(ctx, result.Contract)
	}, func(path string) {
		s.releaseUnusedAppBinary(&appStartPlan{request: devProcessStartRequest{Command: path}})
	})
	if err != nil {
		return nil, err
	}
	if sessionBinary != "" {
		binary = sessionBinary
	}
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
	return &appStartPlan{result: result, metadata: metadata, apiEncoding: apiEncoding, request: devProcessStartRequest{
		Name:    "api",
		Kind:    "app",
		Role:    "scenery-api",
		Dir:     s.root,
		Command: binary,
		Env:     env,
		Stdout:  s.processOutputWriter(os.Stdout),
		Stderr:  s.processOutputWriter(os.Stderr),
		Filter:  s.processOutputFilter,
		OnOutput: func(pid int, stream string, data []byte) {
			source := devdash.DevSource{
				ID:     "api",
				Kind:   "app",
				Name:   "api",
				Role:   "scenery-api",
				PID:    fmt.Sprintf("%d", pid),
				Stream: stream,
				Status: "running",
			}
			s.eventSink().Output(ctx, source, data)
		},
	}}, nil
}

func (s *devSupervisor) startPreparedApp(ctx context.Context, plan *appStartPlan) (*runningApp, error) {
	if s.assistants != nil {
		if err := s.console.Phase("Activating assistant runtimes", func() error {
			return s.assistants.activateStage(ctx, plan.assistants)
		}); err != nil {
			return nil, err
		}
		setAssistantImplementationWatch(s.root, assistantDefinitionsFromResult(plan.result.Contract, s.root))
		s.refreshAssistantRuntimeConfig()
	}
	if err := backendAvailableBeforeStartup(s.backend); err != nil {
		return nil, fmt.Errorf("app listen address %s is unavailable before startup: %w", s.addr, err)
	}
	var process *devManagedProcess
	err := s.console.Phase("Launching application process", func() error {
		var err error
		process, err = startDevManagedProcess(ctx, plan.request)
		return err
	})
	if err != nil {
		return nil, err
	}
	app := &runningApp{
		process:  process,
		cmd:      process.Cmd,
		buildDir: plan.result.Dir,
		pid:      fmt.Sprintf("%d", process.PID),
		output:   process.Tail,
		launch:   plan,
	}
	go func() {
		<-process.Done
		s.handleExit(context.Background(), app)
	}()
	if err := s.console.Phase("Waiting for application listener", func() error { return s.waitForAppStartup(ctx, app) }); err != nil {
		if stopErr := app.stop(); stopErr != nil {
			return app, errors.Join(err, fmt.Errorf("candidate shutdown is unconfirmed; rollback refused: %w", stopErr))
		}
		return nil, err
	}
	if s.assistants != nil {
		_ = s.console.Phase("Starting prepared assistant runtimes", func() error { return s.assistants.StartPrepared(ctx) })
		s.refreshAssistantRuntimeConfig()
	}
	return app, nil
}
