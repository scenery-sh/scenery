package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"scenery.sh/internal/build"
)

func TestServingSourceStatusKeepsPublishedIdentityAcrossEdits(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "app \"source-status\" {}\n")
	writeWatchFile(t, root, "service.go", "package app\n")
	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	identity := &runtimeServingIdentity{Generation: 2, PID: "123", BuildInputDigest: "published", SourceSnapshotDigest: snapshotFingerprint(snapshot)}
	s := &devSupervisor{root: root, servingSnapshot: &snapshot, servingIdentity: identity}
	if serving, freshness := s.currentServingState(context.Background()); serving != identity || freshness != "current" {
		t.Fatalf("unchanged publication = %+v %s", serving, freshness)
	}
	writeWatchFile(t, root, "service.go", "package app\nvar Changed = true\n")
	if serving, freshness := s.currentServingState(context.Background()); serving != identity || freshness != "stale" || serving.BuildInputDigest != "published" {
		t.Fatalf("unapplied edit changed serving identity = %+v %s", serving, freshness)
	}
	s.buildBlock = &devBuildBlock{Reason: buildBlockGeneratedClients}
	if _, freshness := s.currentServingState(context.Background()); freshness != "blocked" {
		t.Fatalf("deterministic block = %s", freshness)
	}
	s.processes = &devProcessModel{generation: 7}
	if serving, _ := s.currentServingState(context.Background()); serving.Generation != 7 || identity.Generation != 2 || serving.BuildInputDigest != "published" {
		t.Fatalf("same-source generation changed the captured source identity: %+v / %+v", serving, identity)
	}
}

func TestGoBuildDiagnosticSurvivesOrchestration(t *testing.T) {
	underlying := errors.New("dependency/module is unavailable")
	err := fmt.Errorf("detached build: %w", &build.GoCommandError{Err: underlying})
	for _, wrapped := range []error{err, preserveCLIDiagnostic(err)} {
		diagnostic := cliErrorDiagnostic(wrapped)
		if cliExitCode(wrapped) != 3 || diagnostic.Code != "SCN6202" || diagnostic.ReportToken != "" || len(diagnostic.Suggestions) == 0 || !errors.Is(wrapped, underlying) {
			t.Fatalf("build failure = %v, %+v", wrapped, diagnostic)
		}
	}
}
