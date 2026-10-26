package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/localproxy"
)

type managedFrontendProcess struct {
	AppRoot string
	Name    string
	Root    string
	Addr    string
	Process *devManagedProcess
	// Static is set instead of Process for serve mode "production": an
	// in-process static server over the built bundle. It has no PID, is
	// never restart-supervised, and dies with the dev supervisor.
	Static  *staticFrontendServer
	LogFile *os.File

	ownerMu   sync.RWMutex
	owner     localagent.Owner
	readiness []frontendReadiness
}

type packageJSONForFrontend struct {
	Scripts map[string]string `json:"scripts"`
}

type managedFrontendStartResult struct {
	index   int
	name    string
	backend localagent.Backend
	process *managedFrontendProcess
	err     error
}

type managedFrontendStarter func(context.Context, string, string, int, localproxy.FrontendConfig, []string, localagent.Session) managedFrontendStartResult
type pendingManagedFrontendStarter func(context.Context, context.CancelFunc, string, string, int, localproxy.FrontendConfig, []string, localagent.Session) pendingManagedFrontendStart

type frontendOverrideResolver func(string) string

type pendingManagedFrontendStart struct {
	managedFrontendStartResult
	ready <-chan error
}

func managedFrontendBackendsForSession(ctx context.Context, root string, cfg app.Config, baseEnv []string, session localagent.Session) (map[string]localagent.Backend, []*managedFrontendProcess, error) {
	return managedFrontendBackendsForSessionWithStarter(ctx, root, cfg, baseEnv, session, startManagedFrontend)
}

func beginManagedFrontendBackendsForSessionWithOverride(ctx context.Context, root string, cfg app.Config, baseEnv []string, session localagent.Session, resolveOverride frontendOverrideResolver) (map[string]localagent.Backend, []*managedFrontendProcess, func(context.Context) error, error) {
	return beginManagedFrontendBackendsForSessionWithOverrideAndStarter(ctx, root, cfg, baseEnv, session, resolveOverride, beginManagedFrontend)
}

func beginManagedFrontendBackendsForSessionWithOverrideAndStarter(ctx context.Context, root string, cfg app.Config, baseEnv []string, session localagent.Session, resolveOverride frontendOverrideResolver, starter pendingManagedFrontendStarter) (map[string]localagent.Backend, []*managedFrontendProcess, func(context.Context) error, error) {
	frontends := configuredFrontends(cfg.Frontends)
	if len(frontends) == 0 {
		return nil, nil, nil, nil
	}
	sort.Slice(frontends, func(i, j int) bool {
		return frontends[i].Name < frontends[j].Name
	})
	backends := map[string]localagent.Backend{}
	var startable []localproxy.FrontendConfig
	for _, frontend := range frontends {
		frontend.Name = localagentLabel(frontend.Name)
		if frontend.Name == "" {
			continue
		}
		if override := resolveOverride(frontend.Name); override != "" {
			backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: override}
			continue
		}
		if frontend.AllowSharedUpstream && frontend.Serve != frontendServeProduction {
			result := startManagedFrontend(ctx, root, cfg.AppID(), len(startable), frontend, baseEnv, session)
			if result.err != nil {
				return nil, nil, nil, result.err
			}
			if result.name != "" {
				backends[result.name] = result.backend
			}
			continue
		}
		startable = append(startable, frontend)
	}
	pending, cancelPending := beginManagedFrontendsWithStarter(ctx, root, cfg.AppID(), startable, baseEnv, session, starter)
	processes := make([]*managedFrontendProcess, 0, len(pending))
	var ready []<-chan error
	for _, result := range pending {
		if result.err != nil {
			cancelPending()
			stopManagedFrontendProcesses(processes)
			return nil, nil, nil, result.err
		}
		if result.process != nil {
			processes = append(processes, result.process)
		}
		if result.name != "" {
			backends[result.name] = result.backend
		}
		if result.ready != nil {
			ready = append(ready, result.ready)
		}
	}
	if len(backends) == 0 {
		backends = nil
	}
	wait := func(context.Context) error {
		defer cancelPending()
		var errs []error
		for _, ch := range ready {
			if err := <-ch; err != nil {
				errs = append(errs, err)
			}
		}
		if err := errors.Join(errs...); err != nil {
			stopManagedFrontendProcesses(processes)
			return err
		}
		return nil
	}
	if len(ready) == 0 {
		cancelPending()
		wait = nil
	}
	return backends, processes, wait, nil
}

func managedFrontendBackendsForSessionWithStarter(ctx context.Context, root string, cfg app.Config, baseEnv []string, session localagent.Session, starter managedFrontendStarter) (map[string]localagent.Backend, []*managedFrontendProcess, error) {
	frontends := configuredFrontends(cfg.Frontends)
	if len(frontends) == 0 {
		return nil, nil, nil
	}
	sort.Slice(frontends, func(i, j int) bool {
		return frontends[i].Name < frontends[j].Name
	})
	backends := map[string]localagent.Backend{}
	var startable []localproxy.FrontendConfig
	for _, frontend := range frontends {
		frontend.Name = localagentLabel(frontend.Name)
		if frontend.Name == "" {
			continue
		}
		if override := localproxy.FrontendOverride(frontend.Name); override != "" {
			backends[frontend.Name] = localagent.Backend{Network: "tcp", Addr: override}
			continue
		}
		startable = append(startable, frontend)
	}
	results := startManagedFrontends(ctx, root, cfg.AppID(), startable, baseEnv, session, starter)
	processes := make([]*managedFrontendProcess, 0, len(results))
	for _, result := range results {
		if result.process != nil {
			processes = append(processes, result.process)
		}
	}
	for _, result := range results {
		if result.err != nil {
			stopManagedFrontendProcesses(processes)
			return nil, nil, result.err
		}
		if result.name == "" {
			continue
		}
		backends[result.name] = result.backend
	}
	if len(backends) == 0 {
		backends = nil
	}
	return backends, processes, nil
}

func configuredFrontends(frontends map[string]app.FrontendConfig) []localproxy.FrontendConfig {
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
			Root:                frontend.Root,
			Upstream:            frontend.Upstream,
			AllowSharedUpstream: frontend.AllowSharedUpstream,
			Serve:               strings.ToLower(strings.TrimSpace(frontend.Serve)),
		})
	}
	return resolved
}

const frontendServeProduction = "production"

// validateFrontendServeModes rejects unknown frontends.<name>.serve values
// before any frontend process management starts.
func validateFrontendServeModes(cfg app.Config) error {
	for name, frontend := range cfg.Frontends {
		switch strings.ToLower(strings.TrimSpace(frontend.Serve)) {
		case "", "development", frontendServeProduction:
		default:
			return fmt.Errorf("frontends.%s.serve must be \"development\" or \"production\"", name)
		}
	}
	return nil
}

func startManagedFrontends(ctx context.Context, appRoot, appID string, frontends []localproxy.FrontendConfig, baseEnv []string, session localagent.Session, starter managedFrontendStarter) []managedFrontendStartResult {
	if len(frontends) == 0 {
		return nil
	}
	if starter == nil {
		starter = startManagedFrontend
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultCh := make(chan managedFrontendStartResult, len(frontends))
	var wg sync.WaitGroup
	for i, frontend := range frontends {
		wg.Add(1)
		go func(index int, frontend localproxy.FrontendConfig) {
			defer wg.Done()
			result := starter(ctx, appRoot, appID, index, frontend, baseEnv, session)
			if result.err != nil {
				cancel()
			}
			resultCh <- result
		}(i, frontend)
	}
	wg.Wait()
	close(resultCh)
	results := make([]managedFrontendStartResult, len(frontends))
	for result := range resultCh {
		results[result.index] = result
	}
	return results
}

func beginManagedFrontendsWithStarter(ctx context.Context, appRoot, appID string, frontends []localproxy.FrontendConfig, baseEnv []string, session localagent.Session, starter pendingManagedFrontendStarter) ([]pendingManagedFrontendStart, context.CancelFunc) {
	if len(frontends) == 0 {
		return nil, func() {}
	}
	if starter == nil {
		starter = beginManagedFrontend
	}
	ctx, cancel := context.WithCancel(ctx)
	resultCh := make(chan pendingManagedFrontendStart, len(frontends))
	var wg sync.WaitGroup
	for i, frontend := range frontends {
		wg.Add(1)
		go func(index int, frontend localproxy.FrontendConfig) {
			defer wg.Done()
			result := starter(ctx, cancel, appRoot, appID, index, frontend, baseEnv, session)
			if result.err != nil {
				cancel()
			}
			resultCh <- result
		}(i, frontend)
	}
	wg.Wait()
	close(resultCh)
	results := make([]pendingManagedFrontendStart, len(frontends))
	for result := range resultCh {
		results[result.index] = result
	}
	return results, cancel
}

func beginManagedFrontend(ctx context.Context, cancel context.CancelFunc, appRoot, appID string, index int, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) pendingManagedFrontendStart {
	result := pendingManagedFrontendStart{
		managedFrontendStartResult: managedFrontendStartResult{index: index, name: frontend.Name},
	}
	if frontend.Serve == frontendServeProduction {
		process, ready, err := startProductionFrontendServer(appRoot, frontend, baseEnv, session)
		if err != nil {
			result.err = fmt.Errorf("build managed frontend %q: %w", frontend.Name, err)
			return result
		}
		result.process = process
		result.backend = localagent.Backend{Network: "tcp", Addr: process.Addr}
		result.ready = ready
		return result
	}
	process, err := startManagedFrontendProcessWithoutWaiting(ctx, appRoot, appID, frontend, baseEnv, session)
	if err != nil {
		if strings.TrimSpace(frontend.Upstream) != "" {
			result.err = fmt.Errorf("start managed frontend %q: %w; configured upstream is ignored unless allow_shared_upstream is true or %s is set", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
			return result
		}
		result.err = fmt.Errorf("start managed frontend %q: %w; set %s for a manual upstream", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
		return result
	}
	result.process = process
	result.backend = localagent.Backend{Network: "tcp", Addr: process.Addr}
	ready := make(chan error, 1)
	go func() {
		if err := waitForManagedFrontend(ctx, process); err != nil {
			cancel()
			_ = process.Stop()
			ready <- err
			close(ready)
			return
		}
		ready <- nil
		close(ready)
	}()
	result.ready = ready
	return result
}

func startManagedFrontend(ctx context.Context, appRoot, appID string, index int, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) managedFrontendStartResult {
	result := managedFrontendStartResult{index: index, name: frontend.Name}
	if frontend.Serve == frontendServeProduction {
		process, ready, err := startProductionFrontendServer(appRoot, frontend, baseEnv, session)
		if err == nil {
			err = <-ready
		}
		if err != nil {
			if process != nil {
				_ = process.Stop()
			}
			result.err = fmt.Errorf("build managed frontend %q: %w", frontend.Name, err)
			return result
		}
		result.process = process
		result.backend = localagent.Backend{Network: "tcp", Addr: process.Addr}
		return result
	}
	process, err := startManagedFrontendProcess(ctx, appRoot, appID, frontend, baseEnv, session)
	if err == nil && process != nil {
		result.process = process
		result.backend = localagent.Backend{Network: "tcp", Addr: process.Addr}
		return result
	}
	if frontend.AllowSharedUpstream {
		upstream := normalizeManagedTCPUpstream(frontend.Upstream)
		if upstream != "" {
			result.backend = localagent.Backend{Network: "tcp", Addr: upstream}
			return result
		}
		result.err = fmt.Errorf("start managed frontend %q: allow_shared_upstream is true but no upstream is configured", frontend.Name)
		return result
	}
	if err == nil {
		err = fmt.Errorf("managed frontend did not start")
	}
	if strings.TrimSpace(frontend.Upstream) != "" {
		result.err = fmt.Errorf("start managed frontend %q: %w; configured upstream is ignored unless allow_shared_upstream is true or %s is set", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
		return result
	}
	result.err = fmt.Errorf("start managed frontend %q: %w; set %s for a manual upstream", frontend.Name, err, frontendOverrideEnvName(frontend.Name))
	return result
}

func frontendOverrideEnvName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return "SCENERY_FRONTEND_" + strings.Trim(b.String(), "_") + "_ADDR"
}

func startManagedFrontendProcess(ctx context.Context, appRoot, appID string, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) (*managedFrontendProcess, error) {
	process, err := startManagedFrontendProcessWithoutWaiting(ctx, appRoot, appID, frontend, baseEnv, session)
	if err != nil {
		return nil, err
	}
	if err := waitForManagedFrontend(ctx, process); err != nil {
		_ = process.Stop()
		return nil, err
	}
	return process, nil
}

func startManagedFrontendProcessWithoutWaiting(ctx context.Context, appRoot, appID string, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) (*managedFrontendProcess, error) {
	root := managedFrontendRoot(appRoot, frontend)
	if root == "" {
		return nil, fmt.Errorf("frontend %s has no root", frontend.Name)
	}
	addr, err := freeLoopbackAddr()
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	allowedHost := managedFrontendAllowedHost(session, frontend.Name)
	cmdName, args, err := managedFrontendCommand(root, port, allowedHost, managedFrontendBasePath(session, frontend.Name))
	if err != nil {
		return nil, err
	}
	logFile, err := managedFrontendLogFile(session, frontend.Name)
	if err != nil {
		return nil, err
	}
	process := &managedFrontendProcess{
		AppRoot: session.AppRoot,
		Name:    frontend.Name,
		Root:    root,
		Addr:    addr,
		LogFile: logFile,
	}
	processCtx := context.Background()
	runner, err := startDevManagedProcess(processCtx, devProcessStartRequest{
		Name:    frontend.Name,
		Kind:    "frontend",
		Role:    "web-frontend",
		Dir:     root,
		Command: cmdName,
		Args:    args,
		Env:     frontendDevEnv(baseEnv, appRoot, addr, session, frontend.Name),
		Stdout:  logFile,
		Stderr:  logFile,
		OnOutput: func(pid int, stream string, data []byte) {
			plain := append([]byte(nil), data...)
			go captureManagedFrontendOutput(processCtx, appID, session.SessionID, process, pid, stream, plain)
		},
	})
	if err != nil {
		_ = logFile.Close()
		return nil, err
	}
	process.Process = runner
	return process, nil
}

func captureManagedFrontendOutput(ctx context.Context, appID, sessionID string, process *managedFrontendProcess, pidValue int, stream string, plain []byte) {
	if process == nil || len(plain) == 0 {
		return
	}
	pid := ""
	if pidValue > 0 {
		pid = fmt.Sprintf("%d", pidValue)
	}
	source := devdash.DevSource{
		ID:     "frontend:" + process.Name,
		Kind:   "frontend",
		Name:   process.Name,
		Role:   "web-frontend",
		PID:    pid,
		Stream: stream,
		Status: "running",
		URL:    "http://" + process.Addr,
	}
	now := time.Now().UTC()
	event := assignDevEventID(devdash.DevEventFromOutput(appID, sessionID, source, plain, now))
	if victoria := resolveLogsVictoriaStackFunc(ctx, process.AppRoot); victoria != nil {
		go func(event devdash.DevEvent) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = victoria.ExportDevEvent(ctx, event)
		}(event)
	}
}
