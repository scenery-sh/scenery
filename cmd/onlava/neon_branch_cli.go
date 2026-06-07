package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

func dbBranchRestoreCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	if !opts.Yes {
		return fmt.Errorf("onlava db branch restore requires --yes")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	lease, adminURL, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	provider := neonSelfHostedBranchProvider{adminURL: adminURL}
	point, err := provider.RestoreBranch(context.Background(), lease, opts.At)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{
			"schema_version": "onlava.db.branch.restore.v1",
			"branch":         lease.Branch,
			"branch_id":      lease.BranchID,
			"restore_point":  point,
			"status":         "restored",
		})
	}
	fmt.Fprintf(os.Stdout, "restored onlava Neon branch %s from %s\n", lease.Branch, point.Ref)
	return nil
}

func dbBranchDiffCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Name) == "" {
		return fmt.Errorf("onlava db branch diff requires a branch name or id")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	current, adminURL, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	target, err := resolveNeonTargetLease(current, opts.Name)
	if err != nil {
		return err
	}
	provider := neonSelfHostedBranchProvider{adminURL: adminURL}
	if err := provider.ensureResolvedBranchDatabase(context.Background(), target); err != nil {
		return err
	}
	diff, err := provider.DiffBranch(context.Background(), current, target)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{
			"schema_version": "onlava.db.branch.diff.v1",
			"branch":         current.Branch,
			"target":         target.Branch,
			"diff":           diff,
		})
	}
	_, err = fmt.Fprint(os.Stdout, diff)
	return err
}

func dbBranchExpireCommand(args []string) error {
	opts, err := parseDBBranchArgs(args, false)
	if err != nil {
		return err
	}
	if opts.After <= 0 {
		return fmt.Errorf("onlava db branch expire requires --after <duration>")
	}
	appRoot, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	lease, adminURL, err := ensureNeonLeaseForCLI(context.Background(), appRoot, cfg)
	if err != nil {
		return err
	}
	provider := neonSelfHostedBranchProvider{adminURL: adminURL}
	lease, err = provider.ExpireBranch(context.Background(), lease, opts.After)
	if err != nil {
		return err
	}
	if err := writeNeonWorktreeLease(appRoot, lease); err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(os.Stdout, map[string]any{
			"schema_version": "onlava.db.branch.expire.v1",
			"branch":         lease.Branch,
			"branch_id":      lease.BranchID,
			"ttl":            lease.TTL,
			"expires_at":     lease.ExpiresAt,
			"status":         "updated",
		})
	}
	fmt.Fprintf(os.Stdout, "updated onlava Neon branch %s expiration to %s\n", lease.Branch, lease.ExpiresAt.Format(time.RFC3339))
	return nil
}

func resolveNeonTargetLease(current neonBranchLease, ref string) (neonBranchLease, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == current.Branch || ref == current.BranchID {
		return current, nil
	}
	if lease, ok := lookupNeonBranchLease(current.Project, ref); ok {
		return lease, nil
	}
	name := sanitizeNeonBranch(ref)
	if name == current.ParentBranch {
		lease := current
		lease.Branch = current.ParentBranch
		lease.BranchID = neonBranchID(current.Project, lease.Branch)
		lease.DatabaseName = neonBranchDatabaseName(current.Project, lease.Branch)
		return lease, nil
	}
	return neonBranchLease{}, fmt.Errorf("unknown Neon branch %q", ref)
}
