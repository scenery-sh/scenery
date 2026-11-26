package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const testSourceWaitPoll = 2 * time.Millisecond

// sourceWaitSupervisor is a supervisor whose host has not started.
func sourceWaitSupervisor(t *testing.T) *devSupervisor {
	t.Helper()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "app \"source-wait\" {}\n")
	return &devSupervisor{root: root, sourceWaitPoll: testSourceWaitPoll}
}

// Before a host serves, a failed or blocked start ends the wait; the running
// host owns every later answer.
func TestSourceWaitWithoutHostEndsOnFailureBlockAndTimeout(t *testing.T) {
	s := sourceWaitSupervisor(t)
	if timedOut, err := s.waitForSourceSnapshot(context.Background(), 3*testSourceWaitPoll); err != nil || timedOut.Outcome != "timeout" || timedOut.OK {
		t.Fatalf("pending start = %+v, %v", timedOut, err)
	}
	s.lastBuild = &devBuildAttempt{Error: "syntax error"}
	failed, err := s.waitForSourceSnapshot(context.Background(), time.Second)
	if err != nil || failed.Outcome != "failed" || failed.CompileError != "syntax error" {
		t.Fatalf("failed start = %+v, %v", failed, err)
	}
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.dev-runtime.wait.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, failed); len(diagnostics) != 0 {
		t.Fatalf("failed result does not match its schema: %v", diagnostics)
	}
	s.lastBuild = nil
	s.buildBlock = &devBuildBlock{Reason: buildBlockGeneratedClients, Cause: "client check failed"}
	if blocked, err := s.waitForSourceSnapshot(context.Background(), time.Second); err != nil || blocked.Outcome != "blocked" || blocked.BuildBlock == nil || blocked.BuildBlock.Cause != "client check failed" {
		t.Fatalf("blocked start = %+v, %v", blocked, err)
	}
	if identity, freshness := s.currentServingState(context.Background()); identity != nil || freshness != "blocked" {
		t.Fatalf("serving state = %+v, %q", identity, freshness)
	}
}
