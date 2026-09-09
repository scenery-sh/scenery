package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"scenery.sh/internal/machine"
	"scenery.sh/internal/stateupgrade"
)

const worktreeRecordKind = "scenery.worktree"

// Every payload field participates in the schema digest. This private artifact
// is strictly current; a failed read never rewrites ownership or allocates data.
const worktreeRecordDescriptor = `{"type":"object","required":["app_root","app_id","user_id","created_at","updated_at","sql_allocation_checked"],"properties":{"app_root":{"type":"string"},"app_id":{"type":"string"},"user_id":{"type":"integer"},"created_at":{"type":"string","format":"date-time"},"updated_at":{"type":"string","format":"date-time"},"sql_allocation_checked":{"type":"boolean"},"router_address":{"type":"string"},"postgres":` + worktreePostgresDescriptor + `},"additionalProperties":false,"identity":"artifact"}`

type WorktreeRecord struct {
	machine.ArtifactIdentity
	AppRoot              string            `json:"app_root"`
	AppID                string            `json:"app_id"`
	UserID               int               `json:"user_id"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	SQLAllocationChecked bool              `json:"sql_allocation_checked"`
	RouterAddress        string            `json:"router_address,omitempty"`
	Postgres             *WorktreePostgres `json:"postgres,omitempty"`
}

func NewWorktreeRecord(paths WorktreePaths, appID string) WorktreeRecord {
	now := time.Now().UTC()
	return WorktreeRecord{
		ArtifactIdentity: machine.NewArtifactIdentity(worktreeRecordKind, worktreeRecordDescriptor),
		AppRoot:          paths.AppRoot, AppID: appID, UserID: os.Getuid(), CreatedAt: now, UpdatedAt: now,
	}
}

// LoadRecord is read-only, including on incompatible or corrupt metadata.
func (p WorktreePaths) LoadRecord(appID string) (WorktreeRecord, error) {
	var record WorktreeRecord
	if err := stateupgrade.CheckPending(p.Directory); err != nil {
		return record, fmt.Errorf("failed_precondition: %w", err)
	}
	if err := checkPrivateWorktreeFile(p.Record); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return record, err
		}
		return record, fmt.Errorf("failed_precondition: retained worktree authority is unsafe or unreadable: %w", err)
	}
	data, err := os.ReadFile(p.Record)
	if err != nil {
		return record, err
	}
	if err := machine.DecodeArtifact(data, &record, &record.ArtifactIdentity, worktreeRecordKind, worktreeRecordDescriptor, "use the matching Scenery binary or explicitly migrate retained worktree state"); err != nil {
		return WorktreeRecord{}, fmt.Errorf("failed_precondition: %w", err)
	}
	if err := p.validateRecord(record, appID); err != nil {
		return WorktreeRecord{}, fmt.Errorf("failed_precondition: %w", err)
	}
	return record, nil
}

func (p WorktreePaths) validateRecord(record WorktreeRecord, appID string) error {
	if record.AppRoot != p.AppRoot || record.AppID == "" || (appID != "" && record.AppID != appID) || record.UserID != os.Getuid() || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return fmt.Errorf("worktree ownership does not match the selected root, application and local user; retained state was not modified")
	}
	if record.Postgres != nil {
		return record.Postgres.validate()
	}
	return nil
}

// WorktreeOperation serializes retained-state changes. The live owner uses a
// separate lifetime lock so its own short capability operations can acquire this.
type WorktreeOperation struct {
	paths WorktreePaths
	lock  *ProcessLock
}

func (p WorktreePaths) BeginOperation() (*WorktreeOperation, error) {
	lock, err := p.AcquireOperationLock()
	if err != nil {
		return nil, err
	}
	return &WorktreeOperation{paths: p, lock: lock}, nil
}

func (op *WorktreeOperation) Close() error {
	if op == nil || op.lock == nil {
		return nil
	}
	err := op.lock.Release()
	op.lock = nil
	return err
}

func (op *WorktreeOperation) SaveRecord(record WorktreeRecord) error {
	if op == nil || op.lock == nil {
		return fmt.Errorf("worktree operation lock is not held")
	}
	if err := op.paths.validateRecord(record, record.AppID); err != nil {
		return err
	}
	existing, err := op.paths.LoadRecord(record.AppID)
	if err == nil {
		if !existing.CreatedAt.Equal(record.CreatedAt) {
			return fmt.Errorf("worktree creation identity cannot be replaced")
		}
		if err := validatePostgresUpdate(existing.Postgres, record.Postgres); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return op.writeRecord(record)
}

func (op *WorktreeOperation) writeRecord(record WorktreeRecord) error {
	record.UpdatedAt = time.Now().UTC()
	record.ArtifactIdentity = machine.NewArtifactIdentity(worktreeRecordKind, worktreeRecordDescriptor)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(op.paths.Record, append(data, '\n'), 0o600)
}

// RetirePostgres is called only after the resolver confirms both exact Docker
// objects are absent. A deleting intent remains recoverable until this write.
func (op *WorktreeOperation) RetirePostgres(instanceID string) error {
	if op == nil || op.lock == nil {
		return fmt.Errorf("worktree operation lock is not held")
	}
	record, err := op.paths.LoadRecord("")
	if err != nil {
		return err
	}
	if record.Postgres == nil || record.Postgres.InstanceID != instanceID || record.Postgres.Phase != "deleting" {
		return fmt.Errorf("retained PostgreSQL deletion intent does not match the selected instance")
	}
	record.Postgres = nil
	return op.writeRecord(record)
}
