package main

import (
	"fmt"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func devRoutingMode(cfg app.Config) (localagent.RouteMode, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Dev.Routing.Mode)) {
	case "", string(localagent.RouteModePath):
		return localagent.RouteModePath, nil
	case string(localagent.RouteModeHost):
		return localagent.RouteModeHost, nil
	default:
		return "", fmt.Errorf("dev.routing.mode must be \"path\" or \"host\"")
	}
}

func pathRouteManifestForLease(lease localagent.PortLease) localagent.RouteManifest {
	return localagent.RouteManifest{
		ArtifactIdentity: localagent.NewRouteManifestIdentity(),
		Mode:             localagent.RouteModePath,
		BaseURL:          lease.URL,
		PortLease:        &lease,
	}
}
