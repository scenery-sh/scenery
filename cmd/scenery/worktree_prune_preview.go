package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
)

// Inventory is observation-only, including invalid retained directories. Bytes
// are logical file bytes; volumes and inaccessible contents remain unmeasured.
type worktreePruneInventory struct {
	AppRoot     string `json:"app_root"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Bytes       int64  `json:"bytes"`
	Complete    bool   `json:"complete"`
	Reclaimable bool   `json:"reclaimable"`
	Reason      string `json:"reason"`
}

func previewWorktreePrune(entries []worktreeStatusEntry, cutoff time.Time, opts pruneOptions) ([]worktreePruneInventory, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	result := make([]worktreePruneInventory, 0, len(entries))
	for _, entry := range entries {
		directory := filepath.Join(paths.Home, "worktrees", entry.Key)
		bytes, complete := worktreeInventoryBytes(directory)
		result = append(result, worktreePruneInventory{AppRoot: entry.AppRoot, Path: directory, Kind: "retained_worktree", Bytes: bytes, Complete: complete, Reason: "retained data and ownership are separate from disposable runtime state"})
		if entry.MetadataStatus != "compatible" || entry.OwnerStatus != "stopped" {
			result[len(result)-1].Reason = "live, unavailable or incompatible ownership prevents reclamation"
			continue
		}
		worktree, err := localagent.PathsForWorktree(paths.Home, entry.AppRoot)
		if err != nil {
			return nil, err
		}
		record, err := worktree.LoadRecord("")
		if err != nil {
			result[len(result)-1].Reason = "retained metadata changed or became unreadable; reclamation refused"
			continue
		}
		old := !record.UpdatedAt.After(cutoff)
		logs := filepath.Join(directory, "control", "dev")
		bytes, complete = worktreeInventoryBytes(logs)
		result = append(result, worktreePruneInventory{AppRoot: entry.AppRoot, Path: logs, Kind: "supervisor_logs", Bytes: bytes, Complete: complete, Reason: "prune retains supervisor logs; separately authorize log retention cleanup"})
		registry, err := worktree.OpenRegistry(record.RouterAddress)
		if err != nil {
			result[len(result)-1].Reason = "retained session registry is unreadable; reclamation refused"
			continue
		}
		for _, session := range registry.List() {
			if session.AppRoot != entry.AppRoot || session.BaseAppID != record.AppID || sessionOwnerLive(session) {
				continue
			}
			if session.StateRoot == "" {
				continue
			}
			expected := filepath.Join(entry.AppRoot, ".scenery", "sessions", session.SessionID)
			bytes, complete := worktreeInventoryBytes(session.StateRoot)
			eligible := old && opts.State && pruneSessionEligible(session, cutoff) && filepath.Clean(session.StateRoot) == expected
			for _, process := range session.Processes {
				if process.PID == process.Owner.PID && localagent.VerifyOwner(process.Owner) == nil {
					eligible = false
				}
			}
			result = append(result, worktreePruneInventory{AppRoot: entry.AppRoot, Path: session.StateRoot, Kind: "session_state", Bytes: bytes, Complete: complete, Reclaimable: eligible && complete, Reason: "only an eligible stopped session beneath its exact managed path is selected by --state"})
		}
		if record.Postgres != nil {
			result = append(result, worktreePruneInventory{AppRoot: entry.AppRoot, Path: record.Postgres.Volume, Kind: "database_volume", Complete: false, Reclaimable: opts.DB && old, Reason: "separately owned database volume; physical bytes are unmeasured; explicit --db scope required"})
		}
	}
	return result, nil
}

func previewBuildCache(ctx context.Context, entries []worktreeStatusEntry, root string, cutoff time.Time) ([]worktreePruneInventory, error) {
	opts := build.WorkspacePruneOptions{Cutoff: cutoff, Preview: true}
	unknownOwner := false
	for _, entry := range entries {
		if entry.AppRoot == "" && entry.MetadataStatus != "absent" {
			unknownOwner = true
		}
		if entry.AppRoot != "" && entry.OwnerStatus != "stopped" {
			opts.ProtectedAppRoots = append(opts.ProtectedAppRoots, entry.AppRoot)
		}
	}
	if root != "" {
		opts.AppRoots = []string{root}
	}
	_, workspaces, err := build.PruneWorkspaces(ctx, opts)
	if err != nil {
		return nil, err
	}
	result := make([]worktreePruneInventory, 0, len(workspaces))
	for _, item := range workspaces {
		bytes, complete := worktreeInventoryBytes(item.Path)
		selected := item.Reason == build.WorkspacePruneStale || item.Reason == build.WorkspacePruneAppRootMissing
		reason := item.Reason
		if unknownOwner {
			reason = "an incompatible owner has no attributable app root; shared cache reclamation refused"
		}
		result = append(result, worktreePruneInventory{AppRoot: item.AppRoot, Path: item.Path, Kind: "build_workspace", Bytes: bytes, Complete: complete, Reclaimable: selected && complete && !unknownOwner, Reason: reason})
	}
	if root != "" {
		framework, err := build.PreviewFrameworkState(root)
		if err != nil {
			return nil, err
		}
		for _, item := range framework {
			bytes, complete := worktreeInventoryBytes(item.Path)
			selected := item.Reason == build.FrameworkPruneUnselected || item.Reason == build.FrameworkPruneInterrupted
			result = append(result, worktreePruneInventory{AppRoot: root, Path: item.Path, Kind: "framework_state", Bytes: bytes, Complete: complete, Reclaimable: selected && complete, Reason: item.Reason})
		}
	}
	return result, nil
}

func worktreeInventoryBytes(path string) (int64, bool) {
	var bytes int64
	complete := true
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			complete = false
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			complete = false
			return nil
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				complete = false
			} else {
				bytes += info.Size()
			}
		}
		return nil
	})
	return bytes, complete && err == nil
}
