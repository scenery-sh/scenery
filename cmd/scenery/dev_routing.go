package main

import (
	"strings"

	localagent "scenery.sh/internal/agent"
)

func pathRouteManifestForLease(lease localagent.PortLease, domainHost string, rootFrontend string) localagent.RouteManifest {
	manifest := localagent.RouteManifest{
		ArtifactIdentity: localagent.NewRouteManifestIdentity(),
		Mode:             localagent.RouteModePath,
		BaseURL:          lease.URL,
		DomainHost:       domainHost,
		PortLease:        &lease,
	}
	if rootFrontend = localagentLabel(rootFrontend); rootFrontend != "" {
		manifest.Root = rootFrontend
		manifest.Routes = map[string]localagent.RouteRecord{
			"root": {
				Name:    "root",
				Kind:    "frontend",
				URL:     strings.TrimRight(lease.URL, "/") + "/",
				Path:    "/",
				Backend: rootFrontend,
			},
		}
	}
	return manifest
}
