package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/stateupgrade"
)

// PrepareSpecUpgrade validates retained authority without ordinary historical
// decoding or writes. The caller holds existing live and operation locks.
func (p WorktreePaths) PrepareSpecUpgrade(appID string) (WorktreeRecord, []stateupgrade.Change, error) {
	return p.prepareSpecUpgrade(appID, VerifyOwner)
}

func (p WorktreePaths) prepareSpecUpgrade(appID string, verifyOwner func(Owner) error) (WorktreeRecord, []stateupgrade.Change, error) {
	var record WorktreeRecord
	store, err := stateupgrade.Open(p.Directory)
	if err != nil {
		return record, nil, err
	}
	defer func() { _ = store.Close() }()
	if err := checkPrivateWorktreeFile(p.Record); err != nil {
		return record, nil, err
	}
	before, err := store.ReadMetadata("worktree.json")
	if err != nil {
		return record, nil, err
	}
	after, err := machine.PrepareArtifactSpecUpgrade(before, &record, &record.ArtifactIdentity, worktreeRecordKind, worktreeRecordDescriptor)
	if err != nil {
		return record, nil, err
	}
	if err := p.validateRecord(record, appID); err != nil {
		return record, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(before, &fields); err != nil {
		return record, nil, err
	}
	if value := string(fields["sql_allocation_checked"]); value != "true" && value != "false" {
		return record, nil, fmt.Errorf("retained worktree SQL allocation check must be an explicit boolean")
	}
	if pg := record.Postgres; pg != nil && (pg.Phase != "ready" || pg.Restore != nil) {
		return record, nil, fmt.Errorf("retained PostgreSQL must be ready without a pending lifecycle operation")
	}
	changes := []stateupgrade.Change{{Path: "worktree.json", Kind: worktreeRecordKind, Before: before, After: after}}
	// Stopped session history is retained, including nested historical records.
	// Only the registry container identity changes; no process is adopted.
	name := filepath.Join("control", "sessions.json")
	before, err = store.ReadMetadata(name)
	if errors.Is(err, os.ErrNotExist) {
		return record, changes, nil
	}
	if err != nil {
		return record, nil, err
	}
	var registry registryFile
	after, err = machine.PrepareArtifactSpecUpgrade(before, &registry, &registry.ArtifactIdentity, AgentRegistryKind, agentRegistrySchemaDescriptor)
	if err != nil {
		return record, nil, err
	}
	if err := p.validateStoppedUpgradeRegistry(registry, record.AppID, verifyOwner); err != nil {
		return record, nil, err
	}
	return record, append(changes, stateupgrade.Change{Path: name, Kind: AgentRegistryKind, Before: before, After: after}), nil
}

func (p WorktreePaths) validateStoppedUpgradeRegistry(registry registryFile, appID string, verifyOwner func(Owner) error) error {
	checkOwner := func(pid int, owner Owner) error {
		if pid == 0 && owner.PID == 0 {
			return nil
		}
		if pid <= 0 || owner.PID != pid || (owner.StartedAt == "" && owner.Exe == "" && owner.CmdlineHash == "") {
			return fmt.Errorf("retained process ownership is incomplete; stop it with the matching binary")
		}
		if verifyOwner(owner) == nil {
			return fmt.Errorf("a verified runtime process is still live; stop it with the matching binary")
		}
		return nil
	}
	seen := make(map[string]bool)
	for _, session := range registry.Sessions {
		if session.SessionID == "" || seen[session.SessionID] || session.AppRoot != p.AppRoot || session.BaseAppID != appID || session.WorktreeProxy != nil {
			return fmt.Errorf("session history does not belong to the selected worktree")
		}
		seen[session.SessionID] = true
		if err := checkOwner(session.OwnerPID, session.Owner); err != nil {
			return err
		}
		for _, process := range session.Processes {
			if err := checkOwner(process.PID, process.Owner); err != nil {
				return err
			}
		}
	}
	for _, substrate := range registry.Substrates {
		if substrate.Kind == "" {
			return fmt.Errorf("retained substrate identity is incomplete")
		}
		if err := checkOwner(substrate.OwnerPID, substrate.Owner); err != nil {
			return err
		}
		for name, pid := range substrate.PIDs {
			if err := checkOwner(pid, substrate.Owners[name]); err != nil {
				return err
			}
		}
	}
	for root, id := range registry.CurrentByAppRoot {
		if root != p.AppRoot || !seen[id] {
			return fmt.Errorf("session selector does not belong to the selected worktree")
		}
	}
	for _, alias := range registry.Aliases {
		if !seen[alias.SessionID] {
			return fmt.Errorf("session alias does not belong to retained session history")
		}
	}
	return nil
}
