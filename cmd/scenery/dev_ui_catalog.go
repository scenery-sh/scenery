package main

import (
	"context"
	"io/fs"
	"maps"
	"path/filepath"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/generate"
)

const (
	uiCatalogPollInterval   = time.Second
	uiCatalogSettleInterval = 300 * time.Millisecond
)

// startUICatalogDevSync watches the envs.local ui_catalog source directory
// and re-materializes the generated scenery-ui catalog into this app's
// TypeScript clients whenever it changes. Catalog edits cannot affect the Go
// binary, so the app process is never rebuilt or restarted: Vite dev servers
// pick the rewritten files up through their own watchers, and
// production-serve frontends are rebuilt in place. Sync failures (including
// staged TypeScript verification errors) keep the previous catalog serving.
func startUICatalogDevSync(ctx context.Context, console *runConsole, supervisor *devSupervisor, root, dir string, env app.ResolvedEnv) {
	go runUICatalogDevSync(ctx, console, supervisor, root, dir, env)
}

func runUICatalogDevSync(ctx context.Context, console *runConsole, supervisor *devSupervisor, root, dir string, env app.ResolvedEnv) {
	current, err := uiCatalogSnapshot(dir)
	if err != nil {
		consolePrintError(console, "ui catalog watch failed", err)
		return
	}
	// Converge immediately: the materialized catalog may predate live-dir
	// edits made while no session was running.
	syncUICatalog(ctx, console, supervisor, root, env)
	ticker := time.NewTicker(uiCatalogPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next, err := uiCatalogSnapshot(dir)
		if err != nil {
			consolePrintError(console, "ui catalog watch failed", err)
			continue
		}
		if maps.Equal(current, next) {
			continue
		}
		next, err = settleUICatalogSnapshot(ctx, dir, next)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			consolePrintError(console, "ui catalog watch failed", err)
			continue
		}
		current = next
		syncUICatalog(ctx, console, supervisor, root, env)
	}
}

// settleUICatalogSnapshot waits until two consecutive scans agree so a save
// burst (editor temp files, multi-file refactors) syncs once.
func settleUICatalogSnapshot(ctx context.Context, dir string, current map[string]uiCatalogStamp) (map[string]uiCatalogStamp, error) {
	timer := time.NewTimer(uiCatalogSettleInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		next, err := uiCatalogSnapshot(dir)
		if err != nil {
			return nil, err
		}
		if maps.Equal(current, next) {
			return next, nil
		}
		current = next
		timer.Reset(uiCatalogSettleInterval)
	}
}

func syncUICatalog(ctx context.Context, console *runConsole, supervisor *devSupervisor, root string, env app.ResolvedEnv) {
	if console != nil {
		console.Event("ui_catalog.sync", map[string]any{"root": root})
	}
	// GenerateTypeScriptClients covers source- and cache-materialized
	// clients; SyncCachedTypeScriptClients would silently skip the default
	// source materialization.
	result, err := generate.GenerateTypeScriptClients(root, "", false)
	if err != nil {
		consolePrintError(console, "ui catalog sync failed", err)
		return
	}
	if console != nil {
		console.Event("ui_catalog.synced", map[string]any{"changed": result.Changed})
		if !console.json && len(result.Changed) > 0 {
			console.printSetupDone("ui catalog synced")
		}
	}
	if len(result.Changed) > 0 {
		supervisor.RebuildProductionFrontends(ctx, productionFrontendNames(env))
	}
}

func consolePrintError(console *runConsole, message string, err error) {
	if console != nil {
		console.printError(message, err)
	}
}

type uiCatalogStamp struct {
	size    int64
	modTime int64
}

func uiCatalogSnapshot(dir string) (map[string]uiCatalogStamp, error) {
	snapshot := map[string]uiCatalogStamp{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Tolerate entries vanishing mid-scan; only a missing walk root
			// is a real failure.
			if path == dir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		snapshot[filepath.ToSlash(rel)] = uiCatalogStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
		return nil
	})
	return snapshot, err
}
