package main

import (
	"encoding/json"

	"os"
	"path/filepath"
	"reflect"

	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/deploydiag"
	"scenery.sh/internal/doctor"
	"scenery.sh/internal/machine"
)

const harnessSchemaValidationKind = "scenery.harness.schema_validation"

func runHarnessSchemaValidationStep(repoRoot string, resp harnessSelfResponse) (harnessStep, *harnessSchemaValidationReport) {
	started := time.Now()
	report := buildHarnessSchemaValidationReport(repoRoot, resp)
	step := harnessStep{
		Name:       "schema validation",
		Command:    []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--release", "--summary", "--write"},
		OK:         !hasErrorDiagnostics(report.Diagnostics),
		DurationMS: time.Since(started).Milliseconds(),
		Summary: map[string]any{
			"validated": len(report.Validated),
			"errors":    countSchemaValidationErrors(report.Validated),
		},
		Diagnostics: report.Diagnostics,
	}
	if !step.OK {
		step.Error = "schema validation failed"
	}
	return step, report
}

func buildHarnessSchemaValidationReport(repoRoot string, resp harnessSelfResponse) *harnessSchemaValidationReport {
	return buildHarnessSchemaValidationReportWithReader(repoRoot, resp, func(target any, args ...string) error {
		return readProductJSON(repoRoot, target, args...)
	})
}

func buildHarnessSchemaValidationReportWithReader(repoRoot string, resp harnessSelfResponse, read func(any, ...string) error) *harnessSchemaValidationReport {
	report := &harnessSchemaValidationReport{PayloadIdentity: newCLIPayloadIdentity(harnessSchemaValidationKind)}
	var versionPayload versionResponse
	versionErr := read(&versionPayload, "version", "-o", "json")
	if versionErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{Stage: "schema validation", Severity: "error", Message: "prepared product version: " + versionErr.Error()})
	}
	var inspectDocsPayload map[string]any
	inspectDocsErr := read(&inspectDocsPayload, "inspect", "docs", "--repo-root", repoRoot, "--all", "-o", "json")
	if inspectDocsErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect docs JSON for schema validation: " + inspectDocsErr.Error(),
			SuggestedAction: "Run `scenery inspect docs --all -o json` and fix the command before relying on schema validation.",
		})
	}
	environmentRegistryPayload, environmentRegistryErr := harnessJSONFilePayload(filepath.Join(repoRoot, "docs", "environment.registry.json"))
	if environmentRegistryErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "environment.registry.json")),
			Message:         "failed to load environment registry JSON for schema validation: " + environmentRegistryErr.Error(),
			SuggestedAction: "Fix docs/environment.registry.json so it can be validated.",
		})
	}
	docsKnowledgePayload, docsKnowledgeErr := harnessJSONFilePayload(filepath.Join(repoRoot, "docs", "knowledge.json"))
	if docsKnowledgeErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "docs", "knowledge.json")),
			Message:         "failed to load docs knowledge index JSON for schema validation: " + docsKnowledgeErr.Error(),
			SuggestedAction: "Fix docs/knowledge.json so it can be validated.",
		})
	}
	var inspectHarnessPayload any
	var harnessPayload map[string]any
	if inspectHarnessErr := read(&harnessPayload, "inspect", "harness", "--repo-root", repoRoot, "-o", "json"); inspectHarnessErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect harness JSON for schema validation: " + inspectHarnessErr.Error(),
			SuggestedAction: "Run `scenery inspect harness -o json --repo-root <repo>` and fix the command before relying on schema validation.",
		})
	} else {
		inspectHarnessPayload = harnessPayload
	}
	artifactEvidencePayload := harnessEvidence{
		PayloadIdentity: newCLIPayloadIdentity(harnessArtifactEvidenceKind),
		Command:         []string{"go", "test", "-json", "./..."},
		CWD:             repoRoot,
		StartedAt:       "2026-06-07T00:00:00Z",
		DurationMS:      1234,
		ExitCode:        intPtr(1),
		StdoutTail:      "{}",
		Artifacts: []harnessEvidenceArtifact{{
			Name: "go-test-json",
			Path: ".scenery/harness/artifacts/20260607T000000Z/go-test.jsonl",
		}},
		ReproCommand: "cd " + repoRoot + " && go test -json ./...",
	}
	helpPayload := map[string]any{}
	if err := read(&helpPayload, "help", "-o", "json"); err != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{Stage: "schema validation", Severity: "error", Message: "prepared product help: " + err.Error()})
	}
	digest := "sha256:" + strings.Repeat("0", 64)
	whenSchemaExists := func(schemaRel string, payload any) any {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(schemaRel))); err != nil {
			return nil
		}
		return payload
	}
	fixturePayload := func(schemaRel, fixtureRel string) any {
		if whenSchemaExists(schemaRel, true) == nil {
			return nil
		}
		payload, err := harnessJSONFilePayload(filepath.Join(repoRoot, filepath.FromSlash(fixtureRel)))
		if err == nil {
			return payload
		}
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			File:            fixtureRel,
			Message:         "failed to load schema fixture: " + err.Error(),
			SuggestedAction: "Regenerate the committed conformance fixtures.",
		})
		return nil
	}
	artifact := func(kind string, values map[string]any) map[string]any {
		values["kind"], values["schema_revision"], values["spec_revision"], values["producer"] = kind, digest, currentMachineSpecRevision(), cliProducer()
		return values
	}
	buildInputManifest := artifact("scenery.go-build-input-manifest", map[string]any{"target": "development", "entries": []any{}, "digest": digest})
	storageScope := map[string]any{"app_id": "app", "app_root": "/tmp/app", "worktree_key": strings.Repeat("a", 64), "incarnation": strings.Repeat("b", 32), "generation": strings.Repeat("c", 32), "store": "files", "tenant": "tenant"}
	storageObject := map[string]any{"store": "files", "tenant": "tenant", "key": "photos/a.jpg", "size_bytes": 5, "etag": `"version"`, "sha256": strings.Repeat("d", 64), "modified_at": "2026-09-09T00:00:00Z", "metadata": map[string]string{"Case-Sensitive": "value"}}
	var manifestPayload any
	if whenSchemaExists("docs/schemas/scenery.manifest.schema.json", true) != nil {
		fixtureRoot := filepath.Join(repoRoot, "internal", "compiler", "testdata", "house")
		compiled, err := compiler.Compile(fixtureRoot)
		if err != nil || compiled == nil || compiled.Manifest == nil {
			message := "compiler returned no manifest"
			if err != nil {
				message = err.Error()
			}
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage: "schema validation", Severity: "error", File: filepath.ToSlash(fixtureRoot),
				Message: "failed to build manifest/status schema fixtures: " + message, SuggestedAction: "Repair the committed House compiler fixture.",
			})
		} else {
			manifestPayload = compiled.Manifest
		}
	}
	items := []struct {
		name      string
		schemaRel string
		payload   any
	}{
		{name: "docs.index", schemaRel: "docs/schemas/scenery.docs.index.schema.json", payload: docsKnowledgePayload},
		{name: "approval.trust", schemaRel: "docs/schemas/scenery.approval-trust.schema.json", payload: artifact("scenery.approval-trust", map[string]any{
			"keys": map[string]any{"maintainer": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		})},
		{name: "approval.token", schemaRel: "docs/schemas/scenery.approval-token.schema.json", payload: artifact("scenery.approval-token", map[string]any{
			"plan_id": "sha256:" + strings.Repeat("0", 64), "caller": "local", "risk_scopes": []any{"deployment.destructive:app/data_source/database"},
			"expires_at": "2026-07-10T12:00:00Z", "signature": "ed25519:maintainer:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		})},
		{name: "change.plan", schemaRel: "docs/schemas/scenery.change-plan.schema.json", payload: whenSchemaExists("docs/schemas/scenery.change-plan.schema.json", artifact("scenery.change-plan", map[string]any{
			"plan_id": digest, "application": "schema-fixture", "base_workspace_revision": digest,
			"base_contract_revision": digest, "predicted_workspace_revision": digest, "predicted_contract_revision": digest,
			"implementation_revision_status": "not_built", "deployment_revision_status": "not_planned", "caller": "harness", "capabilities": []any{},
			"operations_digest": digest, "operations": []any{}, "rename_receipts": []any{}, "semantic_diff": map[string]any{}, "affected_resources": []any{}, "diagnostics": []any{},
			"source_edits": []any{}, "formatting_effects": []any{}, "required_approvals": []any{}, "required_capabilities": []any{}, "risk_records": []any{},
			"expires_at": "2026-07-10T12:00:00Z",
		}))},
		{name: "change.receipt", schemaRel: "docs/schemas/scenery.change-receipt.schema.json", payload: whenSchemaExists("docs/schemas/scenery.change-receipt.schema.json", artifact("scenery.change-receipt", map[string]any{
			"plan_id": digest, "workspace_revision": digest, "contract_revision": digest,
			"implementation_revision_status": "not_built", "deployment_revision_status": "not_planned", "applied": []any{}, "rename_receipts": []any{},
		}))},
		{name: "cli", schemaRel: "docs/schemas/scenery.cli.schema.json", payload: whenSchemaExists("docs/schemas/scenery.cli.schema.json", map[string]any{
			"kind": machine.EnvelopeKind, "schema_revision": machine.EnvelopeSchemaRevision, "spec_revision": currentMachineSpecRevision(), "producer": cliProducer(), "ok": true,
			"workspace_revision": digest, "contract_revision": digest, "implementation_revision": nil, "deployment_revision": nil,
			"data": map[string]any{"fixture": true}, "diagnostics": []any{},
		})},
		{name: "cli.event", schemaRel: "docs/schemas/scenery.cli.event.schema.json", payload: whenSchemaExists("docs/schemas/scenery.cli.event.schema.json", map[string]any{
			"kind": machine.EventEnvelopeKind, "schema_revision": machine.EventEnvelopeSchemaRevision, "spec_revision": currentMachineSpecRevision(), "producer": cliProducer(), "sequence": 1, "event": "summary", "terminal": true,
			"workspace_revision": nil, "contract_revision": nil, "implementation_revision": nil, "deployment_revision": nil,
			"data": map[string]any{"event_count": 0}, "diagnostics": []any{},
		})},
		{name: "deployment.plan", schemaRel: "docs/schemas/scenery.deployment-plan.schema.json", payload: whenSchemaExists("docs/schemas/scenery.deployment-plan.schema.json", artifact("scenery.deployment-plan", map[string]any{
			"plan_id": digest, "application": "schema-fixture", "deployment": "app/deployment/local",
			"deployment_name": "local", "environment": "development", "base_workspace_revision": digest, "contract_revision": digest,
			"implementation_revision": map[string]any{"development": digest}, "deployment_revision": digest,
			"projection":     artifact("scenery.deployment-projection", map[string]any{"deployment": "app/deployment/local", "environment": "development", "contract_revision": digest, "resources": map[string]any{}}),
			"provider_plans": []any{}, "caller": "harness", "capabilities": []any{}, "required_approvals": []any{}, "risk_records": []any{}, "expires_at": "2026-07-10T12:00:00Z",
		}))},
		{name: "deployment.receipt", schemaRel: "docs/schemas/scenery.deployment-receipt.schema.json", payload: whenSchemaExists("docs/schemas/scenery.deployment-receipt.schema.json", artifact("scenery.deployment-receipt", map[string]any{
			"plan_id": digest, "application": "schema-fixture", "deployment": "app/deployment/local",
			"workspace_revision": digest, "contract_revision": digest, "implementation_revision": map[string]any{"development": digest},
			"deployment_revision": digest, "provider_plan_digests": []any{}, "applied_at": "2026-07-10T12:00:00Z",
		}))},
		{name: "generated.application", schemaRel: "docs/schemas/scenery.generated.schema.json", payload: fixturePayload(
			"docs/schemas/scenery.generated.schema.json", "internal/compiler/testdata/native/internal/scenerygen/scenery.generated.json",
		)},
		{name: "go.build-input", schemaRel: "docs/schemas/scenery.go-build-input-manifest.schema.json", payload: whenSchemaExists(
			"docs/schemas/scenery.go-build-input-manifest.schema.json", buildInputManifest,
		)},
		{name: "manifest", schemaRel: "docs/schemas/scenery.manifest.schema.json", payload: whenSchemaExists("docs/schemas/scenery.manifest.schema.json", manifestPayload)},
		{name: "generated.package", schemaRel: "docs/schemas/scenery.package-generated.schema.json", payload: fixturePayload(
			"docs/schemas/scenery.package-generated.schema.json", "internal/compiler/testdata/native/house/scenerycontract/scenery.package-generated.json",
		)},
		{name: "runtime.bundle", schemaRel: "docs/schemas/scenery.runtime-bundle.schema.json", payload: whenSchemaExists("docs/schemas/scenery.runtime-bundle.schema.json", artifact("scenery.runtime-bundle", map[string]any{
			"artifact_kind": "go_runtime_bundle", "application": "schema-fixture", "target": "development",
			"contract_revision": digest, "implementation_revision": digest, "build_input_manifest": buildInputManifest,
			"resolved_go_target": map[string]any{"resolved_platform": map[string]any{}, "resolved_toolchain": map[string]any{}}, "runtime_abi": "scenery.go-runtime/v1",
		}))},
		{name: "generated.typescript", schemaRel: "docs/schemas/scenery.typescript-client-generated.schema.json", payload: fixturePayload(
			"docs/schemas/scenery.typescript-client-generated.schema.json", "internal/compiler/testdata/native/clients/generated/public_api/scenery.typescript-client-generated.json",
		)},
		{name: "environment.registry", schemaRel: "docs/schemas/scenery.environment.registry.schema.json", payload: environmentRegistryPayload},
		{name: "help", schemaRel: "docs/schemas/scenery.help.schema.json", payload: helpPayload},
		{name: "version", schemaRel: "docs/schemas/scenery.version.schema.json", payload: versionPayload},
		{name: "build.result", schemaRel: "docs/schemas/scenery.build.result.schema.json", payload: withCLIPayloadIdentity("scenery.build.result", map[string]any{
			"output_path": "/tmp/scenery-app", "descriptor_path": "/tmp/scenery-app.scenery.runtime-bundle.json", "copied": true,
		})},
		{name: "build.desktop", schemaRel: "docs/schemas/scenery.build.desktop.schema.json", payload: withCLIPayloadIdentity("scenery.build.desktop", map[string]any{
			"environment": "production",
			"frontends": []map[string]any{{
				"name": "app", "tauri_root": "/tmp/app", "frontend_dist": "/tmp/app/dist",
				"artifacts": []string{"/tmp/app/src-tauri/target/release/bundle/dmg/app.dmg"},
			}},
		})},
		{name: "assistant.init", schemaRel: "docs/schemas/scenery.assistant.init.schema.json", payload: withCLIPayloadIdentity("scenery.assistant.init", map[string]any{
			"assistant": "support", "address": "app/assistant/support", "mcp_server": "support", "client": "public_api",
			"source": "./assistants/support", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json",
			"eval_directory": "./assistants/support/eval", "dry_run": false, "applied": true, "idempotent": false,
			"created": []string{"./assistants/support/agent/agent.ts"}, "preserved": []string{}, "plan_id": digest,
			"base_workspace_revision": digest, "predicted_workspace_revision": digest, "contract_revision": digest,
			"files": []map[string]any{{"path": "./assistants/support/agent/agent.ts", "action": "create"}},
		})},
		{name: "assistant.sync", schemaRel: "docs/schemas/scenery.assistant.sync.schema.json", payload: withCLIPayloadIdentity("scenery.assistant.sync", map[string]any{
			"assistant": "support", "address": "app/assistant/support", "source": "./assistants/support",
			"package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json",
			"lock_digest": digest, "package_digest": digest, "cache_path": "/tmp/scenery/assistant-cache/0000000000000000000000000000000000000000000000000000000000000000",
			"status": "synced", "reused": false, "node_path": "/tmp/scenery/node/bin/node", "npm_path": "/tmp/scenery/node/bin/npm",
		})},
		{name: "doctor", schemaRel: "docs/schemas/scenery.doctor.result.schema.json", payload: buildHarnessDoctorSchemaPayload(versionPayload)},
		{name: "deploy.registry", schemaRel: "docs/schemas/scenery.deploy.registry.schema.json", payload: buildHarnessDeployRegistrySchemaPayload()},
		{name: "deploy.status", schemaRel: "docs/schemas/scenery.deploy.status.schema.json", payload: buildHarnessDeployStatusSchemaPayload()},
		{name: "snapshot.save", schemaRel: "docs/schemas/scenery.snapshot.save.schema.json", payload: snapshotSaveResult{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.save"), Archive: "/tmp/app.zip",
			App: snapshotAppResult{Name: "app", ID: "app", Root: "/tmp/app"},
			DB:  &snapshotDBResult{Database: "app_main", Source: "managed", Action: "saved"}, Files: 1, Bytes: 128,
			Storage: &snapshotStorageResult{Scope: storageScope, Stores: 1, Files: 1, Bytes: 5},
		}},
		{name: "snapshot.load", schemaRel: "docs/schemas/scenery.snapshot.load.schema.json", payload: snapshotLoadResult{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.load"), Archive: "/tmp/app.zip",
			App: snapshotAppResult{Name: "app", ID: "app", Root: "/tmp/app"}, Mode: "overwrite",
			DB:      &snapshotDBResult{Database: "app_main", Source: "managed", Action: "overwrite"},
			Storage: &snapshotStorageResult{Scope: storageScope, Stores: 1, Files: 1, Bytes: 5, Cloned: 1},
		}},
		{name: "storage.object", schemaRel: "docs/schemas/scenery.storage.object.schema.json", payload: withCLIPayloadIdentity("scenery.storage.object", map[string]any{"scope": storageScope, "object": storageObject})},
		{name: "storage.list", schemaRel: "docs/schemas/scenery.storage.list.schema.json", payload: withCLIPayloadIdentity("scenery.storage.list", map[string]any{"scope": storageScope, "page": map[string]any{"objects": []any{storageObject}, "prefixes": []string{"photos/"}, "next_cursor": "opaque"}})},
		{name: "storage.delete", schemaRel: "docs/schemas/scenery.storage.delete.schema.json", payload: withCLIPayloadIdentity("scenery.storage.delete", map[string]any{"scope": storageScope, "key": "photos/a.jpg", "dry_run": false, "deleted": true})},
		{name: "storage.cleanup", schemaRel: "docs/schemas/scenery.storage.cleanup.schema.json", payload: withCLIPayloadIdentity("scenery.storage.cleanup", map[string]any{"scope": storageScope, "purge": true, "dry_run": false, "purge_result": map[string]any{"retired": true, "reclaimed": true}})},
		{name: "storage.inspect", schemaRel: "docs/schemas/scenery.storage.inspect.schema.json", payload: withCLIPayloadIdentity("scenery.storage.inspect", map[string]any{
			"app":     map[string]any{"name": "app", "root": "/tmp/app", "config_path": "/tmp/app/.scenery.json"},
			"storage": map[string]any{"configured": true, "declared": true, "readiness": "uninitialized", "scope": map[string]any{"app_id": "app", "app_root": "/tmp/app", "worktree_key": strings.Repeat("a", 64), "incarnation": nil, "generation": nil}},
			"stores":  []any{map[string]any{"name": "files", "kind": "local", "access": "auth", "tenant_scoped": true}},
		})},
		{name: "snapshot.verify", schemaRel: "docs/schemas/scenery.snapshot.verify.schema.json", payload: snapshotVerifyResult{
			cliPayloadIdentity: newCLIPayloadIdentity("scenery.snapshot.verify"), Archive: "/tmp/app.zip",
			App: snapshotManifestApp{Name: "app", ID: "app"}, CreatedAt: time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC),
			Files: 1, Bytes: 128, DB: true,
		}},
		{name: "snapshot.manifest", schemaRel: "docs/schemas/scenery.snapshot.manifest.schema.json", payload: snapshotManifest{
			Kind: snapshotManifestKind, SchemaRevision: snapshotManifestSchemaRevision, CreatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
			App:   snapshotManifestApp{Name: "app", ID: "app"},
			DB:    &snapshotManifestDB{Database: "app_main", Source: "managed", DumpFile: "db/database.postgres.dump", DumpFormat: "pg_custom", Schemas: []snapshotManifestSchema{{Service: "api", Schema: "api"}}},
			Files: []snapshotManifestFile{{Path: "db/database.postgres.dump", Bytes: 128, SHA256: digest}},
		}},
		{name: "inspect.docs", schemaRel: "docs/schemas/scenery.inspect.docs.schema.json", payload: inspectDocsPayload},
		{name: "inspect.harness", schemaRel: "docs/schemas/scenery.inspect.harness.schema.json", payload: inspectHarnessPayload},
		{name: "telemetry", schemaRel: "docs/schemas/scenery.telemetry.schema.json", payload: telemetryResponse{
			cliPayloadIdentity: newCLIPayloadIdentity(cliTelemetryPayloadKind),
			Query:              telemetryQuery{Apps: []string{}, Commands: []string{}, Measurements: []string{}, Limit: defaultTelemetryLimit},
			Apps:               []telemetryAppStats{}, Commands: []telemetryCommandStats{}, Measurements: []telemetryMeasurementStats{}, Records: []cliTelemetryRecord{}, Warnings: []string{},
		}},
		{name: "harness.artifact", schemaRel: "docs/schemas/scenery.harness.artifact.schema.json", payload: artifactEvidencePayload},
		{name: "harness.self", schemaRel: "docs/schemas/scenery.harness.self.schema.json", payload: resp},
		{name: "harness.self.summary", schemaRel: "docs/schemas/scenery.harness.self.summary.schema.json", payload: buildHarnessSelfSummary(resp)},
		{name: "harness.toolchain", schemaRel: "docs/schemas/scenery.harness.toolchain.schema.json", payload: resp.Toolchain},
		{name: "harness.changed_area", schemaRel: "docs/schemas/scenery.harness.changed_area.schema.json", payload: resp.ChangedArea},
		{name: "harness.drift", schemaRel: "docs/schemas/scenery.harness.drift.schema.json", payload: resp.Drift},
		{name: "harness.test_timing", schemaRel: "docs/schemas/scenery.harness.test_timing.schema.json", payload: resp.TestTiming},
		{name: "harness.fixture_matrix", schemaRel: "docs/schemas/scenery.harness.fixture_matrix.schema.json", payload: resp.FixtureMatrix},
		{name: "harness.schema_validation", schemaRel: "docs/schemas/scenery.harness.schema_validation.schema.json", payload: report},
		{name: "agent_context", schemaRel: "docs/schemas/scenery.agent_context.schema.json", payload: buildHarnessAgentContext(repoRoot, resp)},
	}
	for _, item := range items {
		if harnessNilPayload(item.payload) {
			continue
		}
		schemaPath := filepath.Join(repoRoot, filepath.FromSlash(item.schemaRel))
		errs := validateHarnessJSONSchemaFile(schemaPath, item.payload)
		validation := harnessSchemaValidationItem{
			Name:   item.name,
			Schema: item.schemaRel,
			OK:     len(errs) == 0,
		}
		if len(errs) > 0 {
			validation.Error = strings.Join(errs, "; ")
			report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
				Stage:           "schema validation",
				Severity:        "error",
				File:            filepath.ToSlash(schemaPath),
				Message:         item.name + " does not conform to " + item.schemaRel + ": " + validation.Error,
				SuggestedAction: "Update the JSON producer or schema so the contract matches.",
			})
		}
		report.Validated = append(report.Validated, validation)
	}
	return report
}

func harnessJSONFilePayload(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func buildHarnessDoctorSchemaPayload(versionPayload versionResponse) doctorResponse {
	resp := doctorResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(doctorResultKind),
		OK:                 true,
		Scenery:            versionPayload,
		App: &doctor.AppInfo{
			Root:       "/tmp/scenery-doctor-fixture",
			ConfigPath: "/tmp/scenery-doctor-fixture/.scenery.json",
			Name:       "doctorfixture",
			ID:         "doctorfixture",
		},
		Environment: doctor.Environment{
			GOOS:             "linux",
			GOARCH:           "amd64",
			NumCPU:           8,
			TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
			Paths: []doctor.PathReport{{
				Kind:       "app_root",
				Path:       "/tmp/scenery-doctor-fixture",
				FreeBytes:  20 * 1024 * 1024 * 1024,
				TotalBytes: 40 * 1024 * 1024 * 1024,
			}},
		},
		Checks: []doctor.Check{
			{
				ID:       "os.runtime",
				Category: "host",
				Name:     "Operating system",
				Status:   doctor.StatusOK,
				Severity: doctor.SeverityInformational,
				Message:  "linux/amd64",
				Observed: map[string]any{"goos": "linux", "goarch": "amd64"},
			},
			{
				ID:       "tool.go",
				Category: "dependency",
				Name:     "Go toolchain",
				Status:   doctor.StatusOK,
				Severity: doctor.SeverityRequired,
				Message:  "go version go1.26.3 linux/amd64 at /usr/local/go/bin/go",
				Observed: map[string]any{"path": "/usr/local/go/bin/go", "version": "go version go1.26.3 linux/amd64"},
			},
		},
	}
	resp.Summary = doctor.Summarize(resp.Checks)
	return resp
}

func buildHarnessDeployRegistrySchemaPayload() map[string]any {
	registry := localagent.EmptyDeployRegistry()
	encoded, _ := json.Marshal(registry.ArtifactIdentity)
	var payload map[string]any
	_ = json.Unmarshal(encoded, &payload)
	payload["acme_email"] = "ops@example.com"
	payload["acme_ca"] = "staging"
	payload["targets"] = []map[string]any{{
		"domain":       "example.com",
		"app_root":     "/tmp/scenery-deploy-fixture",
		"root_service": "web",
		"enabled":      true,
		"created_at":   "2026-07-07T00:00:00Z",
		"updated_at":   "2026-07-07T00:00:00Z",
	}}
	return payload
}

func buildHarnessDeployStatusSchemaPayload() deployStatusResponse {
	return deployStatusResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.status"),
		Ready:              true,
		RegistryPath:       "/tmp/scenery/agent/deploy.json",
		PrivilegedListener: edgeStatusPrivilegedListener{
			Strategy:                 "helper",
			Installed:                true,
			State:                    "running",
			PID:                      101,
			Listen:                   []string{"0.0.0.0:80", "[::]:80", "0.0.0.0:443", "[::]:443"},
			Target:                   "127.0.0.1:19443",
			TargetPath:               "/tmp/scenery/run/edge-target.json",
			TargetPID:                202,
			OwnerUID:                 501,
			OwnerGID:                 20,
			Version:                  "v1.2.3",
			ContractRevision:         localagent.EdgeHelperContractRevision,
			RequiredForPortlessHTTPS: true,
			InstallCommand:           "scenery deploy setup",
		},
		HelperPublic: true,
		Edge: edgeStatusCaddy{
			Kind:        "caddy",
			State:       "running",
			PID:         303,
			UID:         501,
			HTTPSListen: "127.0.0.1:19443",
			Upstream:    "127.0.0.1:9440",
			AgentRouter: "127.0.0.1:9440",
			Admin:       "unix//tmp/scenery/caddy-admin.sock",
			ConfigPath:  "/tmp/scenery/edge/Caddyfile",
			LogPath:     "/tmp/scenery/edge/caddy.log",
		},
		Agent: deployAgentStatus{
			State:      "running",
			PID:        404,
			StatePath:  "/tmp/scenery/agent/state.json",
			SocketPath: "/tmp/scenery/agent/agent.sock",
			RouterAddr: "127.0.0.1:9440",
		},
		AgentSupervisor: deployAgentSupervisorStatus{
			Installed: true,
			Loaded:    true,
			Running:   true,
			PID:       404,
			Label:     localagent.AgentLaunchdLabel,
			Path:      "/Users/example/Library/LaunchAgents/dev.scenery.agent.plist",
		},
		LaunchAgent: deployLaunchAgentStatus{
			Installed: true,
			Loaded:    true,
			Path:      "/Users/example/Library/LaunchAgents/dev.scenery.deploy-resume.plist",
		},
		ACME: deployACMEStatus{
			Email: "ops@example.com",
			CA:    "staging",
		},
		Targets: []deployTargetStatus{{
			Domain:       "example.com",
			AppRoot:      "/tmp/scenery-deploy-fixture",
			RootService:  "web",
			Enabled:      true,
			LiveSession:  true,
			SessionID:    "example",
			CertPresent:  true,
			CertNotAfter: "2026-10-07T00:00:00Z",
		}},
		DiagnosticsDetail: &deploydiag.Report{
			LANIP:    "192.168.1.20",
			PublicIP: "203.0.113.10",
			Checks: []deploydiag.Check{{
				ID:      "deploy.dns.example.com",
				Status:  "ok",
				Message: "DNS for example.com resolves to this public IP",
				Observed: map[string]any{
					"domain":    "example.com",
					"public_ip": "203.0.113.10",
					"ips":       []string{"203.0.113.10"},
				},
			}},
		},
	}
}

func harnessNilPayload(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func countSchemaValidationErrors(items []harnessSchemaValidationItem) int {
	count := 0
	for _, item := range items {
		if !item.OK {
			count++
		}
	}
	return count
}
