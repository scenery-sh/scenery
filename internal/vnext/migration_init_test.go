package vnext

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationInitializationPlansWithoutWritingAndAppliesAtomically(t *testing.T) {
	root := t.TempDir()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".scenery.json": `{"name":"basicapp"}`,
		"go.mod":        "module example.test/basicapp\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => " + filepath.ToSlash(repositoryRoot) + "\n",
		"service/api.go": `package service

import "context"

//scenery:service
type Service struct{}

type EchoRequest struct {
	Title string ` + "`query:\"title\"`" + `
	Header string ` + "`header:\"X-Echo\"`" + `
	Body string ` + "`json:\"body\"`" + `
}

type EchoResponse struct {
	Message string ` + "`json:\"message\"`" + `
}

//scenery:api public method=GET path=/ping
func (service *Service) Ping(context.Context) error { return nil }

//scenery:api public method=GET,POST path=/echo/:name
func (service *Service) Echo(_ context.Context, name string, input *EchoRequest) (*EchoResponse, error) {
	return &EchoResponse{Message: name + input.Title}, nil
}

//scenery:api private
func (service *Service) Secret(context.Context) (*EchoResponse, error) {
	return &EchoResponse{Message: "secret"}, nil
}
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := PlanMigrationInitialization(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanID == "" || plan.BaseWorkspaceRevision == "" || plan.PredictedWorkspaceRevision == "" || plan.PredictedContractRevision == "" || len(plan.Edits) != 2 {
		t.Fatalf("initialization plan = %#v", plan)
	}
	for _, name := range []string{"scenery.scn", "scenery.migration.scn"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("planning wrote %s: %v", name, err)
		}
	}

	receipt, err := ApplyMigrationInitialization(root, plan, MigrationInitializationApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision,
		Caller:                    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.WorkspaceRevision != plan.PredictedWorkspaceRevision || receipt.ContractRevision != plan.PredictedContractRevision || len(receipt.Services) != 1 || receipt.Services[0] != "service" {
		t.Fatalf("initialization receipt = %#v", receipt)
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("initialized compile: %v %#v", err, result.Diagnostics)
	}
	status := BuildMigrationStatus(result)
	service, err := status.Service("service")
	if err != nil || status.Mode != "mixed" || service.Active != "legacy" || service.LegacyTarget != "go_target.legacy" || len(status.Constructs) == 0 {
		t.Fatalf("initialized status = %#v, %v", status, err)
	}
	for _, construct := range status.Constructs {
		if construct.Address == "service/binding/echo_http_1" && (construct.ActiveOwner != "legacy" || construct.GuaranteeClassification != "verified" || len(construct.ExternalIdentities) != 1) {
			t.Fatalf("typed legacy construct = %#v", construct)
		}
		if construct.OperationalStateRevision == "" || len(construct.CLIProtocolDependencies) != 2 || construct.ExternalAliases == nil || construct.DeployedConsumerGates == nil {
			t.Fatalf("operational status fields are incomplete: %#v", construct)
		}
	}
	if _, err := ApplyMigrationInitialization(root, plan, MigrationInitializationApplyOptions{ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, Caller: "test"}); err == nil {
		t.Fatal("initialization plan replay succeeded")
	}

	candidatePlan, err := PlanMigrationCandidate(root, MigrationCandidateRequest{
		Service:               "service",
		Caller:                "test",
		BaseWorkspaceRevision: receipt.WorkspaceRevision,
		BaseContractRevision:  receipt.ContractRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidatePlan.NativeCandidateDigest == "" || len(candidatePlan.Edits) < 3 {
		t.Fatalf("candidate plan = %#v", candidatePlan)
	}
	if _, err := os.Stat(filepath.Join(root, "service", "scenery.package.scn")); !os.IsNotExist(err) {
		t.Fatalf("candidate planning wrote package source: %v", err)
	}
	candidateReceipt, err := ApplyMigrationCandidate(root, candidatePlan, MigrationCandidateApplyOptions{
		ExpectedWorkspaceRevision: receipt.WorkspaceRevision,
		ExpectedContractRevision:  receipt.ContractRevision,
		Caller:                    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateReceipt.NativeCandidateDigest != candidatePlan.NativeCandidateDigest || candidateReceipt.Active != "legacy" {
		t.Fatalf("candidate receipt = %#v", candidateReceipt)
	}
	shadowResult, err := Compile(root)
	if err != nil || !shadowResult.Valid() {
		t.Fatalf("candidate compile: %v %#v", err, shadowResult.Diagnostics)
	}
	shadowService, err := BuildMigrationStatus(shadowResult).Service("service")
	if err != nil || shadowService.State != "shadow" || shadowService.Active != "legacy" || shadowService.NativeCandidateDigest == "" {
		t.Fatalf("candidate status = %#v, %v", shadowService, err)
	}

	comparison, err := CompareMigrationService(shadowResult, "service")
	if err != nil || !comparison.Complete {
		unknown := make([]string, 0)
		for _, difference := range comparison.Differences {
			if difference.Classification == CompatibilityUnknown || difference.Classification == SecurityUnknown {
				unknown = append(unknown, difference.Address+difference.Path)
			}
		}
		t.Fatalf("candidate comparison incomplete: unknown=%v, error=%v", canonicalStrings(unknown), err)
	}
	evidence := migrationTestEvidence(shadowService.CutoverClasses, false)
	activationPlan, err := PlanMigrationTransition(root, MigrationPlanRequest{
		Action: "activate_native", Service: "service", Caller: "test",
		BaseWorkspaceRevision: shadowResult.WorkspaceRevision, BaseContractRevision: shadowResult.Manifest.ContractRevision,
		ApprovedComparisonDigest: comparison.ComparisonDigest, OperationalEvidence: evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	activationReceipt, err := ApplyMigrationPlan(root, activationPlan, migrationTestApplyOptions(activationPlan))
	if err != nil {
		t.Fatal(err)
	}
	if activationReceipt.Active != "native" || activationReceipt.ReverseAction != "activate_legacy" {
		t.Fatalf("activation receipt = %#v", activationReceipt)
	}
	nativeResult, err := Compile(root)
	if err != nil || !nativeResult.Valid() {
		t.Fatalf("native-active compile: %v %#v", err, nativeResult.Diagnostics)
	}
	nativeStatus := BuildMigrationStatus(nativeResult)
	for _, construct := range nativeStatus.Constructs {
		if construct.Service == "service" && construct.CutoverClass == "stateful_direct_service" {
			if !construct.OperationalReady || len(construct.OperationalEvidence["stateful_direct_service"]) == 0 {
				t.Fatalf("activation receipt was not projected into operational status: %#v", construct)
			}
		}
	}
	rollbackPlan, err := PlanMigrationTransition(root, MigrationPlanRequest{
		Action: "activate_legacy", Service: "service", Caller: "test",
		BaseWorkspaceRevision: nativeResult.WorkspaceRevision, BaseContractRevision: nativeResult.Manifest.ContractRevision,
		ApprovedComparisonDigest: comparison.ComparisonDigest, ActivationReceiptPlanID: activationReceipt.PlanID,
		OperationalEvidence: migrationTestEvidence(shadowService.CutoverClasses, true),
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackReceipt, err := ApplyMigrationPlan(root, rollbackPlan, migrationTestApplyOptions(rollbackPlan))
	if err != nil {
		t.Fatal(err)
	}
	if rollbackReceipt.Active != "legacy" || rollbackReceipt.ReverseAction != "activate_native" {
		t.Fatalf("rollback receipt = %#v", rollbackReceipt)
	}
}

func migrationTestEvidence(classes []string, rollback bool) map[string]string {
	evidence := map[string]string{}
	for _, class := range classes {
		if class == "stateless_route" {
			continue
		}
		key := class
		if rollback {
			key = "rollback_" + class
		}
		evidence[key] = "fixture-proof:" + class
	}
	return evidence
}

func TestRetiredNativeServiceDoesNotRequireEphemeralCutoverReceipts(t *testing.T) {
	serviceResource := Resource{Address: "house/service/house", Kind: "scenery.service/v1", Module: "house", Name: "house", Spec: map[string]any{
		"implementation": map[string]any{"adapter": "native", "root": "house"},
	}}
	service := MigrationService{
		Name: "house", State: "native", Active: "native", Module: "module.house", NativeCandidateValid: true,
		GuaranteeClassification: "verified", MigrationDisposition: "native_equivalent", RollbackSafety: "unavailable",
		CutoverClasses: []string{"stateful_direct_service"},
	}
	result := &Result{
		Root: t.TempDir(), ContractStatus: "valid",
		Manifest: &Manifest{Profiles: []string{"scenery.compiler-core/v1", "scenery.compatibility-core/v1", "scenery.legacy-bridge/v1"}, Resources: []Resource{serviceResource}},
		Migration: &Migration{
			Frontend: "scenery.legacy.v0", Services: []MigrationService{service},
			LegacyCandidates: map[string][]Resource{}, NativeCandidates: map[string][]Resource{"house": {serviceResource}},
		},
	}
	status := BuildMigrationStatus(result)
	if !status.Ready || len(status.Constructs) != 1 {
		t.Fatalf("retired native status = %#v", status)
	}
	construct := status.Constructs[0]
	if !construct.OperationalReady || construct.Blocking || construct.StatefulOperationalState.Drain != "not_applicable" || len(construct.DeployedConsumerGates) != 0 {
		t.Fatalf("retired native construct = %#v", construct)
	}
}

func migrationTestApplyOptions(plan MigrationPlan) MigrationApplyOptions {
	options := MigrationApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision,
		ExpectedContractRevision:  plan.BaseContractRevision,
		Caller:                    plan.Caller,
	}
	if len(plan.RequiredApprovals) == 0 {
		return options
	}
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	options.ApprovalTokens = []ApprovalToken{{
		PlanID: plan.PlanID, Caller: plan.Caller, RiskScopes: append([]string(nil), plan.RequiredApprovals...),
		ExpiresAt: time.Now().UTC().Add(time.Minute), Signature: signature,
	}}
	options.VerifyApproval = func(token ApprovalToken, payload []byte) error {
		if token.Signature != signature || len(payload) == 0 {
			return os.ErrPermission
		}
		return nil
	}
	return options
}
