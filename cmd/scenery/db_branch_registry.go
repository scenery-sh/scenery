package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
)

func dbBranchRegistryPath(root string) string {
	return filepath.Join(root, "branches.json")
}

func decodeBranchRegistry(path string, data []byte) (dbBranchRegistry, error) {
	var registry dbBranchRegistry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&registry); err != nil {
		return dbBranchRegistry{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return registry, nil
}

func writeBranchRegistryFile(path string, registry dbBranchRegistry) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o644)
}

func sameDBBranchLease(a, b worktreeDBPin) bool {
	if a.BranchID != "" && b.BranchID != "" {
		return a.BranchID == b.BranchID
	}
	return sameDBBranch(a, b)
}

func sameDBBranch(a, b worktreeDBPin) bool {
	return a.Project == b.Project && a.Branch == b.Branch
}

func dbLeaseMatchesBranchForDelete(lease dbBranchLease, current worktreeDBPin, branch string) bool {
	if lease.Pin.Branch != branch {
		return false
	}
	if strings.TrimSpace(current.Project) == "" {
		return false
	}
	return lease.Pin.Project == current.Project
}

func isSceneryOwnedDBPin(pin worktreeDBPin) bool {
	return pin.Provider == sqliteBranchProviderName && pin.CreatedBy == "scenery"
}

func isSceneryOwnedDBLease(lease dbBranchLease) bool {
	return isSceneryOwnedDBPin(lease.Pin)
}

func dbBranchLeaseExpiresAt(now time.Time, ttl string) string {
	duration, err := time.ParseDuration(strings.TrimSpace(ttl))
	if err != nil || duration <= 0 {
		return ""
	}
	return now.Add(duration).UTC().Format(time.RFC3339)
}

func dbBranchLeaseExpired(lease dbBranchLease, now time.Time) bool {
	if lease.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, lease.ExpiresAt)
	return err == nil && !expiresAt.After(now)
}

func registryPins(registry dbBranchRegistry, cfg appcfg.Config) []worktreeDBPin {
	project := branchProjectForConfig(cfg)
	pins := make([]worktreeDBPin, 0, len(registry.Leases))
	for _, lease := range registry.Leases {
		if !isSceneryOwnedDBLease(lease) {
			continue
		}
		if project != "" && lease.Pin.Project != project {
			continue
		}
		pins = append(pins, lease.Pin)
	}
	return pins
}

func registryListLeases(ctx context.Context, registry dbBranchRegistry, cfg appcfg.Config) []dbBranchListLease {
	project := branchProjectForConfig(cfg)
	leases := make([]dbBranchListLease, 0, len(registry.Leases))
	provider := sqliteBranchProvider{cfg: cfg}
	for _, lease := range registry.Leases {
		if !isSceneryOwnedDBLease(lease) {
			continue
		}
		if project != "" && lease.Pin.Project != project {
			continue
		}
		leases = append(leases, dbBranchListLeaseFromRegistryLease(ctx, provider, lease))
	}
	return leases
}

func readBranchRegistryForConfig(appcfg.Config) (dbBranchRegistry, string, error) {
	return readSQLiteBranchRegistryForDefaultRoot()
}

func branchProjectForConfig(cfg appcfg.Config) string {
	if _, svc, ok := managedSQLiteDeclared(cfg); ok {
		if project := sanitizeDBIdentifier(svc.Project); project != "" {
			return project
		}
	}
	return sanitizeDBIdentifier(firstNonEmpty(cfg.AppID(), "app"))
}

func dbBranchListLeaseFromRegistryLease(ctx context.Context, provider sqliteBranchProvider, lease dbBranchLease) dbBranchListLease {
	backend := provider.InspectBranch(ctx, lease.Pin)
	return dbBranchListLease{
		Pin:       lease.Pin,
		Status:    firstNonEmpty(backend.Status, lease.Status, "pending"),
		Endpoint:  cloneDBBranchEndpoint(backend.Endpoint),
		CreatedAt: lease.CreatedAt,
		UpdatedAt: lease.UpdatedAt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func resolveBranchCommandTarget(appRoot string, cfg appcfg.Config, opts dbBranchOptions) (worktreeDBPin, error) {
	branch := normalizeDBBranchName(opts.Branch)
	if branch != "" {
		return buildWorktreeDBPin(appRoot, cfg, branch)
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return worktreeDBPin{}, err
	}
	if ok && strings.TrimSpace(pin.Branch) != "" {
		return pin, nil
	}
	return worktreeDBPin{}, fmt.Errorf("no db branch target supplied and no worktree database branch pin exists")
}

func sqliteBranchRegistryRoot() (string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.AgentDir, "sqlite"), nil
}

func readSQLiteBranchRegistryForDefaultRoot() (dbBranchRegistry, string, error) {
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return dbBranchRegistry{}, "", err
	}
	path := dbBranchRegistryPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return dbBranchRegistry{SchemaVersion: dbBranchRegistrySchemaVersion, Provider: sqliteBranchProviderName}, path, nil
	}
	if err != nil {
		return dbBranchRegistry{}, path, err
	}
	registry, err := decodeBranchRegistry(path, data)
	return registry, path, err
}

func mutateSQLiteBranchRegistry(root string, mutate func(*dbBranchRegistry) error) error {
	path := dbBranchRegistryPath(root)
	registry, _, err := readSQLiteBranchRegistryForDefaultRoot()
	if err != nil {
		return err
	}
	registry.SchemaVersion = dbBranchRegistrySchemaVersion
	registry.Provider = sqliteBranchProviderName
	if err := mutate(&registry); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return writeBranchRegistryFile(path, registry)
}

func upsertSQLiteBranchLease(pin worktreeDBPin, endpoint *dbBranchEndpoint, status string) error {
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return err
	}
	return mutateSQLiteBranchRegistry(root, func(registry *dbBranchRegistry) error {
		now := time.Now().UTC().Format(time.RFC3339)
		for i := range registry.Leases {
			if sameDBBranchLease(registry.Leases[i].Pin, pin) {
				registry.Leases[i].Pin = pin
				registry.Leases[i].Endpoint = endpoint
				registry.Leases[i].Status = status
				registry.Leases[i].UpdatedAt = now
				registry.UpdatedAt = now
				return nil
			}
		}
		registry.Leases = append(registry.Leases, dbBranchLease{
			Pin:       pin,
			Status:    status,
			Endpoint:  endpoint,
			CreatedAt: now,
			UpdatedAt: now,
			ExpiresAt: dbBranchLeaseExpiresAt(time.Now().UTC(), pin.TTL),
		})
		registry.UpdatedAt = now
		return nil
	})
}

func deleteSQLiteBranchLease(pin worktreeDBPin) error {
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return err
	}
	return mutateSQLiteBranchRegistry(root, func(registry *dbBranchRegistry) error {
		kept := registry.Leases[:0]
		for _, lease := range registry.Leases {
			if sameDBBranchLease(lease.Pin, pin) || sameDBBranch(lease.Pin, pin) {
				continue
			}
			kept = append(kept, lease)
		}
		registry.Leases = kept
		registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
}

func expireDBBranchLease(pin worktreeDBPin, expiresAt time.Time) error {
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return err
	}
	if strings.TrimSpace(pin.Project) == "" || strings.TrimSpace(pin.Branch) == "" {
		return fmt.Errorf("db branch expire requires a resolved sqlite project and branch")
	}
	return mutateSQLiteBranchRegistry(root, func(registry *dbBranchRegistry) error {
		nowText := time.Now().UTC().Format(time.RFC3339)
		var found bool
		for i := range registry.Leases {
			if !isSceneryOwnedDBLease(registry.Leases[i]) {
				continue
			}
			if !sameDBBranchLease(registry.Leases[i].Pin, pin) && !sameDBBranch(registry.Leases[i].Pin, pin) {
				continue
			}
			registry.Leases[i].ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
			registry.Leases[i].UpdatedAt = nowText
			found = true
		}
		if !found {
			return fmt.Errorf("no Scenery-owned local sqlite branch lease found for %q in project %q", pin.Branch, pin.Project)
		}
		registry.UpdatedAt = nowText
		return nil
	})
}

func pruneExpiredDBBranchLeases(cfg appcfg.Config, project, currentBranchID string, olderThan time.Duration) (int, error) {
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	var pruned int
	err = mutateSQLiteBranchRegistry(root, func(registry *dbBranchRegistry) error {
		kept := make([]dbBranchLease, 0, len(registry.Leases))
		for _, lease := range registry.Leases {
			if !isSceneryOwnedDBLease(lease) || lease.Pin.Project != project || lease.Pin.BranchID == currentBranchID || !shouldPruneDBLease(lease, now, olderThan) {
				kept = append(kept, lease)
				continue
			}
			pruned++
		}
		registry.Leases = kept
		registry.UpdatedAt = now.Format(time.RFC3339)
		return nil
	})
	return pruned, err
}

func shouldPruneDBLease(lease dbBranchLease, now time.Time, olderThan time.Duration) bool {
	if !dbBranchLeaseExpired(lease, now) {
		return false
	}
	if olderThan <= 0 {
		return true
	}
	updatedAt, err := time.Parse(time.RFC3339, firstNonEmpty(lease.UpdatedAt, lease.CreatedAt))
	if err != nil {
		return true
	}
	return updatedAt.Before(now.Add(-olderThan))
}

func removeCurrentDBBranchLease(appRoot string, cfg appcfg.Config) (string, bool, error) {
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil || !ok {
		return "", false, err
	}
	if !isSceneryOwnedDBPin(pin) {
		return "", false, nil
	}
	root, err := sqliteBranchRegistryRoot()
	if err != nil {
		return "", false, err
	}
	removed := false
	if err := mutateSQLiteBranchRegistry(root, func(registry *dbBranchRegistry) error {
		kept := registry.Leases[:0]
		for _, lease := range registry.Leases {
			if sameDBBranchLease(lease.Pin, pin) {
				removed = true
				continue
			}
			kept = append(kept, lease)
		}
		registry.Leases = kept
		registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	}); err != nil {
		return "", false, err
	}
	if removed {
		_ = os.Remove(worktreeDBPinPath(appRoot))
	}
	return pin.Branch, removed, nil
}
