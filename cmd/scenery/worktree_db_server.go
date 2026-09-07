package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

func commandWorktreePaths(root string) (localagent.WorktreePaths, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return localagent.WorktreePaths{}, err
	}
	return localagent.PathsForWorktree(paths.Home, root)
}

func runWorktreeDBServer(ctx context.Context, stdout io.Writer, opts dbServerOptions) error {
	root, err := resolveStatusAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return err
	}
	root = paths.AppRoot
	// External capability selection takes precedence over retained managed data.
	// Status remains read-only and does not require a working Docker daemon.
	if _, cfg, discoverErr := discoverConfiguredApp(root); discoverErr == nil {
		env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
		if err != nil {
			return err
		}
		if value := lookupEnvValue(env, appDatabaseURLEnv); value != "" && len(cfg.DatabaseServices()) > 0 {
			if err := validateAppPostgresURL(value); err != nil {
				return err
			}
			if opts.Action != "status" {
				return worktreePostgresPrecondition("DATABASE_URL is externally owned; db server does not control it")
			}
			status := dbServerStatusResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.server.status"), AppRoot: root, Scope: "external", Status: "external", URL: postgresdb.RedactURL(value)}
			if opts.JSON {
				return writeInspectJSON(stdout, status)
			}
			_, err = fmt.Fprintf(stdout, "%s\texternal\texternal\n", root)
			return err
		}
	}
	if opts.Action == "status" {
		status, err := worktreeDBServerStatus(ctx, paths)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, status)
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", root, status.Scope, status.Status)
		return nil
	}
	if opts.Action != "start" && opts.Action != "stop" && opts.Action != "logs" {
		return fmt.Errorf("unknown db server command %q", opts.Action)
	}
	appID := ""
	if opts.Action == "start" {
		_, cfg, err := discoverConfiguredApp(root)
		if err != nil {
			return err
		}
		appID = cfg.AppID()
		if len(cfg.DatabaseServices()) == 0 {
			return worktreePostgresPrecondition("the selected app declares no managed SQL capability")
		}
		env, err := appEnvWithDotEnv(envpolicy.Environ(), root)
		if err != nil {
			return err
		}
		if lookupEnvValue(env, appDatabaseURLEnv) != "" {
			return worktreePostgresPrecondition("DATABASE_URL is externally owned; db server does not provision or control it")
		}
	}
	resolver, err := newWorktreePostgresResolver(ctx, root, appID)
	if err != nil {
		return err
	}
	if opts.Action == "logs" {
		record, err := resolver.load()
		if err != nil {
			return err
		}
		_, container, err := resolver.inspect(ctx, record)
		if err != nil {
			return err
		}
		if container == nil {
			return worktreePostgresPrecondition("the selected worktree has no existing database container")
		}
		docker := resolver.docker.(*worktreeDockerClient)
		out, err := docker.run(ctx, "logs", "--tail", "200", container.ID)
		if err != nil {
			return err
		}
		// PostgreSQL logs are diagnostic content, never ownership evidence.
		out = strings.ReplaceAll(out, record.Postgres.Password, "[REDACTED]")
		_, err = fmt.Fprintln(stdout, out)
		return err
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		return worktreePostgresPrecondition("a live worktree owner or restore operation prevents standalone database lifecycle changes; use down first")
	}
	defer func() { _ = live.Release() }()
	if opts.Action == "start" {
		if _, err := resolver.ensure(ctx); err != nil {
			return err
		}
		status, err := worktreeDBServerStatus(ctx, paths)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writeInspectJSON(stdout, status)
		}
		_, _ = fmt.Fprintln(stdout, "started selected worktree PostgreSQL; application workers remain stopped")
		return nil
	}
	if err := resolver.stop(ctx); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, withCLIPayloadIdentity("scenery.db.server.stop", map[string]any{"ok": true, "app_root": root, "scope": "worktree", "data_preserved": true}))
	}
	_, _ = fmt.Fprintln(stdout, "stopped selected worktree PostgreSQL; data preserved")
	return nil
}

func worktreeDBServerStatus(ctx context.Context, paths localagent.WorktreePaths) (dbServerStatusResponse, error) {
	response := dbServerStatusResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.db.server.status"), AppRoot: paths.AppRoot, Scope: "worktree", Status: "absent", StatePath: paths.Record}
	record, err := paths.LoadRecord("")
	if errors.Is(err, os.ErrNotExist) || (err == nil && record.Postgres == nil) {
		return response, nil
	}
	if err != nil {
		return response, err
	}
	p := record.Postgres
	response.Container, response.Image, response.ResourceID = p.Container, p.Image, p.InstanceID
	response.Status, response.Retained = p.Phase, p.VolumeCreatedAt != ""
	if p.Restore != nil {
		response.Restore = &dbServerRestoreStatus{ArchiveSHA256: p.Restore.ArchiveSHA256, ArchivePath: worktreeRestoreArchivePath(paths, p.Restore.ArchiveSHA256), Mode: p.Restore.Mode, SQLStarted: p.Restore.SQLStarted}
	}
	resolver, err := newWorktreePostgresResolver(ctx, paths.AppRoot, record.AppID)
	if err != nil {
		return response, err
	}
	_, container, err := resolver.inspect(ctx, record)
	if err != nil {
		return response, err
	}
	if container == nil {
		response.Status = "container-missing"
		if p.Restore != nil {
			response.Status = p.Phase
		}
		return response, nil
	}
	response.Status = "stopped"
	if p.Restore != nil {
		response.Status = p.Phase
	}
	if container.Running {
		response.Status, response.Port = "running", container.Port
		observed := *p
		observed.Port = container.Port
		response.URL = postgresdb.RedactURL(worktreePostgresURL(&observed, "postgres"))
		if p.Restore != nil || p.Phase == "restoring" || p.Phase == "restore-failed" || p.Phase == "deleting" {
			response.Status = p.Phase
			return response, nil
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		systemID, err := resolver.probe(probeCtx, &observed)
		cancel()
		if err != nil {
			response.Status = "unavailable"
			return response, nil
		}
		if p.SystemID == "" || systemID != p.SystemID {
			return response, worktreePostgresPrecondition("the authenticated database does not match retained identity")
		}
		response.OK, response.Status = true, "ready"
	}
	return response, nil
}
