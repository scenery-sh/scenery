package storagefs

import "strings"

// RecoveryInfo is the public diagnostic projection of a private lifecycle
// record. It exposes replay choices, not physical paths or artifact internals.
type RecoveryInfo struct {
	Operation         string `json:"operation"`
	ArchiveSHA256     string `json:"archive_sha256,omitempty"`
	SelectionRevision string `json:"selection_revision,omitempty"`
	Mode              string `json:"mode,omitempty"`
	OnConflict        string `json:"on_conflict,omitempty"`
	Database          bool   `json:"database"`
	Storage           bool   `json:"storage"`
	Phase             string `json:"phase"`
	Resume            string `json:"resume"`
}

func (op *Operation) Recovery() *RecoveryInfo {
	if op == nil {
		return nil
	}
	info := &RecoveryInfo{Operation: op.Operation, ArchiveSHA256: op.ArchiveSHA256, SelectionRevision: op.SelectionRevision, Mode: op.Mode, OnConflict: op.OnConflict, Database: op.Database, Storage: op.Storage, Phase: op.Phase}
	if op.Operation == "purge" {
		info.Resume = "scenery storage cleanup --purge --yes --expect-revision " + op.SelectionRevision + " -o json"
		return info
	}
	args := []string{"scenery snapshot load --input <same-archive>", "--expect-sha256", op.ArchiveSHA256, "--mode", op.Mode, "--storage", "--yes"}
	if op.Mode == "merge" {
		args = append(args, "--on-conflict", op.OnConflict)
	}
	if op.Database {
		args = append(args, "--db")
	}
	info.Resume = strings.Join(append(args, "-o json"), " ")
	return info
}

type RecoveryError struct{ Info *RecoveryInfo }

func (e *RecoveryError) Error() string {
	return "Storage recovery is required in the selected worktree: " + e.Info.Resume
}
func (e *RecoveryError) Unwrap() error { return ErrRecovery }

func operationRecovery(op *Operation) error { return &RecoveryError{Info: op.Recovery()} }
