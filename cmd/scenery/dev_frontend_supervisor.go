package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
)

var (
	managedFrontendRestartDelay = 500 * time.Millisecond

	managedFrontendTestHooks struct {
		sync.Mutex
		sessionUpdated func(string, localagent.Backend, *managedFrontendProcess)
	}
)

func (s *devSupervisor) adoptManagedFrontends(processes []*managedFrontendProcess) {
	if s == nil || len(processes) == 0 {
		return
	}
	for _, process := range processes {
		name := localagentLabel(process.Name)
		if name == "" || process == nil || process.Process == nil {
			continue
		}
		s.setManagedFrontend(name, process)
		go s.monitorManagedFrontend(name, process)
	}
}

func (s *devSupervisor) monitorManagedFrontend(name string, process *managedFrontendProcess) {
	if s == nil || process == nil || process.Process == nil {
		return
	}
	supervisorCtx := s.managedFrontendContext()
	select {
	case <-supervisorCtx.Done():
		return
	case <-process.Process.done:
	}
	select {
	case <-supervisorCtx.Done():
		return
	default:
	}
	if !s.isCurrentManagedFrontend(name, process) {
		return
	}
	_ = closeManagedFrontendLog(process)
	s.handleManagedFrontendExit(name, process)
}

func (s *devSupervisor) handleManagedFrontendExit(name string, process *managedFrontendProcess) {
	if s == nil || process == nil || process.Process == nil {
		return
	}
	fields := map[string]any{
		"name": name,
		"pid":  process.Process.PID,
		"addr": process.Addr,
	}
	if err := process.Process.waitError(); err != nil {
		fields["error"] = err.Error()
	}
	s.eventSink().Emit(context.Background(), devdashSourceForManagedFrontend(name, process, "exited"), "error", "managed frontend exited", fields)

	timer := time.NewTimer(managedFrontendRestartDelay)
	defer timer.Stop()
	supervisorCtx := s.managedFrontendContext()
	select {
	case <-supervisorCtx.Done():
		return
	case <-timer.C:
	}

	ctx, cancel := context.WithTimeout(supervisorCtx, managedFrontendStartupTimeout+5*time.Second)
	defer cancel()
	if err := s.restartManagedFrontend(ctx, name, process); err != nil {
		s.eventSink().Emit(context.Background(), devdashSourceForManagedFrontend(name, process, "error"), "error", "managed frontend restart failed", map[string]any{
			"name":  name,
			"pid":   process.Process.PID,
			"addr":  process.Addr,
			"error": err.Error(),
		})
		return
	}
}

func (s *devSupervisor) restartManagedFrontend(ctx context.Context, name string, previous *managedFrontendProcess) error {
	if s == nil {
		return nil
	}
	session := s.currentAgentSession()
	if session == nil || s.agent == nil {
		return fmt.Errorf("agent session is unavailable")
	}
	frontend, ok := s.managedFrontendConfig(name)
	if !ok {
		return fmt.Errorf("frontend %q is no longer configured", name)
	}
	if override := strings.TrimSpace(localproxy.FrontendOverride(name)); override != "" {
		s.clearManagedFrontend(name, previous)
		return s.updateManagedFrontendSession(ctx, name, localagent.Backend{Network: "tcp", Addr: override}, nil)
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, ".env", ".env.local")
	if err != nil {
		return err
	}
	result := startManagedFrontend(ctx, s.root, s.activeAppID(), 0, frontend, baseEnv, *session)
	if result.err != nil {
		return result.err
	}
	if result.backend.Addr == "" {
		return fmt.Errorf("frontend %q restarted without a backend address", name)
	}
	if result.process != nil {
		s.setManagedFrontend(name, result.process)
	} else {
		s.clearManagedFrontend(name, previous)
	}
	if err := s.updateManagedFrontendSession(ctx, name, result.backend, result.process); err != nil {
		if result.process != nil {
			s.clearManagedFrontend(name, result.process)
			_ = result.process.Stop()
		}
		return err
	}
	if result.process != nil {
		go s.monitorManagedFrontend(name, result.process)
	}
	s.eventSink().Emit(context.Background(), devdashSourceForManagedFrontend(name, result.process, "running"), "info", "managed frontend restarted", map[string]any{
		"name": name,
		"addr": result.backend.Addr,
	})
	return nil
}

func (s *devSupervisor) managedFrontendContext() context.Context {
	if s == nil || s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *devSupervisor) managedFrontendConfig(name string) (localproxy.FrontendConfig, bool) {
	name = localagentLabel(name)
	if s == nil || name == "" {
		return localproxy.FrontendConfig{}, false
	}
	for _, frontend := range configuredFrontends(s.cfg.Frontends) {
		frontend.Name = localagentLabel(frontend.Name)
		if frontend.Name == name {
			return frontend, true
		}
	}
	return localproxy.FrontendConfig{}, false
}

func (s *devSupervisor) updateManagedFrontendSession(ctx context.Context, name string, backend localagent.Backend, process *managedFrontendProcess) error {
	if s == nil || s.agent == nil {
		return nil
	}
	session := s.currentAgentSession()
	if session == nil {
		return fmt.Errorf("agent session is unavailable")
	}
	name = localagentLabel(name)
	if name == "" {
		return fmt.Errorf("frontend name is empty")
	}
	backends := copyAgentSessionBackends(session.Backends)
	if strings.TrimSpace(backend.Network) == "" {
		backend.Network = "tcp"
	}
	backend.Addr = strings.TrimSpace(backend.Addr)
	if backend.Addr == "" {
		delete(backends, name)
	} else {
		backends[name] = backend
	}
	processes := copySessionProcesses(session.Processes)
	processKey := "frontend-" + name
	if process != nil && process.Process != nil && process.Process.PID > 0 {
		processes[processKey] = localagent.Process{PID: process.Process.PID}
	} else {
		delete(processes, processKey)
	}
	if len(processes) == 0 {
		processes = nil
	}
	updated, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:      firstNonEmpty(session.BaseAppID, s.activeAppID()),
		AppRoot:        s.root,
		SessionID:      session.SessionID,
		Branch:         session.Branch,
		Status:         session.Status,
		OwnerPID:       os.Getpid(),
		AppPID:         session.AppPID,
		Processes:      processes,
		Backends:       backends,
		RouteNamespace: session.RouteNamespace,
		RouteManifest:  session.RouteManifest,
		ReportToken:    s.reportToken,
	})
	if err != nil {
		return err
	}
	s.storeAgentSession(&updated)
	notifyManagedFrontendSessionUpdated(name, backend, process)
	return nil
}

func notifyManagedFrontendSessionUpdated(name string, backend localagent.Backend, process *managedFrontendProcess) {
	managedFrontendTestHooks.Lock()
	fn := managedFrontendTestHooks.sessionUpdated
	managedFrontendTestHooks.Unlock()
	if fn != nil {
		fn(name, backend, process)
	}
}

func (s *devSupervisor) setManagedFrontend(name string, process *managedFrontendProcess) {
	name = localagentLabel(name)
	if s == nil || name == "" || process == nil {
		return
	}
	s.mu.Lock()
	if s.frontends == nil {
		s.frontends = map[string]*managedFrontendProcess{}
	}
	s.frontends[name] = process
	s.mu.Unlock()
}

func (s *devSupervisor) clearManagedFrontend(name string, process *managedFrontendProcess) {
	name = localagentLabel(name)
	if s == nil || name == "" {
		return
	}
	s.mu.Lock()
	if s.frontends != nil && (process == nil || s.frontends[name] == process) {
		delete(s.frontends, name)
	}
	s.mu.Unlock()
}

func (s *devSupervisor) detachManagedFrontends() []*managedFrontendProcess {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frontends) == 0 {
		return nil
	}
	processes := make([]*managedFrontendProcess, 0, len(s.frontends))
	for _, process := range s.frontends {
		if process != nil {
			processes = append(processes, process)
		}
	}
	s.frontends = nil
	return processes
}

func (s *devSupervisor) isCurrentManagedFrontend(name string, process *managedFrontendProcess) bool {
	name = localagentLabel(name)
	if s == nil || name == "" || process == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.frontends != nil && s.frontends[name] == process
}

func devdashSourceForManagedFrontend(name string, process *managedFrontendProcess, status string) devdash.DevSource {
	name = localagentLabel(name)
	source := devdash.DevSource{
		ID:     "frontend:" + name,
		Kind:   "frontend",
		Name:   name,
		Role:   "web-frontend",
		Status: status,
	}
	if process != nil {
		source.URL = "http://" + process.Addr
		if process.Process != nil && process.Process.PID > 0 {
			source.PID = fmt.Sprintf("%d", process.Process.PID)
		}
	}
	return source
}

func closeManagedFrontendLog(process *managedFrontendProcess) error {
	if process == nil || process.LogFile == nil {
		return nil
	}
	err := process.LogFile.Close()
	process.LogFile = nil
	return err
}

func copyAgentSessionBackends(values map[string]localagent.Backend) map[string]localagent.Backend {
	copied := make(map[string]localagent.Backend, len(values)+1)
	for key, value := range values {
		key = localagentLabel(key)
		value.Addr = strings.TrimSpace(value.Addr)
		if key == "" || value.Addr == "" {
			continue
		}
		if strings.TrimSpace(value.Network) == "" {
			value.Network = "tcp"
		}
		copied[key] = value
	}
	return copied
}
