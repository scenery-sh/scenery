package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
)

type worktreePrunedResource struct {
	AppRoot    string `json:"app_root"`
	Scope      string `json:"scope"`
	ResourceID string `json:"resource_id"`
	Container  string `json:"container"`
	Volume     string `json:"volume"`
}

func runWorktreePrune(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parsePruneArgs(args)
	if err != nil {
		return err
	}
	if opts.All {
		opts.DB, opts.State = true, true
	}
	if opts.DB && (opts.AppRoot == "" || !filepath.IsAbs(opts.AppRoot)) {
		return fmt.Errorf("database prune requires one explicit absolute --app-root; its scope is the entire retained worktree cluster, container and volume")
	}
	if opts.DB {
		if err := requireManagedDatabaseSelection(opts.AppRoot); err != nil {
			return err
		}
	}
	entries, err := inspectWorktreeOwners(ctx, opts.AppRoot)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-opts.OlderThan)
	response := pruneResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.prune"), Cutoff: cutoff.Format(time.RFC3339Nano), Pruned: []string{}, Skipped: []string{}, DBCleanup: opts.DB, StateCleanup: opts.State, Resources: []worktreePrunedResource{}}
	for _, entry := range entries {
		if entry.Status != "stopped" && entry.Status != "orphaned" {
			if opts.DB && entry.Status != "absent" {
				return fmt.Errorf("database prune requires a compatible stopped worktree without a live owner or restore operation")
			}
			response.Skipped = append(response.Skipped, firstNonEmpty(entry.AppRoot, entry.Key))
			continue
		}
		paths, err := commandWorktreePaths(entry.AppRoot)
		if err != nil {
			return err
		}
		pruned, resource, err := pruneStoppedWorktree(ctx, paths, cutoff, opts)
		if err != nil {
			return err
		}
		response.Pruned = append(response.Pruned, pruned...)
		if resource != nil {
			response.Resources = append(response.Resources, *resource)
		}
		if len(pruned) == 0 && resource == nil {
			response.Skipped = append(response.Skipped, entry.AppRoot)
		}
	}
	if opts.JSON {
		return writeCLIJSON(stdout, response)
	}
	for _, resource := range response.Resources {
		_, _ = fmt.Fprintf(stdout, "removed worktree cluster for %s: container %s, volume %s\n", resource.AppRoot, resource.Container, resource.Volume)
	}
	_, err = fmt.Fprintf(stdout, "pruned %d disposable session records; skipped %d worktrees\n", len(response.Pruned), len(response.Skipped))
	return err
}

func pruneStoppedWorktree(ctx context.Context, paths localagent.WorktreePaths, cutoff time.Time, opts pruneOptions) ([]string, *worktreePrunedResource, error) {
	live, err := paths.AcquireLiveLock()
	if err != nil {
		return nil, nil, fmt.Errorf("worktree became active before prune; no active owner was replaced: %w", err)
	}
	defer func() { _ = live.Release() }()
	record, err := paths.LoadRecord("")
	if err != nil {
		return nil, nil, err
	}
	if record.UpdatedAt.After(cutoff) {
		return nil, nil, nil
	}
	registry, err := paths.OpenRegistry(record.RouterAddress)
	if err != nil {
		return nil, nil, err
	}
	sessions := registry.List()
	for _, session := range sessions {
		if session.AppRoot != paths.AppRoot || session.BaseAppID != record.AppID {
			return nil, nil, fmt.Errorf("retained session does not belong to the selected worktree")
		}
		if sessionOwnerLive(session) {
			return nil, nil, fmt.Errorf("a verified runtime process prevents worktree prune")
		}
		for _, process := range session.Processes {
			if process.PID == process.Owner.PID && localagent.VerifyOwner(process.Owner) == nil {
				return nil, nil, fmt.Errorf("a verified application child still runs; use down before prune")
			}
		}
	}
	var resource *worktreePrunedResource
	if opts.DB && record.Postgres != nil {
		env, err := appEnvWithDotEnv(envpolicy.Environ(), paths.AppRoot)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		if lookupEnvValue(env, appDatabaseURLEnv) != "" {
			return nil, nil, worktreePostgresPrecondition("DATABASE_URL is external; refusing database prune")
		}
		resolver, err := newWorktreePostgresResolver(ctx, paths.AppRoot, record.AppID)
		if err != nil {
			return nil, nil, err
		}
		p := record.Postgres
		resource = &worktreePrunedResource{AppRoot: paths.AppRoot, Scope: "worktree-cluster", ResourceID: p.InstanceID, Container: p.Container, Volume: p.Volume}
		if err := resolver.remove(ctx); err != nil {
			return nil, nil, err
		}
	}
	var pruned []string
	for _, session := range sessions {
		if !pruneSessionEligible(session, cutoff) {
			continue
		}
		if opts.State && session.StateRoot != "" {
			expected := filepath.Join(paths.AppRoot, ".scenery", "sessions", session.SessionID)
			if filepath.Clean(session.StateRoot) != expected {
				return nil, resource, fmt.Errorf("refusing disposable-state removal outside the selected worktree session")
			}
			if err := os.RemoveAll(expected); err != nil {
				return nil, resource, err
			}
		}
		if _, _, err := registry.Delete(session.SessionID); err != nil {
			return nil, resource, err
		}
		pruned = append(pruned, session.SessionID)
	}
	return pruned, resource, nil
}
