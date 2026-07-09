package main

import (
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devdash"
)

func observabilityStateFromVictoria(victoria dashboardVictoria, appID, sessionID, appRoot string, session *localagent.Session) *devdash.ObservabilityState {
	state := &devdash.ObservabilityState{
		Enabled: true,
		Backend: "victoria",
		Scope: &devdash.ObservabilityScope{
			AppID:       strings.TrimSpace(appID),
			SessionID:   strings.TrimSpace(sessionID),
			AppRootHash: appRootHash(appRoot),
		},
	}
	if session != nil {
		state.Scope.Branch = strings.TrimSpace(session.Branch)
	}
	if victoria == nil {
		unavailable := devdash.ObservabilityBackendState{Enabled: true, Status: "unavailable", Message: "Victoria backend is unavailable"}
		state.Metrics = unavailable
		state.Logs = unavailable
		state.Traces = unavailable
		state.Message = "Victoria observability backends are unavailable."
		return state
	}
	urls := victoria.URLs()
	state.Metrics = observabilityBackend(urls["metrics"], "/prometheus/api/v1/query", "PromQL/MetricsQL")
	state.Logs = observabilityBackend(urls["logs"], "/select/logsql/query", "LogsQL")
	state.Traces = observabilityBackend(urls["traces"], "/select/jaeger/api/traces", "Jaeger query API")
	return state
}

func observabilityBackend(rawURL, queryPath, dialect string) devdash.ObservabilityBackendState {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return devdash.ObservabilityBackendState{
			Enabled: true,
			Status:  "unavailable",
			Dialect: dialect,
			Message: "Victoria backend URL is unavailable",
		}
	}
	return devdash.ObservabilityBackendState{
		Enabled:   true,
		Available: true,
		Status:    "ready",
		URL:       rawURL,
		QueryPath: queryPath,
		Dialect:   dialect,
	}
}
