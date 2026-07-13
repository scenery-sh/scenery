package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

const harnessSchemaValidationKind = "scenery.harness.schema_validation"

type harnessSchemaValidationReport struct {
	cliPayloadIdentity
	Validated   []harnessSchemaValidationItem `json:"validated"`
	Diagnostics []checkDiagnostic             `json:"diagnostics,omitempty"`
}

type harnessSchemaValidationItem struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func runHarnessSchemaValidationStep(repoRoot string, resp harnessSelfResponse) (harnessStep, *harnessSchemaValidationReport) {
	started := time.Now()
	report := buildHarnessSchemaValidationReport(repoRoot, resp)
	step := harnessStep{
		Name:       "schema validation",
		Command:    []string{"scenery", "harness", "self", "internal:schema-validation", repoRoot},
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
	report := &harnessSchemaValidationReport{cliPayloadIdentity: newCLIPayloadIdentity(harnessSchemaValidationKind)}
	versionPayload := buildVersionResponse()
	inspectDocsPayload, inspectDocsErr := harnessInspectDocsPayload(repoRoot)
	if inspectDocsErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect docs JSON for schema validation: " + inspectDocsErr.Error(),
			SuggestedAction: "Run `scenery inspect docs -o json` and fix the command before relying on schema validation.",
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
	if payload, inspectHarnessErr := buildInspectHarnessResponse(inspectOptions{Subject: "harness", RepoRoot: repoRoot}); inspectHarnessErr != nil {
		report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
			Stage:           "schema validation",
			Severity:        "error",
			Message:         "failed to build inspect harness JSON for schema validation: " + inspectHarnessErr.Error(),
			SuggestedAction: "Run `scenery inspect harness -o json --repo-root <repo>` and fix the command before relying on schema validation.",
		})
	} else {
		inspectHarnessPayload = payload
	}
	artifactEvidencePayload := harnessEvidence{
		cliPayloadIdentity: newCLIPayloadIdentity(harnessArtifactEvidenceKind),
		Command:            []string{"go", "test", "-json", "./..."},
		CWD:                repoRoot,
		StartedAt:          "2026-06-07T00:00:00Z",
		DurationMS:         1234,
		ExitCode:           intPtr(1),
		StdoutTail:         "{}",
		Artifacts: []harnessEvidenceArtifact{{
			Name: "go-test-json",
			Path: ".scenery/harness/artifacts/20260607T000000Z/go-test.jsonl",
		}},
		ReproCommand: "cd " + repoRoot + " && go test -json ./...",
	}
	var helpJSON bytes.Buffer
	helpPayload := map[string]any{}
	if err := writeHelpJSON(&helpJSON); err == nil {
		_ = decodeCLIJSON(helpJSON.Bytes(), &helpPayload)
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
		{name: "doctor", schemaRel: "docs/schemas/scenery.doctor.result.schema.json", payload: buildHarnessDoctorSchemaPayload(versionPayload)},
		{name: "deploy.registry", schemaRel: "docs/schemas/scenery.deploy.registry.schema.json", payload: buildHarnessDeployRegistrySchemaPayload()},
		{name: "deploy.status", schemaRel: "docs/schemas/scenery.deploy.status.schema.json", payload: buildHarnessDeployStatusSchemaPayload()},
		{name: "inspect.docs", schemaRel: "docs/schemas/scenery.inspect.docs.schema.json", payload: inspectDocsPayload},
		{name: "inspect.harness", schemaRel: "docs/schemas/scenery.inspect.harness.schema.json", payload: inspectHarnessPayload},
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
		App: &doctorAppInfo{
			Root:       "/tmp/scenery-doctor-fixture",
			ConfigPath: "/tmp/scenery-doctor-fixture/.scenery.json",
			Name:       "doctorfixture",
			ID:         "doctorfixture",
		},
		Environment: doctorEnvironment{
			GOOS:             "linux",
			GOARCH:           "amd64",
			NumCPU:           8,
			TotalMemoryBytes: 8 * 1024 * 1024 * 1024,
			Paths: []doctorPathReport{{
				Kind:       "app_root",
				Path:       "/tmp/scenery-doctor-fixture",
				FreeBytes:  20 * 1024 * 1024 * 1024,
				TotalBytes: 40 * 1024 * 1024 * 1024,
			}},
		},
		Checks: []doctorCheck{
			{
				ID:       "os.runtime",
				Category: "host",
				Name:     "Operating system",
				Status:   doctorStatusOK,
				Severity: doctorSeverityInformational,
				Message:  "linux/amd64",
				Observed: map[string]any{"goos": "linux", "goarch": "amd64"},
			},
			{
				ID:       "tool.go",
				Category: "dependency",
				Name:     "Go toolchain",
				Status:   doctorStatusOK,
				Severity: doctorSeverityRequired,
				Message:  "go version go1.26.3 linux/amd64 at /usr/local/go/bin/go",
				Observed: map[string]any{"path": "/usr/local/go/bin/go", "version": "go version go1.26.3 linux/amd64"},
			},
		},
	}
	resp.Summary = summarizeDoctorChecks(resp.Checks)
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
		LaunchAgent: deployLaunchAgentStatus{
			Installed: true,
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
		DiagnosticsDetail: &deployDiagnosticReport{
			LANIP:    "192.168.1.20",
			PublicIP: "203.0.113.10",
			Checks: []deployDiagnosticCheck{{
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

func harnessInspectDocsPayload(repoRoot string) (map[string]any, error) {
	var out bytes.Buffer
	if err := runSceneryInspect([]string{"docs", "--repo-root", repoRoot, "-o", "json"}, &out); err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		return nil, err
	}
	return payload, nil
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

func validateHarnessJSONSchemaFile(schemaPath string, payload any) []string {
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		return []string{err.Error()}
	}
	var schema any
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return []string{"invalid schema JSON: " + err.Error()}
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return []string{"failed to marshal payload: " + err.Error()}
	}
	var value any
	if err := json.Unmarshal(payloadData, &value); err != nil {
		return []string{"failed to normalize payload JSON: " + err.Error()}
	}
	validator := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	return validator.validate(schema, value, "$")
}

type harnessSchemaValidator struct {
	root      any
	schemaDir string
}

func (v harnessSchemaValidator) validate(schema any, value any, path string) []string {
	if allowed, ok := schema.(bool); ok {
		if allowed {
			return nil
		}
		return []string{path + ": value is rejected by false schema"}
	}
	node, ok := schema.(map[string]any)
	if !ok {
		return []string{path + ": schema node is not an object"}
	}
	if ref, _ := node["$ref"].(string); ref != "" {
		resolved, validator, err := v.resolveRef(ref)
		if err != nil {
			return []string{path + ": " + err.Error()}
		}
		return validator.validate(resolved, value, path)
	}
	var diagnostics []string
	diagnostics = append(diagnostics, v.validateCompositions(node, value, path)...)
	if constValue, ok := node["const"]; ok && !reflect.DeepEqual(constValue, value) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %s does not equal const %s", path, compactJSON(value), compactJSON(constValue)))
	}
	if enumValues, ok := node["enum"].([]any); ok {
		matched := false
		for _, enumValue := range enumValues {
			if reflect.DeepEqual(enumValue, value) {
				matched = true
				break
			}
		}
		if !matched {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: value %s is not in enum", path, compactJSON(value)))
		}
	}
	types := schemaTypes(node["type"])
	if len(types) > 0 && !jsonValueMatchesAnyType(value, types) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value has type %s, want %s", path, jsonValueType(value), strings.Join(types, "|")))
		return diagnostics
	}
	if jsonValueMatchesAnyType(value, []string{"object"}) {
		diagnostics = append(diagnostics, v.validateObject(node, value.(map[string]any), path)...)
	}
	if jsonValueMatchesAnyType(value, []string{"array"}) {
		diagnostics = append(diagnostics, v.validateArray(node, value.([]any), path)...)
	}
	if text, ok := value.(string); ok {
		diagnostics = append(diagnostics, validateStringConstraints(node, text, path)...)
	}
	diagnostics = append(diagnostics, validateNumericBounds(node, value, path)...)
	return diagnostics
}

func (v harnessSchemaValidator) validateCompositions(schema map[string]any, value any, path string) []string {
	var diagnostics []string
	if alternatives, ok := schema["oneOf"].([]any); ok {
		matches := 0
		for _, alternative := range alternatives {
			if len(v.validate(alternative, value, path)) == 0 {
				matches++
			}
		}
		if matches != 1 {
			diagnostics = append(diagnostics, fmt.Sprintf("%s: value matches %d oneOf alternatives, want exactly 1", path, matches))
		}
	}
	if alternatives, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, alternative := range alternatives {
			if len(v.validate(alternative, value, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			diagnostics = append(diagnostics, path+": value does not match anyOf alternatives")
		}
	}
	if requirements, ok := schema["allOf"].([]any); ok {
		for _, requirement := range requirements {
			diagnostics = append(diagnostics, v.validate(requirement, value, path)...)
		}
	}
	if rejected, ok := schema["not"]; ok && len(v.validate(rejected, value, path)) == 0 {
		diagnostics = append(diagnostics, path+": value matches forbidden not schema")
	}
	return diagnostics
}

func (v harnessSchemaValidator) validateObject(schema map[string]any, value map[string]any, path string) []string {
	var diagnostics []string
	if minimum, ok := schema["minProperties"].(float64); ok && len(value) < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: object has %d properties, want at least %d", path, len(value), int(minimum)))
	}
	if required, ok := schema["required"].([]any); ok {
		for _, raw := range required {
			key, _ := raw.(string)
			if key == "" {
				continue
			}
			if _, ok := value[key]; !ok {
				diagnostics = append(diagnostics, path+"."+key+": required property missing")
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	propertyNames := schema["propertyNames"]
	patternProperties, _ := schema["patternProperties"].(map[string]any)
	compiledPatterns := map[string]*regexp.Regexp{}
	for pattern := range patternProperties {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			diagnostics = append(diagnostics, path+": invalid patternProperties expression "+pattern)
			continue
		}
		compiledPatterns[pattern] = compiled
	}
	for key, propertyValue := range value {
		if propertyNames != nil {
			diagnostics = append(diagnostics, v.validate(propertyNames, key, path+".<property-name>")...)
		}
		for pattern, propertySchema := range patternProperties {
			if compiledPatterns[pattern] != nil && compiledPatterns[pattern].MatchString(key) {
				diagnostics = append(diagnostics, v.validate(propertySchema, propertyValue, path+"."+key)...)
			}
		}
	}
	for key, propertySchema := range properties {
		propertyValue, ok := value[key]
		if !ok {
			continue
		}
		diagnostics = append(diagnostics, v.validate(propertySchema, propertyValue, path+"."+key)...)
	}
	if additional, ok := schema["additionalProperties"]; ok {
		switch additional := additional.(type) {
		case bool:
			if !additional {
				for key := range value {
					if _, ok := properties[key]; !ok && !matchesSchemaPattern(key, compiledPatterns) {
						diagnostics = append(diagnostics, path+"."+key+": additional property is not allowed")
					}
				}
			}
		case map[string]any:
			for key, propertyValue := range value {
				if _, ok := properties[key]; ok || matchesSchemaPattern(key, compiledPatterns) {
					continue
				}
				diagnostics = append(diagnostics, v.validate(additional, propertyValue, path+"."+key)...)
			}
		}
	}
	sort.Strings(diagnostics)
	return diagnostics
}

func matchesSchemaPattern(value string, patterns map[string]*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern != nil && pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func (v harnessSchemaValidator) validateArray(schema map[string]any, value []any, path string) []string {
	var diagnostics []string
	if minimum, ok := schema["minItems"].(float64); ok && len(value) < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: array has %d items, want at least %d", path, len(value), int(minimum)))
	}
	if maximum, ok := schema["maxItems"].(float64); ok && len(value) > int(maximum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: array has %d items, want at most %d", path, len(value), int(maximum)))
	}
	if unique, _ := schema["uniqueItems"].(bool); unique {
		seen := map[string]bool{}
		for _, item := range value {
			key := compactJSON(item)
			if seen[key] {
				diagnostics = append(diagnostics, path+": array items must be unique")
				break
			}
			seen[key] = true
		}
	}
	if items, ok := schema["items"]; ok {
		for i, item := range value {
			diagnostics = append(diagnostics, v.validate(items, item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return diagnostics
}

func validateStringConstraints(schema map[string]any, value, path string) []string {
	var diagnostics []string
	length := utf8.RuneCountInString(value)
	if minimum, ok := schema["minLength"].(float64); ok && length < int(minimum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: string length %d is less than minimum %d", path, length, int(minimum)))
	}
	if maximum, ok := schema["maxLength"].(float64); ok && length > int(maximum) {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: string length %d exceeds maximum %d", path, length, int(maximum)))
	}
	if pattern, ok := schema["pattern"].(string); ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			diagnostics = append(diagnostics, path+": schema pattern is invalid")
		} else if !compiled.MatchString(value) {
			diagnostics = append(diagnostics, path+": string does not match pattern "+pattern)
		}
	}
	if format, _ := schema["format"].(string); format == "date-time" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			diagnostics = append(diagnostics, path+": string is not an RFC 3339 date-time")
		}
	}
	if encoding, _ := schema["contentEncoding"].(string); encoding == "base64" {
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			diagnostics = append(diagnostics, path+": string is not valid base64")
		}
	}
	return diagnostics
}

func (v harnessSchemaValidator) resolveRef(ref string) (any, harnessSchemaValidator, error) {
	if strings.HasPrefix(ref, "#") {
		return v.resolveLocalRef(ref)
	}
	schemaFile, fragment, _ := strings.Cut(ref, "#")
	if schemaFile == "" {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	if filepath.IsAbs(schemaFile) || strings.Contains(schemaFile, "://") {
		return nil, v, fmt.Errorf("unsupported external $ref %q", ref)
	}
	cleanFile := filepath.Clean(filepath.FromSlash(schemaFile))
	if cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(os.PathSeparator)) {
		return nil, v, fmt.Errorf("external $ref %q escapes schema directory", ref)
	}
	schemaPath := filepath.Join(v.schemaDir, cleanFile)
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, v, fmt.Errorf("failed to read external $ref %q: %w", ref, err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, v, fmt.Errorf("invalid external schema %q: %w", ref, err)
	}
	external := harnessSchemaValidator{root: schema, schemaDir: filepath.Dir(schemaPath)}
	if fragment == "" {
		return schema, external, nil
	}
	return external.resolveRef("#" + fragment)
}

func (v harnessSchemaValidator) resolveLocalRef(ref string) (any, harnessSchemaValidator, error) {
	const prefix = "#/$defs/"
	if ref == "#" {
		return v.root, v, nil
	}
	if !strings.HasPrefix(ref, prefix) {
		return nil, v, fmt.Errorf("unsupported $ref %q", ref)
	}
	root, ok := v.root.(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema root is not an object")
	}
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		return nil, v, fmt.Errorf("schema has no $defs for %q", ref)
	}
	key := strings.TrimPrefix(ref, prefix)
	value, ok := defs[key]
	if !ok {
		return nil, v, fmt.Errorf("schema $defs missing %q", key)
	}
	return value, v, nil
}

func schemaTypes(raw any) []string {
	switch raw := raw.(type) {
	case string:
		return []string{raw}
	case []any:
		var out []string
		for _, value := range raw {
			if text, ok := value.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonValueMatchesAnyType(value any, types []string) bool {
	for _, typ := range types {
		switch typ {
		case "null":
			if value == nil {
				return true
			}
		case "boolean":
			if _, ok := value.(bool); ok {
				return true
			}
		case "object":
			if _, ok := value.(map[string]any); ok {
				return true
			}
		case "array":
			if _, ok := value.([]any); ok {
				return true
			}
		case "number":
			if _, ok := value.(float64); ok {
				return true
			}
		case "integer":
			if number, ok := value.(float64); ok && math.Trunc(number) == number {
				return true
			}
		case "string":
			if _, ok := value.(string); ok {
				return true
			}
		}
	}
	return false
}

func jsonValueType(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case float64:
		if math.Trunc(value) == value {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func validateNumericBounds(schema map[string]any, value any, path string) []string {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	var diagnostics []string
	if min, ok := schema["minimum"].(float64); ok && number < min {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is less than minimum %.3f", path, number, min))
	}
	if max, ok := schema["maximum"].(float64); ok && number > max {
		diagnostics = append(diagnostics, fmt.Sprintf("%s: value %.3f is greater than maximum %.3f", path, number, max))
	}
	return diagnostics
}

func compactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
