package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
	workerruntime "scenery.sh/runtime"
)

// hostRestartRequired is how the host reports a change that it cannot apply
// in place, such as a new application configuration.
const hostRestartRequired = "restart the application"

const (
	hostReadyTimeout  = 2 * time.Minute
	hostStatusPolling = 250 * time.Millisecond
)

// devHostGeneration is one running development host and the private inputs
// it was started with. The host rebuilds and publishes ordinary source edits
// itself; the supervisor replaces the generation only to restart it.
type devHostGeneration struct {
	app      *runningApp
	launch   *workerHostLaunch
	lease    io.Closer
	endpoint workerDevEndpoint
	// status is the latest source status the host reported, guarded by the
	// supervisor's mu.
	status  runtimeSourceWait
	monitor context.CancelFunc
	watched chan struct{}
	// config is the configuration the generation applied to manifest.
	config   *devConfigResolution
	manifest *graph.Manifest
	// services are the worker services the host runs.
	services []string
}

// RebuildAndRestart builds the application's workers from the captured source
// and starts a development host that serves them on the supervisor's API
// backend. A running host is replaced. Later ordinary edits are rebuilt by
// the host, so the watch loop calls this again only to start a host that is
// not running or to restart one that asked for it.
func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot *fileSnapshot) (returnErr error) {
	if snapshot == nil {
		return errors.New("application rebuild requires a captured source snapshot")
	}
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	captured := *snapshot
	operationID := newDevBuildOperationID()
	editClass := "initial"
	if !initial {
		editClass = "restart"
	}
	defer func() {
		if s.recordBuildOutcome(captured, returnErr) {
			_ = s.persistStatus(context.WithoutCancel(ctx))
		}
		if returnErr != nil {
			returnErr = devBuildOperationError{operationID: operationID, err: returnErr}
		}
	}()
	ctx = build.WithTraceOperation(ctx, operationID, func(step build.Step) {
		s.emitBuildStepWithEditClass(step, editClass)
	})
	recordDevScheduling(ctx, clearInheritedBackgroundPolicy)
	requestStarted := time.Now()
	build.RecordStep(ctx, build.Step{Name: "build.queue", StartedAt: requestStarted, Cache: "not_applicable", Reason: "captured_snapshot_to_build_start", OK: true})
	defer func() {
		outcome := "success"
		if returnErr != nil {
			outcome = "failure"
		}
		build.RecordStep(ctx, build.Step{Outcome: outcome, Name: "build.request", StartedAt: requestStarted, Duration: time.Since(requestStarted),
			Cache: "not_applicable", Reason: editClass, OK: returnErr == nil, SnapshotDigest: snapshotFingerprint(captured)})
	}()
	previousConfig, previousEnvironment := s.cfg, s.env
	cfg, err := s.reloadConfig(captured)
	if err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	s.cfg = cfg
	s.setAppIdentity(cfg)
	s.setCompiling(true, "")
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "running"}, "info", "build started", map[string]any{"initial": initial})
	s.console.Event("process.compile-start", map[string]any{"initial": initial})
	// The running host keeps serving until a complete candidate replaces it.
	generation, metadata, apiEncoding, err := s.startHost(ctx, initial, s.stopHost)
	if err != nil {
		s.cfg, s.env = previousConfig, previousEnvironment
		s.setAppIdentity(previousConfig)
		s.mu.Lock()
		// A predecessor still serving keeps the restart pending, so the next
		// change retries it instead of reaching the host.
		s.restartPending = s.host != nil
		s.mu.Unlock()
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	monitor, stopMonitor := context.WithCancel(context.WithoutCancel(ctx))
	generation.monitor, generation.watched = stopMonitor, make(chan struct{})
	s.mu.Lock()
	s.buildFailed = false
	s.restartPending = false
	s.current = generation.app
	s.host = generation
	s.mu.Unlock()
	s.recordAppliedConfig(generation.config, &devActiveGeneration{manifest: generation.manifest})
	s.setCompiling(false, "")
	s.setRunning(generation.app.pid, metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "ready"}, "info", "build succeeded", map[string]any{"initial": initial, "pid": generation.app.pid})
	s.console.Event("process/start", map[string]any{"pid": generation.app.pid, "listen_addr": s.addr, "initial": initial})
	if err := s.console.Phase("Publishing application process identity", func() error {
		s.updateAgentSession(ctx, "running", generation.app.pid)
		return nil
	}); err != nil {
		return err
	}
	if initial {
		s.console.Banner(s.runURLs())
	}
	go s.monitorHost(monitor, generation)
	return nil
}

// startHost prepares the capabilities, database and workers of the current
// application and starts its development host, calling replace to stop its
// predecessor only once the candidate is complete. It returns once the host
// has published its first generation and accepts connections.
func (s *devSupervisor) startHost(ctx context.Context, initial bool, replace func()) (_ *devHostGeneration, metadata, apiEncoding []byte, returnErr error) {
	var err error
	if err := s.console.Phase("Verifying framework source and producer", func() error {
		ctx, err = build.VerifyFrameworkSessionForBuild(ctx, s.root, runningFrameworkVersion())
		return err
	}); err != nil {
		return nil, nil, nil, err
	}
	if initial {
		if err := s.waitForStartupReady(ctx); err != nil {
			return nil, nil, nil, err
		}
	}
	var contract *compiler.Result
	if err := s.console.Phase("Building scenery application graph", func() error {
		contract, err = compiler.CompileContext(ctx, s.root)
		if err == nil && !contract.Valid() {
			err = invalidGraphError(contract)
		}
		return err
	}); err != nil {
		return nil, nil, nil, err
	}
	metadata, apiEncoding, err = buildDevMetadataFromResult(contract)
	if err != nil {
		return nil, nil, nil, err
	}
	if s.currentPID() == "" {
		s.setMetadata(metadata, apiEncoding)
	}
	// Stage assistant overlays concurrently with database setup and Go worker
	// compilation; staging only writes candidate-private trees and never mutates
	// active descriptors until PrepareFromStage activates it.
	var (
		preStage     *assistantStage
		preStageDone chan struct{}
	)
	if s.assistants != nil && len(assistantDefinitionsFromResult(contract, s.root)) > 0 {
		preStageDone = make(chan struct{})
		go func() {
			defer close(preStageDone)
			preStage, _ = s.assistants.stage(ctx, contract)
		}()
		defer func() {
			<-preStageDone
			if preStage != nil {
				s.assistants.releaseStage(preStage)
			}
		}()
	}
	var environment *devRuntimeEnvironment
	if err := s.console.Phase("Resolving database and storage capabilities", func() error {
		environment, err = s.prepareRuntimeEnvironment(ctx, contract)
		if err != nil {
			return err
		}
		// Every worker launch inherits this process's environment.
		processEnv := append(append([]string(nil), environment.managed...), environment.storage...)
		processEnv = append(processEnv, s.observabilityEnvironment()...)
		return setProcessEnvironment(processEnv)
	}); err != nil {
		return nil, metadata, apiEncoding, err
	}
	if initial {
		postgresStart, err := s.beginRetainedPostgresStart(ctx, contract)
		if err != nil {
			return nil, metadata, apiEncoding, err
		}
		err = postgresStart.wait()
		postgresStart.release()
		if err != nil {
			return nil, metadata, apiEncoding, err
		}
	}
	setup, run, err := s.nextDevDatabaseSetup(initial, s.readDevDatabaseSetupInputs(contract))
	if err != nil {
		return nil, metadata, apiEncoding, err
	}
	if run {
		if err := s.console.Phase("Running database setup", func() error {
			return s.runDevDatabaseSetup(ctx, setup, contract, environment)
		}); err != nil {
			return nil, metadata, apiEncoding, err
		}
	}
	reports, err := newWorkerReportListener(s.activeAppID(), s.currentSessionID())
	if err != nil {
		return nil, metadata, apiEncoding, err
	}
	generation := &devHostGeneration{}
	defer func() {
		if returnErr != nil {
			generation.release()
			if generation.launch == nil {
				_ = reports.Close()
			}
		}
	}()
	var (
		dev    *workerDevelopment
		bundle workerBundle
	)
	if err := s.console.Phase("Compiling application workers", func() error {
		scope := &workerInputScope{root: s.root}
		if scope.generated, err = compiler.ManagedGeneratedRoots(contract); err != nil {
			return err
		}
		inputs, err := scope.capture()
		if err != nil {
			return err
		}
		if dev, bundle, err = startWorkerDevelopment(ctx, scope, inputs); err != nil {
			return err
		}
		if generation.config, err = s.resolveGenerationConfig(ctx, dev.compiled.Manifest); err != nil {
			return err
		}
		generation.manifest = dev.compiled.Manifest
		for _, program := range bundle.Programs {
			generation.services = append(generation.services, program.Service)
		}
		bundle.config, err = workerruntime.DecodeConfigSnapshot(generation.config.snapshotFor(applicationConsumer).data)
		return err
	}); err != nil {
		return nil, metadata, apiEncoding, err
	}
	options := workerHostOptions{Root: s.root, Environment: s.env.Name, SessionID: s.currentSessionID(), Victoria: s.victoriaEndpoints()}
	if s.assistants != nil && len(bundle.Host.Assistants) > 0 {
		var staged *assistantStage
		if preStageDone != nil {
			<-preStageDone
			staged, preStage = preStage, nil
		}
		if err := s.assistants.PrepareFromStage(ctx, dev.compiled, staged); err != nil {
			return nil, metadata, apiEncoding, err
		}
		s.refreshAssistantRuntimeConfig()
		options.Assistants = workerAssistantProcess{RuntimePath: s.assistantRuntimeConfigPath(), KeyPath: s.assistantTokenKeyPath}
	}
	environmentVariables := s.hostEnvironment(contract, environment)
	if generation.lease, err = storageTaskLease(ctx, environmentVariables); err != nil {
		return nil, metadata, apiEncoding, err
	}
	if held, ok := generation.lease.(interface{ File() *os.File }); ok {
		options.Held = append(options.Held, held.File())
	} else if generation.lease != nil {
		return nil, metadata, apiEncoding, fmt.Errorf("storage lease does not support host handoff")
	}
	if generation.launch, err = prepareWorkerHost(ctx, bundle, dev, reports, options); err != nil {
		return nil, metadata, apiEncoding, err
	}
	launch := generation.launch
	request := s.hostStartRequest(ctx, launch.Executable, environmentVariables)
	request.Args = append(append([]string(nil), launch.Args...), s.addr)
	request.Configure = func(command *exec.Cmd) { command.ExtraFiles = launch.Files }
	replace()
	var process *devManagedProcess
	if err := s.console.Phase("Starting application host", func() error {
		process, err = startDevManagedProcess(s.ctx, request)
		if err != nil {
			return err
		}
		generation.app = &runningApp{process: process, pid: strconv.Itoa(process.PID)}
		return waitForHostReady(ctx, s.root, launch.Directory, process, &generation.endpoint)
	}); err != nil {
		if generation.app != nil {
			_ = generation.app.stop()
		}
		return nil, metadata, apiEncoding, err
	}
	if options.Assistants.RuntimePath != "" {
		if err := s.assistants.StartPrepared(ctx); err != nil {
			s.eventSink().Emit(ctx, devdash.DevSource{ID: "assistant-runtime", Kind: "assistant", Name: "assistant-runtime", Status: "error"}, "error", "assistant helpers unavailable", map[string]any{"error": err.Error()})
		}
	}
	return generation, metadata, apiEncoding, nil
}

// waitForHostReady waits until the host bound its public listener after its
// first publication and advertised its control endpoint, or until it exits.
func waitForHostReady(ctx context.Context, root, directory string, process *devManagedProcess, endpoint *workerDevEndpoint) error {
	ctx, cancel := context.WithTimeout(ctx, hostReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, "ready")); err == nil {
			found, ok, err := readWorkerDevEndpoint(root)
			if err != nil {
				return err
			}
			if ok {
				*endpoint = found
				return nil
			}
		}
		select {
		case <-process.Done:
			return fmt.Errorf("application host exited during startup: %w\n%s", process.WaitError(), process.Tail.String())
		case <-ctx.Done():
			return fmt.Errorf("application host did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// monitorHost follows one host generation: its rebuild outcomes become
// status, events and console output, and its exit ends the generation.
// It runs until ctx ends or the host exits.
func (s *devSupervisor) monitorHost(ctx context.Context, generation *devHostGeneration) {
	defer close(generation.watched)
	ticker := time.NewTicker(hostStatusPolling)
	defer ticker.Stop()
	var reported string
	for {
		select {
		case <-ctx.Done():
			return
		case <-generation.app.process.Done:
			s.handleExit(ctx, generation.app)
			return
		case <-ticker.C:
		}
		var status runtimeSourceWait
		probe, stop := context.WithTimeout(ctx, time.Second)
		err := readWorkerSourceStatus(probe, generation.endpoint, 0, &status)
		stop()
		if err != nil {
			continue
		}
		s.mu.Lock()
		generation.status = status
		s.mu.Unlock()
		key := status.CompileError
		if status.Serving != nil {
			key += "\x00" + strconv.FormatUint(status.Serving.Generation, 10)
		}
		if key == reported {
			continue
		}
		first := reported == ""
		reported = key
		if status.CompileError != "" {
			s.reportHostBuildFailure(ctx, status.CompileError)
			continue
		}
		if !first {
			s.setCompiling(false, "")
			_ = s.persistStatus(ctx)
			s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "ready"}, "info", "build succeeded", map[string]any{"rebuild_timing": status.RebuildTiming})
			s.console.Event("process/reload", map[string]any{"pid": generation.app.pid, "listen_addr": s.addr})
		}
	}
}

// reportHostBuildFailure records a failed host rebuild. The previous
// generation keeps serving; a change the host cannot apply restarts it.
func (s *devSupervisor) reportHostBuildFailure(ctx context.Context, message string) {
	s.mu.Lock()
	s.buildFailed = true
	s.mu.Unlock()
	s.setCompiling(false, message)
	_ = s.persistStatus(ctx)
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "error", Reason: message}, "error", "build failed", map[string]any{"error": message})
	if strings.Contains(message, hostRestartRequired) {
		s.requestHostRestart()
		return
	}
	s.console.RebuildFailed(&cliDiagnosticError{code: 3, diagnostic: graph.Diagnostic{Code: "SCN6163", Severity: "error", Message: boundedOutput(message)}})
}

// serviceProcessStatuses projects the worker services of the running host.
func (s *devSupervisor) serviceProcessStatuses() []devdash.ServiceProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.host == nil {
		return nil
	}
	var generation uint64
	if s.host.status.Serving != nil {
		generation = s.host.status.Serving.Generation
	}
	statuses := make([]devdash.ServiceProcess, 0, len(s.host.services))
	for _, service := range s.host.services {
		statuses = append(statuses, devdash.ServiceProcess{Name: service, Generation: generation, State: "running"})
	}
	return statuses
}

// requestHostRestart asks the watch loop to replace the running host.
func (s *devSupervisor) requestHostRestart() {
	s.mu.Lock()
	s.hostRestart = true
	s.mu.Unlock()
	select {
	case s.rebuildRequests <- struct{}{}:
	default:
	}
}

// hostOwnsChanges reports whether a running host rebuilds source edits
// itself, and consumes a requested restart. A restart that failed while its
// predecessor kept serving stays pending until one succeeds.
func (s *devSupervisor) hostOwnsChanges() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hostRestart {
		s.hostRestart = false
		return false
	}
	return s.host != nil && !s.restartPending
}

// stopHost stops the running host generation, if any.
func (s *devSupervisor) stopHost() {
	s.mu.Lock()
	generation := s.host
	s.host = nil
	if generation != nil && s.current == generation.app {
		s.current = nil
	}
	s.mu.Unlock()
	if generation == nil {
		return
	}
	if generation.monitor != nil {
		generation.monitor()
		<-generation.watched
	}
	_ = generation.app.stop()
	generation.release()
}

// release closes what a generation holds once its host is gone.
func (generation *devHostGeneration) release() {
	if generation == nil {
		return
	}
	if generation.lease != nil {
		_ = generation.lease.Close()
		generation.lease = nil
	}
	generation.launch.Close()
}

// hostEnvironment is the complete environment of a development host serving
// the supervisor's API backend.
func (s *devSupervisor) hostEnvironment(contract *compiler.Result, environment *devRuntimeEnvironment) []string {
	env := appChildEnv(
		minimalAppProcessEnv(s.appDatabaseAuthorityEnv(environment.base, contract.SQLRequirements)),
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_LISTEN_NETWORK="+s.backend.Network,
		"SCENERY_LISTEN_ADDR="+s.addr,
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_ENV="+s.env.Name,
		"SCENERY_RUNTIME_ENV="+s.env.Name,
	)
	env = append(env, s.sessionIdentityEnv()...)
	// Worker failure reports reach the host's own report listener.
	for _, key := range []string{"SCENERY_DEV_REPORT_URL", "SCENERY_DEV_REPORT_TOKEN"} {
		env = append(env, key+"="+envpolicy.Get(key))
	}
	env = append(env, environment.managed...)
	env = append(env, environment.storage...)
	env = append(env, s.observabilityEnvironment()...)
	env = append(env, s.sessionAuthEnv()...)
	return env
}

// victoriaEndpoints are the OTLP endpoints the host exports telemetry to.
func (s *devSupervisor) victoriaEndpoints() map[string]string {
	if s.victoriaPlanned != nil {
		return s.victoriaPlanned
	}
	s.mu.RLock()
	stack := s.victoria
	s.mu.RUnlock()
	endpoints := map[string]string{}
	if stack == nil {
		return endpoints
	}
	for _, signal := range []string{"logs", "metrics", "traces"} {
		if endpoint := stack.Endpoint(signal); endpoint != "" {
			endpoints[signal] = endpoint
		}
	}
	return endpoints
}

func (s *devSupervisor) hostStartRequest(ctx context.Context, executable string, env []string) devProcessStartRequest {
	const name = "app"
	return devProcessStartRequest{
		Name: name, Kind: "app", Role: "host", Dir: s.root, Command: executable, Env: env,
		Stdout: s.processOutputWriter(os.Stdout), Stderr: s.processOutputWriter(os.Stderr), Filter: s.processOutputFilter,
		OnOutput: func(pid int, stream string, data []byte) {
			source := devdash.DevSource{ID: name, Kind: "app", Name: name, Role: "host", PID: strconv.Itoa(pid), Stream: stream, Status: "running"}
			s.eventSink().Output(ctx, source, data)
		},
	}
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
