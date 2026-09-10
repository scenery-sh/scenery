package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
	"scenery.sh/runtime"
)

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot fileSnapshot) error {
	previousConfig, previousEnvironment := s.cfg, s.env
	activated := false
	defer func() {
		if !activated {
			s.cfg, s.env = previousConfig, previousEnvironment
			s.setAppIdentity(previousConfig)
		}
	}()
	if cfg, err := s.reloadConfig(); err != nil {
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

	plan, err := s.prepareDevRuntimePlan(ctx, initial, snapshot)
	if err != nil {
		metadata, apiEncoding := devBuildErrorPayload(err)
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if initial {
		if err := s.waitForStartupReady(ctx); err != nil {
			return err
		}
	}
	var candidate *appStartPlan
	err = s.console.Phase("Preparing candidate process", func() error {
		candidate, err = s.prepareAppStart(ctx, plan.Result, plan.Metadata, plan.APIEncoding)
		return err
	})
	defer s.releaseUnusedAppBinary(candidate)
	if err == nil {
		err = s.console.Phase("Verifying candidate preflight", func() error { return preflightAppStart(ctx, candidate) })
	}
	if err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}

	// Detach before stopping so the exit watchers treat this as an intentional
	// restart rather than a crash; otherwise handleExit races the restart and
	// can register the session as "stopped" after the new app is running.
	previous := s.detachCurrentApp()
	var current *runningApp
	var recovered bool
	if err := s.console.Phase("Starting scenery application", func() error {
		current, recovered, err = replaceAppGeneration(ctx, previous, candidate, func(app *runningApp) error {
			return s.console.Phase("Stopping previous application process", app.stop)
		}, s.startPreparedApp)
		return err
	}); err != nil {
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
	return nil
}

func (s *devSupervisor) reloadConfig() (app.Config, error) {
	root, cfg, err := app.DiscoverRoot(s.root)
	if err != nil {
		return app.Config{}, err
	}
	if filepath.Clean(root) != filepath.Clean(s.root) {
		return app.Config{}, fmt.Errorf("scenery app config moved from %s to %s", s.root, root)
	}
	resolved, err := cfg.ResolveEnv(s.env.Name)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Frontends = resolved.Frontends
	s.env = resolved
	return cfg, nil
}

func (s *devSupervisor) prepareAppStart(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage) (*appStartPlan, error) {
	if result == nil || result.Contract == nil || !result.Contract.Valid() {
		return nil, fmt.Errorf("application startup requires a valid compiled contract")
	}
	agentSession := s.currentAgentSession()
	binary := result.Binary
	baseEnv, err := appEnvWithDotEnv(s.processEnvironment(), s.root, s.env.DotEnvFiles()...)
	if err != nil {
		return nil, err
	}
	appBaseEnv := s.appDatabaseAuthorityEnv(baseEnv, result.Contract.SQLRequirements)
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
	managedEnv, err := s.managedAppEnv(ctx, baseEnv, result.Contract.SQLRequirements)
	if err != nil {
		return nil, err
	}
	env = append(env, managedEnv...)
	storageEnv, err := storageCapabilityEnv(ctx, s.root, s.cfg, agentSession, baseEnv, "")
	if err != nil {
		return nil, err
	}
	env = append(env, storageEnv...)
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
	if sessionBinary, err := prepareSessionAppBinary(agentSession, result.Binary); err != nil {
		return nil, err
	} else if sessionBinary != "" {
		binary = sessionBinary
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
		// Only replace helper descriptors after the previous app has stopped.
		// Recovery prepares the retained contract again before starting it.
		_ = s.console.Phase("Preparing assistant runtimes", func() error { return s.assistants.Prepare(ctx, plan.result.Contract) })
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
