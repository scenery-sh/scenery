package deployplan

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	scenery "scenery.sh"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

type testDeploymentProvider struct {
	planned     bool
	applied     bool
	rolledBack  bool
	applyErr    error
	nilRollback bool
}

type blockingDeploymentProvider struct {
	testDeploymentProvider
	entered chan struct{}
	release chan struct{}
}

func (provider *blockingDeploymentProvider) Apply(context.Context, DeploymentProviderPlan) (func(context.Context) error, error) {
	provider.applied = true
	close(provider.entered)
	<-provider.release
	return func(context.Context) error { provider.rolledBack = true; return nil }, nil
}

func (provider *testDeploymentProvider) Plan(_ context.Context, request DeploymentProviderRequest) (DeploymentProviderPlan, error) {
	provider.planned = true
	return DeploymentProviderPlan{
		ProviderABI: deploymentProviderABI,
		Actions:     []DeploymentAction{{Kind: "configure", Address: request.Instances[0], After: map[string]any{"planned": true}}},
	}, nil
}

func (provider *testDeploymentProvider) Apply(context.Context, DeploymentProviderPlan) (func(context.Context) error, error) {
	provider.applied = true
	if provider.applyErr != nil {
		return nil, provider.applyErr
	}
	if provider.nilRollback {
		return nil, nil
	}
	return func(context.Context) error { provider.rolledBack = true; return nil }, nil
}

func (provider *testDeploymentProvider) Rollback(context.Context, DeploymentProviderPlan) error {
	provider.rolledBack = true
	return nil
}

func TestDeploymentPlanAndApplyBindExactRevisions(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "external")
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{
		Deployment: "preview", BaseWorkspaceRevision: result.WorkspaceRevision,
		BaseContractRevision: result.Manifest.ContractRevision, ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != deploymentPlanKind || plan.DeploymentRevision == "" || len(plan.ProviderPlans) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	for _, providerPlan := range plan.ProviderPlans {
		if providerPlan.Digest == "" || providerPlan.RequiresApply {
			t.Fatalf("external/core provider plan = %#v", providerPlan)
		}
	}
	receipt, err := ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: result.WorkspaceRevision, ExpectedContractRevision: result.Manifest.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.DeploymentRevision != plan.DeploymentRevision || len(receipt.ProviderPlanDigests) != 2 {
		t.Fatalf("receipt = %#v", receipt)
	}
	stateBytes, err := os.ReadFile(deploymentStatePath(root, "preview"))
	if err != nil {
		t.Fatalf("deployment state: %v", err)
	}
	var state deploymentState
	if err := machine.DecodeArtifact(stateBytes, &state, &state.ArtifactIdentity, deploymentStateKind, deploymentStateSchemaDescriptor, "redeploy"); err != nil {
		t.Fatalf("deployment state identity: %v", err)
	}
	if state.Plan.PlanID != plan.PlanID || state.Receipt.PlanID != receipt.PlanID {
		t.Fatalf("deployment state = %#v", state)
	}
	if _, err := ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{ExpectedWorkspaceRevision: result.WorkspaceRevision, ExpectedContractRevision: result.Manifest.ContractRevision, ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test"}, nil); err == nil || !strings.Contains(err.Error(), "already applied") {
		t.Fatalf("replay error = %v", err)
	}
	after, err := compiler.CompileContractGraph(root)
	if err != nil || after.WorkspaceRevision != result.WorkspaceRevision {
		t.Fatalf("deployment ledger changed managed workspace: %v before=%s after=%s", err, result.WorkspaceRevision, after.WorkspaceRevision)
	}
}

func TestManagedDeploymentRequiresAndInvokesProviderAdapter(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "managed")
	if _, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, nil); err == nil || !strings.Contains(err.Error(), "capability_unavailable") {
		t.Fatalf("missing provider error = %v", err)
	}
	provider := &testDeploymentProvider{}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if !provider.planned {
		t.Fatal("provider planner was not invoked")
	}
	if _, err := ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, ExpectedContractRevision: plan.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, registry); err != nil {
		t.Fatal(err)
	}
	if !provider.applied {
		t.Fatal("provider apply was not invoked")
	}
}

func TestDeploymentApplyRejectsCallerRecomputedProviderPlan(t *testing.T) {
	root := deploymentPlanFixture(t, "managed")
	provider := &testDeploymentProvider{}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	tampered := plan
	tampered.ProviderPlans = append([]DeploymentProviderPlan(nil), plan.ProviderPlans...)
	for index := range tampered.ProviderPlans {
		if tampered.ProviderPlans[index].ProviderAddress != "app/provider/postgres" {
			continue
		}
		tampered.ProviderPlans[index].Actions = []DeploymentAction{{Kind: "delete", Address: "app/data_source/database", Destructive: true}}
		tampered.ProviderPlans[index] = normalizeDeploymentProviderPlan(tampered.ProviderPlans[index])
	}
	tampered.RequiredApprovals = nil
	tampered.RiskRecords = nil
	digests := deploymentProviderPlanDigests(tampered.ProviderPlans)
	result, compileErr := compiler.CompileContractGraph(root)
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	tampered.DeploymentRevision = compiler.ComputeDeploymentRevisions(result.Manifest, tampered.ImplementationRevision, map[string][]string{tampered.Deployment: digests})[tampered.DeploymentName]
	tampered.ExpiresAt = tampered.ExpiresAt.Add(time.Hour)
	tampered.PlanID = deploymentPlanID(tampered)
	if _, err := ApplyDeploymentPlan(context.Background(), root, tampered, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: tampered.BaseWorkspaceRevision, ExpectedContractRevision: tampered.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, registry); err == nil || !strings.Contains(err.Error(), "issued plan") {
		t.Fatalf("tampered deployment plan error = %v", err)
	}
	if provider.applied {
		t.Fatal("tampered provider plan reached adapter apply")
	}
}

func TestDeploymentProviderApplyFailureDoesNotWriteState(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "managed")
	provider := &testDeploymentProvider{applyErr: errors.New("boom")}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, ExpectedContractRevision: plan.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, registry)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("apply error = %v", err)
	}
	if _, err := os.Stat(deploymentStatePath(root, "preview")); !os.IsNotExist(err) {
		t.Fatalf("state exists after failed apply: %v", err)
	}
}

func TestDeploymentProviderNilCompensatorFallsBackToCrashSafeRollback(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "managed")
	provider := &testDeploymentProvider{nilRollback: true}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, ExpectedContractRevision: plan.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, registry)
	if err == nil || !strings.Contains(err.Error(), "returned no compensator") {
		t.Fatalf("nil compensator error = %v", err)
	}
	if !provider.rolledBack {
		t.Fatal("crash-safe provider rollback was not invoked")
	}
	if _, statErr := os.Stat(deploymentStatePath(root, "preview")); !os.IsNotExist(statErr) {
		t.Fatalf("state exists after nil compensator: %v", statErr)
	}
	if _, statErr := os.Stat(deploymentJournalPath(root, plan.PlanID)); !os.IsNotExist(statErr) {
		t.Fatalf("journal exists after successful recovery: %v", statErr)
	}
}

func TestDeploymentApprovalsUseTheSharedPlanCallerScopeBinding(t *testing.T) {
	plan := DeploymentPlan{
		PlanID: "sha256:" + strings.Repeat("a", 64), Caller: "agent:test",
		RequiredApprovals: []string{"deployment.destructive:app/data_source/database"},
	}
	options := DeploymentApplyOptions{Caller: plan.Caller}
	if err := validateDeploymentApprovals(plan, options); err == nil || !strings.Contains(err.Error(), "required approvals are unavailable") {
		t.Fatalf("missing verifier error = %v", err)
	}
	signature := "ed25519:test:" + base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	token := scenery.NewApprovalToken(plan.PlanID, plan.Caller, plan.RequiredApprovals, time.Now().UTC().Add(time.Minute))
	token.Signature = signature
	options.ApprovalTokens = []ApprovalToken{token}
	options.VerifyApproval = func(candidate ApprovalToken, payload []byte) error {
		if candidate.Signature != signature || len(payload) == 0 {
			return os.ErrPermission
		}
		return nil
	}
	if err := validateDeploymentApprovals(plan, options); err != nil {
		t.Fatal(err)
	}
	options.ApprovalTokens[0].RiskScopes = append(options.ApprovalTokens[0].RiskScopes, "deployment.other")
	if err := validateDeploymentApprovals(plan, options); err == nil || !strings.Contains(err.Error(), "unrequested risk scope") {
		t.Fatalf("over-broad approval error = %v", err)
	}
}

func TestDeploymentApplySerializesConcurrentProviderEffects(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "managed")
	provider := &blockingDeploymentProvider{entered: make(chan struct{}), release: make(chan struct{})}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	options := DeploymentApplyOptions{ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, ExpectedContractRevision: plan.ContractRevision, ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test"}
	first := make(chan error, 1)
	go func() {
		_, err := ApplyDeploymentPlan(context.Background(), root, plan, options, registry)
		first <- err
	}()
	<-provider.entered
	if _, err := ApplyDeploymentPlan(context.Background(), root, plan, options, registry); err == nil || !strings.Contains(err.Error(), "deployment apply is active") {
		t.Fatalf("concurrent apply error = %v", err)
	}
	close(provider.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentApplyRejectsSymlinkedStateDirectory(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "external")
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".scenery"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".scenery", "deployments")); err != nil {
		t.Fatal(err)
	}
	_, err = ApplyDeploymentPlan(context.Background(), root, plan, DeploymentApplyOptions{
		ExpectedWorkspaceRevision: plan.BaseWorkspaceRevision, ExpectedContractRevision: plan.ContractRevision,
		ExpectedImplementation: testDeploymentImplementationRevision(), Caller: "test",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("symlinked deployment state error = %v", err)
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("outside deployment directory was modified: %v, %v", entries, readErr)
	}
}

func TestDeploymentRecoveryRestoresPreviousStateBeforeProviderRollback(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := deploymentPlanFixture(t, "managed")
	provider := &testDeploymentProvider{}
	registry := DeploymentProviderRegistry{"app/provider/postgres": provider}
	plan, err := PlanDeployment(context.Background(), root, DeploymentPlanRequest{Deployment: "preview", ImplementationRevisions: testDeploymentImplementationRevision(), Caller: "test"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	managedIndex := -1
	for index, providerPlan := range plan.ProviderPlans {
		if providerPlan.RequiresApply {
			managedIndex = index
			break
		}
	}
	if managedIndex < 0 {
		t.Fatal("managed provider plan is unavailable")
	}
	statePath := deploymentStatePath(root, plan.DeploymentName)
	previous := []byte("previous deployment state\n")
	if err := writeDeploymentFile(root, statePath, []byte("partially published new state\n")); err != nil {
		t.Fatal(err)
	}
	journal := deploymentApplyJournal{
		ArtifactIdentity:    machine.NewArtifactIdentity(deploymentApplyJournalKind, deploymentJournalSchemaDescriptor),
		Plan:                plan,
		Applied:             []int{managedIndex},
		RestoreState:        true,
		PreviousState:       previous,
		PreviousStateExists: true,
	}
	journalPath := deploymentJournalPath(root, plan.PlanID)
	if err := writeDeploymentJournal(root, journalPath, journal); err != nil {
		t.Fatal(err)
	}
	if err := recoverDeploymentJournals(context.Background(), root, registry); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(previous) {
		t.Fatalf("restored state = %q, want %q", got, previous)
	}
	if !provider.rolledBack {
		t.Fatal("provider was not rolled back")
	}
	if _, err := os.Stat(journalPath); !os.IsNotExist(err) {
		t.Fatalf("recovery journal still exists: %v", err)
	}
}

func TestDeploymentRecoveryRefusesLegacyStateWithoutModification(t *testing.T) {
	for _, test := range []struct {
		name, relative, apiVersion string
		invoke                     func(string) error
	}{
		{
			name: "journal", relative: filepath.Join("journal", "legacy.json"), apiVersion: "scenery.deployment-apply-journal/v1",
			invoke: func(root string) error { return recoverDeploymentJournals(context.Background(), root, nil) },
		},
		{
			name: "lock", relative: "apply.lock", apiVersion: "scenery.deployment-apply-lock/v1",
			invoke: func(root string) error {
				_, err := acquireDeploymentApplyLock(root)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".scenery", "deployments", test.relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			legacy := []byte(`{"api_version":"` + test.apiVersion + `","sentinel":"keep"}`)
			if err := os.WriteFile(path, legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			err := test.invoke(root)
			if err == nil || !strings.Contains(err.Error(), "previous Scenery binary") || !strings.Contains(err.Error(), "no state was modified") {
				t.Fatalf("recovery error = %v, want precise legacy-state refusal", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(legacy) {
				t.Fatalf("legacy state changed: got %q, err %v", got, readErr)
			}
		})
	}
}

func testDeploymentImplementationRevision() map[string]string {
	return map[string]string{"development": "sha256:" + strings.Repeat("a", 64)}
}

func deploymentPlanFixture(t *testing.T, lifecycle string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sceneryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(sceneryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scenery.scn")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"scenery.runtime-http",`, `"scenery.runtime-http",
    "scenery.data",
    "scenery.deployment",`, 1))
	data = append(data, []byte(`

provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
}

data_source "database" {
  provider  = provider.postgres
  lifecycle = "`+lifecycle+`"
  config = {
    database = "nativeapp"
  }
}

deployment "preview" {
  environment = "preview"

  data_source {
    target = data_source.database
    config = {
      database = "nativeapp_preview"
    }
  }
}
`)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, ok := compiler.BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres descriptor is unavailable")
	}
	lockfile := fmt.Sprintf(`lock {}

provider "postgres" {
  source                    = "registry.scenery.dev/core/postgres"
  integrity                 = %q
  compile_descriptor_digest = %q
  runtime_abi               = "scenery.data-runtime/v1"
  deployment_abi            = %q
}
`, integrity, integrity, deploymentProviderABI)
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte(lockfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
