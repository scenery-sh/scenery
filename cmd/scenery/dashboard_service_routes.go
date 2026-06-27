package main

import (
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/localproxy"
)

func visibleDashboardRoutesFromAgent(routes map[string]string) map[string]string {
	if len(routes) == 0 {
		return nil
	}
	visible := make(map[string]string, len(routes))
	for name, rawURL := range routes {
		name = strings.TrimSpace(name)
		rawURL = strings.TrimSpace(rawURL)
		if name == "" || rawURL == "" || hiddenDashboardRoute(name) {
			continue
		}
		visible[name] = rawURL
	}
	if len(visible) == 0 {
		return nil
	}
	return visible
}

func visibleDashboardRoutesFromProxy(routes localproxy.Routes, appID string) map[string]string {
	visible := map[string]string{}
	add := func(name, rawURL string) {
		name = strings.TrimSpace(name)
		rawURL = strings.TrimSpace(rawURL)
		if name == "" || rawURL == "" || hiddenDashboardRoute(name) {
			return
		}
		visible[name] = rawURL
	}
	add(localagent.RouteAPI, routes.APIURL)
	add(localagent.RouteDashboard, localproxy.ConsoleAppURL(routes, appID))
	for name, frontend := range routes.Frontends {
		add(name, frontend.URL)
	}
	if len(visible) == 0 {
		return nil
	}
	return visible
}

func hiddenDashboardRoute(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "victoria")
}
