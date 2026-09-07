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
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

func runWorktreeDown(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseDownArgs(args)
	if err != nil {
		return err
	}
	root, err := resolveStatusAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return err
	}
	if opts.All {
		opts.DB, opts.State = true, true
	}
	if opts.DB {
		if err := requireManagedDatabaseSelection(paths.AppRoot); err != nil {
			return err
		}
	}
	response := downResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.down"), AppRoot: paths.AppRoot, DBCleanup: opts.DB, StateCleanup: opts.State}
	record, err := paths.LoadRecord("")
	if errors.Is(err, os.ErrNotExist) {
		response.Messages = []string{"no retained worktree runtime or managed database found"}
		return writeWorktreeDownResult(stdout, opts.JSON, response)
	}
	if err != nil {
		return err
	}
	before, err := paths.OpenRegistry(record.RouterAddress)
	if err != nil {
		return err
	}
	beforeSessions := before.List()
	stopCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	live, err := acquireStoppedWorktree(stopCtx, paths)
	if err != nil {
		return err
	}
	defer func() { _ = live.Release() }()
	registry, err := paths.OpenRegistry(record.RouterAddress)
	if err != nil {
		return err
	}
	sessions := registry.List()
	seen := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		seen[session.SessionID] = true
	}
	for _, session := range beforeSessions {
		if !seen[session.SessionID] {
			sessions = append(sessions, session)
		}
	}
	for _, session := range sessions {
		if session.AppRoot != paths.AppRoot || session.BaseAppID != record.AppID {
			return fmt.Errorf("worktree session ownership differs from the selected retained root")
		}
		for _, process := range session.Processes {
			if process.PID != process.Owner.PID {
				return fmt.Errorf("retained worktree child has an inconsistent owner fingerprint")
			}
			if err := stopVerifiedWorktreeProcess(stopCtx, process.Owner); err != nil {
				return err
			}
		}
		if opts.State {
			expected := filepath.Join(paths.AppRoot, ".scenery", "sessions", session.SessionID)
			if session.StateRoot != "" && filepath.Clean(session.StateRoot) != expected {
				return fmt.Errorf("refusing disposable-state removal outside the selected worktree session")
			}
			if session.StateRoot != "" {
				if err := os.RemoveAll(expected); err != nil {
					return err
				}
				response.StateRootRemoved = expected
			}
		}
		if _, _, err := registry.Delete(session.SessionID); err != nil {
			return err
		}
		response.Deleted = true
	}
	if record.Postgres != nil {
		resolver, err := newWorktreePostgresResolver(stopCtx, paths.AppRoot, record.AppID)
		if err != nil {
			return err
		}
		if opts.DB {
			if dropErr := dropRetainedWorktreeAppDatabase(stopCtx, resolver); dropErr != nil {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
				stopErr := resolver.stop(cleanupCtx)
				cleanupCancel()
				return errors.Join(dropErr, stopErr)
			}
			response.Messages = append(response.Messages, "dropped only the selected app database; retained cluster and credentials")
		}
		if err := resolver.stop(stopCtx); err != nil {
			return err
		}
	}
	response.RecordPreserved = true
	response.Messages = append(response.Messages, "stopped selected worktree runtime and managed PostgreSQL; retained durable ownership and data")
	return writeWorktreeDownResult(stdout, opts.JSON, response)
}

func writeWorktreeDownResult(stdout io.Writer, json bool, response downResponse) error {
	if json {
		return writeDownJSON(stdout, response)
	}
	for _, message := range response.Messages {
		if _, err := fmt.Fprintln(stdout, message); err != nil {
			return err
		}
	}
	return nil
}

// Signal only a compatible live owner, then acquire its lifetime lock before
// offline cleanup. A competing new owner is never replaced during this wait.
func acquireStoppedWorktree(ctx context.Context, paths localagent.WorktreePaths) (*localagent.ProcessLock, error) {
	lock, err := paths.AcquireLiveLock()
	if !errors.Is(err, localagent.ErrProcessLocked) {
		return lock, err
	}
	client, err := commandWorktreeClient(ctx, paths.AppRoot)
	if err != nil {
		return nil, err
	}
	sessions, err := client.List(ctx, paths.AppRoot)
	if err != nil {
		return nil, err
	}
	var owner localagent.Owner
	for _, session := range sessions {
		if session.AppRoot == paths.AppRoot && session.Owner.PID == session.OwnerPID && localagent.VerifyOwner(session.Owner) == nil {
			owner = session.Owner
			break
		}
	}
	if owner.PID <= 0 || owner.PID == os.Getpid() {
		return nil, fmt.Errorf("the worktree owner is not yet registered or cannot be verified; retry down after registration")
	}
	process, err := os.FindProcess(owner.PID)
	if err != nil {
		return nil, err
	}
	if err := localagent.VerifyOwner(owner); err != nil {
		return nil, err
	}
	if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return nil, err
	}
	for {
		lock, err := paths.AcquireLiveLock()
		if !errors.Is(err, localagent.ErrProcessLocked) {
			return lock, err
		}
		if localagent.VerifyOwner(owner) != nil {
			return nil, fmt.Errorf("the original worktree owner exited but another operation holds its lock; no replacement owner was signaled")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("worktree owner did not finish orderly shutdown: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func stopVerifiedWorktreeProcess(ctx context.Context, owner localagent.Owner) error {
	if owner.PID <= 0 || owner.PID == os.Getpid() || localagent.VerifyOwner(owner) != nil {
		return nil
	}
	process, err := os.FindProcess(owner.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if waitForPIDExit(ctx, owner.PID, 5*time.Second) || localagent.VerifyOwner(owner) != nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if !waitForPIDExit(ctx, owner.PID, time.Second) {
		return fmt.Errorf("verified worktree child did not stop")
	}
	return nil
}

// Caller holds the live lock. Never allocate a cluster just to drop an app DB.
func dropRetainedWorktreeAppDatabase(ctx context.Context, resolver worktreePostgresResolver) error {
	env, err := appEnvWithDotEnv(envpolicy.Environ(), resolver.paths.AppRoot)
	if err != nil {
		return err
	}
	if lookupEnvValue(env, appDatabaseURLEnv) != "" {
		return worktreePostgresPrecondition("DATABASE_URL is external; refusing managed app-database deletion")
	}
	op, err := resolver.beginOperation()
	if err != nil {
		return err
	}
	defer func() { _ = op.Close() }()
	server, err := resolver.ensureWithOperation(ctx, op, false)
	if err != nil {
		return err
	}
	admin, err := openPostgresAdmin(ctx, worktreePostgresURL(server, "postgres"))
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()
	return postgresdb.DropDatabase(ctx, admin, postgresname.DatabaseNameFor(resolver.appID, resolver.paths.AppRoot))
}
