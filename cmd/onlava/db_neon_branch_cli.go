package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

func runDBBranchRestore(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.At) == "" {
		return fmt.Errorf("onlava db branch restore requires --at <timestamp-or-lsn>")
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no worktree database branch pin exists; run `onlava db branch checkout <name>` first")
	}
	if isProtectedNeonParentBranch(pin) {
		return fmt.Errorf("refusing to restore protected parent branch %q", pin.Branch)
	}
	if !opts.Yes {
		return fmt.Errorf("onlava db branch restore requires --yes")
	}
	point, err := neonBranchProviderForConfig(cfg).RestoreBranch(ctx, pin, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, map[string]any{
			"schema_version": "onlava.db.branch.restore.v1",
			"branch":         pin.Branch,
			"branch_id":      pin.BranchID,
			"restore_point":  point,
			"status":         "restored",
		})
	}
	fmt.Fprintf(stdout, "restored db branch %s from %s\n", pin.Branch, point.Ref)
	return nil
}

func runDBBranchDiff(ctx context.Context, stdout io.Writer, opts dbBranchOptions) error {
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(opts.Branch)
	if target == "" {
		return fmt.Errorf("usage: onlava db branch diff <branch> [--app-root <path>] [--json]")
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no worktree database branch pin exists; run `onlava db branch checkout <name>` first")
	}
	diff, err := neonBranchProviderForConfig(cfg).DiffBranch(ctx, pin, target, opts)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, map[string]any{
			"schema_version": "onlava.db.branch.diff.v1",
			"branch":         pin.Branch,
			"target":         target,
			"diff":           diff,
		})
	}
	_, err = fmt.Fprint(stdout, diff)
	return err
}
