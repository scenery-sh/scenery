package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func blockTestSnapshot(files map[string]string) fileSnapshot {
	snapshot := fileSnapshot{files: map[string]fileStamp{}}
	for path, hash := range files {
		snapshot.files[path] = fileStamp{hash: hash}
	}
	return snapshot
}

// A framework mismatch blocks builds: unrelated edits are prevented and
// counted, a changed selection (go.mod) builds again, and a success ends the
// block. A failure an ordinary edit may fix never blocks.
func TestBuildBlockFollowsTheDependencyThatCausedIt(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{}
	base := blockTestSnapshot(map[string]string{"go.mod": "mod-1", "svc/api.go": "api-1"})
	mismatch := devBuildOperationError{operationID: "build-1", err: fmt.Errorf("verify: %w", &build.FrameworkMismatchError{Selected: "sha256:a", Producer: "sha256:b"})}
	s.recordBuildOutcome(base, mismatch)
	edited := blockTestSnapshot(map[string]string{"go.mod": "mod-1", "svc/api.go": "api-2"})
	for want := 1; want <= 3; want++ {
		block, prevented := s.preventBlockedBuild(edited)
		if !prevented || block.Reason != buildBlockFrameworkMismatch || block.Prevented != want {
			t.Fatalf("edit %d: block = %+v, prevented %t", want, block, prevented)
		}
	}
	status := s.status.BuildBlock
	if status == nil || status.PreventedBuilds != 3 || status.Reason != buildBlockFrameworkMismatch {
		t.Fatalf("status block = %+v", status)
	}
	reselected := blockTestSnapshot(map[string]string{"go.mod": "mod-2", "svc/api.go": "api-2"})
	if _, prevented := s.preventBlockedBuild(reselected); prevented {
		t.Fatal("a changed framework selection was not built")
	}
	s.recordBuildOutcome(reselected, nil)
	if s.status.BuildBlock != nil {
		t.Fatal("a successful build left the block")
	}

	s.recordBuildOutcome(base, errors.New("svc/api.go:3:1: syntax error"))
	if _, prevented := s.preventBlockedBuild(edited); prevented {
		t.Fatal("an ordinary compile error blocked the next edit")
	}
}

func TestPendingMigrationBlocksUntilMigrationInputsChange(t *testing.T) {
	t.Parallel()

	s := &devSupervisor{}
	base := blockTestSnapshot(map[string]string{"designs/db/migrations/0002.sql": "m2", "designs/api.go": "a1"})
	s.recordBuildOutcome(base, &pendingMigrationError{Service: "designs"})
	if _, prevented := s.preventBlockedBuild(blockTestSnapshot(map[string]string{"designs/db/migrations/0002.sql": "m2", "designs/api.go": "a2"})); !prevented {
		t.Fatal("a handler edit rebuilt against a pending migration")
	}
	if _, prevented := s.preventBlockedBuild(blockTestSnapshot(map[string]string{"designs/db/migrations/0002.sql": "m2b", "designs/api.go": "a2"})); prevented {
		t.Fatal("a changed migration was not built")
	}
}

func TestBuildBlockedEventAndRuntimeStatus(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	console := newRunConsole(&out, &bytes.Buffer{}, false, true, "demo", t.TempDir())
	block := devBuildBlock{Reason: buildBlockMigrationPending, Cause: "schema migration for designs is pending", Prevented: 4}
	console.BuildBlocked(block)
	envelope, err := machine.DecodeEvent[graph.Diagnostic](out.Bytes(), currentMachineSpecRevision())
	if err != nil {
		t.Fatalf("DecodeEvent: %v\n%s", err, out.String())
	}
	event, _ := envelope.Data.(map[string]any)
	data, _ := event["data"].(map[string]any)
	if event["type"] != "build.blocked" || data["reason"] != buildBlockMigrationPending || data["prevented_builds"] != float64(4) {
		t.Fatalf("event = %+v", event)
	}
	// The agent answers /runtime from the persisted session record, so the
	// block must survive the store.
	store, err := devdash.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blocked := &devdash.BuildBlock{Reason: buildBlockMigrationPending, Cause: "pending", Since: "2026-09-24T12:00:00Z", PreventedBuilds: 4}
	if err := store.UpsertApp(t.Context(), devdash.AppRecord{ID: "demo", SessionID: "demo-1", Root: "/app", Running: true, BuildBlock: blocked}); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetAppSession(t.Context(), "demo-1")
	if err != nil {
		t.Fatal(err)
	}
	status := newRuntimeStatus(appRecordStatus(record))
	if status.BuildBlock == nil || status.BuildBlock.PreventedBuilds != 4 || !status.Running {
		t.Fatalf("runtime status = %+v", status)
	}
}

// A blocked session keeps a record in its state root while its owner lives,
// which doctor reports as an error, and removes it when the block ends.
func TestSessionBuildBlockRecordFollowsTheBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, ".scenery", "sessions", "app-1")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &devSupervisor{agentSession: &localagent.Session{SessionID: "app-1", StateRoot: stateRoot, Owner: localagent.CurrentOwner("scenery up")}}
	snapshot := blockTestSnapshot(map[string]string{"go.mod": "mod-1"})
	s.recordBuildOutcome(snapshot, &build.FrameworkMismatchError{Selected: "sha256:a", Producer: "sha256:b"})
	s.preventBlockedBuild(snapshot)
	block, ok := liveSessionBuildBlock(stateRoot)
	if !ok || block.SessionID != "app-1" || block.Reason != buildBlockFrameworkMismatch || block.PreventedBuilds != 1 {
		t.Fatalf("record = %+v, live %t", block, ok)
	}
	if check := doctorBuildBlockCheck(root); check.Status != doctor.StatusError || check.Observed["prevented_builds"] != 1 {
		t.Fatalf("doctor check = %+v", check)
	}

	s.recordBuildOutcome(snapshot, nil)
	if _, err := os.Stat(sessionBuildBlockPath(stateRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record after a successful build: %v", err)
	}
	if check := doctorBuildBlockCheck(root); check.Status != doctor.StatusOK {
		t.Fatalf("doctor check after the block = %+v", check)
	}
}
