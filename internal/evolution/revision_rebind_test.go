package evolution

import (
	"strings"
	"testing"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestRevisionRebindPreservesRenameEvidence(t *testing.T) {
	base := &Manifest{SpecRevision: "sha256:current", ContractRevision: "sha256:new-base", Application: graph.ApplicationIdentity{Name: "app"}, Resources: []Resource{{Address: "app/record/before", Kind: "scenery.record", Name: "before", Module: "app", Spec: map[string]any{}}}}
	target := &Manifest{SpecRevision: "sha256:current", ContractRevision: "sha256:new-target", Application: graph.ApplicationIdentity{Name: "app"}, Resources: []Resource{{Address: "app/record/after", Kind: "scenery.record", Name: "after", Module: "app", Spec: map[string]any{}}}}
	receipt := RenameReceipt{From: "app/record/before", To: "app/record/after", BaseContractRevision: "sha256:old-base", TargetContractRevision: "sha256:old-target"}
	receipt.Digest = RenameReceiptDigest(receipt)
	baseRebind, err := NewRevisionRebind("sha256:old-spec", receipt.BaseContractRevision, base, "cache-only generated Go migration")
	if err != nil {
		t.Fatal(err)
	}
	targetRebind, err := NewRevisionRebind("sha256:old-spec", receipt.TargetContractRevision, target, "cache-only generated Go migration")
	if err != nil {
		t.Fatal(err)
	}
	diff := CompareManifests(base, target, CompareOptions{Renames: []RenameReceipt{receipt}, Rebinds: []RevisionRebind{baseRebind, targetRebind}})
	if len(diff.Changes) == 0 || diff.Changes[0].Operation != "rename" {
		t.Fatalf("diff = %#v", diff.Changes)
	}
	if receipt.Digest != RenameReceiptDigest(receipt) {
		t.Fatal("historical receipt was mutated")
	}
	baseRebind.ProjectionHash = "sha256:tampered"
	diff = CompareManifests(base, target, CompareOptions{Renames: []RenameReceipt{receipt}, Rebinds: []RevisionRebind{baseRebind, targetRebind}})
	if len(diff.Changes) == 0 || diff.Changes[0].Operation == "rename" {
		t.Fatalf("tampered rebind accepted: %#v", diff.Changes)
	}
}

func TestPendingChangePlanReportsRevisionSchemeChanged(t *testing.T) {
	plan := ChangePlan{ArtifactIdentity: machine.ArtifactIdentity{SpecRevision: "sha256:old"}}
	_, err := ApplyChangePlanWithOptions(t.TempDir(), plan, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "revision_scheme_changed") {
		t.Fatalf("error = %v", err)
	}
}
