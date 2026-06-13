package main

import (
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
)

func dbPostgresService(cfg appcfg.Config) appcfg.DevServiceConfig {
	_, svc, _ := managedPostgresDeclared(cfg)
	return svc
}

func dbDatabaseURLEnv(cfg appcfg.Config) string {
	return cfg.DatabaseURLEnv()
}

func cloneDBBranchEndpoint(endpoint *dbBranchEndpoint) *dbBranchEndpoint {
	if endpoint == nil {
		return nil
	}
	out := *endpoint
	return &out
}

func inspectAppRef(appRoot string, cfg appcfg.Config) inspectdata.AppRef {
	return inspectdata.AppRef{
		Name:       cfg.Name,
		ID:         cfg.ID,
		Root:       appRoot,
		ConfigPath: filepath.Join(appRoot, ".scenery.json"),
	}
}

func postgresServiceUsesBranching(svc appcfg.DevServiceConfig) bool {
	if firstNonEmpty(strings.TrimSpace(svc.Kind), "postgres") != "postgres" {
		return false
	}
	return strings.TrimSpace(svc.BranchPolicy) != "" ||
		strings.TrimSpace(svc.BranchNameTemplate) != "" ||
		strings.TrimSpace(svc.BranchStrategy) != "" ||
		strings.TrimSpace(svc.ParentBranch) != "" ||
		strings.TrimSpace(svc.ParentDatabase) != ""
}
