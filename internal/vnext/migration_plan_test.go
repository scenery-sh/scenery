package vnext

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMigrationShadowPlanAppliesAtomicallyAndBlocksUnprovenActivation(t *testing.T) {
	parallelVNextIntegrationTest(t)

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
	tampered := plan
	tampered.Edits = append(append([]SourceEdit(nil), plan.Edits...), SourceEdit{Path: "caller-authored.txt", BeforeDigest: byteDigest(nil), After: []byte("not planned\n"), AfterExists: true, Mode: 0o644})
	tampered.ExpiresAt = tampered.ExpiresAt.Add(time.Hour)
	tampered.PlanID = migrationPlanID(tampered)
	if _, err := ApplyMigrationPlan(root, tampered, MigrationApplyOptions{ExpectedWorkspaceRevision: base.WorkspaceRevision, ExpectedContractRevision: base.Manifest.ContractRevision, Caller: "test"}); err == nil || !strings.Contains(err.Error(), "issued plan") {
		t.Fatalf("tampered migration plan error = %v", err)
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
	shadow, err := compileContractGraph(root, false)
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

func TestAdvisoryLegacyEvidenceDoesNotBecomeVerifiedEquality(t *testing.T) {
	legacy := Resource{
		Address: "house/operation/process", Module: "house", Name: "process", Kind: "scenery.operation/v1",
		Spec:          map[string]any{"input": map[string]any{"$ref": "string"}},
		Compatibility: &LegacyCompatibility{Semantics: "advisory", Contract: "verified", MigrationDisposition: "advisory"},
	}
	native := legacy
	native.Compatibility = nil
	migration := &Migration{
		Services: []MigrationService{{Name: "house", State: "shadow", Active: "legacy", Module: "module.house", Namespace: "house"}},
	}
	_, diagnostics := linkMigrationResources([]Resource{native}, []Resource{legacy}, migration)
	if hasErrors(diagnostics) {
		t.Fatalf("link diagnostics = %#v", diagnostics)
	}
	service := migration.Services[0]
	if service.GuaranteeClassification != "advisory" || service.MigrationDisposition != "advisory" {
		t.Fatalf("service evidence = %#v", service)
	}
	service.LegacyCandidateValid = true
	service.NativeCandidateValid = true
	migration.Services[0] = service
	result := &Result{Migration: migration}
	comparison, err := CompareMigrationService(result, "house")
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.StaticContractComplete || !comparison.StaticContractEqual || comparison.BehavioralEvidenceComplete || comparison.Complete || comparison.Equal {
		t.Fatalf("advisory comparison = %#v", comparison)
	}
}

func TestMigrationEvidenceUsesTypedMetadataAndBothCandidateCutoverClasses(t *testing.T) {
	verified := Resource{
		Address: "house/record/advisory_label", Module: "house", Name: "advisory_label", Kind: "scenery.record/v1",
		Spec:          map[string]any{"field": []any{map[string]any{"name": "status", "type": map[string]any{"$ref": "string"}, "default": "advisory"}}},
		Compatibility: &LegacyCompatibility{Semantics: "legacy_exact", Contract: "verified", MigrationDisposition: "native_equivalent"},
	}
	if !migrationCandidateStaticContractComplete([]Resource{verified}) || !migrationCandidateBehavioralEvidenceComplete([]Resource{verified}) {
		t.Fatal("business literal was mistaken for migration evidence")
	}
	legacyDurable := Resource{Address: "house/execution/retired", Module: "house", Kind: "scenery.execution/v1", Spec: map[string]any{"mode": "durable"}}
	nativeService := Resource{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Spec: map[string]any{"runtime": "go"}}
	classes := migrationServiceCutoverClasses([]Resource{legacyDurable}, []Resource{nativeService})
	if !slices.Contains(classes, "durable_execution") || migrationCandidateOperationalEvidenceComplete([]Resource{legacyDurable}, []Resource{nativeService}) {
		t.Fatalf("legacy-only cutover classes = %#v", classes)
	}
}

func TestMigrationCandidateValidationUsesPredictedActiveGraph(t *testing.T) {
	otherType := Resource{
		Address: "other/record/shared", Module: "other", Name: "shared", Kind: "scenery.record/v1", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"unknown_fields": "reject", "field": []any{map[string]any{"name": "value", "type": map[string]any{"$ref": "string"}}}},
	}
	candidate := Resource{
		Address: "house/record/uses_other", Module: "house", Name: "uses_other", Kind: "scenery.record/v1", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"unknown_fields": "reject", "field": []any{map[string]any{"name": "value", "type": map[string]any{"$ref": "other/record/shared"}}}},
	}
	migration := &Migration{
		Services:         []MigrationService{{Name: "house", State: "shadow", Active: "legacy"}},
		LegacyCandidates: map[string][]Resource{},
		NativeCandidates: map[string][]Resource{"house": {candidate}},
	}
	validateMigrationCandidateGraphs(t.TempDir(), []Resource{otherType}, migration)
	if !migration.Services[0].NativeCandidateValid {
		t.Fatalf("cross-service candidate diagnostics = %#v", migration.Services[0].CandidateDiagnostics["native"])
	}
}

func TestMigrationCandidateValidationSeesOtherOwnerRouteCollisions(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	var candidate, active []Resource
	var collision Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Module == "house" {
			candidate = append(candidate, resource)
			if resource.Kind == "scenery.binding/v1" && stringValue(resource.Spec["protocol"]) == "http" {
				collision = cloneResourceView([]Resource{resource})[0]
			}
			continue
		}
		active = append(active, resource)
	}
	if collision.Address == "" {
		t.Fatal("HTTP binding fixture is unavailable")
	}
	collision.Address, collision.Module, collision.Name = "other/binding/process_scene_http", "other", "process_scene_http"
	active = append(active, collision)
	migration := &Migration{
		Services:         []MigrationService{{Name: "house", State: "shadow", Active: "legacy"}},
		LegacyCandidates: map[string][]Resource{},
		NativeCandidates: map[string][]Resource{"house": candidate},
	}
	validateMigrationCandidateGraphs(root, active, migration)
	service := migration.Services[0]
	if service.NativeCandidateValid || !diagnosticsContain(service.CandidateDiagnostics["native"], "SCN2002") {
		t.Fatalf("route collision candidate status = %#v", service)
	}
}

func TestMigrationCandidateValidationSeesOtherOwnerDurableIdentityCollisions(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	var active, candidate []Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Module == "house" {
			candidate = append(candidate, resource)
		} else {
			active = append(active, resource)
		}
	}
	engine := Resource{
		Address: "app/execution_engine/tasks", Module: "app", Name: "tasks", Kind: "scenery.execution-engine/v1", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"provider": map[string]any{"$ref": "app/provider/durable"}, "require_capabilities": []any{"execution.durable/v1"}},
	}
	provider := Resource{
		Address: "app/provider/durable", Module: "app", Name: "durable", Kind: "scenery.provider/v1", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"source": "registry.scenery.dev/core/durable", "version": "1.0.0"},
	}
	execution := Resource{
		Address: "house/execution/process_scene_durable", Module: "house", Name: "process_scene_durable", Kind: "scenery.execution/v1", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{
			"operation": map[string]any{"$ref": "house/operation/process_scene"}, "mode": "durable",
			"engine": map[string]any{"$ref": "app/execution_engine/tasks"}, "revision": "1", "external_name": "shared.ProcessScene/v1",
			"timeout": "1m", "lease": "30s", "attempts": "1",
			"retry":         map[string]any{"strategy": "none"},
			"retention":     map[string]any{"success": "1h", "failure": "1h"},
			"deduplication": map[string]any{"retention": "1h", "conflict": "return_existing"},
		},
	}
	candidate = append(candidate, execution)
	otherExecution := cloneResourceView([]Resource{execution})[0]
	otherExecution.Address, otherExecution.Module, otherExecution.Name = "other/execution/process_scene_durable", "other", "process_scene_durable"
	active = append(active, provider, engine, otherExecution)
	migration := &Migration{
		Services:         []MigrationService{{Name: "house", State: "shadow", Active: "legacy"}},
		LegacyCandidates: map[string][]Resource{},
		NativeCandidates: map[string][]Resource{"house": candidate},
	}
	validateMigrationCandidateGraphs(root, active, migration)
	service := migration.Services[0]
	if service.NativeCandidateValid || !diagnosticsContain(service.CandidateDiagnostics["native"], "SCN2210") {
		t.Fatalf("durable identity collision candidate status = %#v", service)
	}
	diagnostics := service.CandidateDiagnostics["native"]
	if len(diagnostics) != 1 || diagnostics[0].Code != "SCN2210" {
		t.Fatalf("durable identity fixture has unrelated diagnostics: %#v", diagnostics)
	}
}

func TestMigrationCandidateValidationSeesOtherOwnerScheduleAndEventConsumerIdentities(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	support := cloneResourceView(result.Manifest.Resources)
	support = append(support,
		Resource{Address: "app/event_bus/events", Module: "app", Name: "events", Kind: "scenery.event-bus/v1", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"provider": map[string]any{"$ref": "std.provider.events"}}},
		Resource{Address: "house/event/process_scene_requested", Module: "house", Name: "process_scene_requested", Kind: "scenery.event/v1", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"payload": map[string]any{"$ref": "record.process_scene_input"}, "version": "1"}},
	)
	for _, test := range []struct {
		name     string
		resource Resource
	}{
		{name: "schedule", resource: Resource{
			Address: "house/schedule/nightly", Module: "house", Name: "nightly", Kind: "scenery.schedule/v1", Origin: Origin{Kind: "authored"},
			Spec: map[string]any{
				"trigger": map[string]any{"cron": "0 2 * * *", "timezone": "Europe/Prague"}, "overlap": "skip",
				"invoke": map[string]any{
					"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
					"identity": map[string]any{"$ref": "std.workload_identity.scheduler"}, "authorization": map[string]any{"$ref": "std.authorization.scheduled"},
					"pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "input": map[string]any{"scene_id": "nightly"},
				},
			},
		}},
		{name: "event_consumer", resource: Resource{
			Address: "house/binding/process_scene_event", Module: "house", Name: "process_scene_event", Kind: "scenery.binding/v1", Origin: Origin{Kind: "authored"},
			Spec: map[string]any{
				"operation": map[string]any{"$ref": "operation.process_scene"}, "execution": map[string]any{"$ref": "execution.process_scene_direct"},
				"protocol": "event", "delivery": "call", "exposure": "application",
				"authentication": map[string]any{"$ref": "std.authentication.service_identity"}, "authorization": map[string]any{"$ref": "std.authorization.application"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
				"event": map[string]any{
					"direction": "consume", "bus": map[string]any{"$ref": "app/event_bus/events"}, "channel": "house.process", "contract": map[string]any{"$ref": "event.process_scene_requested"},
					"guarantee": "at_least_once", "broker_retry": map[string]any{"attempts": "3", "backoff": "fixed"},
					"map": map[string]any{"from": "message.payload", "to": "operation.process_scene.input"},
				},
			},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			active := append(cloneResourceView(support), cloneResourceView([]Resource{test.resource})[0])
			migration := &Migration{
				Services:         []MigrationService{{Name: "house", State: "shadow", Active: "legacy"}},
				LegacyCandidates: map[string][]Resource{},
				NativeCandidates: map[string][]Resource{"house": {test.resource}},
			}
			validateMigrationCandidateGraphs(root, active, migration)
			service := migration.Services[0]
			if service.NativeCandidateValid || !diagnosticsContain(service.CandidateDiagnostics["native"], "SCN1104") {
				t.Fatalf("global identity collision candidate status = %#v", service)
			}
			for _, diagnostic := range service.CandidateDiagnostics["native"] {
				if diagnostic.Address == test.resource.Address && diagnostic.Code != "SCN1104" {
					t.Fatalf("%s identity fixture is invalid: %#v", test.name, diagnostic)
				}
			}
		})
	}
}
