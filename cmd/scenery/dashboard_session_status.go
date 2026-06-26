package main

import (
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func applySessionStatusToAppRecord(app *devdash.AppRecord, session *localagent.Session) {
	if app == nil {
		return
	}
	if !dashboardSessionMatches(app.SessionID, session) {
		session = nil
	}
	if session == nil {
		if strings.TrimSpace(app.SessionStatus) == "" {
			app.SessionStatus = fallbackDashboardSessionStatus(app.Running, app.CompileError)
		}
		if !sessionStatusHealthy(app.SessionStatus) {
			app.Running = false
		}
		return
	}
	status, reason := effectiveSessionStatus(*session)
	if strings.TrimSpace(status) == "" {
		status = fallbackDashboardSessionStatus(app.Running, app.CompileError)
	}
	app.SessionStatus = status
	app.SessionStatusReason = reason
	if !sessionStatusHealthy(status) {
		app.Running = false
	}
}

func applySessionStatusToAppStatus(status *devdash.AppStatus, session *localagent.Session) {
	if status == nil {
		return
	}
	if !dashboardSessionMatches(status.SessionID, session) {
		session = nil
	}
	if session == nil {
		if strings.TrimSpace(status.SessionStatus) == "" {
			status.SessionStatus = fallbackDashboardSessionStatus(status.Running, status.CompileError)
		}
		if !sessionStatusHealthy(status.SessionStatus) {
			status.Running = false
		}
		return
	}
	next, reason := effectiveSessionStatus(*session)
	if strings.TrimSpace(next) == "" {
		next = fallbackDashboardSessionStatus(status.Running, status.CompileError)
	}
	status.SessionStatus = next
	status.SessionStatusReason = reason
	if !sessionStatusHealthy(next) {
		status.Running = false
	}
}

func dashboardSessionMatches(sessionID string, session *localagent.Session) bool {
	if session == nil {
		return true
	}
	want := strings.TrimSpace(sessionID)
	got := strings.TrimSpace(session.SessionID)
	return want == "" || (got != "" && want == got)
}

func fallbackDashboardSessionStatus(running bool, compileError string) string {
	if strings.TrimSpace(compileError) != "" {
		return "compile-error"
	}
	if running {
		return "running"
	}
	return "stopped"
}
