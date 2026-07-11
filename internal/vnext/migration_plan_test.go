package vnext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationShadowPlanAppliesAtomicallyAndBlocksUnprovenActivation(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "../../../..", filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(`{"name":"nativeapp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	migration := `migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  legacy_gateway "default" {
    target = http_gateway.public_api
  }

  legacy_service "house" {
    package   = "./house"
    namespace = "house"
    target    = go_target.development
  }
}
`
	if err := os.WriteFile(filepath.Join(root, "scenery.migration.scn"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("mixed base: %v %#v", err, base.Diagnostics)
	}
	plan, err := PlanMigrationTransition(root, MigrationPlanRequest{Action: "shadow", Service: "house", Caller: "test", BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision})
	if err != nil {
		t.Fatal(err)
	}
	if plan.PredictedWorkspaceRevision == base.WorkspaceRevision || plan.PredictedContractRevision != base.Manifest.ContractRevision || len(plan.Edits) == 0 {
		t.Fatalf("shadow plan = %#v", plan)
	}
	receipt, err := ApplyMigrationPlan(root, plan, MigrationApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: base.Manifest.ContractRevision, Caller: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Active != "legacy" || receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision {
		t.Fatalf("shadow receipt = %#v", receipt)
	}
	data, err := os.ReadFile(filepath.Join(root, "scenery.migration.scn"))
	if err != nil || !strings.Contains(string(data), `shadow_service "house"`) || !strings.Contains(string(data), `active = "legacy"`) {
		t.Fatalf("shadow source = %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "scenery.migration.ledger")); err != nil {
		t.Fatalf("migration ledger missing: %v", err)
	}
	if _, err := ApplyMigrationPlan(root, plan, MigrationApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: base.Manifest.ContractRevision, Caller: "test"}); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("migration replay error = %v", err)
	}
	shadow, err := Compile(root)
	if err != nil || !shadow.Valid() {
		t.Fatalf("shadow compile: %v %#v", err, shadow.Diagnostics)
	}
	comparison, err := CompareMigrationService(shadow, "house")
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Equal || comparison.Complete || comparison.ComparisonDigest == "" || len(comparison.Differences) == 0 {
		t.Fatalf("comparison = %#v", comparison)
	}
	if _, err := PlanMigrationTransition(root, MigrationPlanRequest{Action: "activate_native", Service: "house", Caller: "test", BaseWorkspaceRevision: shadow.WorkspaceRevision, BaseContractRevision: shadow.Manifest.ContractRevision}); err == nil || !strings.Contains(err.Error(), "comparison") {
		t.Fatalf("unproven activation error = %v", err)
	}
	if err := mutateMigrationOwnership(root, "house", "activate_native"); err != nil {
		t.Fatal(err)
	}
	native, err := Compile(root)
	if err != nil || !native.Valid() {
		t.Fatalf("native-active compile: %v %#v", err, native.Diagnostics)
	}
	if _, err := PlanMigrationTransition(root, MigrationPlanRequest{Action: "activate_legacy", Service: "house", Caller: "test", BaseWorkspaceRevision: native.WorkspaceRevision, BaseContractRevision: native.Manifest.ContractRevision}); err == nil || !strings.Contains(err.Error(), "activation receipt") {
		t.Fatalf("receipt-free rollback error = %v", err)
	}
}
