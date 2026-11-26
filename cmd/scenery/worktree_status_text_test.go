package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

func TestWorktreeStatusTextPreservesIndependentRuntimeEvidence(t *testing.T) {
	t.Parallel()
	serving := runtimeServingIdentity{Generation: 7, PID: "123", PublishedAt: "2026-11-16T00:00:00Z", SourceSnapshotDigest: "sha256:served-source", ContractRevision: "sha256:contract", ImplementationRevision: "sha256:implementation", BuildInputDigest: "sha256:build", FrameworkSourceDigest: "sha256:framework"}
	entry := worktreeStatusEntry{WorktreeDiscovery: localagent.WorktreeDiscovery{AppRoot: "/app", Status: "running"}, RootStatus: "present", SourceStatus: "present", OwnerStatus: "verified_running", MetadataStatus: "compatible", SourceFreshness: "blocked", Serving: []runtimeServingIdentity{serving}, Sessions: []localagent.Session{{SessionID: "session", Status: "degraded", StatusReason: "service unavailable"}}, BuildBlocks: []sessionBuildBlock{{SessionID: "session", Reason: "migration_pending", Cause: "Apply the pending migration.", Since: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), PreventedBuilds: 3}}}
	var out bytes.Buffer
	writeWorktreeStatusText(&out, []worktreeStatusEntry{entry})
	for _, want := range []string{"owner=verified_running", "freshness=blocked", "service unavailable", "generation=7 pid=123", "builds BLOCKED (migration_pending)", "3 build(s) prevented", "Apply the pending migration."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Serving []runtimeServingIdentity `json:"serving"`
	}
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{wire.Serving[0].SourceSnapshotDigest, wire.Serving[0].ContractRevision, wire.Serving[0].ImplementationRevision, wire.Serving[0].BuildInputDigest, wire.Serving[0].FrameworkSourceDigest, wire.Serving[0].PublishedAt} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("text omits JSON serving evidence %q", value)
		}
	}
}

func TestSourceWaitTextExplainsBlockedSupersededAndUnverifiedOutcomes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		result runtimeSourceWait
		want   []string
	}{
		{runtimeSourceWait{Outcome: "blocked", SourceFreshness: "blocked", TargetSourceSnapshotDigest: "desired", Serving: &runtimeServingIdentity{Generation: 4, SourceSnapshotDigest: "last-good"}, BuildBlock: &runtimeBuildBlock{Reason: "framework_mismatch", Cause: "Restart with the current binary.", Since: "now", PreventedBuilds: 2}}, []string{"target desired", "source=last-good", "BLOCKED (framework_mismatch)", "Restart with the current binary."}},
		{runtimeSourceWait{Outcome: "superseded", SourceFreshness: "stale", TargetSourceSnapshotDigest: "target", CurrentSourceSnapshotDigest: "new-edit"}, []string{"target target", "current source=new-edit", "serving identity: unverified"}},
		{runtimeSourceWait{Outcome: "failed", SourceFreshness: "unknown", CompileError: "handler does not compile"}, []string{"failed", "handler does not compile", "serving identity: unverified"}},
	} {
		var out bytes.Buffer
		writeSourceWaitText(&out, tc.result)
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s missing %q: %s", tc.result.Outcome, want, out.String())
			}
		}
	}
}
