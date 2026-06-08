package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	appcfg "github.com/pbrazdil/onlava/internal/app"
)

func neonBranchProviderForConfig(_ appcfg.Config) neonBranchProvider {
	return neonSelfhostBranchProvider{}
}

func (p neonSelfhostBranchProvider) EnsureBranch(ctx context.Context, pin worktreeDBPin) (neonBranchBackendStatus, error) {
	if err := upsertNeonBranchLease(pin); err != nil {
		return neonBranchBackendStatus{
			Status:  "unknown",
			Message: err.Error(),
		}, err
	}
	driver, ok, err := configuredNeonBranchDriver()
	if err != nil {
		return neonBranchBackendStatus{
			Status:  "unknown",
			Message: err.Error(),
		}, err
	}
	if ok {
		return driver.EnsureBranch(ctx, pin)
	}
	return p.InspectBranch(ctx, pin), nil
}

func (neonSelfhostBranchProvider) InspectBranch(ctx context.Context, pin worktreeDBPin) neonBranchBackendStatus {
	lease, ok, err := findNeonBranchLease(pin)
	if err != nil {
		return neonBranchBackendStatus{
			Status:  "unknown",
			Message: err.Error(),
		}
	}
	if !ok {
		return neonBranchBackendStatus{
			Status:  "missing",
			Message: "Local branch pin exists, but no Onlava-owned lease was found in the Neon branch registry.",
		}
	}
	if neonLeaseExpired(lease, time.Now().UTC()) {
		return neonBranchBackendStatus{
			Status:  "expired",
			Message: "Local branch lease is expired. Run `onlava db branch checkout <name>` to renew it or `onlava db branch prune` to remove old leases.",
		}
	}
	switch lease.Status {
	case "ready":
		if isProtectedNeonParentBranch(pin) {
			return neonBranchBackendStatus{
				Status:  "protected",
				Message: fmt.Sprintf("Local Neon branch lease %q is the protected parent branch; Onlava will not expose it as an app-session database connection.", pin.Branch),
			}
		}
		if lease.Endpoint == nil {
			return neonBranchBackendStatus{
				Status:  "missing",
				Message: "Local Neon branch lease is marked ready, but no endpoint metadata is available.",
			}
		}
		endpoint := normalizedNeonEndpoint(*lease.Endpoint, pin)
		return neonBranchBackendStatus{
			Status:   "ready",
			Message:  "Local Neon branch lease is marked ready.",
			Endpoint: &endpoint,
		}
	case "missing":
		return neonBranchBackendStatus{
			Status:  "missing",
			Message: "Local Neon branch lease is marked missing.",
		}
	case "expired":
		return neonBranchBackendStatus{
			Status:  "expired",
			Message: "Local Neon branch lease is marked expired.",
		}
	default:
		return neonSelfhostPendingBranchStatus(ctx)
	}
}

func neonSelfhostPendingBranchStatus(ctx context.Context) neonBranchBackendStatus {
	status, err := buildDBNeonStatus(ctx)
	if err != nil {
		return neonBranchBackendStatus{
			Status:  "unknown",
			Message: err.Error(),
		}
	}
	switch status.Status {
	case "not_installed":
		return neonBranchBackendStatus{
			Status:  "missing",
			Message: "Local Neon branch lease exists, but the Neon dev-cell is not installed. Run `onlava db neon install --json` and `onlava db neon start --json` before backend branch creation can run.",
		}
	case "ready":
		return neonBranchBackendStatus{
			Status:  "pending",
			Message: "Neon dev-cell is ready, but backend branch creation is not implemented yet.",
		}
	default:
		return neonBranchBackendStatus{
			Status:  "pending",
			Message: fmt.Sprintf("Local Neon branch lease exists, but the Neon dev-cell is %s. Run `onlava db neon status --json` before backend branch creation can run.", firstNonEmpty(status.Status, "unknown")),
		}
	}
}

func (neonSelfhostBranchProvider) Connection(_ context.Context, pin worktreeDBPin) (neonBranchConnectionInfo, error) {
	lease, ok, err := findNeonBranchLease(pin)
	if err != nil {
		return neonBranchConnectionInfo{}, err
	}
	if !ok {
		return neonBranchConnectionInfo{}, fmt.Errorf("local Neon branch pin %q has no Onlava-owned lease", pin.Branch)
	}
	if neonLeaseExpired(lease, time.Now().UTC()) {
		return neonBranchConnectionInfo{}, fmt.Errorf("local Neon branch lease %q is expired", pin.Branch)
	}
	if isProtectedNeonParentBranch(pin) {
		return neonBranchConnectionInfo{}, fmt.Errorf("local Neon branch lease %q is the protected parent branch; refusing to expose it as an app-session database connection", pin.Branch)
	}
	if lease.Status != "ready" {
		return neonBranchConnectionInfo{}, fmt.Errorf("local Neon branch lease %q is %s; backend branch connection is not ready", pin.Branch, firstNonEmpty(lease.Status, "pending"))
	}
	if lease.Endpoint == nil {
		return neonBranchConnectionInfo{}, fmt.Errorf("local Neon branch lease %q is ready but has no endpoint metadata", pin.Branch)
	}
	endpoint := normalizedNeonEndpoint(*lease.Endpoint, pin)
	dsn, err := neonEndpointDatabaseURL(pin, endpoint)
	if err != nil {
		return neonBranchConnectionInfo{}, err
	}
	return neonBranchConnectionInfo{
		DatabaseURL:  dsn,
		DatabaseName: endpoint.Database,
		Endpoint:     endpoint,
	}, nil
}

func (neonSelfhostBranchProvider) ResetBranch(ctx context.Context, pin worktreeDBPin, _ dbBranchOptions) error {
	driver, ok, err := configuredNeonBranchDriver()
	if err != nil {
		return err
	}
	if ok {
		return driver.ResetBranch(ctx, pin)
	}
	if err := neonSelfhostMutationPreflight(ctx, "reset"); err != nil {
		return err
	}
	return fmt.Errorf("db branch reset validated %q but no Neon branch driver is configured; set %s for neon-selfhost or %s for the local fallback", pin.Branch, neonSelfhostBranchDriverEnv, localPostgresBranchDriverEnv)
}

func (neonSelfhostBranchProvider) DeleteBranch(ctx context.Context, pin worktreeDBPin, branch string, opts dbBranchOptions) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return err
	}
	branch = normalizeNeonBranchName(branch)
	if branch == "" {
		return fmt.Errorf("db branch delete requires a branch name")
	}
	driver, driverConfigured, err := configuredNeonBranchDriver()
	if err != nil {
		return err
	}
	kept := make([]neonBranchLease, 0, len(registry.Leases))
	var removed bool
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			kept = append(kept, lease)
			continue
		}
		if !neonLeaseMatchesBranchForDelete(lease, pin, branch) {
			kept = append(kept, lease)
			continue
		}
		if lease.Status == "ready" {
			if !driverConfigured {
				return fmt.Errorf("db branch delete validated %q but no Neon branch driver is configured; set %s for neon-selfhost or %s for the local fallback", branch, neonSelfhostBranchDriverEnv, localPostgresBranchDriverEnv)
			}
			if err := driver.DeleteBranch(ctx, lease.Pin); err != nil {
				return err
			}
			removed = true
			continue
		}
		removed = true
	}
	if !removed {
		return fmt.Errorf("no Onlava-owned local Neon branch lease found for %q", branch)
	}
	registry.Leases = kept
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeNeonBranchRegistry(root, registry); err != nil {
		return err
	}
	if err := deleteNeonRestorePoints(neonLocalBranchID(pin.Project, branch)); err != nil {
		return err
	}
	if pin.Branch == branch && strings.TrimSpace(opts.AppRoot) != "" {
		if err := os.Remove(worktreeDBPinPath(opts.AppRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func neonLeaseMatchesBranchForDelete(lease neonBranchLease, current worktreeDBPin, branch string) bool {
	if lease.Pin.Branch != branch {
		return false
	}
	if strings.TrimSpace(current.Project) == "" {
		return false
	}
	return lease.Pin.Project == current.Project
}

func (neonSelfhostBranchProvider) RestoreBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) (neonBranchRestorePoint, error) {
	driver, ok, err := configuredNeonBranchDriver()
	if err != nil {
		return neonBranchRestorePoint{}, err
	}
	if ok {
		return driver.RestoreBranch(ctx, pin, opts.At)
	}
	if err := neonSelfhostMutationPreflight(ctx, "restore"); err != nil {
		return neonBranchRestorePoint{}, err
	}
	return neonBranchRestorePoint{}, fmt.Errorf("db branch restore validated %q at %q but no Neon branch driver is configured; set %s for neon-selfhost or %s for the local fallback", pin.Branch, strings.TrimSpace(opts.At), neonSelfhostBranchDriverEnv, localPostgresBranchDriverEnv)
}

func (neonSelfhostBranchProvider) DiffBranch(ctx context.Context, pin worktreeDBPin, target string, _ dbBranchOptions) (string, error) {
	driver, ok, err := configuredNeonBranchDriver()
	if err != nil {
		return "", err
	}
	if ok {
		return driver.DiffBranch(ctx, pin, target)
	}
	if err := neonSelfhostMutationPreflight(ctx, "diff"); err != nil {
		return "", err
	}
	return "", fmt.Errorf("db branch diff validated %q against %q but no Neon branch driver is configured; set %s for neon-selfhost or %s for the local fallback", pin.Branch, target, neonSelfhostBranchDriverEnv, localPostgresBranchDriverEnv)
}

func neonSelfhostMutationPreflight(ctx context.Context, action string) error {
	status := neonSelfhostPendingBranchStatus(ctx)
	if status.Status == "missing" {
		return fmt.Errorf("db branch %s requires generated Neon dev-cell readiness: %s", action, status.Message)
	}
	return nil
}

func findNeonBranchLease(pin worktreeDBPin) (neonBranchLease, bool, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return neonBranchLease{}, false, err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return neonBranchLease{}, false, err
	}
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			continue
		}
		if sameNeonLease(lease.Pin, pin) {
			return lease, true, nil
		}
	}
	return neonBranchLease{}, false, nil
}

func neonLeaseExpired(lease neonBranchLease, now time.Time) bool {
	if lease.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, lease.ExpiresAt)
	return err == nil && !expiresAt.After(now)
}
