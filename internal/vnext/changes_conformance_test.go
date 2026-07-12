package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangeApplyRejectsCallerRecomputedPlan(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.Edits = []SourceEdit{{Path: "caller-authored.txt", BeforeDigest: byteDigest(nil), After: []byte("not planned\n"), AfterExists: true, Mode: 0o644}}
	tampered.ExpiresAt = tampered.ExpiresAt.Add(time.Hour)
	tampered.PlanID = changePlanID(tampered)
	if _, err := ApplyChangePlanWithOptions(root, tampered, ApplyOptions{
		ExpectedWorkspaceRevision: base.WorkspaceRevision,
		ExpectedContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Caller:                    tampered.Caller,
	}); err == nil || !strings.Contains(err.Error(), "issued plan") {
		t.Fatalf("tampered plan error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "caller-authored.txt")); !os.IsNotExist(err) {
		t.Fatalf("caller-authored edit reached workspace: %v", err)
	}
}

func TestChangeRenameModuleRecordsDescendantContinuity(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "house"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1", "scenery.compatibility-core/v1"]
}
application "module_rename" { version = "1.0.0" }
module "house" { source = "./house" }
`)
	writeNestedModuleFile(t, filepath.Join(root, "house", "scenery.package.scn"), `package "house" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
}
record "point" {
  field "x" { type = float64 }
}
export "point" { value = record.point }
`)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, base.Diagnostics)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{{Op: "resource.rename", Address: "app/module/house", Value: "home"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	renamed := map[string]string{}
	for _, receipt := range plan.Renames {
		renamed[receipt.From] = receipt.To
	}
	if renamed["app/module/house"] != "app/module/home" || renamed["house/record/point"] != "home/record/point" {
		t.Fatalf("module rename receipts = %#v", plan.Renames)
	}
	for _, change := range plan.SemanticDiff.Changes {
		if change.Address == "house/record/point" || change.Address == "home/record/point" {
			if change.Operation != "rename" {
				t.Fatalf("descendant continuity degraded to %#v", change)
			}
		}
	}
}
