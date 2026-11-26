package main

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"scenery.sh/internal/build"
)

// errBuildSuperseded is a revision conflict: newer source replaced the inputs
// of the candidate being prepared, and the next build uses them.
var errBuildSuperseded = errors.New("revision_conflict: source changed during candidate preparation; discard the superseded generation and retry")

var devBuildOperationSequence atomic.Uint64

// devBuildOperationError names the build operation a failure ended, so its
// build.error event joins that operation's build.step records exactly.
type devBuildOperationError struct {
	operationID string
	err         error
}

func (e devBuildOperationError) Error() string { return e.err.Error() }
func (e devBuildOperationError) Unwrap() error { return e.err }

func devBuildFailureOperation(err error) string {
	var operation devBuildOperationError
	if errors.As(err, &operation) {
		return operation.operationID
	}
	return ""
}

func newDevBuildOperationID() string {
	return fmt.Sprintf("build-%x-%x", time.Now().UnixNano(), devBuildOperationSequence.Add(1))
}

func (s *devSupervisor) emitBuildStepWithEditClass(step build.Step, editClass string) {
	fields := map[string]any{
		"operation_id": step.OperationID, "edit_class": editClass,
		"name": step.Name, "started_at": step.StartedAt.UTC().Format(time.RFC3339Nano),
		"duration_ms": float64(step.Duration.Microseconds()) / 1000,
		"cache":       step.Cache, "reason": step.Reason, "ok": step.OK,
	}
	optionalInt := func(name string, value int) {
		if value != 0 {
			fields[name] = value
		}
	}
	optionalInt64 := func(name string, value int64) {
		if value != 0 {
			fields[name] = value
		}
	}
	optionalString := func(name, value string) {
		if value != "" {
			fields[name] = value
		}
	}
	if step.QueueDuration > 0 {
		fields["queue_ms"] = float64(step.QueueDuration.Microseconds()) / 1000
	}
	optionalInt("actions", step.Actions)
	optionalInt("cache_hits", step.CacheHits)
	optionalInt("cache_misses", step.CacheMisses)
	optionalInt("files_written", step.FilesWritten)
	optionalInt("files_removed", step.FilesRemoved)
	optionalInt64("bytes_written", step.BytesWritten)
	optionalInt("files_hashed", step.FilesHashed)
	optionalInt64("bytes_hashed", step.BytesHashed)
	optionalInt("files_reused", step.FilesReused)
	optionalInt64("bytes_reused", step.BytesReused)
	optionalInt64("executable_bytes", step.ExecutableBytes)
	if step.WrittenPaths != nil {
		fields["written_paths"] = step.WrittenPaths
	}
	if step.RemovedPaths != nil {
		fields["removed_paths"] = step.RemovedPaths
	}
	if step.PackagesRebuilt != nil {
		fields["packages_rebuilt"] = step.PackagesRebuilt
	}
	if step.Name == "go.command" && step.Reason == "build" {
		fields["packages_rebuilt_available"] = step.PackagesRebuiltAvailable
	}
	optionalString("outcome", step.Outcome)
	optionalString("snapshot_digest", step.SnapshotDigest)
	optionalString("contract_revision", step.ContractRevision)
	optionalString("implementation_revision", step.ImplementationRevision)
	optionalString("build_input_digest", step.BuildInputDigest)
	optionalString("framework_source_digest", step.FrameworkSourceDigest)
	optionalString("framework_executable_digest", step.FrameworkExecutableDigest)
	optionalString("go_target", step.GoTarget)
	if session := s.currentAgentSession(); session != nil {
		optionalString("session_id", session.SessionID)
		optionalString("app_root_hash", appRootHash(session.AppRoot))
		optionalString("owner_started_at", session.Owner.StartedAt)
		if session.OwnerPID != 0 {
			fields["owner_pid"] = session.OwnerPID
		}
	}
	s.console.Event("build.step", fields)
}
