package main

import (
	"fmt"
	"sort"

	"scenery.sh/internal/app"
)

func devRouteConfigDiagnostics(cfg app.Config) []checkDiagnostic {
	mode, err := devRoutingMode(cfg)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "config",
			Severity:        "error",
			Message:         err.Error(),
			SuggestedAction: "Use dev.routing.mode \"path\" or \"host\".",
		}}
	}
	if mode != "path" {
		return nil
	}
	reserved := map[string]string{
		"api":       "the API route",
		"dashboard": "the dev dashboard route",
		"console":   "the dev console route",
		"root":      "the local route index",
		"runtime":   "the local runtime route prefix",
		"sync":      "the realtime sync route",
		"__scenery": "the legacy internal Scenery route prefix",
	}
	seen := map[string]string{}
	names := make([]string, 0, len(cfg.Proxy.Frontends))
	for name := range cfg.Proxy.Frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	var diagnostics []checkDiagnostic
	for _, name := range names {
		route := localagentLabel(name)
		if route == "" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "config",
				Severity:        "error",
				Message:         fmt.Sprintf("proxy.frontends.%s has no valid route name after normalization", name),
				SuggestedAction: "Rename the frontend with letters or digits.",
			})
			continue
		}
		if reason := reserved[route]; reason != "" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "config",
				Severity:        "error",
				Message:         fmt.Sprintf("proxy.frontends.%s normalizes to reserved path route %q used by %s", name, route, reason),
				SuggestedAction: "Rename the frontend or use dev.routing.mode \"host\".",
			})
			continue
		}
		if previous := seen[route]; previous != "" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "config",
				Severity:        "error",
				Message:         fmt.Sprintf("proxy.frontends.%s and proxy.frontends.%s both normalize to path route %q", previous, name, route),
				SuggestedAction: "Rename one frontend so path-mode routes are unique.",
			})
			continue
		}
		seen[route] = name
	}
	return diagnostics
}
