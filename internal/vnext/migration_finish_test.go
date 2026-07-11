package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationFinishPlansAndAppliesNativeOnlyTransition(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	migration := `migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  native_service "house" {
    module = module.house
  }
}
`
	if err := os.WriteFile(filepath.Join(root, "scenery.migration.scn"), []byte(migration), 0o644); err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(root, "scenery.scn")
	contractSource, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contractSource = []byte(strings.Replace(string(contractSource), `"scenery.runtime-http/v1",`, `"scenery.runtime-http/v1",
    "scenery.legacy-bridge/v1",`, 1))
	if err := os.WriteFile(contractPath, contractSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil || !base.Valid() || base.Migration == nil {
		t.Fatalf("mixed base = %#v, %v", base, err)
	}
	evidence := migrationFinishTestEvidence(BuildMigrationStatus(base))
	plan, err := PlanMigrationFinish(root, MigrationFinishRequest{Caller: "test", BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision, OperationalEvidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.SourceEdits) == 0 || plan.PredictedWorkspaceRevision == base.WorkspaceRevision {
		t.Fatalf("finish plan = %#v", plan)
	}
	if plan.OperationalStateRevision == "" || plan.OperationalEvidence["v0_cli_consumers"] == "" {
		t.Fatalf("finish operational binding = %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(root, "scenery.migration.scn")); err != nil {
		t.Fatalf("planning mutated migration source: %v", err)
	}
	receipt, err := ApplyMigrationFinish(root, plan, MigrationFinishApplyOptions{Caller: "test", ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: base.Manifest.ContractRevision})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Mode != "native_only" || receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision {
		t.Fatalf("finish receipt = %#v", receipt)
	}
	if receipt.OperationalStateRevision != plan.OperationalStateRevision || receipt.OperationalEvidence["v0_cli_consumers"] == "" {
		t.Fatalf("finish receipt operational binding = %#v", receipt)
	}
	if _, err := os.Stat(filepath.Join(root, "scenery.migration.scn")); !os.IsNotExist(err) {
		t.Fatalf("migration source still exists: %v", err)
	}
	if _, err := ApplyMigrationFinish(root, plan, MigrationFinishApplyOptions{Caller: "test", ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: base.Manifest.ContractRevision}); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("finish replay error = %v", err)
	}
}

func TestMigrationFinishRequiresEveryOperationalClassAndConsumerGate(t *testing.T) {
	classes := []string{"stateless_route", "stateful_direct_service", "durable_execution", "schedule", "schema_owner", "event_consumer", "generated_client", "external_identity"}
	status := MigrationStatus{Services: []MigrationService{{Name: "house", State: "native", Active: "native", CutoverClasses: classes}}}
	blockers := migrationFinishEvidenceBlockers(status, nil)
	want := map[string]bool{
		"v0_cli_consumers": true, "retired_stateful_direct_service": true, "retired_durable_execution": true,
		"retired_schedule": true, "retired_schema_owner": true, "retired_event_consumer": true,
		"legacy_generated_client_consumers": true, "retired_external_identity": true,
	}
	for _, blocker := range blockers {
		delete(want, blocker.Address)
	}
	if len(want) != 0 || len(blockers) != 8 {
		t.Fatalf("operational blockers = %#v, missing expected = %#v", blockers, want)
	}
	if blockers = migrationFinishEvidenceBlockers(status, migrationFinishTestEvidence(status)); len(blockers) != 0 {
		t.Fatalf("complete evidence was rejected: %#v", blockers)
	}
}

func TestMigrationFinishRequiresRetirementOfOpenRollbackReceipt(t *testing.T) {
	activation := MigrationReceipt{PlanID: "activate-plan", Action: "activate_native", Service: "house", ReverseAction: "activate_legacy"}
	state := migrationFinishOperationalState{Receipts: []MigrationReceipt{activation}}
	blockers := migrationFinishReceiptBlockers(state)
	if len(blockers) != 1 || blockers[0].Code != "rollback_ownership_open" || blockers[0].Service != "house" {
		t.Fatalf("rollback blockers = %#v", blockers)
	}
	state.Receipts = append(state.Receipts, MigrationReceipt{PlanID: "retire-plan", Action: "retire", Service: "house"})
	if blockers := migrationFinishReceiptBlockers(state); len(blockers) != 0 {
		t.Fatalf("retired rollback receipt remained open: %#v", blockers)
	}
}

func TestMigrationFinishOperationalStateRevisionTracksReceipts(t *testing.T) {
	root := t.TempDir()
	before, beforeRevision, err := readMigrationFinishOperationalState(root)
	if err != nil || len(before.Receipts) != 0 || beforeRevision == "" {
		t.Fatalf("empty operational state = %#v %q %v", before, beforeRevision, err)
	}
	receipt := MigrationReceipt{APIVersion: "scenery.migrate.activation-receipt.v1", PlanID: "activation", Action: "activate_native", Service: "house", ReverseAction: "activate_legacy"}
	encoded, _ := json.Marshal(receipt)
	path := migrationReceiptPath(root, receipt.PlanID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	after, afterRevision, err := readMigrationFinishOperationalState(root)
	if err != nil || len(after.Receipts) != 1 || afterRevision == beforeRevision {
		t.Fatalf("receipt operational state = %#v %q %v", after, afterRevision, err)
	}
}

func migrationFinishTestEvidence(status MigrationStatus) map[string]string {
	evidence := map[string]string{"v0_cli_consumers": "test://v0-consumers-cleared"}
	for _, service := range status.Services {
		for _, class := range service.CutoverClasses {
			if class != "stateless_route" {
				evidence[migrationFinishEvidenceKey(class)] = "test://" + class + "-retired"
			}
		}
	}
	return evidence
}

func TestMigrationFinishRejectsLegacyGoAdapter(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "bridge"), root)
	rewriteFixtureSceneryReplace(t, root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("bridge base = %#v, %v", base, err)
	}
	_, err = PlanMigrationFinish(root, MigrationFinishRequest{Caller: "test", BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: base.Manifest.ContractRevision})
	if err == nil || !strings.Contains(err.Error(), "legacy_adapter") {
		t.Fatalf("finish blocker error = %v", err)
	}
}

func rewriteFixtureSceneryReplace(t *testing.T, root string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sceneryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(sceneryRoot), 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
