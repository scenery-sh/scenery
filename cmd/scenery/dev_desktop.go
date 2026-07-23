package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/desktop"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
)

type managedDesktopProcess struct {
	Name    string
	Root    string
	Process *devManagedProcess
	LogFile *os.File

	closeOnce sync.Once
	closeErr  error
}

func configuredDesktopShells(appRoot string, cfg app.Config) ([]desktop.Project, error) {
	return desktop.Resolve(appRoot, cfg.Frontends)
}

func (s *devSupervisor) startDesktopShellsAfterFrontends(ctx context.Context, frontendReady <-chan error) <-chan error {
	ready := make(chan error, 1)
	go func() {
		defer close(ready)
		if frontendReady != nil {
			select {
			case err := <-frontendReady:
				if err != nil {
					ready <- err
					return
				}
			case <-ctx.Done():
				ready <- ctx.Err()
				return
			}
		}
		ready <- s.startDesktopShells(ctx)
	}()
	return ready
}

func (s *devSupervisor) startDesktopShells(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("desktop supervisor is unavailable")
	}
	shells, err := configuredDesktopShells(s.root, s.cfg)
	if err != nil {
		return err
	}
	session := s.currentAgentSession()
	if session == nil || s.agent == nil {
		return fmt.Errorf("scenery up --desktop requires the local scenery agent and a registered dev session")
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), s.root, s.env.DotEnvFiles()...)
	if err != nil {
		return err
	}
	started := make([]*managedDesktopProcess, 0, len(shells))
	for _, shell := range shells {
		backend, ok := session.Backends[shell.Name]
		if !ok || strings.TrimSpace(backend.Addr) == "" {
			stopManagedDesktopProcesses(started)
			return fmt.Errorf("desktop frontend %q has no registered frontend backend", shell.Name)
		}
		if backend.Network != "" && backend.Network != "tcp" {
			stopManagedDesktopProcesses(started)
			return fmt.Errorf("desktop frontend %q requires a TCP frontend backend, got %q", shell.Name, backend.Network)
		}
		process, err := s.startDesktopShell(ctx, shell, "http://"+backend.Addr, baseEnv, *session)
		if err != nil {
			stopManagedDesktopProcesses(started)
			return err
		}
		started = append(started, process)
	}
	return nil
}

func (s *devSupervisor) startDesktopShell(_ context.Context, shell desktop.Project, devURL string, baseEnv []string, session localagent.Session) (*managedDesktopProcess, error) {
	command, err := desktop.DevCommand(shell, devURL)
	if err != nil {
		return nil, err
	}
	logFile, err := managedDesktopLogFile(session, shell.Name)
	if err != nil {
		return nil, err
	}
	desktop := &managedDesktopProcess{Name: shell.Name, Root: shell.TauriRoot, LogFile: logFile}
	process, err := startDevManagedProcess(context.Background(), devProcessStartRequest{
		Name:    shell.Name,
		Kind:    "desktop",
		Role:    "desktop-shell",
		Dir:     command.Dir,
		Command: command.Path,
		Args:    command.Args,
		Env:     frontendDevEnv(baseEnv, s.root, strings.TrimPrefix(devURL, "http://"), session, shell.Name),
		Stdout:  logFile,
		Stderr:  logFile,
		OnOutput: func(pid int, stream string, data []byte) {
			plain := append([]byte(nil), data...)
			go captureManagedDesktopOutput(s.activeAppID(), session.SessionID, desktop, pid, stream, plain)
		},
	})
	if err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start desktop frontend %q: %w", shell.Name, err)
	}
	desktop.Process = process
	s.setManagedDesktop(shell.Name, desktop)
	if err := s.updateDesktopSessionProcess(context.Background(), shell.Name, process.PID); err != nil {
		s.clearManagedDesktop(shell.Name, desktop)
		_ = desktop.Stop()
		return nil, fmt.Errorf("register desktop frontend %q: %w", shell.Name, err)
	}
	s.eventSink().Emit(context.Background(), devdashSourceForManagedDesktop(shell.Name, desktop, "running"), "info", "desktop shell started", map[string]any{
		"name": shell.Name,
		"pid":  process.PID,
		"url":  devURL,
	})
	go s.monitorManagedDesktop(shell.Name, desktop)
	return desktop, nil
}

func managedDesktopLogFile(session localagent.Session, name string) (*os.File, error) {
	dir := filepath.Join(session.StateRoot, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "desktop-"+localagentLabel(name)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func captureManagedDesktopOutput(appID, sessionID string, desktop *managedDesktopProcess, pidValue int, stream string, data []byte) {
	if desktop == nil || len(data) == 0 {
		return
	}
	pid := ""
	if pidValue > 0 {
		pid = fmt.Sprintf("%d", pidValue)
	}
	source := devdash.DevSource{
		ID:     "desktop:" + desktop.Name,
		Kind:   "desktop",
		Name:   desktop.Name,
		Role:   "desktop-shell",
		PID:    pid,
		Stream: stream,
		Status: "running",
	}
	event := assignDevEventID(devdash.DevEventFromOutput(appID, sessionID, source, append([]byte(nil), data...), time.Now().UTC()))
	if victoria := resolveLogsVictoriaStackFunc(context.Background(), false); victoria != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = victoria.ExportDevEvent(ctx, event)
	}
}

func (s *devSupervisor) monitorManagedDesktop(name string, desktop *managedDesktopProcess) {
	if desktop == nil || desktop.Process == nil {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	case <-desktop.Process.done:
	}
	if !s.isCurrentManagedDesktop(name, desktop) {
		return
	}
	s.clearManagedDesktop(name, desktop)
	_ = desktop.closeLog()
	_ = s.updateDesktopSessionProcess(context.Background(), name, 0)
	level := "info"
	fields := map[string]any{"name": name, "pid": desktop.Process.PID}
	if err := desktop.Process.waitError(); err != nil {
		level = "error"
		fields["error"] = err.Error()
	}
	s.eventSink().Emit(context.Background(), devdashSourceForManagedDesktop(name, desktop, "exited"), level, "desktop shell exited", fields)
}

func (s *devSupervisor) updateDesktopSessionProcess(ctx context.Context, name string, pid int) error {
	session := s.currentAgentSession()
	if s == nil || s.agent == nil || session == nil {
		return fmt.Errorf("agent session is unavailable")
	}
	processes := copySessionProcesses(session.Processes)
	key := "desktop-" + localagentLabel(name)
	if pid > 0 {
		processes[key] = localagent.Process{PID: pid}
	} else {
		delete(processes, key)
		if len(processes) == 0 {
			// Register treats an empty process map as "preserve existing".
			// A zero-PID tombstone makes this an explicit replacement with no
			// processes, and NewSession discards the tombstone.
			processes = map[string]localagent.Process{key: {PID: 0}}
		}
	}
	if len(processes) == 0 {
		processes = nil
	}
	updated, err := s.agent.Register(ctx, localagent.RegisterRequest{
		BaseAppID:      firstNonEmpty(session.BaseAppID, s.activeAppID()),
		Environment:    firstNonEmpty(session.Environment, s.env.Name),
		AppRoot:        s.root,
		SessionID:      session.SessionID,
		Branch:         session.Branch,
		Status:         session.Status,
		OwnerPID:       os.Getpid(),
		AppPID:         session.AppPID,
		Processes:      processes,
		Backends:       session.Backends,
		RouteNamespace: session.RouteNamespace,
		RouteManifest:  session.RouteManifest,
		ReportToken:    s.reportToken,
	})
	if err != nil {
		return err
	}
	s.storeAgentSession(&updated)
	return nil
}

func (s *devSupervisor) setManagedDesktop(name string, desktop *managedDesktopProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.desktops == nil {
		s.desktops = map[string]*managedDesktopProcess{}
	}
	s.desktops[localagentLabel(name)] = desktop
}

func (s *devSupervisor) clearManagedDesktop(name string, desktop *managedDesktopProcess) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = localagentLabel(name)
	if s.desktops != nil && (desktop == nil || s.desktops[name] == desktop) {
		delete(s.desktops, name)
	}
}

func (s *devSupervisor) isCurrentManagedDesktop(name string, desktop *managedDesktopProcess) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.desktops != nil && s.desktops[localagentLabel(name)] == desktop
}

func (s *devSupervisor) detachManagedDesktops() []*managedDesktopProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	desktops := make([]*managedDesktopProcess, 0, len(s.desktops))
	for _, desktop := range s.desktops {
		if desktop != nil {
			desktops = append(desktops, desktop)
		}
	}
	s.desktops = nil
	return desktops
}

func stopManagedDesktopProcesses(desktops []*managedDesktopProcess) {
	for _, desktop := range desktops {
		_ = desktop.Stop()
	}
}

func (p *managedDesktopProcess) closeLog() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		if p.LogFile != nil {
			p.closeErr = p.LogFile.Close()
		}
	})
	return p.closeErr
}

func (p *managedDesktopProcess) Stop() error {
	if p == nil {
		return nil
	}
	var stopErr error
	if p.Process != nil {
		stopErr = p.Process.Stop(stopTimeout)
	}
	return errors.Join(stopErr, p.closeLog())
}

func devdashSourceForManagedDesktop(name string, desktop *managedDesktopProcess, status string) devdash.DevSource {
	source := devdash.DevSource{
		ID:     "desktop:" + localagentLabel(name),
		Kind:   "desktop",
		Name:   localagentLabel(name),
		Role:   "desktop-shell",
		Status: status,
	}
	if desktop != nil && desktop.Process != nil && desktop.Process.PID > 0 {
		source.PID = fmt.Sprintf("%d", desktop.Process.PID)
	}
	return source
}
