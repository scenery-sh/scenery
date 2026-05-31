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

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/build"
	"github.com/pbrazdil/onlava/internal/codegen"
	"github.com/pbrazdil/onlava/internal/devdash"
	"github.com/pbrazdil/onlava/internal/devmeta"
	"github.com/pbrazdil/onlava/internal/envfile"
	"github.com/pbrazdil/onlava/internal/localproxy"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/parse"
	"github.com/pbrazdil/onlava/internal/workers"
	onlavaruntime "github.com/pbrazdil/onlava/runtime"
)

type runningApp struct {
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

	closeOnce  sync.Once
	mu         sync.RWMutex
	current    *runningApp
	typescript *runningTypeScriptWorker
	status     devdash.AppRecord
}

const (
	appStartupTimeout      = 30 * time.Second
	appStartupPollInterval = 50 * time.Millisecond
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
	return s, nil
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
		if s.agent != nil && s.agentSession != nil {
			if _, err := s.agent.Delete(context.Background(), s.agentSession.SessionID, false); err != nil {
				errs = append(errs, err)
			}
		}
		closeErr = errors.Join(errs...)
	})
	return closeErr
}

func (s *devSupervisor) Start(ctx context.Context) error {
	s.setSessionIdentity(s.agentSession)
	s.updateAgentSession(ctx, "starting", "")
	s.emitDevEvent(ctx, devdash.DevSource{ID: "supervisor", Kind: "supervisor", Name: "supervisor", Status: "starting"}, "info", "dev supervisor starting", map[string]any{
		"listen_addr":    s.addr,
		"listen_network": s.backend.Network,
	})
	if s.console != nil {
		s.console.Event("run.start", map[string]any{
			"listen_addr":    s.addr,
			"listen_network": s.backend.Network,
		})
	}
	if err := ensureOnlavaLocalStateIgnored(s.root); err != nil {
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
	s.victoria = s.startVictoriaStack(ctx)
	if s.victoria != nil {
		s.backfillVictoriaDevEvents(ctx)
		s.emitDevEvent(ctx, devdash.DevSource{ID: "victoria", Kind: "substrate", Name: "victoria", Role: "observability", Status: "running"}, "info", "Victoria stack ready", map[string]any{
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
	if stack := s.agentVictoriaStack(ctx); stack != nil {
		return stack
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnVictoria(s.console, "agent Victoria state path unavailable: %v", err)
		return startVictoriaStack(s.ctx, s.root, s.console)
	}
	stack := startVictoriaStackWithRoot(context.Background(), filepath.Join(paths.AgentDir, "victoria"), s.console)
	if stack == nil {
		return nil
	}
	if _, err := s.agent.UpsertSubstrate(ctx, stack.SubstrateRequest(os.Getpid())); err != nil {
		warnVictoria(s.console, "failed to register shared Victoria substrate with agent: %v", err)
		return stack
	}
	stack.MarkExternal()
	s.emitDevEvent(ctx, devdash.DevSource{ID: "victoria", Kind: "substrate", Name: "victoria", Role: "observability", Status: "running"}, "info", "shared Victoria stack ready", map[string]any{
		"owner":     "agent",
		"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
	})
	if s.console != nil && s.console.verbose {
		s.console.Event("victoria.shared", map[string]any{
			"owner":     "agent",
			"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
		})
	}
	return stack
}

func (s *devSupervisor) agentVictoriaStack(ctx context.Context) *victoriaStack {
	if s == nil || s.agent == nil {
		return nil
	}
	substrate, err := s.agent.GetSubstrate(ctx, localagent.SubstrateVictoria)
	if err != nil {
		return nil
	}
	if err := verifySubstrateOwner(substrate); err != nil {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateVictoria)
		return nil
	}
	stack := victoriaStackFromSubstrate(substrate)
	if stack == nil || !stack.Reachable() {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateVictoria)
		return nil
	}
	if s.console != nil && s.console.verbose {
		s.console.Event("victoria.reuse", map[string]any{
			"owner":     "agent",
			"endpoints": substrate.Endpoints,
		})
	}
	return stack
}

func (s *devSupervisor) RebuildAndRestart(ctx context.Context, initial bool, snapshot fileSnapshot, changedPaths []string) error {
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
	s.emitDevEvent(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "running"}, "info", "build started", map[string]any{
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

	var (
		model       *model.App
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		tsModel     workers.TypeScriptWorkerModel
		tsWorker    *workers.TypeScriptWorkerResult
		cached      *build.CachedGraph
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	if err := s.console.Phase("Building onlava application graph", func() error {
		cached, _, err = build.LoadCachedGraph(s.root, s.cfg.Name, graphFingerprint)
		if err != nil {
			return err
		}
		if cached != nil {
			metadata = append(json.RawMessage(nil), cached.Metadata...)
			apiEncoding = append(json.RawMessage(nil), cached.APIEncoding...)
			result = cached.Result
		}
		model, err = parse.App(s.root, s.cfg.Name)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		if cached != nil {
			return nil
		}
		metadata, err = devmeta.BuildMetadataSnapshot(model)
		if err != nil {
			return err
		}
		apiEncoding, err = devmeta.BuildAPIEncoding(model)
		return err
	}); err != nil {
		return s.handleCompileError(ctx, nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Validating TypeScript Temporal workers", func() error {
		tsModel = workers.DiscoverTypeScriptActivities(s.root)
		if diagnostics := workers.ValidateTypeScriptContracts(tsModel, temporalExternalActivityDeclarations(s.root, model), nativeGoTemporalDeclarations(s.root, model)); len(diagnostics) > 0 {
			return workers.DiagnosticsError(diagnostics)
		}
		return nil
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	s.cfg = effectiveDevConfigForModel(s.cfg, model)
	s.cfg = effectiveDevConfigForTypeScriptWorker(s.cfg, tsModel)
	if err := s.ensureManagedElectric(ctx); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.ensureTemporalDevServer(ctx); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			reused, refreshErr := build.RefreshCachedWorkspace(s.root, result)
			if refreshErr != nil {
				return refreshErr
			}
			if reused {
				return nil
			}
			model, err = parse.App(s.root, s.cfg.Name)
			if err != nil {
				return err
			}
			metadata, err = devmeta.BuildMetadataSnapshot(model)
			if err != nil {
				return err
			}
			apiEncoding, err = devmeta.BuildAPIEncoding(model)
			if err != nil {
				return err
			}
		}
		result, err = build.Prepare(s.root, model, s.cfg, build.PrepareOptions{ChangedPaths: changedPaths})
		if err == nil && result != nil {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return err
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Compiling application source code", func() error {
		if result != nil && result.GraphFingerprint == "" {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return build.CompileContext(ctx, result)
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}
	s.setMetadata(metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	if len(s.cfg.Dev.Setup) > 0 {
		if err := s.console.Phase("Running development setup", func() error {
			return s.runDevSetup(ctx)
		}); err != nil {
			return s.handleCompileError(ctx, metadata, apiEncoding, err)
		}
	}
	if typeScriptWorkerAutoStartEnabled(s.cfg, tsModel) {
		if err := s.console.Phase("Generating TypeScript Temporal worker", func() error {
			generated, generateErr := s.generateTypeScriptTemporalWorker()
			if generateErr != nil {
				return generateErr
			}
			tsWorker = generated
			return nil
		}); err != nil {
			return s.handleCompileError(ctx, metadata, apiEncoding, err)
		}
		if err := s.console.Phase("Installing TypeScript worker dependencies", func() error {
			_, installErr := ensureTypeScriptWorkerDependencies(ctx, tsWorker.OutputDir)
			return installErr
		}); err != nil {
			return s.handleCompileError(ctx, metadata, apiEncoding, err)
		}
	}

	previous := s.currentApp()
	previousTS := s.currentTypeScriptWorker()
	var current *runningApp
	var currentTS *runningTypeScriptWorker
	if err := s.console.Phase("Starting onlava application", func() error {
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
		current, err = s.startApp(ctx, result, metadata, apiEncoding)
		if err != nil {
			return err
		}
		if tsWorker != nil {
			currentTS, err = s.startTypeScriptWorker(ctx, *tsWorker)
			if err != nil {
				_ = current.stop()
				return err
			}
		}
		return nil
	}); err != nil {
		return s.handleCompileError(ctx, metadata, apiEncoding, err)
	}

	s.mu.Lock()
	s.current = current
	if currentTS == nil {
		s.typescript = nil
	}
	s.mu.Unlock()

	s.setCompiling(false, "")
	s.setRunning(current.pid, metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return err
	}
	s.emitDevEvent(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "ready"}, "info", "build succeeded", map[string]any{
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
		return app.Config{}, fmt.Errorf(".onlava.json moved from %s to %s", s.root, root)
	}
	return cfg, nil
}

func effectiveDevConfigForModel(cfg app.Config, appModel *model.App) app.Config {
	if !codegen.AppUsesTemporalRuntime(appModel) {
		return cfg
	}
	cfg.Temporal.Enabled = true
	if strings.TrimSpace(cfg.Temporal.Mode) == "" {
		cfg.Temporal.Mode = "local"
	}
	cfg.Temporal.Local.AutoStart = true
	return cfg
}

func (s *devSupervisor) startApp(ctx context.Context, result *build.Result, metadata, apiEncoding json.RawMessage) (*runningApp, error) {
	cmd := exec.Command(result.Binary)
	configureChildProcess(cmd)
	cmd.WaitDelay = stopTimeout + time.Second
	cmd.Dir = s.root
	baseEnv, err := appEnvWithDotEnv(os.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return nil, err
	}
	cmd.Env = appChildEnv(
		baseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"ONLAVA_LISTEN_NETWORK="+s.backend.Network,
		"ONLAVA_LISTEN_ADDR="+s.addr,
		"ONLAVA_APP_ID="+s.activeAppID(),
		"ONLAVA_APP_ROOT="+s.root,
		"ONLAVA_DEV_SUPERVISOR=1",
		"ONLAVA_DEV_ENDPOINTS=1",
		fmt.Sprintf("ONLAVA_DEV_SUPERVISOR_PID=%d", os.Getpid()),
		"ONLAVA_LOCAL_PROXY=0",
		"ONLAVA_DEV_REPORT_URL="+s.devReportURL(),
		"ONLAVA_DEV_REPORT_TOKEN="+s.reportToken,
	)
	cmd.Env = append(cmd.Env, s.victoria.Env()...)
	cmd.Env = append(cmd.Env, s.temporal.Env()...)
	cmd.Env = append(cmd.Env, s.sessionTemporalEnv()...)
	cmd.Env = append(cmd.Env, s.sessionIdentityEnv()...)
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, managedEnv...)
	electricEnv, err := managedElectricEnv(s.cfg, s.agentSession, cmd.Env)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, electricEnv...)
	if s.proxy != nil {
		cmd.Env = append(cmd.Env, "ONLAVA_PUBLIC_BASE_URL="+s.proxy.Routes().APIURL)
	} else if s.agentSession != nil && s.agentSession.Routes[localagent.RouteAPI] != "" {
		cmd.Env = append(cmd.Env, "ONLAVA_PUBLIC_BASE_URL="+s.agentSession.Routes[localagent.RouteAPI])
	}
	cmd.Env = append(cmd.Env, s.sessionAuthEnv()...)
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := backendAvailableBeforeStartup(s.backend); err != nil {
		return nil, fmt.Errorf("app listen address %s is unavailable before startup: %w", s.addr, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	app := &runningApp{
		cmd:      cmd,
		done:     make(chan error, 1),
		buildDir: result.Dir,
		pid:      fmt.Sprintf("%d", cmd.Process.Pid),
		output:   &safeLineTail{limit: 80},
	}

	go s.captureOutput(ctx, app, "stdout", stdout, os.Stdout)
	go s.captureOutput(ctx, app, "stderr", stderr, os.Stderr)
	go func() {
		app.done <- cmd.Wait()
		close(app.done)
		s.handleExit(context.Background(), app)
	}()
	if err := s.waitForAppStartup(ctx, app); err != nil {
		return nil, err
	}
	return app, nil
}

func (s *devSupervisor) runDevSetup(ctx context.Context) error {
	baseEnv, err := appEnvWithDotEnv(os.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	managedEnv, err := s.managedAppEnv(ctx, baseEnv)
	if err != nil {
		return err
	}
	env := appChildEnv(
		baseEnv,
		s.console != nil && s.console.palette.Enabled(),
		"ONLAVA_APP_ID="+s.activeAppID(),
		"ONLAVA_APP_ROOT="+s.root,
		"ONLAVA_DEV_SUPERVISOR=1",
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

func (s *devSupervisor) managedAppEnv(ctx context.Context, baseEnv []string) ([]string, error) {
	env, err := managedPostgresEnv(ctx, s.cfg, s.agentSession, baseEnv, s.agent)
	if err != nil {
		return nil, err
	}
	if dbName := envValueFromList(env, "ONLAVA_MANAGED_DATABASE_NAME"); dbName != "" {
		s.emitDevEvent(ctx, devdash.DevSource{ID: "postgres", Kind: "substrate", Name: "postgres", Role: "database", Status: "running"}, "info", "managed Postgres ready", map[string]any{
			"database": dbName,
		})
	}
	return env, nil
}

func (s *devSupervisor) sessionIdentityEnv() []string {
	if s == nil || s.agentSession == nil {
		return nil
	}
	baseAppID := strings.TrimSpace(s.agentSession.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	runtimeAppID := strings.TrimSpace(s.agentSession.RuntimeAppID)
	if runtimeAppID == "" {
		runtimeAppID = baseAppID
	}
	return []string{
		"ONLAVA_SESSION_ID=" + strings.TrimSpace(s.agentSession.SessionID),
		"ONLAVA_BASE_APP_ID=" + baseAppID,
		"ONLAVA_RUNTIME_APP_ID=" + runtimeAppID,
		"ONLAVA_APP_ROOT_HASH=" + appRootHash(s.agentSession.AppRoot),
		"ONLAVA_BRANCH=" + strings.TrimSpace(s.agentSession.Branch),
		"ONLAVA_WORKTREE=" + appWorktreeName(s.agentSession.AppRoot),
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
		if s.agentSession != nil {
			if rawURL := reportURLForBackend(s.agentSession.Backends[localagent.RouteDashboard]); rawURL != "" {
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
	if s == nil || s.agentSession == nil || !s.cfg.Temporal.Enabled {
		return nil
	}
	sessionID := strings.TrimSpace(s.agentSession.SessionID)
	if sessionID == "" {
		return nil
	}
	baseAppID := strings.TrimSpace(s.agentSession.BaseAppID)
	if baseAppID == "" {
		baseAppID = s.activeAppID()
	}
	prefix := "onlava." + baseAppID + "." + sessionID
	deploymentName := onlavaruntime.TemporalDeploymentName(onlavaruntime.TemporalRuntimeInfo{
		DeploymentName: prefix,
	})
	return []string{
		onlavaruntime.DefaultTemporalTaskQueueEnv + "=" + prefix,
		onlavaruntime.DefaultTemporalDeploymentEnv + "=" + deploymentName,
		onlavaruntime.DefaultTemporalBuildIDEnv + "=" + sessionID,
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
		"ONLAVA_API_BASE_URL=" + apiURL,
		authEnvName(s.cfg.Auth.PublicAppURLEnv, "PublicAppURL") + "=" + publicAppURL,
		"PUBLIC_APP_URL=" + publicAppURL,
		"ONLAVA_PUBLIC_APP_URL=" + publicAppURL,
		authEnvName(s.cfg.Auth.AuthCookieDomainEnv, "AuthCookieDomain") + "=",
		"AUTH_COOKIE_DOMAIN=",
		"ONLAVA_AUTH_COOKIE_DOMAIN=",
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
			return fmt.Errorf("onlava app did not listen on %s address %s within %s", s.backend.Network, s.addr, appStartupTimeout)
		}
	}
}

func appStartupExitError(app *runningApp, err error) error {
	message := "onlava app exited during startup"
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
		Role:   "onlava-api",
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
			output := devdash.ProcessOutput{
				AppID:     s.activeAppID(),
				SessionID: s.currentSessionID(),
				PID:       source.PID,
				Stream:    source.Stream,
				Output:    plain,
				CreatedAt: time.Now().UTC(),
			}
			_ = s.store.WriteProcessOutput(ctx, output)
			event := devdash.DevEventFromOutput(s.activeAppID(), s.currentSessionID(), source, plain, output.CreatedAt)
			if id, err := s.store.WriteDevEventReturningID(ctx, event); err == nil {
				event.ID = id
				s.exportVictoriaDevEvent(event)
			}
			s.dashboard.notify(&devdash.Notification{
				Method: "process/output",
				Params: map[string]any{
					"appID":      s.activeAppID(),
					"pid":        source.PID,
					"stream":     source.Stream,
					"source":     source,
					"output":     output.Output,
					"created_at": output.CreatedAt.Format(time.RFC3339Nano),
				},
			})
			if s.console != nil {
				s.console.Event("process.output", map[string]any{
					"pid":        source.PID,
					"stream":     source.Stream,
					"source":     source.ID,
					"output":     string(output.Output),
					"created_at": output.CreatedAt.Format(time.RFC3339Nano),
				})
			}
		}
		if err != nil {
			if !isExpectedOutputReadError(err) {
				fmt.Fprintf(os.Stderr, "onlava: failed reading %s: %v\n", source.Stream, err)
			}
			return
		}
	}
}

func (s *devSupervisor) emitDevEvent(ctx context.Context, source devdash.DevSource, level, message string, fields map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	event := devdash.NewDevEvent(s.activeAppID(), s.currentSessionID(), source, level, message, fields, time.Now().UTC())
	if id, err := s.store.WriteDevEventReturningID(ctx, event); err == nil {
		event.ID = id
		s.exportVictoriaDevEvent(event)
	}
}

func (s *devSupervisor) exportVictoriaDevEvent(event devdash.DevEvent) {
	if s == nil || s.victoria == nil {
		return
	}
	victoria := s.victoria
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = victoria.ExportDevEvent(ctx, event)
	}()
}

func (s *devSupervisor) backfillVictoriaDevEvents(ctx context.Context) {
	if s == nil || s.store == nil || s.victoria == nil {
		return
	}
	items, err := s.store.ListDevEvents(ctx, devdash.DevEventQuery{
		AppID:     s.activeAppID(),
		SessionID: s.currentSessionID(),
		Limit:     10000,
	})
	if err != nil || len(items) == 0 {
		return
	}
	victoria := s.victoria
	go func(events []devdash.DevEvent) {
		for _, event := range events {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = victoria.ExportDevEvent(ctx, event)
			cancel()
		}
	}(append([]devdash.DevEvent(nil), items...))
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
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return interruptProcessTree(a.cmd)
}

func (a *runningApp) kill() error {
	if a == nil || a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	return killProcessTree(a.cmd)
}

func (a *runningApp) waitOrKill(grace time.Duration) error {
	if a == nil {
		return nil
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
	if err := a.interrupt(); err != nil {
		return err
	}
	return a.waitOrKill(stopTimeout)
}

func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
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
		return fmt.Errorf("missing required local env file: %s\ncreate .env in the app root before starting onlava locally; process environment values may still override values from the file", path)
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
	if s.agentSession != nil && s.agentSession.Routes[localagent.RouteAPI] != "" {
		return s.agentSession.Routes[localagent.RouteAPI]
	}
	return "http://" + s.addr
}

func (s *devSupervisor) dashboardURL() string {
	if s.proxy != nil && s.proxy.Routes().ConsoleURL != "" {
		return localproxy.ConsoleAppURL(s.proxy.Routes(), s.activeAppID())
	}
	if s.agentSession != nil && s.agentSession.Routes[localagent.RouteDashboard] != "" {
		return s.agentSession.Routes[localagent.RouteDashboard]
	}
	return "http://" + devdash.ListenAddr() + "/" + url.PathEscape(s.activeAppID())
}

func (s *devSupervisor) mcpURL() string {
	if s.proxy != nil && s.proxy.Routes().MCPBaseURL != "" {
		return localproxy.MCPSSEURL(s.proxy.Routes(), s.activeAppID())
	}
	if s.agentSession != nil && s.agentSession.Routes[localagent.RouteMCP] != "" {
		return appendURLQuery(s.agentSession.Routes[localagent.RouteMCP], "appID", s.activeAppID())
	}
	return "http://" + devdash.ListenAddr() + "/sse?appID=" + url.QueryEscape(s.activeAppID())
}

func (s *devSupervisor) temporalURL() string {
	if s.proxy != nil && s.proxy.Routes().TemporalURL != "" {
		return s.proxy.Routes().TemporalURL
	}
	if s.agentSession != nil && s.agentSession.Routes[localagent.RouteTemporal] != "" {
		return s.agentSession.Routes[localagent.RouteTemporal]
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
	if s.agentSession != nil {
		return frontendURLsFromAgentRoutes(s.agentSession.Routes, s.cfg.Proxy.Frontends)
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
		s.emitDevEvent(ctx, devdash.DevSource{ID: "temporal", Kind: "substrate", Name: "temporal", Role: "workflow-server", Status: "running", URL: temporal.URL()}, "info", "Temporal dev server ready", map[string]any{
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
	if temporal := s.agentTemporalDevServer(ctx); temporal != nil {
		return temporal, nil
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnTemporal(s.console, "agent Temporal state path unavailable: %v", err)
		return startTemporalDevServer(ctx, s.root, s.cfg, s.console)
	}
	temporal, err := startTemporalDevServer(context.Background(), filepath.Join(paths.AgentDir, "temporal"), s.cfg, s.console)
	if temporal == nil {
		return nil, err
	}
	if _, registerErr := s.agent.UpsertSubstrate(ctx, temporal.SubstrateRequest(os.Getpid())); registerErr != nil {
		warnTemporal(s.console, "failed to register shared Temporal substrate with agent: %v", registerErr)
		return temporal, err
	}
	temporal.MarkExternal()
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
	if err := verifySubstrateOwner(substrate); err != nil {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateTemporal)
		return nil
	}
	temporal := temporalDevServerFromSubstrate(substrate, s.cfg.Name, s.cfg.Temporal)
	if temporal == nil {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateTemporal)
		return nil
	}
	if !temporal.Reachable(ctx, s.cfg.Name, s.cfg.Temporal) {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateTemporal)
		return nil
	}
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
			if route := strings.TrimSpace(s.agentSession.Routes[localagent.RouteGrafana]); route != "" {
				state = grafanaStateWithBaseURL(state, route)
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
		s.emitDevEvent(ctx, devdash.DevSource{ID: "grafana", Kind: "substrate", Name: "grafana", Role: "observability-ui", Status: state.Status, URL: state.URL}, "info", "Grafana status updated", map[string]any{
			"available": state.Available,
			"status":    state.Status,
			"url":       state.URL,
		})
	}
	return err
}

func (s *devSupervisor) registerAgentSessionBackend(ctx context.Context, route string, backend localagent.Backend) bool {
	route = strings.TrimSpace(route)
	backend.Network = strings.TrimSpace(backend.Network)
	backend.Addr = strings.TrimSpace(backend.Addr)
	if s == nil || s.agent == nil || s.agentSession == nil || route == "" || backend.Addr == "" {
		return false
	}
	if backend.Network == "" {
		backend.Network = "tcp"
	}
	backends := copyManagedBackends(s.agentSession.Backends)
	backends[route] = backend
	session, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.cfg.AppID(),
		AppRoot:     s.root,
		SessionID:   s.agentSession.SessionID,
		Branch:      s.agentSession.Branch,
		Status:      firstNonEmpty(s.agentSession.Status, "starting"),
		OwnerPID:    os.Getpid(),
		AppPID:      s.agentSession.AppPID,
		Backends:    backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		slog.Warn("failed to register onlava agent session backend", "route", route, "err", err)
		return false
	}
	s.agentSession = &session
	s.setSessionIdentity(&session)
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
	if grafana := s.agentGrafana(ctx); grafana != nil {
		return grafana, nil
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		warnGrafana(s.console, "agent Grafana state path unavailable: %v", err)
		return startGrafanaForDev(ctx, s.root, s.victoria, s.plannedGrafanaPublicURL(), s.console)
	}
	grafana, err := startGrafanaForDevWithRoot(context.Background(), filepath.Join(paths.AgentDir, "grafana"), s.victoria, "", s.console)
	if grafana == nil || !grafana.State().Available {
		return grafana, err
	}
	if _, registerErr := s.agent.UpsertSubstrate(ctx, grafana.SubstrateRequest(os.Getpid())); registerErr != nil {
		warnGrafana(s.console, "failed to register shared Grafana substrate with agent: %v", registerErr)
		return grafana, err
	}
	grafana.MarkExternal()
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
	if err := verifySubstrateOwner(substrate); err != nil {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateGrafana)
		return nil
	}
	grafana := grafanaComponentFromSubstrate(substrate, s.victoria, "")
	if grafana == nil {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateGrafana)
		return nil
	}
	if grafana.cfg.MetricsURL == "" && grafana.cfg.LogsURL == "" && grafana.cfg.TracesURL == "" {
		return nil
	}
	if !grafana.Reachable(ctx) {
		_, _ = s.agent.DeleteSubstrate(ctx, localagent.SubstrateGrafana)
		return nil
	}
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
		MCPHost:           s.cfg.Proxy.MCPHost,
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

func (s *devSupervisor) dashboardAuthorizeReport(req *http.Request, _ devdash.ReportEnvelope) bool {
	return req.Header.Get("Authorization") == "Bearer "+s.reportToken
}

func (s *devSupervisor) dashboardRootForApp(ctx context.Context, appID string) (string, error) {
	status, err := s.statusFor(ctx, firstNonEmpty(appID, s.activeAppID()))
	if err != nil {
		return "", err
	}
	return status.AppRoot, nil
}

func (s *devSupervisor) dashboardVictoria() dashboardVictoria {
	if s == nil || s.victoria == nil {
		return nil
	}
	return s.victoria
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
		MCPHost:           s.cfg.Proxy.MCPHost,
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
	s.proxy = proxy
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
			case localagent.RouteAPI, localagent.RouteDashboard, localagent.RouteGrafana, localagent.RouteMCP, localagent.RouteTemporal:
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
		MCP:       s.mcpURL(),
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
	s.emitDevEvent(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "error", Reason: err.Error()}, "error", "build failed", map[string]any{
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
		Routes:       s.statusDashboardRoutesLocked(app.SessionID),
		Compiling:    app.Compiling,
		CompileError: app.CompileError,
	}, nil
}

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
		if routes := visibleDashboardRoutesFromProxy(s.proxy.Routes(), s.activeAppID()); len(routes) > 0 {
			return routes
		}
	}
	return nil
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func (s *devSupervisor) updateAgentSession(ctx context.Context, status, appPID string) {
	if s == nil || s.agent == nil || s.agentSession == nil {
		return
	}
	session, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:   s.cfg.AppID(),
		AppRoot:     s.root,
		SessionID:   s.agentSession.SessionID,
		Branch:      s.agentSession.Branch,
		Status:      status,
		OwnerPID:    os.Getpid(),
		AppPID:      appPID,
		Backends:    s.agentSession.Backends,
		ReportToken: s.reportToken,
	})
	if err != nil {
		slog.Warn("failed to update onlava agent session", "err", err)
		return
	}
	s.agentSession = &session
	s.setSessionIdentity(&session)
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
