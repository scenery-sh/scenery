package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
)

type agentDashboardRuntime struct {
	server *dashboardServer
	store  *devdash.Store
}

type agentDashboardController struct {
	store             *devdash.Store
	agent             *localagent.Server
	victoriaMu        sync.Mutex
	victoria          *victoriaStack
	victoriaSubstrate string
}

func startAgentDashboard(ctx context.Context, agentServer *localagent.Server, addr string) (*agentDashboardRuntime, error) {
	paths := agentServer.Paths()
	store, err := devdash.OpenStore(filepath.Join(paths.AgentDir, "dashboard"))
	if err != nil {
		return nil, err
	}
	controller := &agentDashboardController{
		store: store,
		agent: agentServer,
	}
	server := newDashboardServerWithController(controller, paths.AgentDir, addr, "", nil)
	if err := server.Start(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return &agentDashboardRuntime{server: server, store: store}, nil
}

func (r *agentDashboardRuntime) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	if r.server != nil {
		errs = append(errs, r.server.Close())
	}
	if r.store != nil {
		errs = append(errs, r.store.Close())
	}
	return errors.Join(errs...)
}

func (c *agentDashboardController) dashboardActiveAppID() string {
	return ""
}

func (c *agentDashboardController) dashboardCurrentSessionID() string {
	return ""
}

func (c *agentDashboardController) dashboardListApps(ctx context.Context) ([]map[string]any, error) {
	records, err := c.store.ListAppSessions(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, app := range records {
		app = c.appRecordWithRegistryLiveness(app)
		routeID := firstNonEmpty(app.RouteID, app.SessionID, app.ID)
		if routeID == "" {
			continue
		}
		items = append(items, map[string]any{
			"id":                  routeID,
			"name":                app.Name,
			"app_root":            app.Root,
			"session_id":          app.SessionID,
			"base_app_id":         firstNonEmpty(app.BaseAppID, app.ID),
			"offline":             !app.Running,
			"sessionStatus":       app.SessionStatus,
			"sessionStatusReason": app.SessionStatusReason,
			"compileError":        app.CompileError,
		})
	}
	return items, nil
}

func (c *agentDashboardController) dashboardStatusFor(ctx context.Context, appID string) (devdash.AppStatus, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		records, err := c.store.ListAppSessions(ctx)
		if err != nil {
			return devdash.AppStatus{}, err
		}
		if len(records) == 0 {
			return devdash.AppStatus{}, sql.ErrNoRows
		}
		selected := c.appRecordWithRegistryLiveness(records[0])
		routeID := firstNonEmpty(selected.RouteID, selected.SessionID, selected.ID)
		if routeID != "" {
			if hydrated, err := c.store.GetAppSession(ctx, routeID); err == nil {
				selected = c.appRecordWithRegistryLiveness(hydrated)
			}
		}
		status := appRecordStatus(selected)
		status.Observability = observabilityStateFromVictoria(c.dashboardVictoria(), status.AppID, status.SessionID, status.AppRoot, nil)
		return status, nil
	}
	app, err := c.store.GetAppSession(ctx, appID)
	if err != nil {
		app, err = c.store.GetApp(ctx, appID)
		if err != nil {
			return devdash.AppStatus{}, err
		}
	}
	app = c.appRecordWithRegistryLiveness(app)
	status := appRecordStatus(app)
	status.Observability = observabilityStateFromVictoria(c.dashboardVictoria(), status.AppID, status.SessionID, status.AppRoot, nil)
	return status, nil
}

func (c *agentDashboardController) dashboardStore() *devdash.Store {
	return c.store
}

func (c *agentDashboardController) dashboardAuthorizeReport(req *http.Request, report devdash.ReportEnvelope) dashboardReportAuth {
	sessionID := strings.TrimSpace(report.SessionID)
	if sessionID == "" {
		return dashboardReportAuth{Reason: "missing-session"}
	}
	if c.agent == nil {
		return dashboardReportAuth{Reason: "agent-unavailable"}
	}
	session, ok := c.agent.GetSession(sessionID)
	if !ok || strings.TrimSpace(session.ReportToken) == "" {
		return dashboardReportAuth{Reason: "stale-session"}
	}
	if req.Header.Get("Authorization") != "Bearer "+session.ReportToken {
		return dashboardReportAuth{Reason: "invalid-report-token"}
	}
	return dashboardReportAuth{Authorized: true}
}

func (c *agentDashboardController) dashboardRootForApp(ctx context.Context, appID string) (string, error) {
	status, err := c.dashboardStatusFor(ctx, appID)
	if err != nil {
		return "", err
	}
	return status.AppRoot, nil
}

func (c *agentDashboardController) dashboardVictoria() dashboardVictoria {
	if c == nil || c.agent == nil {
		return nil
	}
	substrate, ok := c.agent.GetSubstrate(localagent.SubstrateVictoria)
	if !ok {
		return nil
	}
	key := substrate.UpdatedAt.Format(time.RFC3339Nano) + "|" + strings.Join(sortedStringMapValues(substrate.URLs), "|") + "|" + strings.Join(sortedStringMapValues(substrate.Endpoints), "|")
	c.victoriaMu.Lock()
	defer c.victoriaMu.Unlock()
	if c.victoria != nil && c.victoriaSubstrate == key {
		return c.victoria
	}
	c.victoria = victoriaStackFromSubstrate(substrate)
	c.victoriaSubstrate = key
	return c.victoria
}

func (c *agentDashboardController) appRecordWithRegistryLiveness(app devdash.AppRecord) devdash.AppRecord {
	if c == nil || c.agent == nil || strings.TrimSpace(app.SessionID) == "" {
		applySessionStatusToAppRecord(&app, nil)
		return app
	}
	session, ok := c.agent.GetSession(app.SessionID)
	if !ok {
		app.Running = false
		app.SessionStatus = "stale"
		app.SessionStatusReason = "session not found in agent registry"
		return app
	}
	applySessionStatusToAppRecord(&app, &session)
	app.Routes = visibleDashboardRoutesFromAgent(session.Routes)
	app.Aliases = visibleDashboardRoutesFromAgent(session.Aliases)
	return app
}

func sortedStringMapValues(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	items := make([]string, 0, len(values))
	for key, value := range values {
		items = append(items, key+"="+value)
	}
	sort.Strings(items)
	return items
}

func appRecordStatus(app devdash.AppRecord) devdash.AppStatus {
	routeID := firstNonEmpty(app.RouteID, app.SessionID, app.ID)
	status := devdash.AppStatus{
		Running:             app.Running,
		AppID:               routeID,
		BaseAppID:           firstNonEmpty(app.BaseAppID, app.ID),
		RuntimeAppID:        app.RuntimeAppID,
		SessionID:           app.SessionID,
		AppRoot:             app.Root,
		PID:                 app.PID,
		Meta:                app.Metadata,
		Addr:                app.ListenAddr,
		APIEncoding:         app.APIEncoding,
		Routes:              app.Routes,
		Aliases:             app.Aliases,
		SessionStatus:       app.SessionStatus,
		SessionStatusReason: app.SessionStatusReason,
		DashboardBundle:     dashboardBundleStatusPtr(),
		Compiling:           app.Compiling,
		CompileError:        app.CompileError,
	}
	applySessionStatusToAppStatus(&status, nil)
	status.Meta = metadataWithRuntimeSQLiteDatabases(status.Meta, status.AppRoot, status.SessionID, appcfg.Config{}, false)
	return status
}

func dashboardBundleStatusPtr() *devdash.DashboardBundle {
	status, err := dashboardBundleStatusForCurrentRepo()
	if err != nil || status.RunningHash == "" {
		return nil
	}
	return &status
}
