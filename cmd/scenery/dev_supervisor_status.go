package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

func (s *devSupervisor) persistStatus(ctx context.Context) error {
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	status.Metadata = s.metadataWithRuntimePostgresDatabases(status.Metadata, status.Root)
	if s.storeWriter == nil {
		return nil
	}
	return s.storeWriter.UpsertApp(ctx, status)
}

func (s *devSupervisor) writeProcessEvent(ctx context.Context, kind string, payload any) {
	if s == nil {
		return
	}
	if s.storeWriter == nil && s.store != nil {
		s.storeWriter = localDashboardControlPlaneWriter{store: s.store}
	}
	if s.storeWriter == nil {
		return
	}
	_ = s.storeWriter.WriteProcessEvent(ctx, s.activeAppID(), s.currentSessionID(), kind, payload)
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

func (s *devSupervisor) detachCurrentApp() *runningApp {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current
	s.current = nil
	return current
}

func (s *devSupervisor) announceRebuild(paths []string) {
	if s.console != nil {
		s.console.RebuildDetected(paths)
	}
}

func (s *devSupervisor) apiURL() string {
	if session := s.currentAgentSession(); session != nil && session.RouteManifest.Routes[localagent.RouteAPI].URL != "" {
		return session.RouteManifest.Routes[localagent.RouteAPI].URL
	}
	return "http://" + s.addr
}

func (s *devSupervisor) frontendURLs() map[string]string {
	if session := s.currentAgentSession(); session != nil {
		return frontendURLsFromAgentRoutes(session.RouteManifest.URLs(), s.cfg.Frontends)
	}
	return nil
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
			case localagent.RouteAPI, localagent.RouteDashboard:
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
		App:       s.devDomainURL,
		API:       s.apiURL(),
		Frontends: s.frontendURLs(),
		Victoria:  s.victoria.URLs(),
	}
}

func (s *devSupervisor) handleCompileError(ctx context.Context, metadata, apiEncoding json.RawMessage, err error) error {
	s.mu.Lock()
	s.buildFailed = true
	s.mu.Unlock()
	s.setCompiling(false, err.Error())
	if s.currentPID() == "" && (len(metadata) > 0 || len(apiEncoding) > 0) {
		s.setMetadata(metadata, apiEncoding)
	}
	_ = s.persistStatus(ctx)
	s.writeProcessEvent(ctx, "compile-error", map[string]any{"error": err.Error()})
	s.eventSink().Emit(ctx, devdash.DevSource{ID: "build", Kind: "build", Name: "build", Status: "error", Reason: err.Error()}, "error", "build failed", map[string]any{
		"error": err.Error(),
	})
	if s.console != nil {
		s.console.Event("process.compile-error", map[string]any{
			"error": err.Error(),
		})
	}
	if pid := s.currentPID(); pid != "" {
		s.updateAgentSession(ctx, "running", pid)
	} else {
		s.updateAgentSession(ctx, "compile-error", "")
	}
	return err
}

// RequestRebuildIfBuildFailed wakes the watch loop for a full rebuild when the
// last app build failed. External fix paths whose writes do not surface as
// watched file changes (like a ui catalog sync succeeding after its earlier
// failure kept the app build stale) call this so a failed session converges
// instead of latching in compile-error until the next unrelated edit.
func (s *devSupervisor) RequestRebuildIfBuildFailed() {
	s.mu.RLock()
	failed := s.buildFailed
	s.mu.RUnlock()
	if !failed {
		return
	}
	select {
	case s.rebuildRequests <- struct{}{}:
	default:
	}
}

// rebuildRequestChan exposes the wake signal for the watch loop.
func (s *devSupervisor) rebuildRequestChan() <-chan struct{} {
	return s.rebuildRequests
}

// compactAppStatus is appStatus without the Meta and APIEncoding blobs, which
// can run to megabytes per app. Persisted process events must use this form;
// stored events are breadcrumbs and the full model is always available live.
func (s *devSupervisor) compactAppStatus() devdash.AppStatus {
	status := s.appStatus()
	status.Meta = nil
	status.APIEncoding = nil
	return status
}

func (s *devSupervisor) appStatus() devdash.AppStatus {
	s.mu.RLock()
	var session *localagent.Session
	if s.agentSession != nil {
		copy := *s.agentSession
		session = &copy
	}
	victoria := s.victoria
	status := devdash.AppStatus{
		Running:       s.status.Running,
		AppID:         s.status.ID,
		BaseAppID:     s.status.BaseAppID,
		RuntimeAppID:  s.status.RuntimeAppID,
		SessionID:     s.status.SessionID,
		AppRoot:       s.status.Root,
		PID:           s.status.PID,
		Meta:          s.status.Metadata,
		Addr:          s.status.ListenAddr,
		APIEncoding:   s.status.APIEncoding,
		Observability: observabilityStateFromVictoria(victoria, s.status.ID, s.status.SessionID, s.status.Root, session),
		Routes:        s.statusDashboardRoutesLocked(s.status.SessionID),
		Aliases:       s.statusDashboardAliasesLocked(s.status.SessionID),
		Compiling:     s.status.Compiling,
		CompileError:  s.status.CompileError,
		BuildBlock:    s.status.BuildBlock,
	}
	s.mu.RUnlock()
	status.ServiceProcesses = s.serviceProcessStatuses()
	applySessionStatusToAppStatus(&status, session)
	status.Meta = s.metadataWithRuntimePostgresDatabases(status.Meta, status.AppRoot)
	return status
}

func (s *devSupervisor) statusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	if appID == "" {
		appID = s.activeAppID()
	}
	if s.store == nil {
		status := s.appStatus()
		if appID == "" || appID == status.AppID || appID == status.SessionID || appID == status.RuntimeAppID {
			return status, nil
		}
		return devdash.AppStatus{}, sql.ErrNoRows
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
	var session *localagent.Session
	var victoria dashboardVictoria
	if s.agentSession != nil {
		copy := *s.agentSession
		session = &copy
	}
	victoria = s.victoria
	s.mu.RUnlock()
	status := devdash.AppStatus{
		Running:       app.Running,
		AppID:         routeID,
		BaseAppID:     app.BaseAppID,
		RuntimeAppID:  app.RuntimeAppID,
		SessionID:     app.SessionID,
		AppRoot:       app.Root,
		PID:           app.PID,
		Meta:          app.Metadata,
		Addr:          app.ListenAddr,
		APIEncoding:   app.APIEncoding,
		Observability: observabilityStateFromVictoria(victoria, routeID, app.SessionID, app.Root, session),
		Routes:        routes,
		Aliases:       aliases,
		Compiling:     app.Compiling,
		CompileError:  app.CompileError,
		BuildBlock:    app.BuildBlock,
	}
	applySessionStatusToAppStatus(&status, session)
	status.Meta = s.metadataWithRuntimePostgresDatabases(status.Meta, status.AppRoot)
	return status, nil
}

type dashboardPostgresDatabase struct {
	Name    string                    `json:"name"`
	Source  string                    `json:"source,omitempty"`
	Schemas []dashboardPostgresSchema `json:"schemas,omitempty"`
}

type dashboardPostgresSchema struct {
	Service string `json:"service"`
	Schema  string `json:"schema"`
}

func (s *devSupervisor) metadataWithRuntimePostgresDatabases(metadata json.RawMessage, appRoot string) json.RawMessage {
	if s == nil {
		return metadata
	}
	root := s.root
	if s.worktreeRootPaths != nil {
		root = s.worktreeRootPaths.AppRoot
	}
	if appRoot != root && appRoot != s.root {
		return metadata
	}
	s.mu.RLock()
	database := s.postgresMetadata
	s.mu.RUnlock()
	if database == nil {
		return metadata
	}
	return metadataWithPostgresDatabases(metadata, []dashboardPostgresDatabase{*database})
}

func metadataWithPostgresDatabases(metadata json.RawMessage, databases []dashboardPostgresDatabase) json.RawMessage {
	if len(databases) == 0 {
		return metadata
	}
	payload := map[string]any{}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &payload); err != nil {
			return metadata
		}
	}
	payload["sql_databases"] = databases
	data, err := json.Marshal(payload)
	if err != nil {
		return metadata
	}
	return data
}

// statusDashboardRoutesLocked requires s.mu to be held.
func (s *devSupervisor) statusDashboardRoutesLocked(sessionID string) map[string]string {
	if s == nil {
		return nil
	}
	if s.agentSession != nil {
		currentSessionID := strings.TrimSpace(s.agentSession.SessionID)
		if sessionID == "" || sessionID == currentSessionID {
			if routes := visibleDashboardRoutesFromAgent(s.agentSession.RouteManifest.URLs()); len(routes) > 0 {
				return routes
			}
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
