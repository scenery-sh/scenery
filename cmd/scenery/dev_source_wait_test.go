package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testSourceWaitPoll = 2 * time.Millisecond

func sourceWaitSupervisor(t *testing.T) (*devSupervisor, string) {
	t.Helper()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "app \"source-wait\" {}\n")
	writeWatchFile(t, root, "service.go", "package app\n")
	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := &runtimeServingIdentity{Generation: 1, SourceSnapshotDigest: snapshotFingerprint(snapshot)}
	return &devSupervisor{root: root, servingSnapshot: &snapshot, servingIdentity: identity, sourceWaitPoll: testSourceWaitPoll}, root
}

func TestSourceWaitReportsTheServedTargetSnapshot(t *testing.T) {
	s, root := sourceWaitSupervisor(t)
	served, err := s.waitForSourceSnapshot(context.Background(), time.Second)
	if err != nil || served.Outcome != "served" || served.Serving == nil || served.TargetSourceSnapshotDigest != served.Serving.SourceSnapshotDigest {
		t.Fatalf("current source = %+v, %v", served, err)
	}

	writeWatchFile(t, root, "service.go", "package app\nvar Edited = true\n")
	edited, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	target := snapshotFingerprint(edited)
	go func() {
		time.Sleep(2 * testSourceWaitPoll)
		s.mu.Lock()
		s.servingSnapshot, s.servingIdentity = &edited, &runtimeServingIdentity{Generation: 2, SourceSnapshotDigest: target}
		s.mu.Unlock()
	}()
	published, err := s.waitForSourceSnapshot(context.Background(), 5*time.Second)
	if err != nil || published.Outcome != "served" || published.Serving.Generation != 2 || published.TargetSourceSnapshotDigest != target {
		t.Fatalf("published edit = %+v, %v", published, err)
	}
}

func TestSourceWaitEndsOnFailureBlockSupersessionAndTimeout(t *testing.T) {
	s, root := sourceWaitSupervisor(t)
	writeWatchFile(t, root, "service.go", "package app\nvar Broken = \n")
	broken, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	s.lastBuild = &devBuildAttempt{SourceSnapshotDigest: snapshotFingerprint(broken), Error: "syntax error"}
	if failed, err := s.waitForSourceSnapshot(context.Background(), time.Second); err != nil || failed.Outcome != "failed" || failed.CompileError != "syntax error" {
		t.Fatalf("failed build = %+v, %v", failed, err)
	}

	s.lastBuild = nil
	if timedOut, err := s.waitForSourceSnapshot(context.Background(), 3*testSourceWaitPoll); err != nil || timedOut.Outcome != "timeout" || timedOut.SourceFreshness != "stale" || timedOut.Serving == nil {
		t.Fatalf("pending build = %+v, %v", timedOut, err)
	}

	go func() {
		time.Sleep(2 * testSourceWaitPoll)
		writeWatchFile(t, root, "service.go", "package app\nvar Later = true\n")
	}()
	superseded, err := s.waitForSourceSnapshot(context.Background(), 5*time.Second)
	if err != nil || superseded.Outcome != "superseded" || superseded.CurrentSourceSnapshotDigest == "" || superseded.CurrentSourceSnapshotDigest == superseded.TargetSourceSnapshotDigest {
		t.Fatalf("superseding edit = %+v, %v", superseded, err)
	}

	s.buildBlock = &devBuildBlock{Reason: buildBlockGeneratedClients, Cause: "client check failed"}
	if blocked, err := s.waitForSourceSnapshot(context.Background(), time.Second); err != nil || blocked.Outcome != "blocked" || blocked.BuildBlock == nil || blocked.BuildBlock.Cause != "client check failed" {
		t.Fatalf("blocked build = %+v, %v", blocked, err)
	}
}

func TestSourceWaitResultsMatchTheCheckedSchema(t *testing.T) {
	s, root := sourceWaitSupervisor(t)
	s.servingIdentity.PublishedAt = "2026-10-19T00:00:00Z"
	served, err := s.waitForSourceSnapshot(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, "service.go", "package app\nvar Broken = \n")
	broken, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	s.lastBuild = &devBuildAttempt{SourceSnapshotDigest: snapshotFingerprint(broken), Error: "syntax error"}
	failed, err := s.waitForSourceSnapshot(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.dev-runtime.wait.schema.json")
	for _, result := range []runtimeSourceWait{served, failed} {
		if diagnostics := validateHarnessJSONSchemaFile(schemaPath, result); len(diagnostics) != 0 {
			t.Fatalf("%s result does not match its schema: %v", result.Outcome, diagnostics)
		}
	}
	if !served.OK || failed.OK {
		t.Fatalf("ok flags = served %t failed %t", served.OK, failed.OK)
	}
}

func TestSourceWaitOmitsUncapturedTarget(t *testing.T) {
	s, root := sourceWaitSupervisor(t)
	if err := os.Remove(filepath.Join(root, "app.scn")); err != nil {
		t.Fatal(err)
	}
	result, err := s.waitForSourceSnapshot(context.Background(), time.Second)
	if err != nil || result.Outcome != "source_missing" {
		t.Fatalf("missing source = %+v, %v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["target_source_snapshot_digest"]; present {
		t.Fatalf("uncaptured target = %s", encoded)
	}
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.dev-runtime.wait.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, result); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	fields["target_source_snapshot_digest"] = ""
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, fields); len(diagnostics) == 0 {
		t.Fatal("schema accepted an empty target digest")
	}
}
