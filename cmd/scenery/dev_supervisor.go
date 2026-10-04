package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/netprobe"
	"scenery.sh/internal/victoria"
)

type runningApp struct {
	process *devManagedProcess
	cmd     *exec.Cmd
	done    chan error
	pid     string
	output  *safeLineTail
	launch  *appStartPlan
}

type devSupervisor struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	root                  string
	cfg                   app.Config
	env                   app.ResolvedEnv
	backend               devBackend
	addr                  string
	invocationEnvironment []string
	worktreeControlPaths  *localagent.Paths
	worktreeRootPaths     *localagent.WorktreePaths
	victoriaDesiredEnv    []string
	victoriaStartupDone   chan struct{}
	victoriaRecoveryDone  <-chan struct{}
	victoriaSubstrateDone <-chan struct{}
	postgresMonitorDone   <-chan struct{}
	postgresTarget        *worktreeDatabaseTarget
	postgresMetadata      *dashboardPostgresDatabase

	store       *devdash.Store
	storeWriter dashboardControlPlaneWriter
	dashboard   *dashboardServer
	victoria    *victoria.Stack
	// victoriaProcesses is an explicit per-supervisor configuration seam for
	// lifecycle tests. Its zero value uses Victoria's environment contract.
	victoriaProcesses victoriaProcessConfig
	storageProxy      *managedStorageProxy
	reportToken       string
	console           *runConsole
	agent             *localagent.Client
	agentSession      *localagent.Session
	// devDomainURL is the edge-verified dev domain base URL advertised in
	// run output; empty when dev.routing.domain does not apply or the edge
	// was not serving it at startup.
	devDomainURL string
	frontends    map[string]*managedFrontendProcess
	desktops     map[string]*managedDesktopProcess
	// desktopSessionProcessUpdater is an in-process lifecycle seam. Production
	// leaves it nil and updates the registered local-agent session.
	desktopSessionProcessUpdater func(context.Context, string, int) error
	assistants                   *assistantSupervisor
	// assistantTokenKeyPath is the supervisor-owned stable sealing key handed
	// to the app child. It is kept out of ordinary status and log payloads.
	assistantTokenKeyPath string
	// productionFrontends holds in-process static servers for frontends with
	// serve mode "production", keyed by normalized frontend name; guarded by mu.
	productionFrontends map[string]*staticFrontendServer
	events              *devEventSink

	closeOnce sync.Once
	mu        sync.RWMutex
	current   *runningApp
	// processes holds the host and service process instances once started.
	processes          *devProcessModel
	status             devdash.AppRecord
	pendingDevEvents   []devdash.DevEvent
	startupReady       <-chan error
	victoriaStarted    bool
	dbSetupFingerprint string
	buildFailed        bool
	// buildBlock is set while builds fail for a cause no ordinary edit
	// resolves (see dev_build_block.go).
	buildBlock *devBuildBlock

	// rebuildRequests wakes the watch loop for a rebuild that no watched
	// file change would trigger (e.g. a ui catalog sync succeeding after the
	// last app build failed). Buffered so requests never block; coalescing
	// duplicate requests into one pending wake is the desired behavior.
	rebuildRequests chan struct{}

	// lifecycle serializes generation changes: source rebuilds and
	// configuration applications.
	lifecycle sync.Mutex
	// config tracks the environment configuration the running generation
	// applied; active is that generation's build, set and child environment,
	// which a configuration-only change reuses without building.
	config devConfigState
	active *devActiveGeneration
}

// devActiveGeneration is the published generation a configuration change
// restarts consumers of. Guarded by lifecycle.
type devActiveGeneration struct {
	result *build.Result
	set    *build.DevelopmentProcessSet
	base   []string
}

const (
	appStartupTimeout      = 30 * time.Second
	appStartupPollInterval = 10 * time.Millisecond
)

func newDevSupervisor(ctx context.Context, root string, cfg app.Config, env app.ResolvedEnv, backend devBackend, console *runConsole, agent *localagent.Client, agentSession *localagent.Session) (*devSupervisor, error) {
	supervisorCtx, cancel := context.WithCancel(ctx)
	backend = backend.normalized()
	token, err := randomToken()
	if err != nil {
		cancel()
		return nil, err
	}
	if agent == nil || agentSession == nil {
		cancel()
		return nil, fmt.Errorf("development supervisor requires its acquired worktree control owner")
	}
	storeWriter, err := newDashboardControlPlaneClient(ctx, agent, *agentSession, token)
	if err != nil {
		cancel()
		return nil, err
	}
	appID := cfg.AppID()
	if console == nil {
		console = newRunConsole(os.Stdout, os.Stderr, false, false, appID, root)
	}

	s := &devSupervisor{
		ctx:          supervisorCtx,
		cancel:       cancel,
		root:         root,
		cfg:          cfg,
		env:          env,
		backend:      backend,
		addr:         backend.Addr,
		storeWriter:  storeWriter,
		reportToken:  token,
		console:      console,
		agent:        agent,
		agentSession: agentSession,
		status: devdash.AppRecord{
			ID:         appID,
			Name:       cfg.Name,
			Root:       root,
			ListenAddr: backend.Addr,
			Offline:    true,
			UpdatedAt:  time.Now().UTC(),
		},
		rebuildRequests: make(chan struct{}, 1),
	}
	assistantStateRoot := filepath.Join(root, ".scenery", "assistants")
	if agentSession != nil && strings.TrimSpace(agentSession.StateRoot) != "" {
		assistantStateRoot = filepath.Join(agentSession.StateRoot, "assistants")
	}
	assistantTokenKeyPath, keyErr := ensureAssistantTokenKey(assistantStateRoot)
	if keyErr != nil {
		cancel()
		return nil, fmt.Errorf("assistant token key: %w", keyErr)
	}
	providerEnv, envErr := assistantProviderEnvFromConfig(supervisorCtx, cfg, env)
	if envErr != nil {
		cancel()
		return nil, fmt.Errorf("assistant provider credential: %w", envErr)
	}
	s.assistants = newAssistantSupervisor(supervisorCtx, assistantSupervisorConfig{
		Root:          root,
		StateRoot:     assistantStateRoot,
		ProviderEnv:   providerEnv,
		UseAppGateway: true,
		OnProcess: func(name string, pid int) {
			if s == nil {
				return
			}
			if s.agent != nil {
				s.updateAgentSession(context.Background(), "running", s.currentPID())
			}
		},
		OnEvent: func(ctx context.Context, source devdash.DevSource, level, message string, fields map[string]any) {
			s.eventSink().Emit(ctx, source, level, message, fields)
			if message == "assistant.step" && s.console != nil {
				s.console.Event("build.step", fields)
			}
		},
		OnStatus: func(statuses []AssistantStatusRecord) {
			s.persistAssistantStatusSnapshot(statuses)
		},
		Output:    os.Stdout,
		ErrOutput: os.Stderr,
	})
	s.assistantTokenKeyPath = assistantTokenKeyPath
	s.dashboard = newDashboardServer(s)
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
		s.releaseConfigPin()
		if s.postgresMonitorDone != nil {
			<-s.postgresMonitorDone
		}
		if s.victoriaStartupDone != nil {
			<-s.victoriaStartupDone
		}
		// Assistant helpers own private gateways and control clients. Stop them
		// before tearing down the ordinary app so no helper can outlive the
		// session it serves.
		if s.assistants != nil {
			if err := s.assistants.Close(); err != nil {
				closeErr = errors.Join(closeErr, err)
			}
		}

		app := s.detachCurrentApp()
		frontends := s.detachManagedFrontends()
		desktops := s.detachManagedDesktops()
		s.mu.RLock()
		victoria := s.victoria
		s.mu.RUnlock()

		var errs []error
		if app != nil {
			if err := app.interrupt(); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.Interrupt(); err != nil {
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
		if s.storageProxy != nil {
			closers = append(closers, closer{name: "storage-proxy", fn: s.storageProxy.Close})
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
				close(errCh)
				close(done)
			}()
			select {
			case <-done:
				for err := range errCh {
					errs = append(errs, err)
				}
			case <-time.After(2 * time.Second):
				errs = append(errs, fmt.Errorf("timed out closing in-process dev services"))
			}
		}

		if app != nil {
			if err := app.waitOrKill(stopTimeout); err != nil {
				errs = append(errs, err)
			}
		}
		if err := s.closeDevProcesses(); err != nil {
			errs = append(errs, err)
		}
		for _, frontend := range frontends {
			if err := frontend.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
		for _, desktop := range desktops {
			if err := desktop.Stop(); err != nil {
				errs = append(errs, err)
			}
		}
		if victoria != nil {
			if err := victoria.WaitOrKill(5 * time.Second); err != nil {
				errs = append(errs, err)
			}
		}
		for _, done := range []<-chan struct{}{s.victoriaRecoveryDone, s.victoriaSubstrateDone} {
			if done != nil {
				<-done
			}
		}

		if s.store != nil {
			if err := s.store.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.worktreeRootPaths != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			record, err := s.worktreeRootPaths.LoadRecord(s.cfg.AppID())
			if err != nil {
				errs = append(errs, err)
			} else if record.Postgres != nil {
				resolver, resolveErr := newWorktreePostgresResolver(stopCtx, s.root, s.cfg.AppID())
				if resolveErr == nil {
					resolveErr = resolver.stop(stopCtx)
				}
				if resolveErr != nil {
					errs = append(errs, resolveErr)
				}
			}
			cancel()
		}
		if session := s.currentAgentSession(); s.agent != nil && session != nil {
			if _, _, err := s.agent.DeleteOwnedSession(context.Background(), *session, false); err != nil {
				errs = append(errs, err)
			}
		}
		closeErr = errors.Join(append([]error{closeErr}, errs...)...)
	})
	return closeErr
}

func (s *devSupervisor) Start(ctx context.Context) error {
	if err := s.configureWorktreeVictoria(); err != nil {
		s.reportVictoriaRecoveryFailure("", err, 0)
	}
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
	if s.agent == nil {
		if err := s.dashboard.Start(ctx); err != nil {
			return err
		}
	}
	ready := s.startDevServiceStartup(s.ctx)
	s.mu.Lock()
	s.startupReady = ready
	s.mu.Unlock()
	s.startPostgresEndpointMonitor()
	return nil
}

func (s *devSupervisor) startDevServiceStartup(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	s.victoriaStartupDone = make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		errCh := make(chan error, 2)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.console.Phase("Starting storage proxy", func() error {
				return s.ensureManagedStorageProxy(ctx)
			}); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer close(s.victoriaStartupDone)
			var victoriaStack *victoria.Stack
			_ = s.console.Phase("Starting Victoria observability stack", func() error {
				victoriaStack = s.startVictoriaStack(ctx)
				return nil
			})
			if ctx.Err() != nil {
				discardVictoriaStack(victoriaStack)
				return
			}
			s.mu.Lock()
			s.victoria = victoriaStack
			s.victoriaStarted = true
			pendingDevEvents := append([]devdash.DevEvent(nil), s.pendingDevEvents...)
			s.pendingDevEvents = nil
			s.mu.Unlock()
			if s.agent != nil && victoria.Enabled() {
				s.startVictoriaRecoveryMonitor()
			}
			if victoriaStack != nil {
				for _, event := range pendingDevEvents {
					s.eventSink().ExportVictoriaDevEvent(event)
				}
				s.eventSink().Emit(ctx, devdash.DevSource{ID: "victoria", Kind: "substrate", Name: "victoria", Role: "observability", Status: "running"}, "info", "Victoria stack ready", map[string]any{
					"urls": victoriaStack.URLs(),
				})
			}
		}()
		wg.Wait()
		close(errCh)
		var errs []error
		for err := range errCh {
			if err != nil {
				errs = append(errs, err)
			}
		}
		err := errors.Join(errs...)
		if err != nil && s.cancel != nil {
			s.cancel()
		}
		done <- err
		close(done)
	}()
	return done
}

func (s *devSupervisor) waitForStartupReady(ctx context.Context) error {
	s.mu.RLock()
	ready := s.startupReady
	s.mu.RUnlock()
	if ready == nil {
		return nil
	}
	select {
	case err := <-ready:
		s.mu.Lock()
		if s.startupReady == ready {
			s.startupReady = nil
		}
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *devSupervisor) addStartupReady(ready <-chan error) {
	if s == nil || ready == nil {
		return
	}
	s.mu.Lock()
	existing := s.startupReady
	s.startupReady = joinStartupReady(existing, ready)
	s.mu.Unlock()
}

func joinStartupReady(left, right <-chan error) <-chan error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	done := make(chan error, 1)
	go func() {
		done <- errors.Join(<-left, <-right)
		close(done)
	}()
	return done
}

func (s *devSupervisor) startVictoriaStack(ctx context.Context) *victoria.Stack {
	if s == nil || s.agent == nil {
		return nil
	}
	paths, err := s.controlPaths()
	if err != nil {
		victoria.Warn(victoriaConsole(s.console), "agent Victoria state path unavailable: %v", err)
		return nil
	}
	stack, reused, err := s.ensureVictoriaStack(ctx, filepath.Join(paths.AgentDir, "victoria"))
	if err != nil {
		victoria.Warn(victoriaConsole(s.console), "failed to prepare worktree Victoria substrate with agent: %v", err)
		return stack
	}
	if stack == nil {
		return nil
	}
	s.victoriaSubstrateDone = monitorVictoriaSubstrate(filepath.Join(paths.AgentDir, "victoria"), s.agent, s.eventSink(), stack)
	if s.console != nil && s.console.verbose {
		s.console.Event("victoria.worktree", map[string]any{
			"owner":     "worktree",
			"mode":      "worktree-owner",
			"reused":    reused,
			"endpoints": stack.SubstrateRequest(os.Getpid()).Endpoints,
		})
	}
	return stack
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
	s.writeProcessEvent(ctx, "process-stop", s.compactAppStatus())
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

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// ensureAssistantTokenKey creates or validates the supervisor-owned stable
// assistant sealing key. The generated key is hex-encoded text containing
// exactly 32 bytes of entropy. Invalid existing material fails closed with an
// error so the caller cannot start an app child with an ambiguous key.
func ensureAssistantTokenKey(stateRoot string) (string, error) {
	stateRoot = strings.TrimSpace(stateRoot)
	if stateRoot == "" {
		return "", errors.New("assistant token key state root is required")
	}
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return "", fmt.Errorf("assistant token key state root: %w", err)
	}
	if info, err := os.Lstat(stateRoot); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("assistant token key state root is not a private directory")
	} else if err := os.Chmod(stateRoot, 0o700); err != nil {
		return "", fmt.Errorf("protect assistant token key state root: %w", err)
	}
	path := filepath.Join(stateRoot, "token-key")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return "", errors.New("assistant token key file is not a private regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read assistant token key: %w", err)
		}
		if len(data) == 32 && utf8.Valid(data) && strings.TrimSpace(string(data)) == string(data) {
			return path, nil
		}
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return "", errors.New("assistant token key file contains invalid key material")
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect assistant token key: %w", err)
	}

	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", fmt.Errorf("generate assistant token key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ensureAssistantTokenKey(stateRoot)
		}
		return "", fmt.Errorf("create assistant token key: %w", err)
	}
	data := []byte(hex.EncodeToString(key[:]))
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("write assistant token key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync assistant token key: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close assistant token key: %w", err)
	}
	remove = false
	return path, nil
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
		BaseAppID:     s.activeAppID(),
		Environment:   firstNonEmpty(session.Environment, s.env.Name),
		AppRoot:       s.root,
		SessionID:     session.SessionID,
		Branch:        session.Branch,
		Status:        status,
		OwnerPID:      os.Getpid(),
		AppPID:        appPID,
		Processes:     s.sessionProcessesFor(session, appPID),
		Backends:      session.Backends,
		RouteManifest: session.RouteManifest,
		ReportToken:   s.reportToken,
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
	s.mu.RLock()
	frontends := make([]*managedFrontendProcess, 0, len(s.frontends))
	for _, process := range s.frontends {
		frontends = append(frontends, process)
	}
	s.mu.RUnlock()
	for key, process := range frontendSessionProcesses(frontends) {
		processes[key] = process
	}
	for key := range processes {
		if strings.HasPrefix(key, "assistant-") {
			delete(processes, key)
		}
	}
	if pid := atoiPID(appPID); pid > 0 {
		processes[localagent.RouteAPI] = localagent.Process{PID: pid}
	}
	if s.assistants != nil {
		for key, process := range s.assistants.ProcessSnapshot() {
			processes[key] = process
		}
	}
	for key := range processes {
		if strings.HasPrefix(key, devProcessSessionPrefix) {
			delete(processes, key)
		}
	}
	for key, process := range s.sessionServiceProcesses() {
		processes[key] = process
	}
	if len(processes) == 0 {
		return nil
	}
	return processes
}

func (s *devSupervisor) currentPID() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current == nil {
		return ""
	}
	return s.current.pid
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
	return netprobe.BindFree(addr)
}
