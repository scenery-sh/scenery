package vnext

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangePlanDoesNotWriteAndApplyIsRevisionBound(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "house", "scenery.package.scn")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  stringPointer(base.Manifest.ContractRevision),
		Operations:            []SemanticOperation{{Op: "value.set", Address: "house/execution/process_scene_direct", Path: "/spec/timeout", Value: "45m", Precondition: &ChangePrecondition{Equals: "40m"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterPlan, _ := os.ReadFile(path)
	if string(before) != string(afterPlan) {
		t.Fatal("planning wrote source")
	}
	receipt, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision || receipt.ContractRevision != plan.PredictedContractRevision {
		t.Fatalf("receipt = %#v plan = %#v", receipt, plan)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err == nil {
		t.Fatal("stale plan applied twice")
	}
}

func TestChangeRenameUpdatesTypedReferences(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision), Operations: []SemanticOperation{{Op: "resource.rename", Address: "house/operation/process_scene", Value: "process_roof_scene"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "house", "scenery.package.scn"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsJSONText(b, "operation \"process_roof_scene\"") || !containsJSONText(b, "operation.process_roof_scene") {
		t.Fatalf("source:\n%s", b)
	}
}

func TestChangeCreateAddsTypedResource(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanChanges(root, ChangeRequest{BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision), Operations: []SemanticOperation{{Op: "resource.create", Address: "app/authentication/test", Value: map[string]any{"provider": map[string]any{"$ref": "std.provider.standard_auth"}, "scheme": "session"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlan(root, plan, base.WorkspaceRevision, base.Manifest.ContractRevision); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resourcesByAddress(result.Manifest)["app/authentication/test"]; !ok {
		t.Fatal("created resource missing")
	}
}

func TestChangeApplyRequiresBoundApprovalAndRejectsReplay(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil || !base.Valid() {
		t.Fatalf("compile: %v %#v", err, base.Diagnostics)
	}
	plan := ChangePlan{
		APIVersion: "scenery.change-plan/v1", Application: base.Manifest.Application.Name,
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision),
		PredictedWorkspaceRevision: base.WorkspaceRevision, PredictedContractRevision: base.Manifest.ContractRevision,
		ImplementationStatus: "unchanged", DeploymentStatus: "unchanged", Caller: "agent:test",
		Capabilities: []string{"scenery.agent-mutation/v1"}, Operations: []SemanticOperation{}, Edits: []SourceEdit{},
		RequiredApprovals: []string{"risk_critical"}, RequiredCapabilities: []string{}, ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	plan.OperationsDigest = semanticOperationsDigest(plan.Operations)
	plan.PlanID = changePlanID(plan)
	options := ApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: stringPointer(base.Manifest.ContractRevision), Caller: plan.Caller}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err == nil || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("missing approval error = %v", err)
	}
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	token := ApprovalToken{PlanID: plan.PlanID, Caller: plan.Caller, RiskScopes: []string{"risk_critical"}, ExpiresAt: time.Now().UTC().Add(time.Minute), Signature: signature}
	options.ApprovalTokens = []ApprovalToken{token}
	options.VerifyApproval = func(token ApprovalToken, payload []byte) error {
		if token.Signature != signature || len(payload) == 0 {
			return os.ErrPermission
		}
		return nil
	}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyChangePlanWithOptions(root, plan, options); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("replay error = %v", err)
	}
}

func TestRepairPlanUsesNullContractRevisionAndEstablishesContract(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	path := filepath.Join(root, "house", "scenery.package.scn")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(source, []byte("\n  implementation {\n    constructor = \"NewService\"\n  }\n"), nil, 1)
	if bytes.Equal(source, broken) {
		t.Fatal("fixture implementation block not found")
	}
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if base.Manifest != nil || base.PartialGraph == nil || base.WorkspaceRevision == "" {
		t.Fatalf("invalid base = %#v", base)
	}
	plan, err := PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision,
		BaseContractRevision:  nil,
		Caller:                "agent:repair",
		Operations: []SemanticOperation{{
			Op: "value.set", Address: "house/service/house", Path: "/spec/implementation",
			Value: map[string]any{"constructor": "NewService"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BaseContractRevision != nil || plan.PredictedContractRevision == "" {
		t.Fatalf("repair plan revisions = %#v", plan)
	}
	receipt, err := ApplyChangePlanWithOptions(root, plan, ApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: nil, Caller: plan.Caller})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractRevision == "" {
		t.Fatalf("repair receipt = %#v", receipt)
	}
	result, err := Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("repaired check: %v %#v", err, result.Diagnostics)
	}
}

func TestChangePlanRejectsSecretPlaintext(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	base, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PlanChanges(root, ChangeRequest{
		BaseWorkspaceRevision: base.WorkspaceRevision, BaseContractRevision: stringPointer(base.Manifest.ContractRevision),
		Operations: []SemanticOperation{{Op: "value.set", Address: "house/service/house", Path: "/spec/provider_token", Value: "top-secret"}},
	})
	if err == nil || !strings.Contains(err.Error(), "secret plaintext") {
		t.Fatalf("secret mutation error = %v", err)
	}
}

func TestModuleUpgradeResolvesLocalPackageAndRejectsUnavailableRegistryProfile(t *testing.T) {
	local := Resource{Kind: "scenery.module/v1", Spec: map[string]any{
		"source": "./house", "workspace_package_root": "house", "package": map[string]any{"version": "2.1.0"},
	}}
	if err := validateLocalModuleUpgrade(local, ">= 2.0.0, < 3.0.0"); err != nil {
		t.Fatalf("local upgrade constraint was not resolved: %v", err)
	}
	if err := validateLocalModuleUpgrade(local, ">= 3.0.0"); err == nil || !strings.Contains(err.Error(), "failed_precondition") {
		t.Fatalf("incompatible local upgrade = %v", err)
	}
	registry := Resource{Kind: "scenery.module/v1", Spec: map[string]any{"source": "registry.scenery.dev/example/house", "package": map[string]any{"version": "2.1.0"}}}
	if err := validateLocalModuleUpgrade(registry, ">= 2.2.0"); err == nil || !strings.Contains(err.Error(), "capability_unavailable") {
		t.Fatalf("registry upgrade without registry profile = %v", err)
	}
}
