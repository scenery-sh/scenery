package main

import (
	"strings"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
)

func dbSQLiteService(cfg appcfg.Config) appcfg.DevServiceConfig {
	_, svc, _ := managedSQLiteDeclared(cfg)
	return svc
}

func managedSQLiteDeclared(cfg appcfg.Config) (string, appcfg.DevServiceConfig, bool) {
	for _, svc := range cfg.SQLiteServices() {
		return svc.Name, svc.Raw, true
	}
	return "", appcfg.DevServiceConfig{}, false
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
		ConfigPath: cfg.SourcePath(appRoot),
	}
}

func sqliteServiceUsesBranching(svc appcfg.DevServiceConfig) bool {
	if firstNonEmpty(strings.TrimSpace(svc.Kind), "sqlite") != "sqlite" {
		return false
	}
	return true
}
