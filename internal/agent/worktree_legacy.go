package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/machine"
)

// CheckLegacyWorktreeClaim is a read-only allocation guard, not an old runtime
// decoder. Incompatible evidence requires explicit migration; it is never
// rewritten or treated as an empty registry. Checkout execution state remains
// evidence even when the old supervisor has already deregistered the app.
func CheckLegacyWorktreeClaim(machinePaths Paths, worktree WorktreePaths) error {
	sessions := filepath.Join(worktree.AppRoot, ".scenery", "sessions")
	entries, err := os.ReadDir(sessions)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("existing worktree execution provenance cannot be read; verify and migrate retained data before allocating PostgreSQL")
	}
	if len(entries) != 0 {
		return fmt.Errorf("existing checkout execution state may refer to pre-cutover data; export and verify the selected database before explicitly retiring its old execution claim")
	}
	// A server record is machine-scoped, not a claim on every future checkout.
	// If the ownership registry is unavailable its provenance is ambiguous;
	// otherwise inspect exact root bindings without reading any credentials.
	legacyServer := filepath.Join(machinePaths.AgentDir, "postgres", "server.json")
	_, serverErr := os.Lstat(legacyServer)
	data, err := os.ReadFile(machinePaths.RegistryPath)
	if errors.Is(err, os.ErrNotExist) {
		if !errors.Is(serverErr, os.ErrNotExist) {
			return fmt.Errorf("pre-cutover PostgreSQL authority has no readable ownership registry; inspect and migrate the selected data with its matching tools before allocating an empty database")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("existing machine registry provenance cannot be read; verify retained application data before allocating PostgreSQL")
	}
	var registry registryFile
	if err := machine.DecodeArtifact(data, &registry, &registry.ArtifactIdentity, AgentRegistryKind, agentRegistrySchemaDescriptor, "inspect the original registry with its matching Scenery binary before migrating retained data"); err != nil {
		// The cutover only added an optional edge proxy field. This exact
		// predecessor schema is read solely as migration provenance, never
		// as a live protocol, credential authority, or writable registry.
		// Unknown shapes/specifications still fail closed.
		const predecessor = `{"identity":"artifact","registry":"sessions-substrates-aliases"}`
		if err := machine.DecodeArtifact(data, &registry, &registry.ArtifactIdentity, AgentRegistryKind, predecessor, "inspect retained ownership with its matching binary"); err != nil {
			return fmt.Errorf("existing machine registry provenance is incompatible or malformed; verify retained application data with its matching Scenery binary before allocating PostgreSQL")
		}
	}
	for claimedRoot := range registry.CurrentByAppRoot {
		root, err := canonicalWorktreePath(claimedRoot)
		if err != nil || root == worktree.AppRoot {
			return fmt.Errorf("the selected checkout has an existing machine runtime binding; verify its retained database before allocation")
		}
	}
	for _, session := range registry.Sessions {
		root, err := canonicalWorktreePath(session.AppRoot)
		if err != nil {
			return fmt.Errorf("existing machine registry has unverifiable application ownership; resolve it before allocating PostgreSQL")
		}
		if root == worktree.AppRoot {
			return fmt.Errorf("the selected checkout has a pre-cutover runtime claim; stop it with the matching Scenery binary and explicitly migrate its retained database")
		}
	}
	for _, substrate := range registry.Substrates {
		if substrate.Kind != SubstratePostgres {
			continue
		}
		for _, lease := range substrate.Leases {
			root, err := canonicalWorktreePath(lease.AppRoot)
			if err != nil || root == worktree.AppRoot {
				return fmt.Errorf("the selected checkout may retain a pre-cutover PostgreSQL lease; verify its source data before allocation")
			}
		}
	}
	return nil
}
