package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envfile"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
	"scenery.sh/internal/model"
	sceneryruntime "scenery.sh/runtime"
)

type runningApp struct {
	process  *devManagedProcess
	cmd      *exec.Cmd
	done     chan error
	buildDir string
	pid      string
	output   *safeLineTail
}

type devSupervisor struct {
	ctx     context.Context
	cancel  context.CancelFunc
	root    string
	cfg     app.Config
	backend devBackend
	addr    string

	store        *devdash.Store
	dashboard    *dashboardServer
	proxy        *localproxy.Proxy
	temporal     *temporalDevServer
	victoria     *victoriaStack
	grafana      *grafanaComponent
	electric     *managedElectricService
	reportToken  string
	console      *runConsole
	agent        *localagent.Client
	agentSession *localagent.Session
	events       *devEventSink

	closeOnce          sync.Once
	mu                 sync.RWMutex
	current            *runningApp
	typescript         *runningTypeScriptWorker
	status             devdash.AppRecord
	pendingDevEvents   []devdash.DevEvent
	victoriaStarted    bool
	dbSetupFingerprint string
}

const (
	appStartupTimeout      = 30 * time.Second
	appStartupPollInterval = 10 * time.Millisecond
)

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func newDevSupervisor(ctx context.Context, root string, cfg app.Config, backend devBackend, verbose, jsonMode bool) (*devSupervisor, error) {
	supervisorCtx, cancel := context.WithCancel(ctx)
	backend = backend.normalized()
	store, err := openDevdashStore()
	if err != nil {
		cancel()
		return nil, err
	}
	token, err := randomToken()
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, err
	}
	appID := cfg.AppID()

	s := &devSupervisor{
		ctx:         supervisorCtx,
		cancel:      cancel,
		root:        root,
		cfg:         cfg,
		backend:     backend,
		addr:        backend.Addr,
		store:       store,
		reportToken: token,
		console:     newRunConsole(os.Stdout, os.Stderr, verbose, jsonMode, appID, root),
		status: devdash.AppRecord{
			ID:         appID,
			Name:       cfg.Name,
			Root:       root,
			ListenAddr: backend.Addr,
			Offline:    true,
			UpdatedAt:  time.Now().UTC(),
		},
	}
	uiDir, err := prepareDashboardUIDir(supervisorCtx, s.console)
	if err != nil {
		cancel()
		_ = store.Close()
		return nil, err
	}
	s.dashboard = newDashboardServer(s, uiDir)
	s.events = newDevEventSink(s)
	return s, nil
}

func (s *devSupervisor) eventSink() *devEventSink {
	if s == nil {
		return nil
	}
	if s.events == nil {
		s.events = newDevEventSink(s)
	}
	return s.events
}

func (s *devSupervisor) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}

		app := s.detachCurrentApp()
		typescript := s.detachTypeScriptWorker()
		victoria := s.victoria
		grafana := s.grafana
		temporal := s.temporal
		electric := s.electric

		var errs []error
		if app != nil {
			if err := app.interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if typescript != nil {
			if err := typescript.interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if grafana != nil {
			if err := grafana.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if temporal != nil {
			if err := temporal.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if electric != nil {
			if err := electric.Interrupt(); err != nil {
				errs = append(errs, err)
			}
		}

		type closer struct {
			name string
			fn   func() error
		}
		closers := []closer{}
		if s.dashboard != nil {
			closers = append(closers, closer{name: "dashboard", fn: s.dashboard.Close})
		}
		if s.proxy != nil {
			closers = append(closers, closer{name: "proxy", fn: s.proxy.Close})
		}

		if len(closers) > 0 {
			errCh := make(chan error, len(closers))
			var wg sync.WaitGroup
			for _, item := range closers {
				wg.Add(1)
				go func(fn func() error) {
					defer wg.Done()
					if err := fn(); err != nil {
						errCh <- err
					}
				}(item.fn)
			}
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				errs = append(errs, fmt.Errorf("timed out closing in-process dev services"))
			}
			close(errCh)
			for err := range errCh {
				errs = append(errs, err)
			}
		}

		if app != nil {
			if err := app.waitOrKill(stopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
		if typescript != nil {
			if err := typescript.waitOrKill(stopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		if grafana != nil {
			if err := grafana.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		if temporal != nil {
			if err := temporal.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}

		if s.store != nil {
			if err := s.store.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if session := s.currentAgentSession(); s.agent != nil && session != nil {
			if _, _, err := s.agent.DeleteOwnedSession(context.Background(), *session, false); err != nil {
				errs = append(errs, err)
			}
		}
		closeErr = errors.Join(errs...)
	})
	return closeErr
}

func (s *devSupervisor) Start(ctx context.Context) error {
	s.setSessionIdentity(s.currentAgentSession())
	s.updateAgentSession(ctx, "starting", "")
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "supervisor", Kind: "supervisor", Name: "supervisor", Status: "starting"}, "info", "dev supervisor starting", map[string]any{
		"listen_addr":    s.addr,
		"listen_network": s.backend.Network,
	})
	if s.console != nil {
		s.console.Event("run.start", map[string]any{
			"listen_addr":    s.addr,
			"listen_network": s.backend.Network,
		})
	}
	if err := ensureSceneryLocalStateIgnored(s.root); err != nil {
		return err
	}
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	if s.agent == nil || localproxy.Enabled() {
		if err := s.dashboard.Start(ctx); err != nil {
			return err
		}
	}
	if err := s.ensureManagedElectric(ctx); err != nil {
		return err
	}
	if err := s.ensureTemporalDevServer(ctx); err != nil {
		return err
	}
	victoria := s.startVictoriaStack(ctx)
	s.mu.Lock()
	s.victoria = victoria
	s.victoriaStarted = true
	pendingDevEvents := append([]devdash.DevEvent(nil), s.pendingDevEvents...)
	s.pendingDevEvents = nil
	s.mu.Unlock()
	if s.victoria != nil {
		for _, event := range pendingDevEvents {
			s.eventSink().ExportVictoriaDevEvent(event)
		}
		s.eventSink().Emit(ctx, devdash.DevSource{ID: "victoria", Kind: "substrate", Name: "victoria", Role: "observability", Status: "running"}, "info", "Victoria stack ready", map[string]any{
			"urls": s.victoria.URLs(),
		})
	}
	if err := s.startGrafana(s.ctx); err != nil {
		return err
	}
	if err := s.startLocalProxy(); err != nil {
		slog.Warn("local HTTPS proxy unavailable", "err", err)
	}
	return nil
}

func (s *devSupervisor) startVictoriaStack(ctx context.Context) *victoriaStack {
	if s == nil || s.agent == nil {
		return startVictoriaStack(s.ctx, s.root, s.console)
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnVictoria(s.console, "agent Victoria state path unavailable: %v", err)
		return startVictoriaStack(s.ctx, s.root, s.console)
	}
	adapter := victoriaSubstrateAdapter{console: s.console}
	handle, _, err := s.substrateManager().Ensure(ctx, filepath.Join(paths.AgentDir, "victoria"), adapter)
	stack, _ := handle.(*victoriaStack)
	if err != nil {
		warnVictoria(s.console, "failed to prepare shared Victoria substrate with agent: %v", err)
		return stack
	}
	if stack == nil {
		return nil
	}
	s.substrateManager().Monitor(stack, adapter)
	if s.console != nil && s.console.verbose {
		s.console.Event("victoria.shared", map[string]any{
			"owner":     "agent",
			"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
		})
	}
	return stack
}

func (s *devSupervisor) substrateManager() managedSubstrateManager {
	if s == nil {
		return managedSubstrateManager{}
	}
	return managedSubstrateManager{agent: s.agent, events: s.eventSink()}
}

func (s *devSupervisor) agentVictoriaStack(ctx context.Context) *victoriaStack {
	if s == nil || s.agent == nil {
		return nil
	}
	substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return nil
	}
	handle, reusable := s.substrateManager().reusable(ctx, victoriaSubstrateAdapter{console: s.console}, substrate)
	if !reusable {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateVictoria)
		return nil
	}
	stack, _ := handle.(*victoriaStack)
	if s.console != nil && s.console.verbose {
		s.console.Event("victoria.reuse", map[string]any{
			"owner":     "agent",
			"endpoints": substrate.Endpoints,
		})
	}
	return stack
}

func (s *devSupervisor) monitorSharedVictoriaStack(stack *victoriaStack) <-chan struct{} {
	return s.substrateManager().Monitor(stack, victoriaSubstrateAdapter{console: s.console})
}

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot fileSnapshot) error {
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
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "compile-start", s.appStatus())
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

	// Detach before stopping so the exit watchers treat this as an intentional
	// restart rather than a crash; otherwise handleExit races the restart and
	// can register the session as "stopped" after the new app is running.
	previous := s.detachCurrentApp()
	previousTS := s.detachTypeScriptWorker()
	var current *runningApp
	if err := s.console.Phase("Starting scenery application", func() error {
		if previous != nil {
			if err := previous.stop(); err != nil {
				return err
			}
		}
		if previousTS != nil {
			if err := previousTS.stop(); err != nil {
				return err
			}
		}
		current, err = s.startApp(ctx, plan.Result, plan.Metadata, plan.APIEncoding)
		if err != nil {
			return err
		}
		if plan.TypeScript != nil {
			if _, err = s.startTypeScriptWorker(ctx, *plan.TypeScript); err != nil {
				_ = current.stop()
				return err
			}
		}
		return nil
	}); err != nil {
		return s.handleCompileError(ctx, plan.Metadata, plan.APIEncoding, err)
	}

	s.mu.Lock()
	s.current = current
	s.mu.Unlock()

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
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), method, s.appStatus())
	if s.console != nil {
		s.console.Event(method, map[string]any{
			"pid":         current.pid,
			"listen_addr": s.addr,
			"initial":     initial,
		})
	}
	s.updateAgentSession(ctx, "running", current.pid)
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
		return app.Config{}, fmt.Errorf(".scenery.json moved from %s to %s", s.root, root)
	}
	return cfg, nil
}

func effectiveDevConfigForModel(cfg app.Config, appModel *model.App) app.Config {
	if !cfg.Temporal.Enabled || !codegen.AppUsesTemporalRuntime(appModel) {
		return cfg
	}
	if strings.TrimSpace(cfg.Temporal.Mode) == "" {
		cfg.Temporal.Mode = "local"
	}
	cfg.Temporal.Local.AutoStart = true
	return cfg
}

func (s *devSupervisor) startApp(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage) (*runningApp, error) {
	agentSession := s.currentAgentSession()
	binary := result.Binary
	if sessionBinary, err := prepareSessionAppBinary(agentSession, result.Binary); err != nil {
		return nil, err
	} else if sessionBinary != "" {
		binary = sessionBinary
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return nil, err
	}
	appBaseEnv := s.appDatabaseAuthorityEnv(baseEnv)
	env := appChildEnv(
		appBaseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_LISTEN_NETWORK="+s.backend.Network,
		"SCENERY_LISTEN_ADDR="+s.addr,
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_DEV_SUPERVISOR=1",
		"SCENERY_DEV_ENDPOINTS=1",
		fmt.Sprintf("SCENERY_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"SCENERY_LOCAL_PROXY=0",
		"SCENERY_DEV_REPORT_URL="+s.devReportURL(),
		"SCENERY_DEV_REPORT_TOKEN="+s.reportToken,
	)
	env = append(env, s.victoria.Env()...)
	env = append(env, s.temporal.Env()...)
	env = append(env, s.sessionTemporalEnv()...)
	env = append(env, s.sessionIdentityEnv()...)
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return nil, err
	}
	env = append(env, managedEnv...)
	electricEnv, err := managedElectricEnv(s.cfg, agentSession, env)
	if err != nil {
		return nil, err
	}
	env = append(env, electricEnv...)
	if s.proxy != nil {
		env = append(env, "SCENERY_PUBLIC_BASE_URL="+s.proxy.Routes().APIURL)
	} else if agentSession != nil && agentSession.Routes[localagent.RouteAPI] != "" {
		env = append(env, "SCENERY_PUBLIC_BASE_URL="+agentSession.Routes[localagent.RouteAPI])
	}
	env = append(env, s.sessionAuthEnv()...)
	if err := backendAvailableBeforeStartup(s.backend); err != nil {
		return nil, fmt.Errorf("app listen address %s is unavailable before startup: %w", s.addr, err)
	}
	process, err := startDevManagedProcess(ctx, devProcessStartRequest{
		Name:    "api",
		Kind:    "app",
		Role:    "scenery-api",
		Dir:     s.root,
		Command: binary,
		Env:     env,
		Stdout:  s.processOutputWriter(os.Stdout),
		Stderr:  s.processOutputWriter(os.Stderr),
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
	})
	if err != nil {
		return nil, err
	}
	app := &runningApp{
		process:  process,
		cmd:      process.Cmd,
		buildDir: result.Dir,
		pid:      fmt.Sprintf("%d", process.PID),
		output:   process.Tail,
	}
	go func() {
		<-process.done
		s.handleExit(context.Background(), app)
	}()
	if err := s.waitForAppStartup(ctx, app); err != nil {
		_ = app.stop()
		return nil, err
	}
	return app, nil
}

func prepareSessionAppBinary(session *localagent.Session, binary string) (string, error) {
	if session == nil || strings.TrimSpace(session.StateRoot) == "" || strings.TrimSpace(binary) == "" {
		return "", nil
	}
	dir := filepath.Join(session.StateRoot, "run", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(binary)
	if strings.TrimSpace(name) == "" || name == "." || name == string(filepath.Separator) {
		name = "scenery-app"
	}
	if !strings.HasPrefix(name, "scenery-app") {
		name = "scenery-app-" + name
	}
	target := filepath.Join(dir, name)
	_ = os.Remove(target)
	if err := os.Symlink(binary, target); err == nil {
		return target, nil
	} else {
		linkErr := err
		if err := os.Link(binary, target); err == nil {
			return target, nil
		} else if copyErr := copySessionAppBinary(binary, target); copyErr == nil {
			return target, nil
		} else {
			return "", fmt.Errorf("prepare session app binary %s: symlink: %v; hardlink/copy: %w", target, linkErr, copyErr)
		}
	}
}

func copySessionAppBinary(source, target string) error {
	stat, err := os.Stat(source)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, stat.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return closeErr
	}
	return nil
}

func (s *devSupervisor) runDevSetup(ctx context.Context) error {
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	appBaseEnv := s.appDatabaseAuthorityEnv(baseEnv)
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return err
	}
	env := appChildEnv(
		appBaseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_DEV_SUPERVISOR=1",
	)
	env = append(env, managedEnv...)
	for _, command := range s.cfg.Dev.Setup {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		cmd.Dir = s.root
		cmd.Env = env
		stdout := newSetupOutputWriter(s.console, "stdout", os.Stdout)
		stderr := newSetupOutputWriter(s.console, "stderr", os.Stderr)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			stdout.Close()
			stderr.Close()
			return fmt.Errorf("dev.setup %q failed: %w", command, err)
		}
		stdout.Close()
		stderr.Close()
	}
	return nil
}

type devDatabaseSetup struct {
	Fingerprint string
	Seeds       []dbSeedPlan
}

func (s *devSupervisor) nextDevDatabaseSetup(initial bool) (devDatabaseSetup, bool, error) {
	setup, hasWork, err := buildDevDatabaseSetup(s.root, s.cfg)
	if err != nil || !hasWork {
		return setup, false, err
	}
	if !initial && setup.Fingerprint == s.dbSetupFingerprint {
		if s.console != nil && s.console.verbose {
			s.console.Event("database.setup.skip", map[string]any{
				"reason": "unchanged-inputs",
			})
		}
		return setup, false, nil
	}
	return setup, true, nil
}

func buildDevDatabaseSetup(root string, cfg app.Config) (devDatabaseSetup, bool, error) {
	var inputs []string
	applyCommand := strings.TrimSpace(cfg.Database.Apply.Command)
	if applyCommand != "" {
		data, err := json.Marshal(cfg.Database.Apply)
		if err != nil {
			return devDatabaseSetup{}, false, err
		}
		inputs = append(inputs, "apply:"+string(data))
	}
	seeds, err := discoverDBSeedPlans(root, cfg)
	if err != nil {
		return devDatabaseSetup{}, false, err
	}
	for _, seed := range seeds {
		inputs = append(inputs, "seed:"+seed.Path+":"+seed.SHA256)
	}
	if len(inputs) == 0 {
		return devDatabaseSetup{}, false, nil
	}
	sort.Strings(inputs)
	sum := sha256.Sum256([]byte(strings.Join(inputs, "\n")))
	return devDatabaseSetup{
		Fingerprint: hex.EncodeToString(sum[:]),
		Seeds:       seeds,
	}, true, nil
}

func (s *devSupervisor) runDevDatabaseSetup(ctx context.Context, setup devDatabaseSetup) error {
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	appBaseEnv := s.appDatabaseAuthorityEnv(baseEnv)
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return err
	}
	env := appChildEnv(
		appBaseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"SCENERY_APP_ID="+s.activeAppID(),
		"SCENERY_APP_ROOT="+s.root,
		"SCENERY_DEV_SUPERVISOR=1",
	)
	env = append(env, managedEnv...)
	env = append(env, managedDatabaseSetupEnv(s.cfg, managedEnv)...)
	source := devdash.DevSource{ID: "database-setup", Kind: "setup", Name: "database setup", Role: "database", Status: "running"}
	s.eventSink().Emit(ctx, source, "info", "database setup started", map[string]any{
		"seed_count": len(setup.Seeds),
	})
	if err := runDatabaseApplyProviderWithEnv(ctx, s.root, s.cfg.Database.Apply, env); err != nil {
		source.Status = "error"
		s.eventSink().Emit(ctx, source, "error", "database apply failed", map[string]any{
			"error": err.Error(),
		})
		return err
	}
	seedResult, err := buildDBSeedResultWithEnv(ctx, s.root, s.cfg, dbSeedOptions{}, env, false)
	if err != nil {
		source.Status = "error"
		s.eventSink().Emit(ctx, source, "error", "database seed failed", map[string]any{
			"error": err.Error(),
		})
		return err
	}
	source.Status = "ready"
	s.eventSink().Emit(ctx, source, "info", "database setup completed", map[string]any{
		"seeds": seedResult.Summary,
	})
	s.dbSetupFingerprint = setup.Fingerprint
	return nil
}

func managedDatabaseSetupEnv(cfg app.Config, managedEnv []string) []string {
	if _, svc, ok := managedPostgresDeclared(cfg); !ok || !postgresServiceUsesBranching(svc) {
		return nil
	}
	if value, _ := lookupEnvValue(managedEnv, appDatabaseURLEnv); value != "" {
		return nil
	}
	if value, _ := lookupEnvValue(managedEnv, "SCENERY_MANAGED_DATABASE_URL"); value != "" {
		return []string{appDatabaseURLEnv + "=" + value}
	}
	return nil
}

func (s *devSupervisor) managedAppEnv(ctx context.Context, baseEnv []string) ([]string, error) {
	if _, _, ok := managedPostgresDeclared(s.cfg); ok && managedPostgresUsesExternalDatabase(baseEnv) {
		if _, err := externalPostgresDatabaseURL(baseEnv); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if _, svc, ok := managedPostgresDeclared(s.cfg); ok && postgresServiceUsesBranching(svc) {
		env, resolution, connection, err := dbBranchManagedPostgresEnv(ctx, s.root, s.cfg, s.currentAgentSession())
		status := "running"
		message := "database branch lease ready"
		if err != nil {
			status = "pending"
			message = "database branch lease resolved"
		}
		provider := branchProviderNameForConfig(s.cfg)
		s.eventSink().Emit(ctx, devdash.DevSource{ID: provider, Kind: "substrate", Name: provider, Role: "database", Status: status}, "info", message, map[string]any{
			"branch":  resolution.Pin.Branch,
			"source":  resolution.Source,
			"created": resolution.Created,
			"host":    connection.Endpoint.Host,
			"port":    connection.Endpoint.Port,
		})
		if err != nil {
			return nil, fmt.Errorf("dev.services.postgres kind %q resolved branch %q, but branch connection is not ready: %w", svc.Kind, resolution.Pin.Branch, err)
		}
		return env, nil
	}
	env, err := managedPostgresEnv(ctx, s.cfg, s.currentAgentSession(), baseEnv, s.agent)
	if err != nil {
		return nil, err
	}
	if dbName := envValueFromList(env, "SCENERY_MANAGED_DATABASE_NAME"); dbName != "" {
		s.eventSink().Emit(ctx, devdash.DevSource{ID: "postgres", Kind: "substrate", Name: "postgres", Role: "database", Status: "running"}, "info", "managed Postgres ready", map[string]any{
			"database": dbName,
		})
	}
	return env, nil
}

func (s *devSupervisor) appDatabaseAuthorityEnv(baseEnv []string) []string {
	if s == nil {
		return baseEnv
	}
	if _, _, ok := managedPostgresDeclared(s.cfg); !ok {
		return baseEnv
	}
	if managedPostgresUsesExternalDatabase(baseEnv) {
		return envWithoutKeys(baseEnv, legacyDatabaseURLEnv)
	}
	keys := []string{appDatabaseURLEnv, legacyDatabaseURLEnv}
	if _, svc, ok := managedPostgresDeclared(s.cfg); ok && postgresServiceUsesBranching(svc) {
		if envName := dbDatabaseURLEnv(s.cfg); envName != appDatabaseURLEnv {
			keys = append(keys, envName)
		}
	}
	return envWithoutKeys(baseEnv, keys...)
}

func (s *devSupervisor) sessionIdentityEnv() []string {
	session := s.currentAgentSession()
	if session == nil {
		return nil
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" {
		runtimeAppID = baseAppID
	}
	return []string{
		"SCENERY_SESSION_ID=" + strings.TrimSpace(session.SessionID),
		"SCENERY_BASE_APP_ID=" + baseAppID,
		"SCENERY_RUNTIME_APP_ID=" + runtimeAppID,
		"SCENERY_APP_ROOT_HASH=" + appRootHash(session.AppRoot),
		"SCENERY_BRANCH=" + strings.TrimSpace(session.Branch),
		"SCENERY_WORKTREE=" + appWorktreeName(session.AppRoot),
	}
}

func appRootHash(root string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:12]
}

func appWorktreeName(root string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return ""
	}
	return filepath.Base(root)
}

func (s *devSupervisor) devReportURL() string {
	if s != nil && s.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if health, err := s.agent.Health(ctx); err == nil {
			if rawURL := reportURLForBackend(health.DashboardBackend); rawURL != "" {
				return rawURL
			}
		}
		if session := s.currentAgentSession(); session != nil {
			if rawURL := reportURLForBackend(session.Backends[localagent.RouteDashboard]); rawURL != "" {
				return rawURL
			}
		}
	}
	return "http://" + devdash.ListenAddr() + devdash.ReportPath
}

func reportURLForBackend(backend localagent.Backend) string {
	network := strings.TrimSpace(backend.Network)
	addr := strings.TrimSpace(backend.Addr)
	if addr == "" || (network != "" && network != "tcp") {
		return ""
	}
	return "http://" + addr + devdash.ReportPath
}

func (s *devSupervisor) sessionTemporalEnv() []string {
	if s == nil || !s.cfg.Temporal.Enabled {
		return nil
	}
	session := s.currentAgentSession()
	if session == nil {
		return nil
	}
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID == "" {
		return nil
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	prefix := "scenery." + baseAppID + "." + sessionID
	deploymentName := sceneryruntime.TemporalDeploymentName(sceneryruntime.TemporalRuntimeInfo{
		DeploymentName: prefix,
	})
	return []string{
		sceneryruntime.DefaultTemporalTaskQueueEnv + "=" + prefix,
		sceneryruntime.DefaultTemporalDeploymentEnv + "=" + deploymentName,
		sceneryruntime.DefaultTemporalBuildIDEnv + "=" + sessionID,
	}
}

func (s *devSupervisor) sessionAuthEnv() []string {
	if s == nil || !s.cfg.Auth.Enabled {
		return nil
	}
	apiURL := strings.TrimSpace(s.apiURL())
	if apiURL == "" {
		return nil
	}
	publicAppURL := apiURL
	frontends := s.frontendURLs()
	if len(frontends) > 0 {
		names := make([]string, 0, len(frontends))
		for name := range frontends {
			names = append(names, name)
		}
		sort.Strings(names)
		if value := strings.TrimSpace(frontends[names[0]]); value != "" {
			publicAppURL = value
		}
	}
	return []string{
		authEnvName(s.cfg.Auth.APIBaseURLEnv, "APIBaseURL") + "=" + apiURL,
		"API_BASE_URL=" + apiURL,
		"SCENERY_API_BASE_URL=" + apiURL,
		authEnvName(s.cfg.Auth.PublicAppURLEnv, "PublicAppURL") + "=" + publicAppURL,
		"PUBLIC_APP_URL=" + publicAppURL,
		"SCENERY_PUBLIC_APP_URL=" + publicAppURL,
		authEnvName(s.cfg.Auth.AuthCookieDomainEnv, "AuthCookieDomain") + "=",
		"AUTH_COOKIE_DOMAIN=",
		"SCENERY_AUTH_COOKIE_DOMAIN=",
	}
}

func authEnvName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (s *devSupervisor) waitForAppStartup(ctx context.Context, app *runningApp) error {
	if app == nil || app.process == nil {
		deadline := time.NewTimer(appStartupTimeout)
		defer deadline.Stop()
		ticker := time.NewTicker(appStartupPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = app.stop()
				return ctx.Err()
			case err, ok := <-app.done:
				if !ok {
					return appStartupExitError(app, nil)
				}
				return appStartupExitError(app, err)
			case <-ticker.C:
				if backendAcceptsConnections(s.backend) {
					return nil
				}
			case <-deadline.C:
				_ = app.stop()
				return fmt.Errorf("scenery app did not listen on %s address %s within %s", s.backend.Network, s.addr, appStartupTimeout)
			}
		}
	}
	return app.process.WaitReady(ctx, devProcessReadyRequest{
		Timeout:  appStartupTimeout,
		Interval: appStartupPollInterval,
		Probe: func(context.Context) error {
			if backendAcceptsConnections(s.backend) {
				return nil
			}
			return fmt.Errorf("scenery app is not accepting %s connections on %s", s.backend.Network, s.addr)
		},
	})
}

func appStartupExitError(app *runningApp, err error) error {
	message := "scenery app exited during startup"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += ": process exited without an error"
	}
	if app != nil && app.output != nil {
		if output := strings.TrimSpace(app.output.String()); output != "" {
			message += "\n" + output
		}
	}
	return errors.New(message)
}

func backendAcceptsConnections(backend devBackend) bool {
	backend = backend.normalized()
	target := backend.Addr
	if backend.Network == "tcp" {
		if host, port, err := net.SplitHostPort(backend.Addr); err == nil && host == "" {
			target = net.JoinHostPort("127.0.0.1", port)
		}
	}
	conn, err := net.DialTimeout(backend.Network, target, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func backendAvailableBeforeStartup(backend devBackend) error {
	backend = backend.normalized()
	if backend.Network == "unix" {
		return nil
	}
	return portAvailable(backend.Addr)
}

func tcpAddrAcceptsConnections(addr string) bool {
	target := addr
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		target = net.JoinHostPort("127.0.0.1", port)
	}
	conn, err := net.DialTimeout("tcp", target, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *devSupervisor) captureOutput(ctx context.Context, app *runningApp, stream string, src io.Reader, dst io.Writer) {
	var tail *safeLineTail
	reader := bufio.NewReader(src)
	pid := ""
	if app != nil {
		pid = app.pid
		tail = app.output
	}
	source := devdash.DevSource{
		ID:     "api",
		Kind:   "app",
		Name:   "api",
		Role:   "scenery-api",
		PID:    pid,
		Stream: stream,
		Status: "running",
	}
	s.captureServiceOutput(ctx, source, tail, reader, dst)
}

func (s *devSupervisor) captureProcessOutput(ctx context.Context, pid, stream string, tail *safeLineTail, reader *bufio.Reader, dst io.Writer) {
	source := devdash.DevSource{
		ID:     "process:" + strings.TrimSpace(pid),
		Kind:   "process",
		Name:   "process",
		PID:    pid,
		Stream: stream,
		Status: "running",
	}
	if strings.TrimSpace(pid) == "" {
		source.ID = "process"
	}
	s.captureServiceOutput(ctx, source, tail, reader, dst)
}

func (s *devSupervisor) captureServiceOutput(ctx context.Context, source devdash.DevSource, tail *safeLineTail, reader *bufio.Reader, dst io.Writer) {
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if s.console == nil || !s.console.json {
				_, _ = dst.Write(chunk)
			}
			plain := stripANSI(chunk)
			if tail != nil {
				tail.Add(strings.TrimRight(string(plain), "\n"))
			}
			s.eventSink().Output(ctx, source, plain)
		}
		if err != nil {
			if !isExpectedOutputReadError(err) {
				fmt.Fprintf(os.Stderr, "scenery: failed reading %s: %v\n", source.Stream, err)
			}
			return
		}
	}
}

func (s *devSupervisor) processOutputWriter(dst io.Writer) io.Writer {
	if s == nil || s.console == nil || !s.console.json {
		return dst
	}
	return nil
}

func isExpectedOutputReadError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed)
}

func appChildEnv(base []string, forceColor bool, vars ...string) []string {
	env := append(append([]string(nil), base...), vars...)
	if forceColor {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	return env
}

func envWithoutKeys(base []string, keys ...string) []string {
	if len(keys) == 0 {
		return append([]string(nil), base...)
	}
	omit := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		omit[key] = struct{}{}
	}
	env := make([]string, 0, len(base))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, drop := omit[key]; drop {
				continue
			}
		}
		env = append(env, item)
	}
	return env
}

func appEnvWithDotEnv(base []string, root string, names ...string) ([]string, error) {
	if len(names) == 0 {
		names = []string{".env"}
	}
	values, err := envfile.MergeFiles(root, names...)
	if err != nil {
		return nil, err
	}
	return envfile.AppendMissing(base, values), nil
}

func appEnvWithRequiredDotEnv(base []string, root string, names ...string) ([]string, error) {
	if err := requireDotEnv(root); err != nil {
		return nil, err
	}
	return appEnvWithDotEnv(base, root, names...)
}

func stripANSI(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	clean := ansiEscapeRE.ReplaceAll(data, nil)
	return append([]byte(nil), clean...)
}

func (s *devSupervisor) handleExit(ctx context.Context, app *runningApp) {
	if app == nil {
		return
	}
	s.mu.Lock()
	if s.current == nil || s.current.pid != app.pid {
		s.mu.Unlock()
		return
	}
	s.current = nil
	s.status.Running = false
	s.status.Offline = true
	s.status.PID = ""
	s.status.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()

	_ = s.persistStatus(ctx)
	s.dashboard.notify(&devdash.Notification{
		Method: "process/stop",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "process-stop", s.appStatus())
	if s.console != nil {
		s.console.Event("process.stop", map[string]any{
			"pid": app.pid,
		})
	}
	s.updateAgentSession(ctx, "stopped", "")
}

func (a *runningApp) interrupt() error {
	if a != nil && a.process != nil {
		return a.process.Interrupt()
	}
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(a.cmd)
}

func (a *runningApp) kill() error {
	if a != nil && a.process != nil {
		if a.process.Cmd != nil {
			return killProcessTree(a.process.Cmd)
		}
		return nil
	}
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return killProcessTree(a.cmd)
}

func (a *runningApp) waitOrKill(grace time.Duration) error {
	if a == nil {
		return nil
	}
	if a.process != nil {
		return a.process.WaitOrKill(grace)
	}
	select {
	case err := <-a.done:
		if err == nil || isExpectedExit(err) {
			return nil
		}
		return err
	case <-time.After(grace):
		_ = a.kill()
		select {
		case err := <-a.done:
			if err == nil || isExpectedExit(err) {
				return nil
			}
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("app did not exit after SIGKILL")
		}
	}
}

func (a *runningApp) stop() error {
	if a != nil && a.process != nil {
		return a.process.Stop(stopTimeout)
	}
	if err := a.interrupt(); err != nil {
		return err
	}
	return a.waitOrKill(stopTimeout)
}

func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	_, ok := errors.AsType[*exec.ExitError](err)
	return ok
}

func validateLocalSecretsFiles(root string) error {
	if err := requireDotEnv(root); err != nil {
		return err
	}
	for _, name := range []string{".env", ".env.local"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := envfile.ParseFile(path); err != nil {
			return err
		}
	}
	return nil
}

func requireDotEnv(root string) error {
	path := filepath.Join(root, ".env")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("missing required local env file: %s\ncreate .env in the app root before starting scenery locally; process environment values may still override values from the file", path)
	}
	if err != nil {
		return fmt.Errorf("check required local env file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("required local env file is a directory: %s", path)
	}
	return nil
}

func (s *devSupervisor) persistStatus(ctx context.Context) error {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	return s.store.UpsertApp(ctx, status)
}

func (s *devSupervisor) setCompiling(compiling bool, compileErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Compiling = compiling
	s.status.CompileError = compileErr
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setMetadata(metadata, apiEncoding json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Metadata = metadata
	s.status.APIEncoding = apiEncoding
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setRunning(pid string, metadata, apiEncoding json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = true
	s.status.Offline = false
	s.status.PID = pid
	s.status.ListenAddr = s.addr
	s.status.Metadata = metadata
	s.status.APIEncoding = apiEncoding
	s.status.CompileError = ""
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) setSessionIdentity(session *localagent.Session) {
	if s == nil || session == nil {
		return
	}
	baseAppID := strings.TrimSpace(session.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	runtimeAppID := strings.TrimSpace(session.RuntimeAppID)
	if runtimeAppID == "" {
		runtimeAppID = baseAppID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.BaseAppID = baseAppID
	s.status.RuntimeAppID = runtimeAppID
	s.status.SessionID = strings.TrimSpace(session.SessionID)
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) currentSessionID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status.SessionID
}

// currentAgentSession returns the latest agent session snapshot. Stored
// sessions are immutable: writers register a refreshed session with the agent
// and publish it via storeAgentSession instead of mutating fields in place.
func (s *devSupervisor) currentAgentSession() *localagent.Session {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agentSession
}

func (s *devSupervisor) storeAgentSession(session *localagent.Session) {
	if s == nil || session == nil {
		return
	}
	s.mu.Lock()
	s.agentSession = session
	s.mu.Unlock()
	s.setSessionIdentity(session)
}

func (s *devSupervisor) currentElectric() *managedElectricService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.electric
}

func (s *devSupervisor) setGrafanaState(state devdash.GrafanaState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Grafana = encodeGrafanaState(state)
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) currentApp() *runningApp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *devSupervisor) detachCurrentApp() *runningApp {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current
	s.current = nil
	return current
}

func (s *devSupervisor) currentTypeScriptWorker() *runningTypeScriptWorker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.typescript
}

func (s *devSupervisor) detachTypeScriptWorker() *runningTypeScriptWorker {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.typescript
	s.typescript = nil
	return current
}

func (s *devSupervisor) announceRebuild(paths []string) {
	if s.console != nil {
		s.console.RebuildDetected(paths)
	}
}

func (s *devSupervisor) apiURL() string {
	if s.proxy != nil && s.proxy.Routes().APIURL != "" {
		return s.proxy.Routes().APIURL
	}
	if session := s.currentAgentSession(); session != nil && session.Routes[localagent.RouteAPI] != "" {
		return session.Routes[localagent.RouteAPI]
	}
	return "http://" + s.addr
}

func (s *devSupervisor) dashboardURL() string {
	if s.proxy != nil && s.proxy.Routes().ConsoleURL != "" {
		return localproxy.ConsoleAppURL(s.proxy.Routes(), s.activeAppID())
	}
	if session := s.currentAgentSession(); session != nil && session.Routes[localagent.RouteDashboard] != "" {
		return session.Routes[localagent.RouteDashboard]
	}
	return "http://" + devdash.ListenAddr() + "/" + url.PathEscape(s.activeAppID())
}

func (s *devSupervisor) temporalURL() string {
	if s.proxy != nil && s.proxy.Routes().TemporalURL != "" {
		return s.proxy.Routes().TemporalURL
	}
	if session := s.currentAgentSession(); session != nil && session.Routes[localagent.RouteTemporal] != "" {
		return session.Routes[localagent.RouteTemporal]
	}
	return s.temporal.URL()
}

func (s *devSupervisor) grafanaUpstream() string {
	if s == nil || s.grafana == nil {
		return ""
	}
	return s.grafana.URL()
}

func (s *devSupervisor) frontendURLs() map[string]string {
	if s.proxy != nil {
		return frontendURLs(s.proxy.Routes())
	}
	if session := s.currentAgentSession(); session != nil {
		return frontendURLsFromAgentRoutes(session.Routes, s.cfg.Proxy.Frontends)
	}
	return nil
}

func (s *devSupervisor) ensureTemporalDevServer(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.temporal != nil {
		if s.temporal.Reachable(ctx, s.cfg.Name, s.cfg.Temporal) {
			return nil
		}
		s.temporal = nil
		if s.agent != nil {
			_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateTemporal)
		}
	}
	var (
		temporal *temporalDevServer
		err      error
	)
	if s.agent != nil {
		temporal, err = s.ensureAgentTemporalDevServer(ctx)
	} else {
		temporal, err = startTemporalDevServer(ctx, s.root, s.cfg, s.console)
	}
	if err != nil {
		return err
	}
	s.temporal = temporal
	if temporal != nil {
		s.registerAgentSessionBackend(ctx, localagent.RouteTemporal, backendFromHTTPURL(temporal.URL()))
		s.eventSink().Emit(ctx, devdash.DevSource{ID: "temporal", Kind: "substrate", Name: "temporal", Role: "workflow-server", Status: "running", URL: temporal.URL()}, "info", "Temporal dev server ready", map[string]any{
			"address":   temporal.info.Address,
			"namespace": temporal.info.Namespace,
			"ui_url":    temporal.URL(),
		})
	}
	return nil
}

func (s *devSupervisor) ensureAgentTemporalDevServer(ctx context.Context) (*temporalDevServer, error) {
	if s == nil || s.agent == nil {
		return startTemporalDevServer(ctx, s.root, s.cfg, s.console)
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnTemporal(s.console, "agent Temporal state path unavailable: %v", err)
		return startTemporalDevServer(ctx, s.root, s.cfg, s.console)
	}
	adapter := temporalSubstrateAdapter{cfg: s.cfg, console: s.console}
	handle, _, err := s.substrateManager().Ensure(ctx, filepath.Join(paths.AgentDir, "temporal"), adapter)
	temporal, _ := handle.(*temporalDevServer)
	if err != nil {
		warnTemporal(s.console, "failed to prepare shared Temporal substrate with agent: %v", err)
		return temporal, err
	}
	if temporal == nil {
		return nil, nil
	}
	s.substrateManager().Monitor(temporal, adapter)
	if s.console != nil && s.console.verbose {
		s.console.Event("temporal.shared", map[string]any{
			"owner":     "agent",
			"address":   temporal.info.Address,
			"namespace": temporal.info.Namespace,
			"ui_url":    temporal.URL(),
		})
	}
	return temporal, err
}

func (s *devSupervisor) agentTemporalDevServer(ctx context.Context) *temporalDevServer {
	if s == nil || s.agent == nil {
		return nil
	}
	substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateTemporal)
	if err != nil {
		return nil
	}
	handle, reusable := s.substrateManager().reusable(ctx, temporalSubstrateAdapter{cfg: s.cfg, console: s.console}, substrate)
	if !reusable {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateTemporal)
		return nil
	}
	temporal, _ := handle.(*temporalDevServer)
	if s.console != nil && s.console.verbose {
		s.console.Event("temporal.reuse", map[string]any{
			"owner":     "agent",
			"address":   temporal.info.Address,
			"namespace": temporal.info.Namespace,
			"ui_url":    temporal.URL(),
		})
	}
	return temporal
}

func (s *devSupervisor) monitorSharedTemporalDevServer(temporal *temporalDevServer) <-chan struct{} {
	return s.substrateManager().Monitor(temporal, temporalSubstrateAdapter{cfg: s.cfg, console: s.console})
}

func substrateExitEventFields(exit localagent.SubstrateExit) map[string]any {
	fields := map[string]any{
		"component":       exit.Component,
		"pid":             exit.PID,
		"started_at":      exit.StartedAt,
		"exited_at":       exit.ExitedAt,
		"exit_code":       exit.ExitCode,
		"log_path":        exit.LogPath,
		"stdout_log_path": exit.StdoutLogPath,
		"stderr_log_path": exit.StderrLogPath,
	}
	if exit.Signal != "" {
		fields["signal"] = exit.Signal
	}
	if exit.Error != "" {
		fields["error"] = exit.Error
	}
	return fields
}

func (s *devSupervisor) startGrafana(ctx context.Context) error {
	if s == nil || s.grafana != nil {
		return nil
	}
	var (
		grafana *grafanaComponent
		err     error
	)
	if s.agent != nil {
		grafana, err = s.startAgentGrafana(ctx)
	} else {
		grafana, err = startGrafanaForDev(ctx, s.root, s.victoria, s.plannedGrafanaPublicURL(), s.console)
	}
	if grafana != nil {
		s.grafana = grafana
		state := grafana.State()
		if s.registerAgentSessionBackend(ctx, localagent.RouteGrafana, backendFromHTTPURL(grafana.URL())) {
			if session := s.currentAgentSession(); session != nil {
				if route := strings.TrimSpace(session.Routes[localagent.RouteGrafana]); route != "" {
					state = grafanaStateWithBaseURL(state, route)
				}
			}
		}
		s.setGrafanaState(state)
		_ = s.persistStatus(ctx)
		if s.dashboard != nil {
			s.dashboard.notify(&devdash.Notification{
				Method: "grafana/status",
				Params: state,
			})
		}
		s.eventSink().Emit(ctx, devdash.DevSource{ID: "grafana", Kind: "substrate", Name: "grafana", Role: "observability-ui", Status: state.Status, URL: state.URL}, "info", "Grafana status updated", map[string]any{
			"available": state.Available,
			"status":    state.Status,
			"url":       state.URL,
		})
	}
	return err
}

func (s *devSupervisor) registerAgentSessionBackend(ctx context.Context, route string, backend localagent.Backend) bool {
	if s == nil || s.agent == nil {
		return false
	}
	route = strings.TrimSpace(route)
	backend.Network = strings.TrimSpace(backend.Network)
	backend.Addr = strings.TrimSpace(backend.Addr)
	session := s.currentAgentSession()
	if session == nil || route == "" || backend.Addr == "" {
		return false
	}
	if backend.Network == "" {
		backend.Network = "tcp"
	}
	backends := copyManagedBackends(session.Backends)
	backends[route] = backend
	updated, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.activeAppID(),
		AppRoot:     s.root,
		SessionID:   session.SessionID,
		Branch:      session.Branch,
		Status:      firstNonEmpty(session.Status, "starting"),
		OwnerPID:    os.Getpid(),
		AppPID:      session.AppPID,
		Backends:    backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		slog.Warn("failed to register scenery agent session backend", "route", route, "err", err)
		return false
	}
	s.storeAgentSession(&updated)
	return true
}

func backendFromHTTPURL(raw string) localagent.Backend {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return localagent.Backend{}
	}
	return localagent.Backend{Network: "tcp", Addr: parsed.Host}
}

func (s *devSupervisor) startAgentGrafana(ctx context.Context) (*grafanaComponent, error) {
	if s == nil || s.agent == nil {
		return startGrafanaForDev(ctx, s.root, s.victoria, s.plannedGrafanaPublicURL(), s.console)
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnGrafana(s.console, "agent Grafana state path unavailable: %v", err)
		return startGrafanaForDev(ctx, s.root, s.victoria, s.plannedGrafanaPublicURL(), s.console)
	}
	adapter := grafanaSubstrateAdapter{victoria: s.victoria, console: s.console}
	handle, _, err := s.substrateManager().Ensure(ctx, filepath.Join(paths.AgentDir, "grafana"), adapter)
	grafana, _ := handle.(*grafanaComponent)
	if grafana == nil {
		return grafana, err
	}
	if !grafana.State().Available {
		return grafana, err
	}
	s.substrateManager().Monitor(grafana, adapter)
	if s.console != nil && s.console.verbose {
		s.console.Event("grafana.shared", map[string]any{
			"owner": "agent",
			"url":   grafana.URL(),
		})
	}
	return grafana, err
}

func (s *devSupervisor) agentGrafana(ctx context.Context) *grafanaComponent {
	if s == nil || s.agent == nil {
		return nil
	}
	substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateGrafana)
	if err != nil {
		return nil
	}
	handle, reusable := s.substrateManager().reusable(ctx, grafanaSubstrateAdapter{victoria: s.victoria, console: s.console}, substrate)
	if !reusable {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateGrafana)
		return nil
	}
	grafana, _ := handle.(*grafanaComponent)
	if s.console != nil && s.console.verbose {
		s.console.Event("grafana.reuse", map[string]any{
			"owner": "agent",
			"url":   grafana.URL(),
		})
	}
	return grafana
}

func (s *devSupervisor) plannedGrafanaPublicURL() string {
	if s == nil || !localproxy.Enabled() {
		return ""
	}
	workspace := s.cfg.Proxy.Workspace
	if workspace == "" {
		workspace = localproxy.DiscoverWorkspace(s.root, s.activeAppID())
	}
	proxyCfg := localproxy.BuildConfig(localproxy.Config{
		Workspace:         workspace,
		APIHost:           s.cfg.Proxy.APIHost,
		ConsoleHost:       s.cfg.Proxy.ConsoleHost,
		TemporalHost:      s.cfg.Proxy.TemporalHost,
		GrafanaHost:       s.cfg.Proxy.GrafanaHost,
		APIUpstream:       s.addr,
		DashboardUpstream: devdash.ListenAddr(),
		TemporalUpstream:  temporalUIUpstreamForConfig(s.cfg),
		GrafanaUpstream:   fmt.Sprintf("%s:%d", grafanaDefaultHost, grafanaDefaultPort),
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return ""
	}
	return localproxy.PreviewRoutes(proxyCfg).GrafanaURL
}

func (s *devSupervisor) activeAppID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeAppIDLocked()
}

// activeAppIDLocked requires s.mu to be held. The s.cfg fallback only applies
// before setAppIdentity has run, when no other goroutines are active.
func (s *devSupervisor) activeAppIDLocked() string {
	if s.status.ID != "" {
		return s.status.ID
	}
	return s.cfg.AppID()
}

func (s *devSupervisor) dashboardActiveAppID() string {
	return s.activeAppID()
}

func (s *devSupervisor) dashboardCurrentSessionID() string {
	return s.currentSessionID()
}

func (s *devSupervisor) dashboardListApps(ctx context.Context) ([]map[string]any, error) {
	return s.listApps(ctx)
}

func (s *devSupervisor) dashboardStatusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	return s.statusFor(ctx, appID)
}

func (s *devSupervisor) dashboardStore() *devdash.Store {
	return s.store
}

func (s *devSupervisor) dashboardAuthorizeReport(req *http.Request, report devdash.ReportEnvelope) dashboardReportAuth {
	if report.SessionID != "" && report.SessionID != s.currentSessionID() {
		return dashboardReportAuth{Reason: "stale-session"}
	}
	if req.Header.Get("Authorization") != "Bearer "+s.reportToken {
		return dashboardReportAuth{Reason: "invalid-report-token"}
	}
	return dashboardReportAuth{Authorized: true}
}

func (s *devSupervisor) dashboardRootForApp(ctx context.Context, appID string) (string, error) {
	status, err := s.statusFor(ctx, firstNonEmpty(appID, s.activeAppID()))
	if err != nil {
		return "", err
	}
	return status.AppRoot, nil
}

func (s *devSupervisor) dashboardVictoria() dashboardVictoria {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	victoria := s.victoria
	s.mu.RUnlock()
	if victoria == nil {
		return nil
	}
	return victoria
}

func (s *devSupervisor) setAppIdentity(cfg app.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.ID = cfg.AppID()
	s.status.Name = cfg.Name
	s.status.UpdatedAt = time.Now().UTC()
}

func (s *devSupervisor) startLocalProxy() error {
	if !localproxy.Enabled() {
		return nil
	}
	workspace := s.cfg.Proxy.Workspace
	if workspace == "" {
		workspace = localproxy.DiscoverWorkspace(s.root, s.activeAppID())
	}
	proxyCfg := localproxy.BuildConfig(localproxy.Config{
		Workspace:         workspace,
		APIHost:           s.cfg.Proxy.APIHost,
		ConsoleHost:       s.cfg.Proxy.ConsoleHost,
		TemporalHost:      s.cfg.Proxy.TemporalHost,
		GrafanaHost:       s.cfg.Proxy.GrafanaHost,
		APIUpstream:       s.addr,
		DashboardUpstream: devdash.ListenAddr(),
		TemporalUpstream:  temporalUIUpstreamForConfig(s.cfg),
		GrafanaUpstream:   s.grafanaUpstream(),
		Frontends:         localproxy.ResolveFrontends(s.root, localProxyFrontends(s.cfg.Proxy.Frontends)),
		Verbose:           s.console != nil && s.console.verbose,
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return nil
	}
	proxy, err := localproxy.Start(proxyCfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.proxy = proxy
	s.mu.Unlock()
	if s.grafana != nil && proxy.Routes().GrafanaURL != "" {
		state := grafanaStateWithBaseURL(s.grafana.State(), proxy.Routes().GrafanaURL)
		s.setGrafanaState(state)
		s.dashboard.notify(&devdash.Notification{
			Method: "grafana/status",
			Params: state,
		})
	}
	return nil
}

func localProxyFrontends(frontends map[string]app.FrontendConfig) []localproxy.FrontendConfig {
	names := make([]string, 0, len(frontends))
	for name := range frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make([]localproxy.FrontendConfig, 0, len(names))
	for _, name := range names {
		frontend := frontends[name]
		resolved = append(resolved, localproxy.FrontendConfig{
			Name:                name,
			Host:                frontend.Host,
			Root:                frontend.Root,
			Upstream:            frontend.Upstream,
			AllowSharedUpstream: frontend.AllowSharedUpstream,
		})
	}
	return resolved
}

func frontendURLs(routes localproxy.Routes) map[string]string {
	if len(routes.Frontends) == 0 {
		return nil
	}
	names := make([]string, 0, len(routes.Frontends))
	for name := range routes.Frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	urls := make(map[string]string, len(names))
	for _, name := range names {
		urls[name] = routes.Frontends[name].URL
	}
	return urls
}

func frontendURLsFromAgentRoutes(routes map[string]string, frontends map[string]app.FrontendConfig) map[string]string {
	if len(routes) == 0 {
		return nil
	}
	names := make([]string, 0, len(frontends))
	if len(frontends) > 0 {
		for name := range frontends {
			value := strings.TrimSpace(routes[name])
			if value == "" {
				continue
			}
			names = append(names, name)
		}
	} else {
		for name, value := range routes {
			switch name {
			case localagent.RouteAPI, localagent.RouteDashboard, localagent.RouteGrafana, localagent.RouteTemporal:
				continue
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	urls := make(map[string]string, len(names))
	for _, name := range names {
		urls[name] = routes[name]
	}
	return urls
}

func (s *devSupervisor) runURLs() runURLs {
	return runURLs{
		API:       s.apiURL(),
		Dashboard: s.dashboardURL(),
		Frontends: s.frontendURLs(),
		Temporal:  s.temporalURL(),
		Victoria:  s.victoria.URLs(),
		Grafana:   s.appStatus().Grafana,
	}
}

func appendURLQuery(rawURL, key, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		return rawURL + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	values := parsed.Query()
	values.Set(key, value)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func (s *devSupervisor) handleCompileError(ctx context.Context, metadata, apiEncoding json.RawMessage, err error) error {
	s.setCompiling(false, err.Error())
	if len(metadata) > 0 || len(apiEncoding) > 0 {
		s.setMetadata(metadata, apiEncoding)
	}
	_ = s.persistStatus(ctx)
	s.dashboard.notify(&devdash.Notification{
		Method: "process/compile-error",
		Params: s.appStatus(),
	})
	_ = s.store.WriteProcessEvent(ctx, s.activeAppID(), "compile-error", map[string]any{"error": err.Error()})
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "error", Reason: err.Error()}, "error", "build failed", map[string]any{
		"error": err.Error(),
	})
	if s.console != nil {
		s.console.Event("process.compile-error", map[string]any{
			"error": err.Error(),
		})
	}
	s.updateAgentSession(ctx, "compile-error", "")
	return err
}

func (s *devSupervisor) appStatus() devdash.AppStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return devdash.AppStatus{
		Running:      s.status.Running,
		AppID:        s.status.ID,
		BaseAppID:    s.status.BaseAppID,
		RuntimeAppID: s.status.RuntimeAppID,
		SessionID:    s.status.SessionID,
		AppRoot:      s.status.Root,
		PID:          s.status.PID,
		Meta:         s.status.Metadata,
		Addr:         s.status.ListenAddr,
		APIEncoding:  s.status.APIEncoding,
		Grafana:      decodeGrafanaState(s.status.Grafana),
		Routes:       s.statusDashboardRoutesLocked(s.status.SessionID),
		Aliases:      s.statusDashboardAliasesLocked(s.status.SessionID),
		Compiling:    s.status.Compiling,
		CompileError: s.status.CompileError,
	}
}

func (s *devSupervisor) listApps(ctx context.Context) ([]map[string]any, error) {
	appID := s.activeAppID()
	if appID == "" {
		return []map[string]any{}, nil
	}
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		status := s.appStatus()
		return []map[string]any{{
			"id":           status.AppID,
			"name":         status.AppID,
			"app_root":     status.AppRoot,
			"session_id":   status.SessionID,
			"offline":      !status.Running,
			"compileError": status.CompileError,
		}}, nil
	}
	return []map[string]any{{
		"id":           app.ID,
		"name":         app.Name,
		"app_root":     app.Root,
		"session_id":   app.SessionID,
		"offline":      !app.Running,
		"compileError": app.CompileError,
	}}, nil
}

func (s *devSupervisor) statusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	if appID == "" {
		appID = s.activeAppID()
	}
	app, err := s.store.GetApp(ctx, appID)
	if err != nil {
		app, err = s.store.GetAppSession(ctx, appID)
		if err != nil {
			return devdash.AppStatus{}, err
		}
	}
	routeID := firstNonEmpty(app.RouteID, app.ID)
	s.mu.RLock()
	routes := s.statusDashboardRoutesLocked(app.SessionID)
	aliases := s.statusDashboardAliasesLocked(app.SessionID)
	s.mu.RUnlock()
	return devdash.AppStatus{
		Running:      app.Running,
		AppID:        routeID,
		BaseAppID:    app.BaseAppID,
		RuntimeAppID: app.RuntimeAppID,
		SessionID:    app.SessionID,
		AppRoot:      app.Root,
		PID:          app.PID,
		Meta:         app.Metadata,
		Addr:         app.ListenAddr,
		APIEncoding:  app.APIEncoding,
		Grafana:      decodeGrafanaState(app.Grafana),
		Routes:       routes,
		Aliases:      aliases,
		Compiling:    app.Compiling,
		CompileError: app.CompileError,
	}, nil
}

// statusDashboardRoutesLocked requires s.mu to be held.
func (s *devSupervisor) statusDashboardRoutesLocked(sessionID string) map[string]string {
	if s == nil {
		return nil
	}
	if s.agentSession != nil {
		currentSessionID := strings.TrimSpace(s.agentSession.SessionID)
		if sessionID == "" || sessionID == currentSessionID {
			if routes := visibleDashboardRoutesFromAgent(s.agentSession.Routes); len(routes) > 0 {
				return routes
			}
		}
	}
	if s.proxy != nil {
		if routes := visibleDashboardRoutesFromProxy(s.proxy.Routes(), s.activeAppIDLocked()); len(routes) > 0 {
			return routes
		}
	}
	return nil
}

// statusDashboardAliasesLocked requires s.mu to be held.
func (s *devSupervisor) statusDashboardAliasesLocked(sessionID string) map[string]string {
	if s == nil || s.agentSession == nil {
		return nil
	}
	currentSessionID := strings.TrimSpace(s.agentSession.SessionID)
	if sessionID != "" && sessionID != currentSessionID {
		return nil
	}
	return visibleDashboardRoutesFromAgent(s.agentSession.Aliases)
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func (s *devSupervisor) updateAgentSession(ctx context.Context, status, appPID string) {
	if s == nil || s.agent == nil {
		return
	}
	session := s.currentAgentSession()
	if session == nil {
		return
	}
	updated, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.activeAppID(),
		AppRoot:     s.root,
		SessionID:   session.SessionID,
		Branch:      session.Branch,
		Status:      status,
		OwnerPID:    os.Getpid(),
		AppPID:      appPID,
		Processes:   s.sessionProcessesFor(session, appPID),
		Backends:    session.Backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		slog.Warn("failed to update scenery agent session", "err", err)
		return
	}
	s.storeAgentSession(&updated)
}

func (s *devSupervisor) sessionProcessesFor(session *localagent.Session, appPID string) map[string]localagent.Process {
	if s == nil || session == nil {
		return nil
	}
	processes := copySessionProcesses(session.Processes)
	if pid := atoiPID(appPID); pid > 0 {
		processes[localagent.RouteAPI] = localagent.Process{PID: pid}
	}
	if worker := s.currentTypeScriptWorker(); worker != nil {
		if pid := atoiPID(worker.pid); pid > 0 {
			processes["worker-typescript"] = localagent.Process{PID: pid}
		}
	}
	if electric := s.currentElectric(); electric != nil {
		if pid := electric.PID(); pid > 0 {
			processes["electric"] = localagent.Process{PID: pid}
		}
	}
	if len(processes) == 0 {
		return nil
	}
	return processes
}

func copySessionProcesses(values map[string]localagent.Process) map[string]localagent.Process {
	if len(values) == 0 {
		return map[string]localagent.Process{}
	}
	copied := make(map[string]localagent.Process, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || value.PID <= 0 {
			continue
		}
		copied[key] = value
	}
	return copied
}

func portAvailable(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

func localPath(root, target string) (string, error) {
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("file path must be within the app root")
	}
	return target, nil
}
