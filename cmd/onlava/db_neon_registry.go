package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	appcfg "github.com/pbrazdil/onlava/internal/app"
)

func readNeonBranchRegistryForDefaultRoot() (neonBranchRegistry, string, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return neonBranchRegistry{}, "", err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return neonBranchRegistry{}, "", err
	}
	return registry, neonBranchRegistryPath(root), nil
}

func neonBranchRegistryPath(root string) string {
	return filepath.Join(root, "branches.json")
}

func ensureNeonBranchRegistry(root string) error {
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return err
	}
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return writeNeonBranchRegistry(root, registry)
}

func readNeonBranchRegistry(root string) (neonBranchRegistry, error) {
	path := neonBranchRegistryPath(root)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return neonBranchRegistry{
			SchemaVersion: dbBranchRegistrySchemaVersion,
			Provider:      neonSelfhostProvider,
			Leases:        []neonBranchLease{},
		}, nil
	}
	if err != nil {
		return neonBranchRegistry{}, err
	}
	var registry neonBranchRegistry
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&registry); err != nil {
		if migrated, migrateErr := readLegacyNeonBranchRegistry(data); migrateErr == nil {
			return migrated, nil
		}
		return neonBranchRegistry{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if registry.SchemaVersion != dbBranchRegistrySchemaVersion {
		return neonBranchRegistry{}, fmt.Errorf("%s has unsupported schema_version %q", path, registry.SchemaVersion)
	}
	if registry.Provider != neonSelfhostProvider {
		return neonBranchRegistry{}, fmt.Errorf("%s has unsupported provider %q", path, registry.Provider)
	}
	if registry.Leases == nil {
		registry.Leases = []neonBranchLease{}
	}
	return registry, nil
}

func readLegacyNeonBranchRegistry(data []byte) (neonBranchRegistry, error) {
	var legacy struct {
		SchemaVersion string                     `json:"schema_version"`
		Branches      map[string]json.RawMessage `json:"branches"`
		UpdatedAt     string                     `json:"updated_at,omitempty"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return neonBranchRegistry{}, err
	}
	if legacy.SchemaVersion != "onlava.db.neon.branches.v1" {
		return neonBranchRegistry{}, fmt.Errorf("unsupported legacy schema %q", legacy.SchemaVersion)
	}
	registry := neonBranchRegistry{
		SchemaVersion: dbBranchRegistrySchemaVersion,
		Provider:      neonSelfhostProvider,
		UpdatedAt:     legacy.UpdatedAt,
		Leases:        []neonBranchLease{},
	}
	for _, raw := range legacy.Branches {
		var pin worktreeDBPin
		if err := json.Unmarshal(raw, &pin); err != nil {
			return neonBranchRegistry{}, err
		}
		if pin.SchemaVersion != dbBranchPinSchemaVersion || pin.Provider != neonSelfhostProvider {
			continue
		}
		var meta struct {
			Endpoint  *neonEndpoint `json:"endpoint,omitempty"`
			Status    string        `json:"status,omitempty"`
			CreatedAt string        `json:"created_at,omitempty"`
			UpdatedAt string        `json:"updated_at,omitempty"`
			ExpiresAt string        `json:"expires_at,omitempty"`
		}
		_ = json.Unmarshal(raw, &meta)
		registry.Leases = append(registry.Leases, neonBranchLease{
			Pin:       pin,
			Status:    firstNonEmpty(meta.Status, "pending"),
			Endpoint:  meta.Endpoint,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			ExpiresAt: meta.ExpiresAt,
		})
	}
	return registry, nil
}

func writeNeonBranchRegistry(root string, registry neonBranchRegistry) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	registry.SchemaVersion = dbBranchRegistrySchemaVersion
	registry.Provider = neonSelfhostProvider
	if registry.Leases == nil {
		registry.Leases = []neonBranchLease{}
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(neonBranchRegistryPath(root), data, 0o644)
}

func upsertNeonBranchLease(pin worktreeDBPin) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	expiresAt := neonLeaseExpiresAt(now, pin.TTL)
	for i := range registry.Leases {
		if sameNeonLease(registry.Leases[i].Pin, pin) || sameNeonBranch(registry.Leases[i].Pin, pin) {
			if !isOnlavaOwnedNeonLease(registry.Leases[i]) {
				return fmt.Errorf("refusing to reuse foreign local Neon branch lease %q; remove or rename that lease before checkout", pin.Branch)
			}
			createdAt := registry.Leases[i].CreatedAt
			if createdAt == "" {
				createdAt = nowText
			}
			status := registry.Leases[i].Status
			if status != "ready" {
				status = "pending"
			}
			registry.Leases[i] = neonBranchLease{
				Pin:       pin,
				Status:    status,
				Endpoint:  registry.Leases[i].Endpoint,
				CreatedAt: createdAt,
				UpdatedAt: nowText,
				ExpiresAt: expiresAt,
			}
			registry.UpdatedAt = nowText
			return writeNeonBranchRegistry(root, registry)
		}
	}
	registry.Leases = append(registry.Leases, neonBranchLease{
		Pin:       pin,
		Status:    "pending",
		CreatedAt: nowText,
		UpdatedAt: nowText,
		ExpiresAt: expiresAt,
	})
	registry.UpdatedAt = nowText
	return writeNeonBranchRegistry(root, registry)
}

func sameNeonLease(a, b worktreeDBPin) bool {
	if a.BranchID != "" && b.BranchID != "" {
		return a.BranchID == b.BranchID
	}
	return sameNeonBranch(a, b)
}

func sameNeonBranch(a, b worktreeDBPin) bool {
	return a.Project == b.Project && a.Branch == b.Branch
}

func isOnlavaOwnedNeonPin(pin worktreeDBPin) bool {
	return pin.Provider == neonSelfhostProvider && pin.CreatedBy == "onlava"
}

func isOnlavaOwnedNeonLease(lease neonBranchLease) bool {
	return isOnlavaOwnedNeonPin(lease.Pin)
}

func neonLeaseExpiresAt(now time.Time, ttl string) string {
	duration, err := time.ParseDuration(strings.TrimSpace(ttl))
	if err != nil || duration <= 0 {
		return ""
	}
	return now.Add(duration).UTC().Format(time.RFC3339)
}

func registryPins(registry neonBranchRegistry, cfg appcfg.Config) []worktreeDBPin {
	project := sanitizeNeonBranchSegment(firstNonEmpty(neonPostgresService(cfg).Project, cfg.AppID(), "app"))
	pins := make([]worktreeDBPin, 0, len(registry.Leases))
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			continue
		}
		if project != "" && lease.Pin.Project != project {
			continue
		}
		pins = append(pins, lease.Pin)
	}
	return pins
}

func registryListLeases(ctx context.Context, registry neonBranchRegistry, cfg appcfg.Config) []dbBranchListLease {
	project := neonProjectForConfig(cfg)
	leases := make([]dbBranchListLease, 0, len(registry.Leases))
	provider := neonBranchProviderForConfig(cfg)
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			continue
		}
		if project != "" && lease.Pin.Project != project {
			continue
		}
		leases = append(leases, dbBranchListLeaseFromRegistryLease(ctx, provider, lease))
	}
	return leases
}

func dbBranchListLeaseFromRegistryLease(ctx context.Context, provider neonBranchProvider, lease neonBranchLease) dbBranchListLease {
	backend := provider.InspectBranch(ctx, lease.Pin)
	return dbBranchListLease{
		Pin:       lease.Pin,
		Status:    firstNonEmpty(backend.Status, lease.Status, "pending"),
		Endpoint:  cloneNeonEndpoint(backend.Endpoint),
		CreatedAt: lease.CreatedAt,
		UpdatedAt: lease.UpdatedAt,
		ExpiresAt: lease.ExpiresAt,
	}
}

func resolveBranchCommandTarget(appRoot string, cfg appcfg.Config, opts dbBranchOptions) (worktreeDBPin, error) {
	branch := normalizeNeonBranchName(opts.Branch)
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

func expireNeonBranchLease(pin worktreeDBPin, expiresAt time.Time) error {
	root, err := neonSubstrateRoot()
	if err != nil {
		return err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return err
	}
	if strings.TrimSpace(pin.Project) == "" || strings.TrimSpace(pin.Branch) == "" {
		return fmt.Errorf("db branch expire requires a resolved Neon project and branch")
	}
	nowText := time.Now().UTC().Format(time.RFC3339)
	var found bool
	for i := range registry.Leases {
		if !isOnlavaOwnedNeonLease(registry.Leases[i]) {
			continue
		}
		if !sameNeonLease(registry.Leases[i].Pin, pin) && !sameNeonBranch(registry.Leases[i].Pin, pin) {
			continue
		}
		registry.Leases[i].ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
		registry.Leases[i].UpdatedAt = nowText
		found = true
	}
	if !found {
		return fmt.Errorf("no Onlava-owned local Neon branch lease found for %q in project %q", pin.Branch, pin.Project)
	}
	registry.UpdatedAt = nowText
	return writeNeonBranchRegistry(root, registry)
}

func pruneExpiredNeonBranchLeases(project, currentBranchID string, olderThan time.Duration) (int, error) {
	root, err := neonSubstrateRoot()
	if err != nil {
		return 0, err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	cutoff := time.Time{}
	if olderThan > 0 {
		cutoff = now.Add(-olderThan)
	}
	kept := make([]neonBranchLease, 0, len(registry.Leases))
	var pruned int
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			kept = append(kept, lease)
			continue
		}
		if strings.TrimSpace(project) != "" && lease.Pin.Project != project {
			kept = append(kept, lease)
			continue
		}
		if lease.Pin.BranchID == currentBranchID && currentBranchID != "" {
			kept = append(kept, lease)
			continue
		}
		if neonLeasePrunable(lease, now, cutoff) {
			pruned++
			continue
		}
		kept = append(kept, lease)
	}
	if pruned == 0 {
		return 0, nil
	}
	registry.Leases = kept
	registry.UpdatedAt = now.Format(time.RFC3339)
	return pruned, writeNeonBranchRegistry(root, registry)
}

func neonLeasePrunable(lease neonBranchLease, now, cutoff time.Time) bool {
	if lease.ExpiresAt == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, lease.ExpiresAt)
	if err != nil || expiresAt.After(now) {
		return false
	}
	if cutoff.IsZero() {
		return true
	}
	updatedAt, err := time.Parse(time.RFC3339, lease.UpdatedAt)
	if err != nil {
		return false
	}
	return updatedAt.Before(cutoff)
}

func removeNeonBranchLeaseForSession(appRoot string, session localagent.Session) (string, bool, error) {
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil {
		return "", false, err
	}
	root, err := neonSubstrateRoot()
	if err != nil {
		return "", false, err
	}
	registry, err := readNeonBranchRegistry(root)
	if err != nil {
		return "", false, err
	}
	kept := make([]neonBranchLease, 0, len(registry.Leases))
	var removed bool
	var removedBranch string
	for _, lease := range registry.Leases {
		if !isOnlavaOwnedNeonLease(lease) {
			kept = append(kept, lease)
			continue
		}
		if neonLeaseMatchesSessionCleanup(lease.Pin, pin, ok, session) {
			if isProtectedNeonParentBranch(lease.Pin) {
				return "", false, fmt.Errorf("refusing to remove protected parent branch lease %q", lease.Pin.Branch)
			}
			removed = true
			removedBranch = lease.Pin.Branch
			continue
		}
		kept = append(kept, lease)
	}
	if !removed {
		if ok {
			return pin.Branch, false, nil
		}
		return "", false, nil
	}
	registry.Leases = kept
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return removedBranch, true, writeNeonBranchRegistry(root, registry)
}

func neonLeaseMatchesSessionCleanup(leasePin, currentPin worktreeDBPin, hasCurrent bool, session localagent.Session) bool {
	sessionID := strings.TrimSpace(session.SessionID)
	if sessionID != "" {
		if strings.TrimSpace(leasePin.SessionID) != "" {
			return leasePin.SessionID == sessionID
		}
		return hasCurrent && strings.TrimSpace(currentPin.SessionID) == "" && sameNeonLease(leasePin, currentPin)
	}
	return hasCurrent && sameNeonLease(leasePin, currentPin)
}
