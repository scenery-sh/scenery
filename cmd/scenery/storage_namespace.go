package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/storagefs"
)

type storageNamespacePlan struct {
	Worktree   localagent.WorktreePaths
	Binding    storagefs.Binding
	Root       string
	LegacyRoot string
}

// Resolution is pure: even an orphaned canonical root retains its namespace
// identity. Discovery and allocation are deliberately separate operations.
func resolveStorageNamespacePlan(cfg app.Config, appRoot, agentHome string) (*storageNamespacePlan, error) {
	var paths localagent.Paths
	if agentHome != "" {
		paths = localagent.PathsForHome(agentHome)
	} else {
		var err error
		paths, err = commandAgentPaths()
		if err != nil {
			return nil, err
		}
	}
	worktree, err := localagent.PathsForWorktree(paths.Home, appRoot)
	if err != nil {
		return nil, err
	}
	return &storageNamespacePlan{
		Worktree:   worktree,
		Binding:    storagefs.Binding{AppID: cfg.AppID(), AppRoot: worktree.AppRoot, WorktreeKey: worktree.Key, UserID: os.Getuid(), Managed: true},
		Root:       filepath.Join(worktree.Directory, "storage"),
		LegacyRoot: filepath.Join(paths.AgentDir, "storage", legacyStorageSlug(cfg.AppID())),
	}, nil
}

// This is detection-only provenance for the former derived location. Runtime
// never reads payloads here; explicitly named old cells belong to the exporter.
func legacyStorageSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	if slug := strings.Trim(b.String(), "-"); slug != "" {
		return slug
	}
	return "app"
}

func retainedStorageNamespacePlan(root string) (*storageNamespacePlan, error) {
	paths, err := commandWorktreePaths(root)
	if err != nil {
		return nil, err
	}
	record, err := paths.LoadRecord("")
	if err != nil {
		return nil, err
	}
	return &storageNamespacePlan{Worktree: paths, Binding: storagefs.Binding{AppID: record.AppID, AppRoot: paths.AppRoot, WorktreeKey: paths.Key, UserID: record.UserID, Managed: true}, Root: filepath.Join(paths.Directory, "storage")}, nil
}

func (p *storageNamespacePlan) discover(ctx context.Context) (storagefs.Owner, error) {
	_, recordErr := p.Worktree.LoadRecord(p.Binding.AppID)
	if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
		return storagefs.Owner{}, recordErr
	}
	owner, err := storagefs.Discover(ctx, p.Root, p.Binding)
	if err == nil && recordErr != nil {
		return storagefs.Owner{}, fmt.Errorf("%w: storage has no retained worktree authority", storagefs.ErrOwnership)
	}
	return owner, err
}

func (p *storageNamespacePlan) allocate(ctx context.Context) (*storagefs.Namespace, error) {
	// A legacy cell is evidence even when it is empty or malformed. Runtime
	// startup cannot turn migration failure into an apparently empty app.
	owner, err := p.discover(ctx)
	if err == nil {
		// Ordinary writes use namespace/object leases, not the worktree's
		// allocation lock. Restore and purge still block through CheckReady
		// and each operation's namespace lease.
		n, err := storagefs.Bind(p.Root, p.Binding, owner.Incarnation)
		if err != nil {
			return nil, err
		}
		if err := n.CheckReady(ctx); err != nil {
			return nil, err
		}
		return n, nil
	}
	if err != nil && !errors.Is(err, storagefs.ErrUninitialized) {
		return nil, err
	}
	if errors.Is(err, storagefs.ErrUninitialized) {
		if _, err := os.Lstat(p.LegacyRoot); err == nil {
			return nil, storagefs.ErrMigration
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		// Establish the stable lifetime inode during allocation, never during
		// later read-only capture or purge preview. Do not stop a live owner.
		live, err := p.Worktree.AcquireLiveLock()
		if err == nil {
			if err := live.Release(); err != nil {
				return nil, err
			}
		} else if !errors.Is(err, localagent.ErrProcessLocked) {
			return nil, err
		}
	}
	var op *localagent.WorktreeOperation
	for {
		op, err = p.Worktree.BeginOperation()
		if !errors.Is(err, localagent.ErrProcessLocked) {
			break
		}
		// Concurrent first writers serialize allocation, then evaluate their
		// object preconditions against the one published namespace.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = op.Close() }()
	return p.allocateHeld(ctx, op)
}

// allocateHeld is also used by verified snapshot import while it already holds
// worktree operation ownership. It never reacquires that lock or allocates SQL.
func (p *storageNamespacePlan) allocateHeld(ctx context.Context, op *localagent.WorktreeOperation) (*storagefs.Namespace, error) {
	if _, err := p.Worktree.LoadRecord(p.Binding.AppID); errors.Is(err, os.ErrNotExist) {
		if err := op.SaveRecord(localagent.NewWorktreeRecord(p.Worktree, p.Binding.AppID)); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return storagefs.Allocate(ctx, p.Root, p.Binding)
}
