package main

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestAssistantStageAttemptCancellationJoinsPrivateWork(t *testing.T) {
	s, original, _ := assistantStageFixture(t)
	candidate := nextAssistantStageResult(original)
	candidate.Root, candidate.WorkspaceRevision = s.config.Root, "workspace-1"
	before := s.RuntimeConfig()
	entered := make(chan struct{})
	s.config.InstallDeps = func(ctx context.Context, _, _, _ string) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	attempt := s.beginStage(context.Background(), candidate)
	<-entered
	if !attempt.matches(candidate) {
		t.Error("identical graph and definitions did not match")
	}
	changed := *candidate
	changed.WorkspaceRevision = "workspace-2"
	if attempt.matches(&changed) {
		t.Error("changed workspace reused a speculative stage")
	}
	changed = *candidate
	changed.ImplementationRevisions = map[string]string{"app/assistant/support": "changed-runtime"}
	if attempt.matches(&changed) {
		t.Error("changed assistant definition reused a speculative stage")
	}
	attempt.release()
	stage, err := attempt.wait()
	if stage == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("release did not cancel and join preparation: %v", err)
	}
	if !reflect.DeepEqual(before, s.RuntimeConfig()) {
		t.Fatal("speculative stage changed active descriptors")
	}
	entries, err := os.ReadDir(s.config.StateRoot)
	if err != nil || len(entries) != 1 {
		t.Fatalf("candidate resources leaked or active files were removed: %v %v", entries, err)
	}
}
