package main

import (
	"strings"
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

func hiddenDashboardRoute(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "victoria")
}
