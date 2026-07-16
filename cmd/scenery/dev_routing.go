package main

import (
	"fmt"
	"sort"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func devRoutingMode(env app.ResolvedEnv) (localagent.RouteMode, error) {
	switch strings.ToLower(strings.TrimSpace(env.Mode)) {
	case "", string(localagent.RouteModePath):
		return localagent.RouteModePath, nil
	case string(localagent.RouteModeHost):
		if strings.TrimSpace(env.Domain) != "" {
			return "", fmt.Errorf("envs.%s.domain applies to path mode; remove it or use envs.%s.mode \"path\"", env.Name, env.Name)
		}
		return localagent.RouteModeHost, nil
	default:
		return "", fmt.Errorf("envs.%s.mode must be \"path\" or \"host\"", env.Name)
	}
}

func pathRouteManifestForLease(lease localagent.PortLease, domainHost string, publicRoutes []string) localagent.RouteManifest {
	return localagent.RouteManifest{
		ArtifactIdentity: localagent.NewRouteManifestIdentity(),
		Mode:             localagent.RouteModePath,
		BaseURL:          lease.URL,
		DomainHost:       domainHost,
		PublicRoutes:     publicRoutes,
		PortLease:        &lease,
	}
}

// devExposeRouteNames validates dev.routing.expose against the routes this
// app can actually serve and returns the canonical route names carried on
// the manifest as public_routes. Nil means no narrowing (full surface).
func devExposeRouteNames(cfg app.Config, env app.ResolvedEnv) ([]string, error) {
	entries := env.Expose
	if len(entries) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(env.Domain) == "" {
		return nil, fmt.Errorf("envs.%s.expose requires envs.%s.domain", env.Name, env.Name)
	}
	valid := map[string]bool{
		"root":                    true,
		localagent.RouteAPI:       true,
		localagent.RouteDashboard: true,
		"runtime":                 true,
	}
	for name := range cfg.Frontends {
		if label := sanitizeRouteLabel(name); label != "" {
			valid[label] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range entries {
		name := sanitizeRouteLabel(raw)
		if name == "console" {
			name = localagent.RouteDashboard
		}
		if name == "" || !valid[name] {
			return nil, fmt.Errorf("envs.%s.expose entry %q is not root, api, console, runtime, or a configured frontend name", env.Name, raw)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
